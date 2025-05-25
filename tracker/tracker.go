package tracker

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/Bugra020/Gorrent/torrent"
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

type Peer struct {
	Ip   string
	Port int
}

func Get_peers(t *torrent.Torrent, peer_id [20]byte) ([]Peer, error) {
	req := tracker_request{
		Announce:   t.Announce.(string),
		InfoHash:   t.Info_hash,
		PeerID:     peer_id,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       int64(t.Length),
	}

	response, err := contactTracker(req)
	if err != nil {
		fmt.Println("\nERROR:\n", err)
		return nil, err
	}

	decoded_response, err := torrent.DecodeBencode(response)
	if err != nil {
		fmt.Println("\nERROR:\n", err)
		return nil, err
	}

	peer_list, err := parse_response(decoded_response)
	if err != nil {
		fmt.Println("\nERROR:\n", err)
		return nil, err
	}

	return peer_list, nil
}

func parse_response(response *torrent.Parsed) ([]Peer, error) {
	rootDict, ok := response.Data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid tracker response: not a dictionary")
	}

	peersRaw, ok := rootDict["peers"]
	if !ok {
		return nil, fmt.Errorf("tracker response missing 'peers'")
	}

	switch v := peersRaw.(type) {
	case string:
		return parseCompactPeers(v), nil
	case []interface{}:
		var peers []Peer
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			ip := m["ip"].(string)
			port := int(m["port"].(int))
			peers = append(peers, Peer{Ip: ip, Port: port})
		}
		return peers, nil
	default:
		return nil, fmt.Errorf("unknown 'peers' format")
	}
}

func parseCompactPeers(peers string) []Peer {
	data := []byte(peers)
	var result []Peer

	for i := 0; i+6 <= len(data); i += 6 {
		ip := net.IPv4(data[i], data[i+1], data[i+2], data[i+3]).String()
		port := int(binary.BigEndian.Uint16(data[i+4 : i+6]))
		result = append(result, Peer{Ip: ip, Port: port})
	}
	return result
}

func buildURL(req tracker_request) (string, error) {
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

func contactTracker(req tracker_request) ([]byte, error) {
	trackerURL, err := buildURL(req)
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
