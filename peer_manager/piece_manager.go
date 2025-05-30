package peer_manager

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

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
	CompletedPieces  int
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
		CompletedPieces:  0,
	}
}

func (pm *PieceManager) Pick_piece(peerBitfield []bool) (int, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Simple sequential strategy - can be improved to rarest-first later
	for i := 0; i < pm.NumPieces; i++ {
		if !pm.Have[i] && !pm.Requested[i] && i < len(peerBitfield) && peerBitfield[i] {
			pm.Requested[i] = true
			return i, true
		}
	}
	return -1, false
}

func (pm *PieceManager) Mark_completed(index int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if !pm.Have[index] {
		pm.Have[index] = true
		pm.CompletedPieces++
	}
	pm.Requested[index] = false
}

func (pm *PieceManager) Mark_failed(index int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Requested[index] = false
}

func (pm *PieceManager) IncrementDownloadedBlocks() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.DownloadedBlocks++
}

func (pm *PieceManager) GetProgress() (int, int, int, int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.DownloadedBlocks, pm.TotalBlocks, pm.CompletedPieces, pm.NumPieces
}

func (pm *PieceManager) IsComplete() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.CompletedPieces == pm.NumPieces
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

// Add this improved StartDownloader function to replace the existing one in piece_manager.go

func StartDownloader(ctx context.Context, conn net.Conn, bitfield []bool, pm *PieceManager, fw *FileWriter, pieces []PieceWork) {
	defer fw.Close()

	fmt.Printf("Starting downloader for %s\n", conn.RemoteAddr())
	maxRetries := 3
	backoffTime := time.Second

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("Downloader context cancelled for %s\n", conn.RemoteAddr())
			return
		default:
		}

		if pm.IsComplete() {
			fmt.Printf("Download completed by %s\n", conn.RemoteAddr())
			return
		}

		if bitfield == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		index, ok := pm.Pick_piece(bitfield)
		if !ok {
			// No more pieces available from this peer, wait a bit and retry
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}

		pw := pieces[index]
		var data []byte
		var err error

		// Retry failed downloads with exponential backoff
		for retry := 0; retry < maxRetries; retry++ {
			select {
			case <-ctx.Done():
				pm.Mark_failed(index)
				return
			default:
			}

			// Set timeouts for download attempt
			downloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			data, err = Download_piece_with_context(downloadCtx, conn, pw, bitfield, pm)
			cancel()

			if err == nil {
				break
			}

			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				fmt.Printf("Download cancelled or timed out for piece %d from %s\n", index, conn.RemoteAddr())
				pm.Mark_failed(index)
				return
			}

			fmt.Printf("Failed to download piece %d from %s (attempt %d/%d): %v\n",
				index, conn.RemoteAddr(), retry+1, maxRetries, err)

			if retry == maxRetries-1 {
				pm.Mark_failed(index)
				break
			}

			// Exponential backoff
			waitTime := backoffTime * time.Duration(1<<retry)
			select {
			case <-ctx.Done():
				pm.Mark_failed(index)
				return
			case <-time.After(waitTime):
			}
		}

		if err != nil {
			continue
		}

		// Save the piece
		err = fw.save_piece(pw.Index, pw.Length, data)
		if err != nil {
			fmt.Printf("Failed to save piece %d: %v\n", pw.Index, err)
			pm.Mark_failed(index)
			continue
		}

		pm.Mark_completed(index)
		fmt.Printf("✓ Completed piece %d from %s\n", pw.Index, conn.RemoteAddr())
	}
}

