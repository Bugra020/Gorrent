package tracker

import (
	"fmt"
	"net/url"

	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/utils"
)

type tracker_request struct {
	Announce   string
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64
}

type PeerData struct {
	Ip   string
	Port int
}

func Get_peers(t *torrent.Torrent) ([]PeerData, error) {
	trackerURLs, ok := t.Announce.([]string)
	if !ok {
		return nil, fmt.Errorf("invalid Announce format")
	}

	for _, trackerURL := range trackerURLs {
		u, err := url.Parse(trackerURL)
		if err != nil {
			utils.Debuglog("invalid tracker URL: %v\n", err)
			continue
		}

		switch u.Scheme {
		case "http", "https":
			peers, err := getHTTPPeers(trackerURL, t, t.PeerId)
			if err == nil {
				return peers, nil
			}
			utils.Debuglog("HTTP tracker failed: %v\n", err)

		case "udp":
			connID, addr, conn, err := ConnectToTracker(trackerURL)
			if err != nil {
				utils.Debuglog("failed UDP connect: %v\n", err)
				continue
			}
			defer conn.Close()

			peers, err := AnnounceToTracker(conn, addr, connID, t.Info_hash, t.PeerId, int64(t.Length), 6881)
			if err != nil {
				utils.Debuglog("failed UDP announce: %v\n", err)
				continue
			}
			return peers, nil

		default:
			utils.Debuglog("unsupported tracker scheme: %s\n", u.Scheme)
		}
	}

	return nil, fmt.Errorf("no valid tracker responded")
}
