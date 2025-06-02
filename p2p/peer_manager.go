package p2p

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/tracker"
)

type Client struct {
	Bitfield []byte
	Id       [20]byte
}

var client Client

type Peer struct {
	Id            [20]byte
	Ip            net.IP
	Port          uint16
	Conn          net.Conn
	Choked        bool
	Handshaked    bool
	Interested    bool
	Bitfield      []bool
	MsgChan       chan Message
	UnchokeSignal chan struct{}
	ChokeSignal   chan struct{}
}

type PeerPool struct {
	mu    sync.RWMutex
	peers map[string]*Peer // key: "ip:port"
}

func NewPeerPool() *PeerPool {
	return &PeerPool{
		peers: make(map[string]*Peer),
	}
}

func (pp *PeerPool) Add(peer *Peer) {
	addr := net.JoinHostPort(peer.Ip.String(), strconv.Itoa(int(peer.Port)))
	pp.mu.Lock()
	defer pp.mu.Unlock()
	pp.peers[addr] = peer
}

func (pp *PeerPool) Remove(peer *Peer) {
	addr := net.JoinHostPort(peer.Ip.String(), strconv.Itoa(int(peer.Port)))
	pp.mu.Lock()
	defer pp.mu.Unlock()
	delete(pp.peers, addr)
}

func (pp *PeerPool) List() []*Peer {
	pp.mu.RLock()
	defer pp.mu.RUnlock()

	list := make([]*Peer, 0, len(pp.peers))
	for _, p := range pp.peers {
		list = append(list, p)
	}

	return list
}

func HandlePeer(peer *Peer, t *torrent.Torrent) {
	conn, err := connectToPeer(peer)
	if err != nil {
		fmt.Printf("connection failed to %s:%d -> %v\n", peer.Ip, peer.Port, err)
		return
	}
	if conn == nil {
		fmt.Printf("connection returned nil for %s:%d\n", peer.Ip, peer.Port)
		return
	}
	peer.Conn = conn

	if err := Handshake(peer, t.Info_hash, client.Id); err != nil {
		fmt.Printf("handshake failed with %s:%d -> %v\n", peer.Ip, peer.Port, err)
		conn.Close()
		return
	}

	peer.Handshaked = true
	peer.Choked = true
	peer.Interested = false

	go requestPieceFromPeer(peer, t)
	go readFromPeer(peer, t)
	go respondToPeer(peer, t)
}

func sendInterested(conn net.Conn) error {
	msg := &Message{
		Msg_id: MsgInterested,
	}

	return Send_msg(conn, msg)
}

func peerHasInterestingPieces(peerBitfield []bool, t *torrent.Torrent) bool {
	piecesMu.Lock()
	defer piecesMu.Unlock()

	client_field := parse_bitfield(client.Bitfield, t.Num_pieces)
	for i := 0; i < t.Num_pieces; i++ {
		if i < len(peerBitfield) && i < len(client_field) && !client_field[i] && peerBitfield[i] {
			if Pieces[i].status == NotDownloaded {
				return true
			}
		}
	}
	return false
}

type PieceWork struct {
	index          int
	hash           [20]byte
	length         int
	status         int
	data           []byte
	receivedBlocks map[int]bool
	mu             sync.Mutex
}

const (
	NotDownloaded = 0
	InProgress    = 1
	Downloaded    = 2
)

const BlockSize = 16 * 1024

var Pieces []PieceWork

var piecesMu sync.Mutex

