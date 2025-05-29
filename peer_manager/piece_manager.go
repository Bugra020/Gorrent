package peer_manager

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

const block_size = 16 * 1024 //16kb

type PieceWork struct {
	Index  int
	Length int
	Hash   [20]byte
}

type FileWriter struct {
	File *os.File
	Mu   sync.Mutex
}

type PieceManager struct {
	Have             []bool
	Requested        []bool
	NumPieces        int
	TotalBlocks      int
	DownloadedBlocks int
	mu               sync.Mutex
}

func New_piece_manager(numPieces int, totalLength int, pieceLength int) *PieceManager {
	// Calculate total number of blocks across all pieces
	totalBlocks := 0
	for i := 0; i < numPieces; i++ {
		pieceLen := pieceLength
		if i == numPieces-1 {
			// Last piece might be smaller
			pieceLen = totalLength - (pieceLength * (numPieces - 1))
		}
		blocksInPiece := (pieceLen + block_size - 1) / block_size
		totalBlocks += blocksInPiece
	}

	return &PieceManager{
		Have:             make([]bool, numPieces),
		Requested:        make([]bool, numPieces),
		NumPieces:        numPieces,
		TotalBlocks:      totalBlocks,
		DownloadedBlocks: 0,
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

func (pm *PieceManager) IncrementDownloadedBlocks() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.DownloadedBlocks++
}

func (pm *PieceManager) GetProgress() (int, int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.DownloadedBlocks, pm.TotalBlocks
}

func build_piece_works(pieces [][20]byte, pieceLen, totalLen int) []PieceWork {
	works := make([]PieceWork, len(pieces))
	for i := 0; i < len(pieces); i++ {
		length := pieceLen
		if i == len(pieces)-1 {
			length = totalLen - (pieceLen * (len(pieces) - 1))
		}
		works[i] = PieceWork{
			Index:  i,
			Length: length,
			Hash:   pieces[i],
		}
	}
	return works
}

func StartDownloader(conn net.Conn, bitfield []bool, pm *PieceManager, fw *FileWriter, pieces []PieceWork) {
	for {
		if bitfield == nil {
			continue
		}
		index, ok := pm.Pick_piece(bitfield)
		if !ok {
			return
		}

		pw := pieces[index]

		data, err := Download_piece(conn, pw, bitfield, pm)
		if err != nil {
			if err.Error() == "closedconnerr" {
				return
			}

			fmt.Printf("failed to download piece %d: %v\n", index, err)

			pm.mu.Lock()
			pm.Requested[index] = false
			pm.mu.Unlock()

			continue
		}

		err = fw.save_piece(pw.Index, pw.Length, data)
		if err != nil {
			fmt.Printf("failed to save piece %d: %v\n", pw.Index, err)

			pm.mu.Lock()
			pm.Have[index] = false
			pm.Requested[index] = false
			pm.mu.Unlock()

			continue
		}

		pm.Mark_completed(index)
	}
}

func Download_piece(conn net.Conn, pw PieceWork, bitfield []bool, pm *PieceManager) ([]byte, error) {
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
			err_msg := err.Error()
			if strings.Contains(err_msg, "use of closed network connection") ||
				strings.Contains(err_msg, "EOF") ||
				strings.Contains(err_msg, "connection reset by peer") {

				return nil, fmt.Errorf("closedconnerr")
			}
			return nil, fmt.Errorf("send failed: %w", err)
		}

		msg, err := Read_msg(conn)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}

		if msg == nil {
			return nil, fmt.Errorf("msg is nil: %w", err)
		}

		if msg.Msg_id == MsgPiece && len(msg.Payload) < 8 {
			return nil, fmt.Errorf("short piece message")
		}

		if msg.Msg_id == MsgPiece {
			index := binary.BigEndian.Uint32(msg.Payload[0:4])
			beginResp := binary.BigEndian.Uint32(msg.Payload[4:8])
			copy(buf[beginResp:], msg.Payload[8:])

			// Increment downloaded blocks counter
			pm.IncrementDownloadedBlocks()

			fmt.Printf("<-- received block: piece %d, begin %d, len %d\n", index, beginResp, len(msg.Payload[8:]))
		}
	}

	// hash check
	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.Hash[:]) {
		return nil, fmt.Errorf("piece hash mismatch: piece %d", pw.Index)
	}

	fmt.Printf("downloaded piece %d (%d bytes)\n", pw.Index, pw.Length)
	return buf, nil
}

func (fw *FileWriter) save_piece(index int, piece_len int, data []byte) error {
	offset := int64(index) * int64(piece_len)

	fw.Mu.Lock()
	defer fw.Mu.Unlock()

	_, err := fw.File.WriteAt(data, offset)
	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	fmt.Printf("wrote piece %d (%d bytes) at offset %d\n", index, len(data), offset)
	return nil
}
