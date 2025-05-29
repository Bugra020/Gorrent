package peer_manager

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Bugra020/Gorrent/torrent"
)

const block_size = 16 * 1024 //16kb

type PieceWork struct {
	Index  int
	Length int
	Hash   [20]byte
}

type FileWriter struct {
	Torrent   *torrent.Torrent
	OpenFiles map[string]*os.File
	Mu        sync.Mutex
}

type PieceManager struct {
	Have             []bool
	Requested        []bool
	NumPieces        int
	TotalBlocks      int
	DownloadedBlocks int
	mu               sync.Mutex
}

func NewFileWriter(t *torrent.Torrent) *FileWriter {
	return &FileWriter{
		Torrent:   t,
		OpenFiles: make(map[string]*os.File),
	}
}

func (fw *FileWriter) getOrCreateFile(filePath string) (*os.File, error) {
	if file, exists := fw.OpenFiles[filePath]; exists {
		return file, nil
	}

	dir := filepath.Dir(filePath)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create file %s: %v", filePath, err)
	}

	fw.OpenFiles[filePath] = file
	return file, nil
}

func (fw *FileWriter) Close() {
	fw.Mu.Lock()
	defer fw.Mu.Unlock()

	for _, file := range fw.OpenFiles {
		file.Close()
	}
	fw.OpenFiles = make(map[string]*os.File)
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
	defer fw.Close() // Ensure files are closed when downloader finishes

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

			pm.IncrementDownloadedBlocks()

			fmt.Printf("<-- received block: piece %d, begin %d, len %d\n", index, beginResp, len(msg.Payload[8:]))
		}
	}

	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.Hash[:]) {
		return nil, fmt.Errorf("piece hash mismatch: piece %d", pw.Index)
	}

	fmt.Printf("downloaded piece %d (%d bytes)\n", pw.Index, pw.Length)
	return buf, nil
}

func (fw *FileWriter) save_piece(pieceIndex int, pieceLength int, data []byte) error {
	fw.Mu.Lock()
	defer fw.Mu.Unlock()

	if !fw.Torrent.IsMultiFile {
		outputPath := filepath.Join(fw.Torrent.OutputDir, fw.Torrent.Name)
		file, err := fw.getOrCreateFile(outputPath)
		if err != nil {
			return err
		}

		offset := int64(pieceIndex) * int64(fw.Torrent.Piece_len)
		_, err = file.WriteAt(data, offset)
		if err != nil {
			return fmt.Errorf("write failed: %w", err)
		}
		fmt.Printf("wrote piece %d (%d bytes) at offset %d to %s\n",
			pieceIndex, len(data), offset, fw.Torrent.Name)
		return nil
	}

	pieceOffset := int64(pieceIndex) * int64(fw.Torrent.Piece_len)
	dataOffset := 0
	currentFileOffset := int64(0)

	for _, fileInfo := range fw.Torrent.Files {
		fileEnd := currentFileOffset + int64(fileInfo.Length)

		if pieceOffset < fileEnd && int64(pieceOffset)+int64(len(data)) > currentFileOffset {
			writeStart := max(pieceOffset, currentFileOffset)
			writeEnd := min(int64(pieceOffset)+int64(len(data)), fileEnd)

			if writeStart < writeEnd {
				fileWriteOffset := writeStart - currentFileOffset
				dataReadOffset := writeStart - pieceOffset
				writeLength := writeEnd - writeStart

				filePath := filepath.Join(fw.Torrent.OutputDir, filepath.Join(fileInfo.Path...))
				file, err := fw.getOrCreateFile(filePath)
				if err != nil {
					return err
				}

				writeData := data[dataReadOffset : dataReadOffset+writeLength]
				_, err = file.WriteAt(writeData, fileWriteOffset)
				if err != nil {
					return fmt.Errorf("write to %s failed: %w", filePath, err)
				}

				fmt.Printf("wrote %d bytes to %s at offset %d (piece %d)\n",
					len(writeData), filepath.Join(fileInfo.Path...), fileWriteOffset, pieceIndex)

				dataOffset += int(writeLength)
			}
		}

		currentFileOffset = fileEnd

		if dataOffset >= len(data) {
			break
		}
	}

	return nil
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
