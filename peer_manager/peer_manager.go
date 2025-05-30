package peer_manager

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/tracker"
)

type Handshake struct {
	Info_hash [20]byte
	Peer_id   [20]byte
}

const handshake_len = 68
const protocol = "BitTorrent protocol"
const connectionTimeout = 30 * time.Second
const readTimeout = 15 * time.Second
const writeTimeout = 10 * time.Second

func new_handshake(info_hash [20]byte, peer_id [20]byte) []byte {
	buf := make([]byte, 0, handshake_len)

	buf = append(buf, byte(len(protocol)))
	buf = append(buf, []byte(protocol)...)
	buf = append(buf, make([]byte, 8)...)
	buf = append(buf, info_hash[:]...)
	buf = append(buf, peer_id[:]...)

	return buf
}

func count_true(bits []bool) int {
	count := 0
	for _, b := range bits {
		if b {
			count++
		}
	}
	return count
}

func Start_torrenting(conns []net.Conn, t *torrent.Torrent, peerID [20]byte) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	pm := New_piece_manager(t.Num_pieces, t.Length, t.Piece_len)
	successfulHandshakes := 0
	mu := sync.Mutex{}

	// Progress reporting goroutine with proper cancellation
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if pm.IsComplete() {
					fmt.Println("Download completed!")
					cancel() // Signal all goroutines to stop
					return
				}

				downloadedBlocks, totalBlocks, completedPieces, totalPieces := pm.GetProgress()
				blockPercentage := float64(downloadedBlocks) / float64(totalBlocks) * 100
				piecePercentage := float64(completedPieces) / float64(totalPieces) * 100

				fmt.Printf("Progress: %d/%d pieces (%.1f%%) | %d/%d blocks (%.1f%%)\n",
					completedPieces, totalPieces, piecePercentage,
					downloadedBlocks, totalBlocks, blockPercentage)
			}
		}
	}()

	for _, conn := range conns {
		wg.Add(1)
		go func(conn net.Conn) {
			defer wg.Done()
			defer conn.Close()

			// Set initial timeout for handshake
			conn.SetDeadline(time.Now().Add(connectionTimeout))

			err := handshake(conn, t.Info_hash, peerID)
			if err != nil {
				fmt.Printf("Handshake failed with %s: %v\n", conn.RemoteAddr(), err)
				return
			}

			mu.Lock()
			successfulHandshakes++
			mu.Unlock()
			fmt.Printf("Handshake successful with %s\n", conn.RemoteAddr())

			// Reset deadline after successful handshake
			conn.SetDeadline(time.Time{})

			handle_peer(ctx, conn, pm, t)
		}(conn)
	}

	wg.Wait()
	mu.Lock()
	fmt.Printf("Finished with %d successful handshakes out of %d connections\n",
		successfulHandshakes, len(conns))
	mu.Unlock()
}

func handle_peer(ctx context.Context, conn net.Conn, pm *PieceManager, t *torrent.Torrent) {
	send_bitfield(conn, empty_bitfield(t.Num_pieces))
	var bitfield []bool
	var isDownloading bool
	downloaderCtx, cancelDownloader := context.WithCancel(ctx)
	defer cancelDownloader()

	// Set read timeout for message reading
	conn.SetReadDeadline(time.Now().Add(readTimeout))

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("Context cancelled for peer %s\n", conn.RemoteAddr())
			return
		default:
		}

		// Refresh read deadline for each message
		conn.SetReadDeadline(time.Now().Add(readTimeout))

		msg, err := Read_msg(conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				fmt.Printf("Read timeout from %s, closing connection\n", conn.RemoteAddr())
			} else {
				fmt.Printf("Error reading from %s: %v\n", conn.RemoteAddr(), err)
			}
			return
		}

		if msg == nil {
			fmt.Printf("Keep-alive from %s\n", conn.RemoteAddr())
			continue
		}

		switch msg.Msg_id {
		case MsgBitfield:
			bitfield = parse_bitfield(msg.Payload, t.Num_pieces)
			count := count_true(bitfield)
			fmt.Printf("Peer %s has %d/%d pieces\n", conn.RemoteAddr(), count, t.Num_pieces)

			// Send interested message
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			err := Send_msg(conn, &Message{Msg_id: MsgInterested})
			if err != nil {
				fmt.Printf("Failed to send interested to %s: %v\n", conn.RemoteAddr(), err)
				return
			}

		case MsgUnchoke:
			if !isDownloading && bitfield != nil {
				isDownloading = true
				fw := NewFileWriter(t)
				pieces := build_piece_works(t.Pieces, t.Piece_len, t.Length)

				go func() {
					defer cancelDownloader()
					StartDownloader(downloaderCtx, conn, bitfield, pm, fw, pieces)
				}()
			}

		case MsgChoke:
			fmt.Printf("Peer %s choked us\n", conn.RemoteAddr())
			if isDownloading {
				cancelDownloader()
				isDownloading = false
			}

		case MsgHave:
			if len(msg.Payload) < 4 {
				fmt.Printf("Invalid have message from %s\n", conn.RemoteAddr())
				continue
			}
			index := binary.BigEndian.Uint32(msg.Payload)
			if int(index) < len(bitfield) {
				bitfield[index] = true
			}
			fmt.Printf("Peer %s now has piece %d\n", conn.RemoteAddr(), index)

		case MsgPiece:
			// This is handled in the downloader, but we can log it here too
			if len(msg.Payload) >= 8 {
				index := binary.BigEndian.Uint32(msg.Payload[0:4])
				begin := binary.BigEndian.Uint32(msg.Payload[4:8])
				fmt.Printf("Received piece data: index=%d begin=%d length=%d from %s\n",
					index, begin, len(msg.Payload)-8, conn.RemoteAddr())
			}

		default:
			fmt.Printf("Peer %s sent message %s (%d bytes)\n",
				conn.RemoteAddr(), message_name(msg.Msg_id), len(msg.Payload))
		}
	}
}

func handshake(conn net.Conn, infoHash [20]byte, peerID [20]byte) error {
	msg := new_handshake(infoHash, peerID)

	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := conn.Write(msg)
	if err != nil {
		return fmt.Errorf("sending handshake failed: %v", err)
	}

	resp := make([]byte, handshake_len)
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	_, err = io.ReadFull(conn, resp)
	if err != nil {
		return fmt.Errorf("reading handshake failed: %v", err)
	}

	if int(resp[0]) != len(protocol) || string(resp[1:20]) != protocol {
		return errors.New("invalid protocol identifier in handshake")
	}

	var receivedInfoHash [20]byte
	copy(receivedInfoHash[:], resp[28:48])
	if !bytes.Equal(receivedInfoHash[:], infoHash[:]) {
		return errors.New("info hash mismatch in handshake")
	}

	return nil
}

func Connect_to_peers(peers []tracker.Peer, timeout time.Duration, maxConcurrent int) []net.Conn {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var connections []net.Conn
	sem := make(chan struct{}, maxConcurrent)

	for _, p := range peers {
		wg.Add(1)
		go func(p tracker.Peer) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			addr := net.JoinHostPort(p.Ip, fmt.Sprintf("%d", p.Port))
			conn, err := net.DialTimeout("tcp", addr, timeout)
			if err != nil {
				fmt.Printf("Failed to connect to %s: %v\n", addr, err)
				return
			}

			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
		}(p)
	}

	wg.Wait()

	fmt.Printf("Successfully connected to %d/%d peers\n", len(connections), len(peers))
	return connections
}