func requestPieceFromPeer(p *Peer, t *torrent.Torrent) {
	for {
		select {
		case <-p.UnchokeSignal:
			fmt.Printf("requestPiecesFromPeer for %s received unchoke signal.\n", p.Ip)
		case <-p.ChokeSignal:
			fmt.Printf("requestPiecesFromPeer for %s received choke signal. Pausing requests.\n", p.Ip)
			continue
		case <-time.After(5 * time.Second):
			if p.Choked {
				continue
			}
		}

		var pieceToRequest *PieceWork
		piecesMu.Lock()
		for i := 0; i < t.Num_pieces; i++ {
			client_field := parse_bitfield(client.Bitfield, t.Num_pieces)
			if i < len(client_field) && i < len(p.Bitfield) && !client_field[i] && p.Bitfield[i] && Pieces[i].status == NotDownloaded {
				pieceToRequest = &Pieces[i]
				pieceToRequest.status = InProgress
				break
			}
		}
		piecesMu.Unlock()

		if pieceToRequest == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		pieceToRequest.mu.Lock()
		for offset := 0; offset < pieceToRequest.length; offset += BlockSize {
			if pieceToRequest.receivedBlocks[offset] {
				continue
			}

			blockLength := BlockSize
			if offset+blockLength > pieceToRequest.length {
				blockLength = pieceToRequest.length - offset
			}

			if p.Choked {
				fmt.Printf("peer %s choked us while requesting piece %d. Pausing requests.\n", p.Ip, pieceToRequest.index)
				break
			}

			err := Send_msg(p.Conn, new_request(pieceToRequest.index, offset, blockLength))
			if err != nil {
				fmt.Printf("failed to send request for piece %d, offset %d to %s:%d -> %v\n", pieceToRequest.index, offset, p.Ip, p.Port, err)
				delete(pieceToRequest.receivedBlocks, offset)
				break
			}
			pieceToRequest.receivedBlocks[offset] = true
			fmt.Printf("requested piece %d, offset %d, length %d from %s\n", pieceToRequest.index, offset, blockLength, p.Ip)

			time.Sleep(50 * time.Millisecond)
		}
		pieceToRequest.mu.Unlock()
	}
}

func respondToPeer(p *Peer, t *torrent.Torrent) {

	if p.Handshaked {
		send_bitfield(p.Conn, client.Bitfield) //
	}

	sentInterested := false
	if peerHasInterestingPieces(p.Bitfield, t) {
		sendInterested(p.Conn)
		sentInterested = true
	}

	for msg := range p.MsgChan {
		switch msg.Msg_id {
		case MsgUnchoke: //
			p.Choked = false
			fmt.Printf("peer %s unchoked us\n", p.Ip)
			select {
			case p.UnchokeSignal <- struct{}{}:
			default:
			}

		case MsgBitfield:
			p.Bitfield = parse_bitfield(msg.Payload, t.Num_pieces) //
			fmt.Printf("received bitfield from %s\n", p.Ip)

			if !sentInterested && peerHasInterestingPieces(p.Bitfield, t) {
				sendInterested(p.Conn)
				sentInterested = true
			}
		case MsgChoke:
			p.Choked = true
			fmt.Printf("peer %s choked us\n", p.Ip)
			select {
			case p.ChokeSignal <- struct{}{}:
			default:
			}
		case MsgHave:
			if len(msg.Payload) >= 4 {
				pieceIndex := binary.BigEndian.Uint32(msg.Payload[0:4])
				if int(pieceIndex) >= 0 && int(pieceIndex) < len(p.Bitfield) {
					p.Bitfield[pieceIndex] = true
					fmt.Printf("peer %s has piece %d\n", p.Ip, pieceIndex)
					if !sentInterested && peerHasInterestingPieces(p.Bitfield, t) {
						sendInterested(p.Conn)
						sentInterested = true
					}
				}
			}
		case MsgRequest:
			if len(msg.Payload) >= 12 {
				reqPieceIndex := binary.BigEndian.Uint32(msg.Payload[0:4])
				reqOffset := binary.BigEndian.Uint32(msg.Payload[4:8])
				reqLength := binary.BigEndian.Uint32(msg.Payload[8:12])
				fmt.Printf("received request from %s for piece %d, offset %d, length %d\n", p.Ip, reqPieceIndex, reqOffset, reqLength)
				// TODO: Implement serving requested pieces if you become a seeder
			} else {
				fmt.Printf("received malformed request message from %s\n", p.Ip)
			}
		case MsgPiece:
			if len(msg.Payload) >= 8 {
				pieceIndex := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
				begin := int(binary.BigEndian.Uint32(msg.Payload[4:8]))
				block := msg.Payload[8:]

				fmt.Printf("received piece data from %s: piece %d, begin %d, length %d\n", p.Ip, pieceIndex, begin, len(block))

				if pieceIndex < 0 || pieceIndex >= len(Pieces) {
					fmt.Printf("received piece data for invalid piece index %d from %s\n", pieceIndex, p.Ip)
					continue
				}

				pw := &Pieces[pieceIndex]
				pw.mu.Lock()
				copy(pw.data[begin:begin+len(block)], block)
				pw.receivedBlocks[begin] = true

				allBlocksReceived := true
				for offset := 0; offset < pw.length; offset += BlockSize {
					if !pw.receivedBlocks[offset] {
						allBlocksReceived = false
						break
					}
				}
				pw.mu.Unlock()

				if allBlocksReceived {
					hash := sha1.Sum(pw.data)
					if bytes.Equal(hash[:], pw.hash[:]) {
						fmt.Printf("Piece %d downloaded and verified successfully!\n", pieceIndex)
						piecesMu.Lock()
						pw.status = Downloaded
						set_bit(client.Bitfield, pieceIndex)
						piecesMu.Unlock()

						err := writePieceToFile(pw, t)
						if err != nil {
							fmt.Printf("Error writing piece %d to file: %v\n", pieceIndex, err)
							// Optionally, reset status to NotDownloaded and clear data if writing fails
							piecesMu.Lock()
							pw.status = NotDownloaded
							clear_bit(client.Bitfield, pieceIndex)
							pw.receivedBlocks = make(map[int]bool)
							pw.data = make([]byte, pw.length)
							piecesMu.Unlock()
						} else {
							// inform other peers that we have this piece
							// send a have msg to all connected peers
						}

					} else {
						fmt.Printf("Piece %d hash mismatch! Resetting for re-download.\n", pieceIndex)
						piecesMu.Lock()
						pw.status = NotDownloaded
						pw.receivedBlocks = make(map[int]bool)
						piecesMu.Unlock()
					}
				}
			} else {
				fmt.Printf("received malformed piece message from %s\n", p.Ip)
			}
		default:
			fmt.Printf("received message %s from %s\n", message_name(msg.Msg_id), p.Ip)
		}
	}
}

