package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/tracker"
)

func printDecodedData(data map[string]interface{}, hash interface{}) {
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

func main() {

	torrent_path := flag.String("t", "", "path to the .torrent file")

	flag.Parse()

	if *torrent_path == "" {
		fmt.Println("no file path!\nusage: ./gorrent -t=<path to .torrent file>")
		os.Exit(-1)
	}
	fmt.Printf("received .torrent path: %s\n", *torrent_path)

	metadata, hash, err := torrent.Read_torrent(*torrent_path)
	if err != nil {
		fmt.Println("\nERROR:\n", err)
		os.Exit(-1)
	}

	fmt.Println("successfully parsed the torrent metadata")
	printDecodedData(metadata, hash)

	peerId, _ := tracker.GeneratePeerID()
	req := tracker.Tracker_request{
		Announce:   metadata["announce"].(string),
		InfoHash:   hash.([20]byte),
		PeerID:     peerId,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       int64(metadata["info"].(map[string]interface{})["length"].(int)),
	}

	tracker_response, err := tracker.ContactTracker(req)
	if err != nil {
		fmt.Println("error contacting tracker")
	}

	fmt.Println("tracker response: ", tracker_response)
}
