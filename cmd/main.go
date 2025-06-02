package main

import (
	"crypto/rand"
	"flag"
	"os"

	"github.com/Bugra020/Gorrent/p2p"
	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/tracker"
	"github.com/Bugra020/Gorrent/utils"
)

func main() {
	torrent_path := flag.String("t", "", "path to the .torrent file")
	var debugMode bool
	flag.BoolVar(&debugMode, "d", false, "enables debug mode")
	flag.Parse()
	utils.Set_debug_mode(debugMode)

	if *torrent_path == "" {
		utils.Debuglog("no file path!\nusage: ./gorrent -t=<path to .torrent file>")
		os.Exit(-1)
	}
	utils.Debuglog("received .torrent path: %s\n", *torrent_path)

	torrent_file, err := torrent.Read_torrent(*torrent_path)
	if err != nil {
		utils.Debuglog("\nERROR:\n", err)
		os.Exit(-1)
	}
	peer_id := generatePeerID()
	torrent_file.PeerId = peer_id

	utils.Debuglog("successfully parsed the torrent metadata")
	torrent.PrintDecodedData(torrent_file)

	peer_list, err := tracker.Get_peers(torrent_file)
	p2p.Start_download(peer_list, torrent_file)
}

func generatePeerID() [20]byte {
	var peerID [20]byte
	copy(peerID[:], []byte("-GT0010-"))
	_, err := rand.Read(peerID[8:])
	if err != nil {
		utils.Debuglog("error generating peer id")
	}
	return peerID
}
