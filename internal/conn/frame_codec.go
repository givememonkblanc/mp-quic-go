package conn

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	PixelFormatYUYV  = "YUYV"
	PixelFormatZ16   = "Z16"
	PixelFormatRGB24 = "RGB24"
)

const FrameHeaderSize = 37

var mpquicMagic = []byte{0x4D, 0x50, 0x51, 0x31}

type FrameHeader struct {
	Kind      FrameKind
	Serial    uint64
	Timestamp time.Time
	Width     int
	Height    int
	PixelFmt  string
	Size      uint32
}

type FrameKind byte

func EncodeFrameHeader(w io.Writer, header FrameHeader) error {
	buf := new(bytes.Buffer)

	buf.WriteByte(byte(header.Kind))

	if err := binary.Write(buf, binary.LittleEndian, header.Serial); err != nil {
		return err
	}

	timestamp := header.Timestamp.UnixNano()
	if err := binary.Write(buf, binary.LittleEndian, timestamp); err != nil {
		return err
	}

	if err := binary.Write(buf, binary.LittleEndian, uint32(header.Width)); err != nil {
		return err
	}

	if err := binary.Write(buf, binary.LittleEndian, uint32(header.Height)); err != nil {
		return err
	}

	pixelFmtBytes := make([]byte, 8)
	copy(pixelFmtBytes, header.PixelFmt)
	if _, err := buf.Write(pixelFmtBytes); err != nil {
		return err
	}

	if err := binary.Write(buf, binary.LittleEndian, header.Size); err != nil {
		return err
	}

	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}

	return nil
}

func DecodeFrameHeader(r io.Reader) (FrameHeader, error) {
	buf := new(bytes.Buffer)
	if _, err := io.CopyN(buf, r, FrameHeaderSize); err != nil {
		return FrameHeader{}, err
	}

	reader := bytes.NewReader(buf.Bytes())

	var kind byte
	if err := binary.Read(reader, binary.LittleEndian, &kind); err != nil {
		return FrameHeader{}, err
	}

	var serial uint64
	if err := binary.Read(reader, binary.LittleEndian, &serial); err != nil {
		return FrameHeader{}, err
	}

	var timestamp int64
	if err := binary.Read(reader, binary.LittleEndian, &timestamp); err != nil {
		return FrameHeader{}, err
	}

	var width, height uint32
	if err := binary.Read(reader, binary.LittleEndian, &width); err != nil {
		return FrameHeader{}, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &height); err != nil {
		return FrameHeader{}, err
	}

	var pixelFmtBytes [8]byte
	if _, err := reader.Read(pixelFmtBytes[:]); err != nil {
		return FrameHeader{}, err
	}
	pixelFmt := string(pixelFmtBytes[:])
	pixelFmt = pixelFmt[:bytes.IndexByte(pixelFmtBytes[:], 0)]

	var size uint32
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		return FrameHeader{}, err
	}

	return FrameHeader{
		Kind:      FrameKind(kind),
		Serial:    serial,
		Timestamp: time.Unix(0, timestamp),
		Width:     int(width),
		Height:    int(height),
		PixelFmt:  pixelFmt,
		Size:      size,
	}, nil
}

func ValidateFramePayloadSize(header FrameHeader) error {
	if header.Kind != FrameKind(0) && header.Kind != FrameKind(1) {
		return fmt.Errorf("unknown frame kind: %d", header.Kind)
	}

	var expectedSize int
	switch header.PixelFmt {
	case "YUYV", "Z16":
		expectedSize = header.Width * header.Height * 2
	case "RGB24":
		expectedSize = header.Width * header.Height * 3
	default:
		return fmt.Errorf("unknown pixel format: %s", header.PixelFmt)
	}

	if expectedSize != int(header.Size) {
		return fmt.Errorf("payload size mismatch: expected %d, got %d", expectedSize, header.Size)
	}

	return nil
}

func WriteFrame(w io.Writer, header FrameHeader, payload []byte) error {
	if err := EncodeFrameHeader(w, header); err != nil {
		return err
	}

	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: expected %d, got %d", header.Size, len(payload))
	}

	if _, err := w.Write(payload); err != nil {
		return err
	}

	return nil
}

func ReadFrame(r io.Reader) (FrameHeader, []byte, error) {
	header, err := DecodeFrameHeader(r)
	if err != nil {
		return FrameHeader{}, nil, err
	}

	payload := make([]byte, header.Size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return FrameHeader{}, nil, err
	}

	return header, payload, nil
}

func ReadJetsonFrame(r io.Reader) (FrameHeader, []byte, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return FrameHeader{}, nil, fmt.Errorf("failed to read magic: %w", err)
	}
	if !bytes.Equal(magic[:], mpquicMagic) {
		return FrameHeader{}, nil, fmt.Errorf("invalid magic: got %x, want %x", magic[:], mpquicMagic)
	}

	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return FrameHeader{}, nil, fmt.Errorf("failed to read length: %w", err)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return FrameHeader{}, nil, fmt.Errorf("failed to read payload: %w", err)
	}

	return FrameHeader{
		Kind:     FrameKind(payload[0]),
		Serial:   0,
		Width:    640,
		Height:   480,
		PixelFmt: "JPEG",
		Size:     length - 1,
	}, payload[1:], nil
}
