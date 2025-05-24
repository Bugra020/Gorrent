package tracker

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Tracker_request struct {
	Announce   string
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64
}

func GeneratePeerID() ([20]byte, error) {
	var peerID [20]byte
	copy(peerID[:], []byte("-GT0010-"))
	_, err := rand.Read(peerID[8:])
	return peerID, err
}

func BuildURL(req Tracker_request) (string, error) {
	u, err := url.Parse(req.Announce)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	params.Set("info_hash", string(req.InfoHash[:]))
	params.Set("peer_id", string(req.PeerID[:]))
	params.Set("port", fmt.Sprintf("%d", req.Port))
	params.Set("uploaded", fmt.Sprintf("%d", req.Uploaded))
	params.Set("downloaded", fmt.Sprintf("%d", req.Downloaded))
	params.Set("left", fmt.Sprintf("%d", req.Left))
	params.Set("compact", "1")
	params.Set("event", "started")

	u.RawQuery = params.Encode()
	return u.String(), nil
}

func ContactTracker(req Tracker_request) ([]byte, error) {
	trackerURL, err := BuildURL(req)
	if err != nil {
		return nil, err
	}

	resp, err := http.Get(trackerURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
