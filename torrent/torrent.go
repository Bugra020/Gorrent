package torrent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Bugra020/Gorrent/utils"
)

type FileInfo struct {
	Path   []string // path components
	Length int      // file length in bytes
}

type Torrent struct {
	PeerId      [20]byte
	Name        string
	Path        string
	Info_hash   [20]byte
	Length      int // total length for single file, sum of all files for multi-file
	Piece_len   int
	Num_pieces  int
	Pieces      [][20]byte
	Announce    interface{}
	Files       []FileInfo // empty for single file torrents
	IsMultiFile bool
	OutputDir   string // directory where files will be saved
}

func Read_torrent(path string) (*Torrent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read the .torrent file: %w", err)
	}

	parsed, err := DecodeBencode(data)
	if err != nil {
		return nil, err
	}

	dict, ok := parsed.Data.(map[string]interface{})
	if !ok {
		return nil, errors.New("torrent file invalid")
	}

	infoDict := dict["info"].(map[string]interface{})
	piecesRaw := infoDict["pieces"].(string)
	pieces := []byte(piecesRaw)
	if len(pieces)%20 != 0 {
		return nil, errors.New("invalid pieces length")
	}
	numPieces := len(pieces) / 20
	hashes := make([][20]byte, numPieces)
	for i := 0; i < numPieces; i++ {
		copy(hashes[i][:], pieces[i*20:(i+1)*20])
	}

	var trackers []string
	if ann, ok := dict["announce"].(string); ok && ann != "" {
		trackers = append(trackers, ann)
	}

	if al, ok := dict["announce-list"].([]interface{}); ok {
		for _, tier := range al {
			if tierList, ok := tier.([]interface{}); ok {
				for _, url := range tierList {
					if s, ok := url.(string); ok {
						trackers = append(trackers, s)
					}
				}
			}
		}
	}

	if len(trackers) == 0 {
		return nil, errors.New("no tracker URL found in announce or announce-list")
	}

	torrent_file := Torrent{
		Name:       infoDict["name"].(string),
		Path:       path,
		Info_hash:  parsed.Hash,
		Piece_len:  infoDict["piece length"].(int),
		Num_pieces: numPieces,
		Pieces:     hashes,
		Announce:   trackers,
	}

	// Check if it's a multi-file torrent
	if files, exists := infoDict["files"]; exists {
		torrent_file.IsMultiFile = true
		filesList := files.([]interface{})
		torrent_file.Files = make([]FileInfo, len(filesList))
		totalLength := 0

		for i, file := range filesList {
			fileDict := file.(map[string]interface{})
			length := fileDict["length"].(int)
			pathList := fileDict["path"].([]interface{})

			pathStrings := make([]string, len(pathList))
			for j, pathComponent := range pathList {
				pathStrings[j] = pathComponent.(string)
			}

			torrent_file.Files[i] = FileInfo{
				Path:   pathStrings,
				Length: length,
			}
			totalLength += length
		}
		torrent_file.Length = totalLength
	} else {
		torrent_file.IsMultiFile = false
		torrent_file.Length = infoDict["length"].(int)
	}

	outputDir := filepath.Join("output_files", torrent_file.Name)
	err = os.MkdirAll(outputDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create output directory: %v", err)
	}
	torrent_file.OutputDir = outputDir

	return &torrent_file, nil
}

func PrintDecodedData(t *Torrent) {
	utils.Debuglog("name: %s\n", t.Name)
	utils.Debuglog("path: %s\n", t.Path)
	utils.Debuglog("info_hash: %x\n", t.Info_hash)
	utils.Debuglog("length: %d\n", t.Length)
	utils.Debuglog("piece_len: %d\n", t.Piece_len)
	utils.Debuglog("num_pieces: %d\n", t.Num_pieces)
	utils.Debuglog("announce: %v\n", t.Announce)
	utils.Debuglog("multi-file: %t\n", t.IsMultiFile)
	if t.IsMultiFile {
		utils.Debuglog("files (%d):\n", len(t.Files))
		for i, file := range t.Files {
			utils.Debuglog("  %d: %s (%d bytes)\n", i, filepath.Join(file.Path...), file.Length)
		}
	}
}