func readFromPeer(p *Peer, t *torrent.Torrent) {
	defer p.Conn.Close()

	for {
		msg, err := Read_msg(p.Conn)
		if err != nil {
			if err == io.EOF {
				fmt.Printf("peer %s:%d disconnected gracefully.\n", p.Ip, p.Port)
			} else {
				fmt.Printf("couldn't read from %s:%d -> %v\n", p.Ip, p.Port, err)
			}
			return
		}
		if msg != nil {
			p.MsgChan <- *msg
		}
	}
}

func connectToPeer(peer *Peer) (net.Conn, error) {
	addr := net.JoinHostPort(peer.Ip.String(), strconv.Itoa(int(peer.Port)))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Printf("failed to connect %s, %v\n", addr, err)
		return nil, err
	}

	return conn, nil
}

func Handshake(peer *Peer, infoHash, peerID [20]byte) error {
	err := sendHandshake(peer.Conn, infoHash, peerID)
	if err != nil {
		return fmt.Errorf("send handshake failed: %w", err)
	}

	recvInfoHash, _, err := readHandshake(peer.Conn)
	if err != nil {
		return fmt.Errorf("read handshake failed: %w", err)
	}

	if recvInfoHash != infoHash {
		return fmt.Errorf("infoHash mismatch")
	}
	fmt.Printf("handshaked with %s\n", peer.Ip)

	return nil
}

func sendHandshake(conn net.Conn, infoHash [20]byte, peerID [20]byte) error {
	pstr := "BitTorrent protocol"
	buf := make([]byte, 49+len(pstr))

	buf[0] = byte(len(pstr))    // pstrlen
	copy(buf[1:], []byte(pstr)) // pstr
	// reserved 8 bytes left as zero (buf[20:28])
	copy(buf[28:48], infoHash[:]) // info_hash
	copy(buf[48:68], peerID[:])   // peer_id

	_, err := conn.Write(buf)
	return err
}

func readHandshake(conn net.Conn) (infoHash [20]byte, peerID [20]byte, err error) {
	pstrlenBuf := make([]byte, 1)
	_, err = io.ReadFull(conn, pstrlenBuf)
	if err != nil {
		return
	}

	pstrlen := int(pstrlenBuf[0])
	if pstrlen != 19 {
		err = fmt.Errorf("invalid protocol string length: %d", pstrlen)
		return
	}

	pstrBuf := make([]byte, pstrlen)
	_, err = io.ReadFull(conn, pstrBuf)
	if err != nil {
		return
	}

	if string(pstrBuf) != "BitTorrent protocol" {
		err = fmt.Errorf("unexpected protocol string: %s", string(pstrBuf))
		return
	}

	reserved := make([]byte, 8)
	_, err = io.ReadFull(conn, reserved)
	if err != nil {
		return
	}

	_, err = io.ReadFull(conn, infoHash[:])
	if err != nil {
		return
	}

	_, err = io.ReadFull(conn, peerID[:])
	return
}

