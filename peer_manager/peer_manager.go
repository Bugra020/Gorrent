package peer_manager

import (
	"bytes"
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

func New_handshake(info_hash [20]byte, peer_id [20]byte) []byte {
	buf := make([]byte, 0, handshake_len)

	buf = append(buf, byte(len(protocol)))
	buf = append(buf, []byte(protocol)...)
	buf = append(buf, make([]byte, 8)...)
	buf = append(buf, info_hash[:]...)
	buf = append(buf, peer_id[:]...)

	return buf
}

func Do_handshakes(conns []net.Conn, hash [20]byte, peer_id [20]byte) {
	successes := len(conns)
	for _, conn := range conns {
		err := do_handshake(conn, hash, peer_id)
		if err != nil {
			//fmt.Printf("Handshake failed with %s: %v\n", conn.RemoteAddr(), err)
			conn.Close()
			successes--
			continue
		}
		//fmt.Printf("Handshake succeeded with %s\n", conn.RemoteAddr())

		//can now proceed to read bitfield or exchange messages
	}
	fmt.Printf("Successfully handshaked with %d/%d peers", successes, len(conns))
}

func do_handshake(conn net.Conn, infoHash [20]byte, peerID [20]byte) error {
	msg := New_handshake(infoHash, peerID)
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
