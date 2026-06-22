package frame

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

func writeVarints(buf *bytes.Buffer, values ...uint64) {
	for _, value := range values {
		_ = writeVarint(buf, value)
	}
}

func writeVarint(buf *bytes.Buffer, value uint64) error {
	switch {
	case value <= 63:
		buf.WriteByte(byte(value))
	case value <= 16383:
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(value)|0x4000)
		buf.Write(b[:])
	case value <= 1073741823:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(value)|0x80000000)
		buf.Write(b[:])
	case value <= 4611686018427387903:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], value|0xc000000000000000)
		buf.Write(b[:])
	default:
		return fmt.Errorf("value %d overflows QUIC varint", value)
	}
	return nil
}

func readVarint(r *bytes.Reader) (uint64, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	length := 1 << ((first & 0xc0) >> 6)
	buf := make([]byte, length)
	buf[0] = first
	if _, err := io.ReadFull(r, buf[1:]); err != nil {
		return 0, err
	}

	switch length {
	case 1:
		return uint64(first & 0x3f), nil
	case 2:
		return uint64(binary.BigEndian.Uint16(buf) & 0x3fff), nil
	case 4:
		return uint64(binary.BigEndian.Uint32(buf) & 0x3fffffff), nil
	case 8:
		return binary.BigEndian.Uint64(buf) & 0x3fffffffffffffff, nil
	default:
		return 0, fmt.Errorf("unsupported varint length %d", length)
	}
}
