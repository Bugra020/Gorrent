package tracker

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/Bugra020/Gorrent/torrent"
	"github.com/Bugra020/Gorrent/utils"
)

func getHTTPPeers(trackerURL string, t *torrent.Torrent, peer_id [20]byte) ([]PeerData, error) {
	req := tracker_request{
		Announce:   trackerURL,
		InfoHash:   t.Info_hash,
		PeerID:     peer_id,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       int64(t.Length),
	}

	response, err := contactTracker(req)
	if err != nil {
		utils.Debuglog("\nERROR:\n", err)
		return nil, err
	}

	decoded_response, err := torrent.DecodeBencode(response)
	if err != nil {
		utils.Debuglog("\nERROR:\n", err)
		return nil, err
	}

	peer_list, err := parse_response(decoded_response)
	if err != nil {
		utils.Debuglog("\nERROR:\n", err)
		return nil, err
	}

	return peer_list, nil
}

func parse_response(response *torrent.Parsed) ([]PeerData, error) {
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
		var peers []PeerData
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			ip := m["ip"].(string)
			port := int(m["port"].(int))
			peers = append(peers, PeerData{Ip: ip, Port: port})
		}
		return peers, nil
	default:
		return nil, fmt.Errorf("unknown 'peers' format")
	}
}

func parseCompactPeers(peers string) []PeerData {
	data := []byte(peers)
	var result []PeerData

	for i := 0; i+6 <= len(data); i += 6 {
		ip := net.IPv4(data[i], data[i+1], data[i+2], data[i+3]).String()
		port := int(binary.BigEndian.Uint16(data[i+4 : i+6]))
		result = append(result, PeerData{Ip: ip, Port: port})
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

	utils.Debuglog("Contacting tracker: %s\n", trackerURL)

	resp, err := http.Get(trackerURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tracker returned non-200 status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}
