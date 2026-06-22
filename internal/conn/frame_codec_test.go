package conn

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

const (
	testFrameWidth       = 640
	testFrameHeight      = 480
	testBytesPerPixel    = 2
	testFramePayloadSize = testFrameWidth * testFrameHeight * testBytesPerPixel
)

type PixelFormat string

const (
	FrameKindRGB   = FrameKind(0)
	FrameKindDepth = FrameKind(1)
)

func TestFrameHeaderEncodeDecode(t *testing.T) {
	original := FrameHeader{
		Kind:      FrameKindRGB,
		Serial:    42,
		Timestamp: time.Unix(1700000000, 123456789),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatYUYV,
		Size:      testFramePayloadSize,
	}

	var buf bytes.Buffer

	if err := EncodeFrameHeader(&buf, original); err != nil {
		t.Fatalf("encode frame header failed: %v", err)
	}

	decoded, err := DecodeFrameHeader(&buf)
	if err != nil {
		t.Fatalf("decode frame header failed: %v", err)
	}

	if decoded.Kind != original.Kind {
		t.Fatalf("unexpected kind: got=%v want=%v", decoded.Kind, original.Kind)
	}
	if decoded.Serial != original.Serial {
		t.Fatalf("unexpected serial: got=%d want=%d", decoded.Serial, original.Serial)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Fatalf("unexpected timestamp: got=%v want=%v", decoded.Timestamp, original.Timestamp)
	}
	if decoded.Width != original.Width {
		t.Fatalf("unexpected width: got=%d want=%d", decoded.Width, original.Width)
	}
	if decoded.Height != original.Height {
		t.Fatalf("unexpected height: got=%d want=%d", decoded.Height, original.Height)
	}
	if decoded.PixelFmt != original.PixelFmt {
		t.Fatalf("unexpected pixel format: got=%v want=%v", decoded.PixelFmt, original.PixelFmt)
	}
	if decoded.Size != original.Size {
		t.Fatalf("unexpected size: got=%d want=%d", decoded.Size, original.Size)
	}
}

func TestFrameHeaderDecodeRejectsTruncatedHeader(t *testing.T) {
	original := FrameHeader{
		Kind:      FrameKind(0),
		Serial:    1,
		Timestamp: time.Now(),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatYUYV,
		Size:      testFramePayloadSize,
	}

	var buf bytes.Buffer
	if err := EncodeFrameHeader(&buf, original); err != nil {
		t.Fatalf("encode frame header failed: %v", err)
	}

	raw := buf.Bytes()
	if len(raw) < 2 {
		t.Fatalf("encoded header too small: %d", len(raw))
	}

	truncated := raw[:len(raw)-1]

	_, err := DecodeFrameHeader(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected truncated header decode to fail")
	}
}

func TestValidateFramePayloadSizeAcceptsRGBYUYV(t *testing.T) {
	header := FrameHeader{
		Kind:     FrameKind(0),
		Width:    testFrameWidth,
		Height:   testFrameHeight,
		PixelFmt: PixelFormatYUYV,
		Size:     testFramePayloadSize,
	}

	if err := ValidateFramePayloadSize(header); err != nil {
		t.Fatalf("expected RGB YUYV frame size to be valid: %v", err)
	}
}

func TestValidateFramePayloadSizeAcceptsDepthZ16(t *testing.T) {
	header := FrameHeader{
		Kind:     FrameKind(1),
		Width:    testFrameWidth,
		Height:   testFrameHeight,
		PixelFmt: PixelFormatZ16,
		Size:     testFramePayloadSize,
	}

	if err := ValidateFramePayloadSize(header); err != nil {
		t.Fatalf("expected depth Z16 frame size to be valid: %v", err)
	}
}

func TestValidateFramePayloadSizeRejectsInvalidRGBSize(t *testing.T) {
	header := FrameHeader{
		Kind:     FrameKind(0),
		Width:    testFrameWidth,
		Height:   testFrameHeight,
		PixelFmt: PixelFormatYUYV,
		Size:     testFramePayloadSize - 1,
	}

	if err := ValidateFramePayloadSize(header); err == nil {
		t.Fatal("expected invalid RGB frame size to fail")
	}
}

func TestValidateFramePayloadSizeRejectsInvalidDepthSize(t *testing.T) {
	header := FrameHeader{
		Kind:     FrameKind(1),
		Width:    testFrameWidth,
		Height:   testFrameHeight,
		PixelFmt: PixelFormatZ16,
		Size:     testFramePayloadSize + 1,
	}

	if err := ValidateFramePayloadSize(header); err == nil {
		t.Fatal("expected invalid depth frame size to fail")
	}
}

func TestValidateFramePayloadSizeRejectsUnknownFrameKind(t *testing.T) {
	header := FrameHeader{
		Kind:     FrameKind(255),
		Width:    testFrameWidth,
		Height:   testFrameHeight,
		PixelFmt: PixelFormatYUYV,
		Size:     testFramePayloadSize,
	}

	if err := ValidateFramePayloadSize(header); err == nil {
		t.Fatal("expected unknown frame kind to fail")
	}
}

