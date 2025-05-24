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

	dict, ok := parsed.data.(map[string]interface{})
	if !ok {
		return nil, nil, errors.New("torrent file invalid!!!")
	}

	return dict, parsed.hash, nil
}
