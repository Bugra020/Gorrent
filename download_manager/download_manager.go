package download_manager

import (
	"fmt"
	"io"
	"net"
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
	Id         [20]byte
	Ip         net.IP
	Port       uint16
	Conn       net.Conn
	Choked     bool
	Handshaked bool
	Interested bool
	Bitfield   []bool
	MsgChan    chan Message
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

	peer.Conn = conn

	if err := Handshake(peer, t.Info_hash, client.Id); err != nil {
		fmt.Printf("handshake failed with %s:%d -> %v\n", peer.Ip, peer.Port, err)
		conn.Close()
		return
	}

	peer.Handshaked = true
	peer.Choked = true
	peer.Interested = false

	go readFromPeer(peer, t)
	go respondToPeer(peer, t)
}

func sendInterested(conn net.Conn) error {
	msg := &Message{
		Msg_id: 2,
	}

	return Send_msg(conn, msg)
}

func peerHasInterestingPieces(peerBitfield []bool, t *torrent.Torrent) bool {
	for i := 0; i < t.Num_pieces; i++ {
		if i >= len(peerBitfield) {
			break
		}
		if peerBitfield[i] {
			byteIndex := i / 8
			bitOffset := 7 - (i % 8)
			if (client.Bitfield[byteIndex]>>bitOffset)&1 == 0 {
				return true
			}
		}
	}
	return false
}

func respondToPeer(p *Peer, t *torrent.Torrent) {
	defer p.Conn.Close()

	if p.Handshaked {
		send_bitfield(p.Conn, client.Bitfield)
	}

	sentInterested := false
	if peerHasInterestingPieces(p.Bitfield, t) {
		sendInterested(p.Conn)
		sentInterested = true
	}

	for msg := range p.MsgChan {
		switch msg.Msg_id {
		case MsgUnchoke:
			p.Choked = false
			fmt.Printf("peer %s unchoked us\n", p.Ip)

		case MsgBitfield:
			p.Bitfield = parse_bitfield(msg.Payload, t.Num_pieces)
			fmt.Printf("received bitfield from %s\n", p.Ip)

			if !sentInterested && peerHasInterestingPieces(p.Bitfield, t) {
				sendInterested(p.Conn)
				sentInterested = true
			}
		default:
			fmt.Printf("received mesage %s\n", message_name(msg.Msg_id))
		}
	}
}

func readFromPeer(p *Peer, t *torrent.Torrent) {
	defer p.Conn.Close()

	for {
		msg, err := Read_msg(p.Conn)
		if err != nil {
			fmt.Printf("couldn't read %v\n", err)
			return
		}
		if msg != nil {
			p.MsgChan <- *msg
		}
	}
}

func connectToPeer(peer *Peer) (net.Conn, error) {
	addr := fmt.Sprintf("[%s]:%d", peer.Ip, peer.Port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Printf("failed to connect %s, %v\n", addr, err)
	}

	return conn, nil
}

func Handshake(peer *Peer, infoHash, peerID [20]byte) error {
	if err := sendHandshake(peer.Conn, infoHash, peerID); err != nil {
		return fmt.Errorf("send handshake failed: %w", err)
	}

	recvInfoHash, _, err := readHandshake(peer.Conn)
	if err != nil {
		return fmt.Errorf("read handshake failed: %w", err)
	}

	if recvInfoHash != infoHash {
		return fmt.Errorf("infoHash mismatch")
	}
	fmt.Printf("handshekd with %s\n", peer.Ip)

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
	pp := NewPeerPool()
	client.Bitfield = make([]byte, t.Num_pieces)
	client.Id = t.PeerId
	for i := range peers {
		new_peer := &Peer{
			Ip:         net.ParseIP(peers[i].Ip),
			Port:       uint16(peers[i].Port),
			Handshaked: false,
			MsgChan:    make(chan Message, 16),
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
