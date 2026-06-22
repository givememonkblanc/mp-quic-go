package conn

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"

	"github.com/quic-go/quic-go"
	"mp-quic-go/internal/handler"
)

var jetsonMagic = []byte{0x4D, 0x50, 0x51, 0x31}

// StreamHandler handles a single stream lifecycle.
type StreamHandler struct {
	logger        *slog.Logger
	handler       handler.Handler
	jetsonHandler *handler.JetsonHandler
}

func NewStreamHandler(logger *slog.Logger, streamHandler handler.Handler, jetsonHandler ...*handler.JetsonHandler) *StreamHandler {
	sh := &StreamHandler{
		logger:  logger,
		handler: streamHandler,
	}
	if len(jetsonHandler) > 0 && jetsonHandler[0] != nil {
		sh.jetsonHandler = jetsonHandler[0]
	}
	return sh
}

func (h *StreamHandler) Handle(ctx context.Context, stream quic.Stream) error {
	defer stream.Close()

	if h.jetsonHandler != nil {
		if err := h.handleJetsonStream(ctx, stream); err == nil {
			return nil
		}
	}

	buffer := make([]byte, 64*1024)
	n, err := stream.Read(buffer)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("read stream %d: %w", stream.StreamID(), err)
	}

	payload := buffer[:n]
	h.logger.Debug("received data", "len", len(payload), "stream_id", stream.StreamID())

	response, err := h.handler.Handle(ctx, payload)
	if err != nil {
		return fmt.Errorf("handle stream %d payload: %w", stream.StreamID(), err)
	}

	if _, err := stream.Write(response); err != nil {
		return fmt.Errorf("write stream %d: %w", stream.StreamID(), err)
	}

	return nil
}

func (h *StreamHandler) HandleReader(ctx context.Context, r io.Reader) error {
	payload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return fmt.Errorf("empty payload")
	}

	_, err = h.handler.Handle(ctx, payload)
	return err
}

func (h *StreamHandler) handleJetsonStream(ctx context.Context, stream quic.Stream) error {
	fmt.Println("[JetsonStream] Starting continuous stream...")
	ack := []byte{0x41, 0x43} // "AC"
	frameCount := 0

	for {
		// Read magic (4 bytes)
		magic := make([]byte, 4)
		_, err := io.ReadFull(stream, magic)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				fmt.Printf("[JetsonStream] Client closed after %d frames\n", frameCount)
				return nil
			}
			return fmt.Errorf("read jetson magic: %w", err)
		}

		if !bytes.Equal(magic, jetsonMagic) {
			return fmt.Errorf("bad magic: %v (expected MPQ1)", magic)
		}

		// Read length (4 bytes big-endian)
		lengthBuf := make([]byte, 4)
		if _, err := io.ReadFull(stream, lengthBuf); err != nil {
			return fmt.Errorf("read jetson length: %w", err)
		}
		length := binary.BigEndian.Uint32(lengthBuf)
		if length < 1 {
			return fmt.Errorf("payload too short: %d", length)
		}

		// Read payload
		payload := make([]byte, length)
		if _, err := io.ReadFull(stream, payload); err != nil {
			return fmt.Errorf("read jetson payload: %w", err)
		}

		kind := payload[0]
		var frameKind handler.FrameKind
		switch kind {
		case 0x00:
			frameKind = handler.FrameKindDepth
		case 0x01:
			frameKind = handler.FrameKindRGB
		default:
			return fmt.Errorf("unknown jetson kind: %d", kind)
		}

		if err := h.jetsonHandler.HandleJetsonRawStream(payload[1:], frameKind); err != nil {
			return fmt.Errorf("handle jetson stream: %w", err)
		}

		frameCount++

		// Write ACK
		if _, err := stream.Write(ack); err != nil {
			return fmt.Errorf("write ack: %w", err)
		}
	}
}

