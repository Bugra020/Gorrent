package torrent

import (
	"errors"
	"fmt"
	"os"
)

func Read_torrent(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read the .torrent file: %w", err)
	}

	result, err := decodeBencode(data)
	if err != nil {
		return nil, err
	}

	dict, ok := result.(map[string]interface{})
	if !ok {
		return nil, errors.New("torrent file invalid!!!")
	}

	return dict, nil
}
