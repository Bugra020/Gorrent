package peer_manager

import (
	"encoding/binary"
	"io"
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

func (m *Message) Serialize() []byte {
	length := len(m.Payload) + 1

	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], uint32(length))
	buf[4] = m.Msg_id
	copy(buf[5:], m.Payload)
	return buf
}

func Read_msg(r io.Reader) (*Message, error) {
	var lenght_buf [4]byte
	_, err := io.ReadFull(r, lenght_buf[:])
	if err != nil {
		return nil, err
	}

	lenght := binary.BigEndian.Uint32(lenght_buf[:])
	if lenght == 0 {
		return nil, nil //keep alive
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