func Download_piece_with_context(ctx context.Context, conn net.Conn, pw PieceWork, bitfield []bool, pm *PieceManager) ([]byte, error) {
	if pw.Index >= len(bitfield) || !bitfield[pw.Index] {
		return nil, fmt.Errorf("peer doesn't have piece %d", pw.Index)
	}

	buf := make([]byte, pw.Length)
	blocks := (pw.Length + block_size - 1) / block_size
	receivedBlocks := make([]bool, blocks)
	completedBlocks := 0

	// Request all blocks first
	for b := 0; b < blocks; b++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		begin := b * block_size
		reqLen := block_size
		if begin+block_size > pw.Length {
			reqLen = pw.Length - begin
		}

		req := new_request(pw.Index, begin, reqLen)

		// Set write timeout
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		err := Send_msg(conn, req)
		if err != nil {
			return nil, fmt.Errorf("send request failed: %w", err)
		}
	}

	// Receive blocks until we have them all
	for completedBlocks < blocks {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Set read timeout for each message
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		msg, err := Read_msg(conn)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}

		if msg == nil {
			continue // keep-alive message
		}

		if msg.Msg_id != MsgPiece {
			continue // ignore non-piece messages
		}

		if len(msg.Payload) < 8 {
			return nil, fmt.Errorf("short piece message")
		}

		// Validate the received block
		index := binary.BigEndian.Uint32(msg.Payload[0:4])
		beginResp := binary.BigEndian.Uint32(msg.Payload[4:8])
		blockData := msg.Payload[8:]

		if int(index) != pw.Index {
			continue // not for our piece
		}

		blockIndex := int(beginResp) / block_size
		if blockIndex >= blocks || receivedBlocks[blockIndex] {
			continue // invalid block or already received
		}

		// Validate block size
		expectedLen := block_size
		if blockIndex == blocks-1 && pw.Length%block_size != 0 {
			expectedLen = pw.Length % block_size
		}

		if len(blockData) != expectedLen {
			fmt.Printf("Unexpected block size: got %d, expected %d\n", len(blockData), expectedLen)
			continue
		}

		// Copy block data to buffer
		copy(buf[beginResp:], blockData)
		receivedBlocks[blockIndex] = true
		completedBlocks++

		pm.IncrementDownloadedBlocks()

		fmt.Printf("Received block %d/%d for piece %d (begin=%d, len=%d) from %s\n",
			completedBlocks, blocks, index, beginResp, len(blockData), conn.RemoteAddr())
	}

	// Verify piece hash
	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.Hash[:]) {
		return nil, fmt.Errorf("piece hash mismatch: piece %d", pw.Index)
	}

	fmt.Printf("✓ Downloaded and verified piece %d (%d bytes) from %s\n",
		pw.Index, pw.Length, conn.RemoteAddr())
	return buf, nil
}

func Download_piece(conn net.Conn, pw PieceWork, bitfield []bool, pm *PieceManager) ([]byte, error) {
	if pw.Index >= len(bitfield) || !bitfield[pw.Index] {
		return nil, fmt.Errorf("peer doesn't have piece %d", pw.Index)
	}

	buf := make([]byte, pw.Length)
	blocks := (pw.Length + block_size - 1) / block_size
	receivedBlocks := make([]bool, blocks)
	completedBlocks := 0

	for b := 0; b < blocks; b++ {
		begin := b * block_size
		reqLen := block_size
		if begin+block_size > pw.Length {
			reqLen = pw.Length - begin
		}

		req := new_request(pw.Index, begin, reqLen)
		err := Send_msg(conn, req)
		if err != nil {
			return nil, fmt.Errorf("send request failed: %w", err)
		}
	}

	// Receive blocks until we have them all
	for completedBlocks < blocks {
		msg, err := Read_msg(conn)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}

		if msg == nil {
			continue // keep-alive message
		}

		if msg.Msg_id != MsgPiece {
			continue // ignore non-piece messages
		}

		if len(msg.Payload) < 8 {
			return nil, fmt.Errorf("short piece message")
		}

		// Validate the received block
		index := binary.BigEndian.Uint32(msg.Payload[0:4])
		beginResp := binary.BigEndian.Uint32(msg.Payload[4:8])
		blockData := msg.Payload[8:]

		if int(index) != pw.Index {
			continue // not for our piece
		}

		blockIndex := int(beginResp) / block_size
		if blockIndex >= blocks || receivedBlocks[blockIndex] {
			continue // invalid block or already received
		}

		// Validate block size
		expectedLen := block_size
		if blockIndex == blocks-1 && pw.Length%block_size != 0 {
			expectedLen = pw.Length % block_size
		}

		if len(blockData) != expectedLen {
			fmt.Printf("unexpected block size: got %d, expected %d\n", len(blockData), expectedLen)
			continue
		}

		// Copy block data to buffer
		copy(buf[beginResp:], blockData)
		receivedBlocks[blockIndex] = true
		completedBlocks++

		pm.IncrementDownloadedBlocks()

		fmt.Printf("<-- received block %d/%d for piece %d (begin=%d, len=%d)\n",
			completedBlocks, blocks, index, beginResp, len(blockData))
	}

	// Verify piece hash
	hash := sha1.Sum(buf)
	if !bytes.Equal(hash[:], pw.Hash[:]) {
		return nil, fmt.Errorf("piece hash mismatch: piece %d", pw.Index)
	}

	fmt.Printf("✓ downloaded and verified piece %d (%d bytes)\n", pw.Index, pw.Length)
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
