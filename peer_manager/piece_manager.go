package peer_manager

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

const block_size = 16 * 1024 //16kb
type PieceWork struct {
	Index  int
	Length int
	Hash   [20]byte
}

type PieceManager struct {
	Have      []bool
	Requested []bool
	NumPieces int
	mu        sync.Mutex
}

func New_piece_manager(numPieces int) *PieceManager {
	return &PieceManager{
		Have:      make([]bool, numPieces),
		Requested: make([]bool, numPieces),
		NumPieces: numPieces,
	}
}

func (pm *PieceManager) Pick_piece(peerBitfield []bool) (int, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i := 0; i < pm.NumPieces; i++ {
		if !pm.Have[i] && !pm.Requested[i] && peerBitfield[i] {
			pm.Requested[i] = true
			return i, true
		}
	}
	return -1, false
}

func (pm *PieceManager) Mark_completed(index int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Have[index] = true
	pm.Requested[index] = false
}

func Download_piece(conn net.Conn, pw PieceWork, bitfield []bool) ([]byte, error) {
	if pw.Index >= len(bitfield) || !bitfield[pw.Index] {
		return nil, fmt.Errorf("peer doesn't have piece %d", pw.Index)
	}

	buf := make([]byte, pw.Length)
	blocks := (pw.Length + block_size - 1) / block_size

	for b := 0; b < blocks; b++ {
		begin := b * block_size
		reqLen := block_size
		if begin+block_size > pw.Length {
			reqLen = pw.Length - begin
		}

		req := new_request(pw.Index, begin, reqLen)
		err := Send_msg(conn, req)
		if err != nil {
			return nil, fmt.Errorf("send failed: %w", err)
		}

		msg, err := Read_msg(conn)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}

		if msg.Msg_id == MsgPiece && len(msg.Payload) < 8 {
			return nil, fmt.Errorf("short piece message")
		}

		// parse index/begin/block
		index := binary.BigEndian.Uint32(msg.Payload[0:4])
		beginResp := binary.BigEndian.Uint32(msg.Payload[4:8])
		copy(buf[beginResp:], msg.Payload[8:])

		fmt.Printf("<-- received block: piece %d, begin %d, len %d\n", index, beginResp, len(msg.Payload[8:]))
	}

	// hash check
	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.Hash[:]) {
		return nil, fmt.Errorf("piece hash mismatch: piece %d", pw.Index)
	}

	fmt.Printf("downloaded piece %d (%d bytes)\n", pw.Index, pw.Length)
	return buf, nil
}
