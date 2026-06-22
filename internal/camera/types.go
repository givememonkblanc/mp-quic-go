package camera

import "time"

// FrameKind identifies the type of camera frame.
type FrameKind byte

const (
	FrameKindRGB   FrameKind = 0
	FrameKindDepth FrameKind = 1
)

// Frame is a single captured frame.
type Frame struct {
	Kind      FrameKind
	Data      []byte
	Width     int
	Height    int
	Channels  int
	PixelFmt  PixelFormat
	Serial    uint64
	Timestamp time.Time
}

// Provider captures depth and RGB frames.
type Provider interface {
	Next() (Frame, Frame, error)
	Close() error
}

// PixelFormat represents the pixel format of a camera frame.
type PixelFormat string

const (
	PixelFormatYUYV  PixelFormat = "YUYV"
	PixelFormatZ16   PixelFormat = "Z16"
	PixelFormatRGB24 PixelFormat = "RGB24"
)
