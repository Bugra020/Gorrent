package p2p

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/Bugra020/Gorrent/utils"
)

/*
| length (4 bytes) | message ID (1 byte) | payload (optional) |
*/

const ( //payload y/n bits
	MsgChoke         = 0 // n
	MsgUnchoke       = 1 // n
	MsgInterested    = 2 // n
	MsgNotInterested = 3 // n
	MsgHave          = 4 // y 4 bytes piece index
	MsgBitfield      = 5 // y bitfield
	MsgRequest       = 6 // y 12 bytes
	MsgPiece         = 7 // y piece index + begin + block
	MsgCancel        = 8 // y 12 bytes
	MsgPort          = 9 // y 2 bytes
)

type Message struct {
	Msg_id  byte
	Payload []byte
}

func message_name(id byte) string {
	switch id {
	case MsgChoke:
		return "Choke"
	case MsgUnchoke:
		return "Unchoke"
	case MsgInterested:
		return "Interested"
	case MsgNotInterested:
		return "NotInterested"
	case MsgHave:
		return "Have"
	case MsgBitfield:
		return "Bitfield"
	case MsgRequest:
		return "Request"
	case MsgPiece:
		return "Piece"
	case MsgCancel:
		return "Cancel"
	case MsgPort:
		return "Port"
	default:
		return fmt.Sprintf("unknown id %d", id)
	}
}

func (m *Message) Serialize() []byte {
	length := len(m.Payload) + 1

	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], uint32(length))
	buf[4] = m.Msg_id
	copy(buf[5:], m.Payload)
	return buf
}

func empty_bitfield(num_pieces int) []byte {
	lenght := (num_pieces + 7) / 8
	return make([]byte, lenght)
}

func send_bitfield(conn net.Conn, bitfield []byte) error {
	msg := &Message{
		Msg_id:  MsgBitfield,
		Payload: bitfield,
	}
	return Send_msg(conn, msg)
}

func parse_bitfield(raw []byte, numPieces int) []bool {
	bits := make([]bool, numPieces)
	for i := 0; i < numPieces; i++ {
		byteIndex := i / 8
		bitOffset := 7 - (i % 8)
		if byteIndex >= len(raw) {
			break
		}
		bits[i] = (raw[byteIndex]>>bitOffset)&1 == 1
	}
	return bits
}

func new_request(index int, offset int, length int) *Message {
	//request =
	// 4byte length + 1byte msg id(6)
	// + 4byte piece index + 4byte offset + 4byte length
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], uint32(index))
	binary.BigEndian.PutUint32(payload[4:8], uint32(offset))
	binary.BigEndian.PutUint32(payload[8:], uint32(length))

	return &Message{
		Msg_id:  MsgRequest,
		Payload: payload,
	}
}

func Send_msg(w io.Writer, m *Message) error {
	_, err := w.Write(m.Serialize())
	if err != nil {
		return err
	}

	utils.Debuglog("--> sent message: %s Payload length=%d\n", message_name(m.Msg_id), len(m.Payload))
	return nil
}

func Read_msg(r io.Reader) (*Message, error) {
	var lenght_buf [4]byte
	_, err := io.ReadFull(r, lenght_buf[:])
	if err != nil {
		return nil, err
	}

	lenght := binary.BigEndian.Uint32(lenght_buf[:])
	if lenght == 0 {
		return nil, nil
	}

	msg_buf := make([]byte, lenght)
	_, err = io.ReadFull(r, msg_buf)
	if err != nil {
		return nil, err
	}

	msg := &Message{
		Msg_id:  msg_buf[0],
		Payload: msg_buf[1:],
	}

	return msg, nil
}
