package tracker

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/url"
	"time"
)

const (
	connectAction  = 0
	announceAction = 1
	protocolID     = 0x41727101980 // default magic constant
)

func generateTransactionID() (uint32, error) {
	var b [4]byte
	_, err := rand.Read(b[:])
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func ConnectToTracker(trackerURL string) (uint64, *net.UDPAddr, *net.UDPConn, error) {
	u, err := url.Parse(trackerURL)
	if err != nil {
		return 0, nil, nil, err
	}
	addr, err := net.ResolveUDPAddr("udp", u.Host)
	if err != nil {
		return 0, nil, nil, err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return 0, nil, nil, err
	}

	transactionID, _ := generateTransactionID()
	buf := new(bytes.Buffer)

	// send connect request
	binary.Write(buf, binary.BigEndian, uint64(protocolID)) // protocol ID
	binary.Write(buf, binary.BigEndian, uint32(connectAction))
	binary.Write(buf, binary.BigEndian, transactionID)

	_, err = conn.Write(buf.Bytes())
	if err != nil {
		return 0, nil, nil, err
	}

	resp := make([]byte, 16)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(resp)
	if err != nil || n < 16 {
		return 0, nil, nil, errors.New("failed to read connect response")
	}

	action := binary.BigEndian.Uint32(resp[0:4])
	respTxn := binary.BigEndian.Uint32(resp[4:8])
	if action != connectAction || respTxn != transactionID {
		return 0, nil, nil, errors.New("invalid connect response")
	}
	connectionID := binary.BigEndian.Uint64(resp[8:16])
	return connectionID, addr, conn, nil
}

func AnnounceToTracker(conn *net.UDPConn, addr *net.UDPAddr, connID uint64, infoHash, peerID [20]byte, left int64, port uint16) ([]PeerData, error) {
	transactionID, _ := generateTransactionID()
	buf := new(bytes.Buffer)

	// announce request
	binary.Write(buf, binary.BigEndian, connID)
	binary.Write(buf, binary.BigEndian, uint32(announceAction))
	binary.Write(buf, binary.BigEndian, transactionID)
	buf.Write(infoHash[:])
	buf.Write(peerID[:])
	binary.Write(buf, binary.BigEndian, uint64(0))          // downloaded
	binary.Write(buf, binary.BigEndian, uint64(left))       // left
	binary.Write(buf, binary.BigEndian, uint64(0))          // uploaded
	binary.Write(buf, binary.BigEndian, uint32(0))          // event
	binary.Write(buf, binary.BigEndian, uint32(0))          // IP
	binary.Write(buf, binary.BigEndian, uint32(0xDEADBEEF)) // key
	binary.Write(buf, binary.BigEndian, int32(-1))          // num_want
	binary.Write(buf, binary.BigEndian, port)

	_, err := conn.Write(buf.Bytes())
	if err != nil {
		return nil, err
	}

	resp := make([]byte, 1500)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := conn.ReadFromUDP(resp)
	if err != nil || n < 20 {
		return nil, errors.New("announce response too short")
	}

	action := binary.BigEndian.Uint32(resp[0:4])
	respTxn := binary.BigEndian.Uint32(resp[4:8])
	if action != announceAction || respTxn != transactionID {
		return nil, errors.New("invalid announce response")
	}

	var peers []PeerData
	for i := 20; i+6 <= n; i += 6 {
		ip := net.IPv4(resp[i], resp[i+1], resp[i+2], resp[i+3])
		port := binary.BigEndian.Uint16(resp[i+4 : i+6])
		peers = append(peers, PeerData{Ip: ip.String(), Port: int(port)})
	}

	return peers, nil
}
