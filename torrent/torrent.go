package torrent

import (
	"errors"
	"fmt"
	"os"
)

func Read_torrent(path string) (map[string]interface{}, interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read the .torrent file: %w", err)
	}

	parsed, err := DecodeBencode(data)
	if err != nil {
		return nil, nil, err
	}

	dict, ok := parsed.Data.(map[string]interface{})
	if !ok {
		return nil, nil, errors.New("torrent file invalid!!!")
	}

	return dict, parsed.Hash, nil
}

func PrintDecodedData(data map[string]interface{}, hash interface{}) {
	for k, v := range data {
		if k == "info" {
			infoDict, ok := v.(map[string]interface{})
			if !ok {
				fmt.Println("info field is not a dictionary")
				continue
			}
			fmt.Println("Info dictionary:")
			fmt.Printf("	info SHA1 hash: %x\n", hash)
			for ik, iv := range infoDict {
				if ik == "pieces" {
					pieces, ok := iv.(string)
					if ok {
						fmt.Printf("	%s: %d SHA1 hashes\n", ik, len(pieces)/20)
					}
				} else {
					fmt.Printf("	%s: %v\n", ik, iv)
				}
			}
		} else {
			fmt.Printf("%s: %v\n", k, v)
		}
	}
}
