package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Bugra020/Gorrent/peer_manager"
	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/tracker"
)

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
	peer_id := generatePeerID()

	fmt.Println("successfully parsed the torrent metadata")
	torrent.PrintDecodedData(metadata, hash)

	peer_list, err := tracker.Get_peers(metadata, hash.([20]byte), peer_id)
	conns := peer_manager.Connect_to_peers(peer_list, 5*time.Second, 100)

	peer_manager.Do_handshakes(conns, hash.([20]byte), peer_id)
}

func generatePeerID() [20]byte {
	var peerID [20]byte
	copy(peerID[:], []byte("-GT0010-"))
	_, err := rand.Read(peerID[8:])
	if err != nil {
		fmt.Println("erro generating peer id")
	}
	return peerID
}