func Start_download(peers []tracker.PeerData, t *torrent.Torrent) {
	Pieces = make([]PieceWork, t.Num_pieces)
	for i := 0; i < t.Num_pieces; i++ {
		pieceLen := t.Piece_len
		if i == t.Num_pieces-1 {
			lastPieceLen := t.Length % t.Piece_len
			if lastPieceLen != 0 {
				pieceLen = lastPieceLen
			}
		}

		Pieces[i] = PieceWork{
			index:          i,
			hash:           t.Pieces[i],
			length:         pieceLen,
			status:         NotDownloaded,
			data:           make([]byte, pieceLen),
			receivedBlocks: make(map[int]bool),
		}
	}

	pp := NewPeerPool()
	client.Bitfield = empty_bitfield(t.Num_pieces)
	client.Id = t.PeerId
	for i := range peers {
		new_peer := &Peer{
			Ip:            net.ParseIP(peers[i].Ip),
			Port:          uint16(peers[i].Port),
			Handshaked:    false,
			MsgChan:       make(chan Message, 16),
			UnchokeSignal: make(chan struct{}, 1),
			ChokeSignal:   make(chan struct{}, 1),
		}
		pp.Add(new_peer)
	}

	peer_list := pp.List()
	var wg sync.WaitGroup
	for i := range peer_list {
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			HandlePeer(p, t)
		}(peer_list[i])
	}
	wg.Wait()
}

func set_bit(bitfield []byte, index int) {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)
	if byteIndex < len(bitfield) {
		bitfield[byteIndex] |= (1 << bitOffset)
	}
}

func clear_bit(bitfield []byte, index int) {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)
	if byteIndex < len(bitfield) {
		bitfield[byteIndex] &^= (1 << bitOffset)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func writePieceToFile(pw *PieceWork, t *torrent.Torrent) error {
	if len(t.Files) == 0 {
		filePath := t.Name
		file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			return fmt.Errorf("failed to open single file %s: %w", filePath, err)
		}
		defer file.Close()

		offset := int64(pw.index) * int64(t.Piece_len)

		_, err = file.WriteAt(pw.data, offset)
		if err != nil {
			return fmt.Errorf("failed to write piece %d at offset %d to single file: %w", pw.index, offset, err)
		}
		fmt.Printf("Successfully wrote piece %d to single file.\n", pw.index)
		return nil
	}

	pieceStartOffsetInTorrent := pw.index * t.Piece_len
	pieceEndOffsetInTorrent := pieceStartOffsetInTorrent + len(pw.data)

	currentTorrentOffset := 0

	for _, file := range t.Files {
		fileStartOffsetInTorrent := currentTorrentOffset
		fileEndOffsetInTorrent := currentTorrentOffset + file.Length

		overlapStartGlobal := max(pieceStartOffsetInTorrent, fileStartOffsetInTorrent)
		overlapEndGlobal := min(pieceEndOffsetInTorrent, fileEndOffsetInTorrent)

		if overlapStartGlobal < overlapEndGlobal {
			pieceDataStartInPieceBuffer := overlapStartGlobal - pieceStartOffsetInTorrent
			pieceDataEndInPieceBuffer := overlapEndGlobal - pieceStartOffsetInTorrent
			dataToWrite := pw.data[pieceDataStartInPieceBuffer:pieceDataEndInPieceBuffer]

			fileWriteOffset := overlapStartGlobal - fileStartOffsetInTorrent

			outputPath := filepath.Join(append([]string{t.Name}, file.Path...)...)

			dir := filepath.Dir(outputPath)
			if dir != "." && dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return fmt.Errorf("failed to create directory %s for file %s: %w", dir, outputPath, err)
				}
			}

			// `os.O_WRONLY`: Open the file for writing only.
			// `os.O_CREATE`: Create the file if it does not exist.
			// `0666`: File permissions (read/write for owner, group, others).
			f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE, 0666)
			if err != nil {
				return fmt.Errorf("failed to open file %s for writing piece %d: %w", outputPath, pw.index, err)
			}
			defer f.Close()

			n, err := f.WriteAt(dataToWrite, int64(fileWriteOffset))
			if err != nil {
				return fmt.Errorf("failed to write %d bytes to file %s at offset %d for piece %d: %w", len(dataToWrite), outputPath, fileWriteOffset, pw.index, err)
			}
			if n != len(dataToWrite) {
				return fmt.Errorf("incomplete write to file %s for piece %d: wrote %d of %d bytes", outputPath, pw.index, n, len(dataToWrite))
			}
		}
		currentTorrentOffset += file.Length
	}
	fmt.Printf("Successfully wrote piece %d to file(s).\n", pw.index)
	return nil
}
