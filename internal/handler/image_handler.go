package handler

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ImageFrameHandler stores frames to PNG files.
type ImageFrameHandler struct {
	baseDir   string
	mu        sync.Mutex
	lastRGB   *Frame
	lastDepth *Frame
}

// NewImageFrameHandler creates a new handler that saves frames to files.
// frames are saved to <baseDir>/rgb/<timestamp>.png and <baseDir>/depth/<timestamp>.png
func NewImageFrameHandler(baseDir string) (*ImageFrameHandler, error) {
	if err := os.MkdirAll(filepath.Join(baseDir, "rgb"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create rgb directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "depth"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create depth directory: %w", err)
	}

	return &ImageFrameHandler{baseDir: baseDir}, nil
}

func (h *ImageFrameHandler) StoreFrame(frame Frame) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if frame.Kind == FrameKindRGB {
		h.lastRGB = &frame
	} else {
		h.lastDepth = &frame
	}

	dir := filepath.Join(h.baseDir, "rgb")
	if frame.Kind == FrameKindDepth {
		dir = filepath.Join(h.baseDir, "depth")
	}
	filename := fmt.Sprintf("frame_%d_%s.png", frame.Serial, frame.Timestamp.Format("20060102_150405"))
	filepath := filepath.Join(dir, filename)

	var pngData []byte
	var err error

	if frame.PixelFmt == "JPEG" {
		pngData, err = h.encodeJPEGtoPNG(frame.Data, frame.Width, frame.Height)
		if err != nil {
			return fmt.Errorf("failed to encode JPEG to PNG: %w", err)
		}
	} else {
		pngData = frame.Data
	}

	if err := os.WriteFile(filepath, pngData, 0644); err != nil {
		return fmt.Errorf("failed to write frame file: %w", err)
	}

	return nil
}

func (h *ImageFrameHandler) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	snapshot := Snapshot{
		RGB:   nil,
		Depth: nil,
	}

	if h.lastRGB != nil {
		rgbCopy := *h.lastRGB
		snapshot.RGB = &rgbCopy
	}

	if h.lastDepth != nil {
		depthCopy := *h.lastDepth
		snapshot.Depth = &depthCopy
	}

	return snapshot
}

func (h *ImageFrameHandler) encodeJPEGtoPNG(jpegData []byte, width, height int) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode JPEG: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}

	return buf.Bytes(), nil
}

func (h *ImageFrameHandler) Handle(ctx context.Context, payload []byte) ([]byte, error) {
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
			PixelFmt:  "JPEG",
			Serial:    1,
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
			PixelFmt:  "JPEG",
			Serial:    1,
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
