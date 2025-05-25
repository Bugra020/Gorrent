package peer_manager

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Bugra020/Gorrent/tracker"
)

type Handshake struct {
	Info_hash [20]byte
	Peer_id   [20]byte
}

const handshake_len = 68
const protocol = "BitTorrent protocol"

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

func Do_handshakes(conns []net.Conn, infoHash [20]byte, peerID [20]byte, numPieces int) {
	var wg sync.WaitGroup
	successes := 0

	for _, conn := range conns {
		wg.Add(1)
		go func(conn net.Conn) {
			defer wg.Done()

			err := handshake(conn, infoHash, peerID)
			if err != nil {
				conn.Close()
				return
			}

			successes++

			handle_peer(conn, infoHash, peerID, numPieces)
		}(conn)
	}

	wg.Wait()
	fmt.Printf("Successfully handshaked with %d/%d peers\n", successes, len(conns))
}

func handle_peer(conn net.Conn, infoHash [20]byte, peerID [20]byte, numPieces int) {
	defer conn.Close()

	send_bitfield(conn, empty_bitfield(numPieces))

	for {
		msg, err := Read_msg(conn)
		if err != nil {
			fmt.Printf("error reading from %s: %v\n", conn.RemoteAddr(), err)
			return
		}
		if msg == nil {
			fmt.Printf("keep-alive from %s\n", conn.RemoteAddr())
			continue
		}

		switch msg.Msg_id {
		case MsgBitfield:
			bitfield := parse_bitfield(msg.Payload, numPieces)
			count := count_true(bitfield)
			fmt.Printf("peer %s has %d/%d pieces\n", conn.RemoteAddr(), count, numPieces)

			err := Send_msg(conn, &Message{Msg_id: MsgInterested})
			if err != nil {
				fmt.Printf("failed to send interested to %s: %v\n", conn.RemoteAddr(), err)
				return
			}

		case MsgHave:
			if len(msg.Payload) < 4 {
				fmt.Printf("invalid have message from %s\n", conn.RemoteAddr())
				continue
			}
			index := binary.BigEndian.Uint32(msg.Payload)
			fmt.Printf("peer %s having piece %d\n", conn.RemoteAddr(), index)

		case MsgUnchoke:
			fmt.Printf("peer %s unchoked us\n", conn.RemoteAddr())
			req := new_request(0, 0, 16*1024)
			err := Send_msg(conn, req)
			if err != nil {
				fmt.Printf("failed to send request to %s: %v\n", conn.RemoteAddr(), err)
				return
			}
			fmt.Printf("--> sent request: piece=0 begin=0 length=16384\n")

		case MsgPiece:
			if len(msg.Payload) < 8 {
				fmt.Printf("invalid piece message from %s\n", conn.RemoteAddr())
				return
			}
			index := binary.BigEndian.Uint32(msg.Payload[0:4])
			begin := binary.BigEndian.Uint32(msg.Payload[4:8])
			block := msg.Payload[8:]

			fmt.Printf("<-- received piece: index=%d begin=%d length=%d from %s\n",
				index, begin, len(block), conn.RemoteAddr())

		default:
			fmt.Printf("peer %s sent message %s (%d bytes)\n", conn.RemoteAddr(), message_name(msg.Msg_id), len(msg.Payload))
		}
	}
}

func handshake(conn net.Conn, infoHash [20]byte, peerID [20]byte) error {
	msg := new_handshake(infoHash, peerID)
	_, err := conn.Write(msg)
	if err != nil {
		return fmt.Errorf("sending handshake failed: %v", err)
	}

	resp := make([]byte, handshake_len)
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
				fmt.Printf("failed to connect to %s: %v\n", addr, err)
				return
			}
			//fmt.Printf("connected to %s\n", addr)

			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
		}(p)
	}

	wg.Wait()

	fmt.Printf("successfully connected to %d/%d peers\n", len(connections), len(peers))
	return connections
}
