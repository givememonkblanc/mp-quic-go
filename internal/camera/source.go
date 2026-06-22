package camera

import (
	"context"
	"fmt"
	"time"
)

type TestPatternConfig struct {
	Kind      FrameKind
	Width     int
	Height    int
	PixelFmt  PixelFormat
	FrameRate int
}

type Source interface {
	Start(context.Context) error
	ReadFrame(context.Context) (Frame, error)
	Close() error
}

type TestPatternSource struct {
	config TestPatternConfig
	started bool
	serial uint64
}

func NewTestPatternSource(config TestPatternConfig) *TestPatternSource {
	return &TestPatternSource{config: config}
}

func (s *TestPatternSource) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if s.config.Width <= 0 {
		return fmt.Errorf("invalid width %d", s.config.Width)
	}
	if s.config.Height <= 0 {
		return fmt.Errorf("invalid height %d", s.config.Height)
	}
	if s.config.PixelFmt == "" {
		return fmt.Errorf("empty pixel format")
	}
	if s.config.FrameRate <= 0 {
		return fmt.Errorf("invalid frame rate %d", s.config.FrameRate)
	}
	if s.config.Kind != FrameKindRGB && s.config.Kind != FrameKindDepth {
		return fmt.Errorf("invalid frame kind: %d", s.config.Kind)
	}
	if !IsSupportedV4L2PixelFormat(s.config.PixelFmt) {
		return fmt.Errorf("unsupported pixel format: %s", s.config.PixelFmt)
	}
	if s.config.Kind == FrameKindRGB {
		if s.config.PixelFmt == PixelFormatZ16 {
			return fmt.Errorf("RGB kind with Z16 pixel format is invalid")
		}
	}
	if s.config.Kind == FrameKindDepth {
		if s.config.PixelFmt == PixelFormatYUYV || s.config.PixelFmt == PixelFormatRGB24 {
			return fmt.Errorf("Depth kind with %s pixel format is invalid", s.config.PixelFmt)
		}
	}

	s.started = true
	return nil
}

func (s *TestPatternSource) ReadFrame(ctx context.Context) (Frame, error) {
	select {
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	default:
	}

	if !s.started {
		return Frame{}, fmt.Errorf("source not started")
	}

	s.serial++
	timestamp := time.Now()

	var frameSize int
	switch s.config.PixelFmt {
	case PixelFormatYUYV, PixelFormatZ16:
		frameSize = s.config.Width * s.config.Height * 2
	case PixelFormatRGB24:
		frameSize = s.config.Width * s.config.Height * 3
	}

	frame := Frame{
		Kind:      s.config.Kind,
		Data:      make([]byte, frameSize),
		Width:     s.config.Width,
		Height:    s.config.Height,
		Channels:  2,
		PixelFmt:  s.config.PixelFmt,
		Serial:    s.serial,
		Timestamp: timestamp,
	}

	for i := range frame.Data {
		frame.Data[i] = byte((i + int(s.serial)) % 256)
	}

	return frame, nil
}

func (s *TestPatternSource) Close() error {
	s.started = false
	return nil
}

func ExpectedTestPatternFrameSize(config TestPatternConfig) (int, error) {
	if !IsSupportedV4L2PixelFormat(config.PixelFmt) {
		return 0, fmt.Errorf("unsupported pixel format: %s", config.PixelFmt)
	}
	switch config.PixelFmt {
	case PixelFormatYUYV, PixelFormatZ16:
		return config.Width * config.Height * 2, nil
	case PixelFormatRGB24:
		return config.Width * config.Height * 3, nil
	default:
		return 0, fmt.Errorf("unsupported pixel format: %s", config.PixelFmt)
	}
}
