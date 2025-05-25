package torrent

import (
	"errors"
	"fmt"
	"os"
)

type Torrent struct {
	Name        string
	Path        string
	Info_hash   [20]byte
	Length      int
	Piece_len   int
	Num_pieces  int
	Pieces      [][20]byte
	Announce    interface{}
	Output_file *os.File
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
		return nil, errors.New("torrent file invalid!!!")
	}

	piecesRaw := dict["info"].(map[string]interface{})["pieces"].(string)
	pieces := []byte(piecesRaw)
	if len(pieces)%20 != 0 {
		return nil, errors.New("invalid pieces length")
	}
	numPieces := len(pieces) / 20
	hashes := make([][20]byte, numPieces)
	for i := 0; i < numPieces; i++ {
		copy(hashes[i][:], pieces[i*20:(i+1)*20])
	}

	torrent_file := Torrent{
		Name:       dict["info"].(map[string]interface{})["name"].(string),
		Path:       path,
		Info_hash:  parsed.Hash,
		Length:     (dict["info"].(map[string]interface{}))["length"].(int),
		Piece_len:  (dict["info"].(map[string]interface{}))["piece length"].(int),
		Num_pieces: len((dict["info"].(map[string]interface{}))["pieces"].(string)) / 20,
		Pieces:     hashes,
		Announce:   dict["announce"],
	}

	output_path := "output_files\\" + torrent_file.Name
	output_file, err := os.Create(output_path)
	if err != nil {
		fmt.Printf("failed to create output file: %v", err)
		os.Exit(-1)
	}
	torrent_file.Output_file = output_file

	return &torrent_file, nil
}

func PrintDecodedData(t *Torrent) {
	fmt.Printf("name: %s\n", t.Name)
	fmt.Printf("path: %s\n", t.Path)
	fmt.Printf("info_hash: %x\n", t.Info_hash)
	fmt.Printf("lenght: %d\n", t.Length)
	fmt.Printf("piece_len: %d\n", t.Piece_len)
	fmt.Printf("num_pieces: %d\n", t.Num_pieces)
	fmt.Printf("announce: %v\n", t.Announce)
}
