package handler

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Handler processes a stream payload and returns a response payload.
// This interface is used for simple echo/stream handling.
type Handler interface {
	Handle(context.Context, []byte) ([]byte, error)
}

// FrameHandler processes a Frame directly.
type FrameHandler interface {
	StoreFrame(frame Frame) error
	Snapshot() Snapshot
}

// StreamType identifies the content type on a QUIC stream.
type StreamType byte

const (
	StreamDepth StreamType = 0
	StreamRGB   StreamType = 1
)

// FrameKind identifies the type of frame.
type FrameKind byte

const (
	FrameKindRGB   FrameKind = 0
	FrameKindDepth FrameKind = 1
)

// Frame represents a camera frame.
type Frame struct {
	Kind      FrameKind
	Serial    uint64
	Timestamp time.Time
	Width     int
	Height    int
	PixelFmt  string
	Data      []byte
}

// Snapshot holds the latest frames.
type Snapshot struct {
	RGB   *Frame
	Depth *Frame
}

// MultiStreamHandler routes depth and RGB frames to separate buffers.
type MultiStreamHandler struct {
	mu      sync.RWMutex
	rgb     *Frame
	depth   *Frame
}

func NewMultiStreamHandler() *MultiStreamHandler {
	return &MultiStreamHandler{
		rgb:   nil,
		depth: nil,
	}
}

// StoreFrame stores a frame in the appropriate buffer.
func (h *MultiStreamHandler) StoreFrame(frame Frame) error {
	if frame.Kind != FrameKindRGB && frame.Kind != FrameKindDepth {
		return fmt.Errorf("invalid frame kind: %d", frame.Kind)
	}

	expectedSize := frame.Width * frame.Height * 2
	if len(frame.Data) != expectedSize {
		return fmt.Errorf("invalid frame size: expected %d, got %d", expectedSize, len(frame.Data))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	switch frame.Kind {
	case FrameKindRGB:
		h.rgb = &frame
	case FrameKindDepth:
		h.depth = &frame
	}

	return nil
}

// Snapshot returns a copy of the current state.
func (h *MultiStreamHandler) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	snapshot := Snapshot{
		RGB:   nil,
		Depth: nil,
	}

	if h.rgb != nil {
		rgbCopy := *h.rgb
		snapshot.RGB = &rgbCopy
	}

	if h.depth != nil {
		depthCopy := *h.depth
		snapshot.Depth = &depthCopy
	}

	return snapshot
}

// serials tracks serials for each frame kind
func (h *MultiStreamHandler) nextSerial(kind FrameKind) uint64 {
	switch kind {
	case FrameKindRGB:
		if h.rgb != nil {
			return h.rgb.Serial + 1
		}
	case FrameKindDepth:
		if h.depth != nil {
			return h.depth.Serial + 1
		}
	}
	return 1
}

// Handle implements Handler interface by extracting frame from payload.
// The payload format is: [StreamType][FrameData...]
func (h *MultiStreamHandler) Handle(ctx context.Context, payload []byte) ([]byte, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("empty payload")
	}
	st := payload[0]
	data := payload[1:]

	switch st {
	case byte(StreamDepth):
		depthFrame := Frame{
			Kind:      FrameKindDepth,
			Width:     640,
			Height:    480,
			PixelFmt:  "YUYV",
			Serial:    h.nextSerial(FrameKindDepth),
			Data:      data,
			Timestamp: time.Now(),
		}
		if err := h.StoreFrame(depthFrame); err != nil {
			return nil, err
		}
		return []byte{byte(StreamDepth), 0x06}, nil
	case byte(StreamRGB):
		rgbFrame := Frame{
			Kind:      FrameKindRGB,
			Width:     640,
			Height:    480,
			PixelFmt:  "YUYV",
			Serial:    h.nextSerial(FrameKindRGB),
			Data:      data,
			Timestamp: time.Now(),
		}
		if err := h.StoreFrame(rgbFrame); err != nil {
			return nil, err
		}
		return []byte{byte(StreamRGB), 0x06}, nil
	default:
		return nil, fmt.Errorf("unknown stream type: %d", st)
	}
}