func TestValidateFramePayloadSizeRejectsUnknownPixelFormat(t *testing.T) {
	header := FrameHeader{
		Kind:     FrameKind(0),
		Width:    testFrameWidth,
		Height:   testFrameHeight,
		PixelFmt: "UNKNOWN",
		Size:     testFramePayloadSize,
	}

	if err := ValidateFramePayloadSize(header); err == nil {
		t.Fatal("expected unknown pixel format to fail")
	}
}

func TestWriteReadFrameRoundTrip(t *testing.T) {
	header := FrameHeader{
		Kind:      FrameKind(0),
		Serial:    100,
		Timestamp: time.Unix(1700000000, 0),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatYUYV,
		Size:      testFramePayloadSize,
	}

	payload := makeTestPayload(testFramePayloadSize, 0x11)

	var buf bytes.Buffer

	if err := WriteFrame(&buf, header, payload); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}

	gotHeader, gotPayload, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("read frame failed: %v", err)
	}

	if gotHeader.Kind != header.Kind {
		t.Fatalf("unexpected kind: got=%v want=%v", gotHeader.Kind, header.Kind)
	}
	if gotHeader.Serial != header.Serial {
		t.Fatalf("unexpected serial: got=%d want=%d", gotHeader.Serial, header.Serial)
	}
	if gotHeader.Width != header.Width || gotHeader.Height != header.Height {
		t.Fatalf("unexpected dimensions: got=%dx%d want=%dx%d", gotHeader.Width, gotHeader.Height, header.Width, header.Height)
	}
	if gotHeader.PixelFmt != header.PixelFmt {
		t.Fatalf("unexpected pixel format: got=%v want=%v", gotHeader.PixelFmt, header.PixelFmt)
	}
	if gotHeader.Size != header.Size {
		t.Fatalf("unexpected payload size in header: got=%d want=%d", gotHeader.Size, header.Size)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatal("payload mismatch after round trip")
	}
}

func TestWriteFrameRejectsPayloadSizeMismatch(t *testing.T) {
	header := FrameHeader{
		Kind:      FrameKind(0),
		Serial:    1,
		Timestamp: time.Now(),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatYUYV,
		Size:      testFramePayloadSize,
	}

	payload := makeTestPayload(testFramePayloadSize-1, 0x22)

	var buf bytes.Buffer

	if err := WriteFrame(&buf, header, payload); err == nil {
		t.Fatal("expected WriteFrame to reject payload size mismatch")
	}
}

func TestReadFrameHandlesPartialReads(t *testing.T) {
	header := FrameHeader{
		Kind:      FrameKind(1),
		Serial:    77,
		Timestamp: time.Unix(1700000001, 0),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatZ16,
		Size:      testFramePayloadSize,
	}

	payload := makeTestPayload(testFramePayloadSize, 0x33)

	var encoded bytes.Buffer
	if err := WriteFrame(&encoded, header, payload); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}

	reader := &chunkedReader{
		data:      encoded.Bytes(),
		chunkSize: 17,
	}

	gotHeader, gotPayload, err := ReadFrame(reader)
	if err != nil {
		t.Fatalf("read frame with partial reads failed: %v", err)
	}

	if gotHeader.Kind != FrameKind(1) {
		t.Fatalf("expected depth frame, got %v", gotHeader.Kind)
	}
	if gotHeader.Serial != 77 {
		t.Fatalf("expected serial 77, got %d", gotHeader.Serial)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatal("payload mismatch after partial reads")
	}
}

func TestReadFrameRejectsTruncatedPayload(t *testing.T) {
	header := FrameHeader{
		Kind:      FrameKind(0),
		Serial:    9,
		Timestamp: time.Now(),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatYUYV,
		Size:      testFramePayloadSize,
	}

	payload := makeTestPayload(testFramePayloadSize, 0x44)

	var encoded bytes.Buffer
	if err := WriteFrame(&encoded, header, payload); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}

	raw := encoded.Bytes()
	truncated := raw[:len(raw)-10]

	_, _, err := ReadFrame(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected truncated payload to fail")
	}
}

func TestReadFramePropagatesReaderError(t *testing.T) {
	expectedErr := errors.New("reader failed")

	_, _, err := ReadFrame(errReader{err: expectedErr})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected reader error %v, got %v", expectedErr, err)
	}
}

func TestFrameCodecDoesNotMutatePayload(t *testing.T) {
	header := FrameHeader{
		Kind:      FrameKind(0),
		Serial:    10,
		Timestamp: time.Now(),
		Width:     testFrameWidth,
		Height:    testFrameHeight,
		PixelFmt:  PixelFormatYUYV,
		Size:      testFramePayloadSize,
	}

	payload := makeTestPayload(testFramePayloadSize, 0x55)
	original := append([]byte(nil), payload...)

	var buf bytes.Buffer

	if err := WriteFrame(&buf, header, payload); err != nil {
		t.Fatalf("write frame failed: %v", err)
	}

	if !bytes.Equal(payload, original) {
		t.Fatal("WriteFrame mutated input payload")
	}
}

func makeTestPayload(size int, seed byte) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = seed + byte(i%251)
	}
	return payload
}

type chunkedReader struct {
	data      []byte
	chunkSize int
	offset    int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}

	n := r.chunkSize
	if n > len(p) {
		n = len(p)
	}

	remaining := len(r.data) - r.offset
	if n > remaining {
		n = remaining
	}

	copy(p[:n], r.data[r.offset:r.offset+n])
	r.offset += n

	return n, nil
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}
