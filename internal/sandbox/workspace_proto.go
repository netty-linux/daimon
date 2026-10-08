package sandbox

import (
	"encoding/binary"
	"errors"
)

var errWorkspaceWire = errors.New("workspace_protocol")

func wi(n int, v uint64) []byte {
	return append(binary.AppendUvarint(nil, uint64(n<<3)), binary.AppendUvarint(nil, v)...)
}
func wb(n int, v []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(n<<3|2))
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}
func ws(n int, v string) []byte { return wb(n, []byte(v)) }

type wireValue struct {
	number uint64
	data   []byte
	wire   uint64
}

func wire(raw []byte) (map[int][]wireValue, error) {
	m := map[int][]wireValue{}
	for count := 0; len(raw) > 0; count++ {
		if count > 512 {
			return nil, errWorkspaceWire
		}
		tag, n := binary.Uvarint(raw)
		if n <= 0 || tag>>3 == 0 {
			return nil, errWorkspaceWire
		}
		raw = raw[n:]
		v := wireValue{wire: tag & 7}
		switch v.wire {
		case 0:
			x, n := binary.Uvarint(raw)
			if n <= 0 {
				return nil, errWorkspaceWire
			}
			v.number = x
			raw = raw[n:]
		case 2:
			l, n := binary.Uvarint(raw)
			if n <= 0 || l > uint64(len(raw)-n) {
				return nil, errWorkspaceWire
			}
			v.data = raw[n : n+int(l)]
			raw = raw[n+int(l):]
		default:
			return nil, errWorkspaceWire
		}
		k := int(tag >> 3)
		m[k] = append(m[k], v)
	}
	return m, nil
}
func only(m map[int][]wireValue, n int) wireValue {
	if len(m[n]) != 1 {
		return wireValue{wire: 99}
	}
	return m[n][0]
}
func number(m map[int][]wireValue, n int) (uint64, error) {
	if len(m[n]) == 0 {
		return 0, nil
	}
	v := only(m, n)
	if v.wire != 0 {
		return 0, errWorkspaceWire
	}
	return v.number, nil
}
func textField(m map[int][]wireValue, n int) (string, error) {
	if len(m[n]) == 0 {
		return "", nil
	}
	v := only(m, n)
	if v.wire != 2 {
		return "", errWorkspaceWire
	}
	return string(v.data), nil
}
