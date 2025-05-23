package torrent

import (
	"errors"
	"strconv"
)

type decoder struct {
	data []byte
	pos  int
}

func decodeBencode(data []byte) (interface{}, error) {
	dec := decoder{data: data}
	return dec.decode()
}

func (d *decoder) decode() (interface{}, error) {
	if d.pos >= len(d.data) {
		return nil, errors.New("unexpected end of data")
	}
	switch d.data[d.pos] {
	case 'i':
		return d.decodeInt()
	case 'l':
		return d.decodeList()
	case 'd':
		return d.decodeDict()
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return d.decodeString()
	default:
		return nil, errors.New("invalid bencode prefix")
	}
}

func (d *decoder) decodeInt() (int, error) {
	d.pos++
	start := d.pos
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		d.pos++
	}
	if d.pos == len(d.data) {
		return 0, errors.New("unterminated integer")
	}
	val, err := strconv.Atoi(string(d.data[start:d.pos]))
	d.pos++
	return val, err
}

func (d *decoder) decodeString() (string, error) {
	start := d.pos
	for d.pos < len(d.data) && d.data[d.pos] != ':' {
		d.pos++
	}
	if d.pos == len(d.data) {
		return "", errors.New("invalid string format")
	}
	length, err := strconv.Atoi(string(d.data[start:d.pos]))
	if err != nil {
		return "", err
	}
	d.pos++
	if d.pos+length > len(d.data) {
		return "", errors.New("string out of range")
	}
	str := string(d.data[d.pos : d.pos+length])
	d.pos += length
	return str, nil
}

func (d *decoder) decodeList() ([]interface{}, error) {
	d.pos++
	var list []interface{}
	for {
		if d.pos >= len(d.data) {
			return nil, errors.New("unterminated list")
		}
		if d.data[d.pos] == 'e' {
			d.pos++
			break
		}
		val, err := d.decode()
		if err != nil {
			return nil, err
		}
		list = append(list, val)
	}
	return list, nil
}

func (d *decoder) decodeDict() (map[string]interface{}, error) {
	d.pos++
	dict := make(map[string]interface{})
	for {
		if d.pos >= len(d.data) {
			return nil, errors.New("unterminated dict")
		}
		if d.data[d.pos] == 'e' {
			d.pos++
			break
		}
		key, err := d.decodeString()
		if err != nil {
			return nil, err
		}
		val, err := d.decode()
		if err != nil {
			return nil, err
		}
		dict[key] = val
	}
	return dict, nil
}
