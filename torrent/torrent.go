package torrent

import (
	"errors"
	"fmt"
	"os"
)

type Torrent struct {
	Name       string
	Path       string
	Info_hash  [20]byte
	Length     int
	Piece_len  int
	Num_pieces int
	Announce   interface{}
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

	torrent_file := Torrent{
		Name:       dict["info"].(map[string]interface{})["name"].(string),
		Path:       path,
		Info_hash:  parsed.Hash,
		Length:     (dict["info"].(map[string]interface{}))["length"].(int),
		Piece_len:  (dict["info"].(map[string]interface{}))["piece length"].(int),
		Num_pieces: len((dict["info"].(map[string]interface{}))["pieces"].(string)) / 20,
		Announce:   dict["announce"],
	}

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
