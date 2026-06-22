package camera

import (
	"context"
	"testing"
	"time"
)

const (
	v4l2TestWidth     = 640
	v4l2TestHeight    = 480
	v4l2TestFrameRate = 30

	v4l2YUYVSize  = v4l2TestWidth * v4l2TestHeight * 2
	v4l2Z16Size   = v4l2TestWidth * v4l2TestHeight * 2
	v4l2RGB24Size = v4l2TestWidth * v4l2TestHeight * 3
)

func TestNewV4L2SourceImplementsSource(t *testing.T) {
	var _ Source = NewV4L2Source(V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	})
}

func TestValidateV4L2ConfigAcceptsRGBYUYV(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err != nil {
		t.Fatalf("expected RGB YUYV config to be valid: %v", err)
	}
}

func TestValidateV4L2ConfigAcceptsDepthZ16(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindDepth,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatZ16,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err != nil {
		t.Fatalf("expected depth Z16 config to be valid: %v", err)
	}
}

func TestValidateV4L2ConfigAcceptsRGB24(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatRGB24,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err != nil {
		t.Fatalf("expected RGB24 config to be valid: %v", err)
	}
}

func TestValidateV4L2ConfigRejectsEmptyDevicePath(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected empty device path to fail")
	}
}

func TestValidateV4L2ConfigRejectsZeroWidth(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      0,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected zero width to fail")
	}
}

func TestValidateV4L2ConfigRejectsZeroHeight(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     0,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected zero height to fail")
	}
}

func TestValidateV4L2ConfigRejectsZeroFrameRate(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  0,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected zero frame rate to fail")
	}
}

func TestValidateV4L2ConfigRejectsUnknownFrameKind(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKind(255),
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected unknown frame kind to fail")
	}
}

func TestValidateV4L2ConfigRejectsUnknownPixelFormat(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormat("UNKNOWN"),
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected unknown pixel format to fail")
	}
}

func TestValidateV4L2ConfigRejectsRGBWithDepthFormat(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatZ16,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected RGB source with Z16 format to fail")
	}
}

func TestValidateV4L2ConfigRejectsDepthWithRGBFormat(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindDepth,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	}

	if err := ValidateV4L2Config(cfg); err == nil {
		t.Fatal("expected depth source with YUYV format to fail")
	}
}

func TestExpectedV4L2FrameSize(t *testing.T) {
	tests := []struct {
		name     string
		cfg      V4L2Config
		wantSize int
	}{
		{
			name: "RGB YUYV",
			cfg: V4L2Config{
				DevicePath: "/dev/video0",
				Kind:       FrameKindRGB,
				Width:      v4l2TestWidth,
				Height:     v4l2TestHeight,
				PixelFmt:   PixelFormatYUYV,
				FrameRate:  v4l2TestFrameRate,
			},
			wantSize: v4l2YUYVSize,
		},
		{
			name: "Depth Z16",
			cfg: V4L2Config{
				DevicePath: "/dev/video0",
				Kind:       FrameKindDepth,
				Width:      v4l2TestWidth,
				Height:     v4l2TestHeight,
				PixelFmt:   PixelFormatZ16,
				FrameRate:  v4l2TestFrameRate,
			},
			wantSize: v4l2Z16Size,
		},
		{
			name: "RGB24",
			cfg: V4L2Config{
				DevicePath: "/dev/video0",
				Kind:       FrameKindRGB,
				Width:      v4l2TestWidth,
				Height:     v4l2TestHeight,
				PixelFmt:   PixelFormatRGB24,
				FrameRate:  v4l2TestFrameRate,
			},
			wantSize: v4l2RGB24Size,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpectedV4L2FrameSize(tt.cfg)
			if err != nil {
				t.Fatalf("expected frame size calculation to succeed: %v", err)
			}

			if got != tt.wantSize {
				t.Fatalf("unexpected frame size: got=%d want=%d", got, tt.wantSize)
			}
		})
	}
}

func TestExpectedV4L2FrameSizeRejectsInvalidConfig(t *testing.T) {
	cfg := V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormat("UNKNOWN"),
		FrameRate:  v4l2TestFrameRate,
	}

	if _, err := ExpectedV4L2FrameSize(cfg); err == nil {
		t.Fatal("expected invalid pixel format to fail")
	}
}

func TestIsSupportedV4L2PixelFormat(t *testing.T) {
	tests := []struct {
		format PixelFormat
		want   bool
	}{
		{format: PixelFormatYUYV, want: true},
		{format: PixelFormatZ16, want: true},
		{format: PixelFormatRGB24, want: true},
		{format: PixelFormat("UNKNOWN"), want: false},
		{format: PixelFormat(""), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			got := IsSupportedV4L2PixelFormat(tt.format)
			if got != tt.want {
				t.Fatalf("unexpected support result for %q: got=%v want=%v", tt.format, got, tt.want)
			}
		})
	}
}

func TestV4L2SourceStartWithMissingDeviceFails(t *testing.T) {
	src := NewV4L2Source(V4L2Config{
		DevicePath: "/dev/mp-quic-orbbec-missing-device",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := src.Start(ctx)
	if err == nil {
		t.Fatal("expected Start with missing V4L2 device to fail")
	}
}

func TestV4L2SourceStartWithCanceledContextFails(t *testing.T) {
	src := NewV4L2Source(V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := src.Start(ctx)
	if err == nil {
		t.Fatal("expected Start with canceled context to fail")
	}
}

func TestV4L2SourceReadBeforeStartFails(t *testing.T) {
	src := NewV4L2Source(V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	})

	_, err := src.ReadFrame(context.Background())
	if err == nil {
		t.Fatal("expected ReadFrame before Start to fail")
	}
}

func TestV4L2SourceCloseBeforeStartIsAllowed(t *testing.T) {
	src := NewV4L2Source(V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	})

	if err := src.Close(); err != nil {
		t.Fatalf("expected Close before Start to be allowed: %v", err)
	}
}

func TestV4L2SourceCloseIsIdempotent(t *testing.T) {
	src := NewV4L2Source(V4L2Config{
		DevicePath: "/dev/video0",
		Kind:       FrameKindRGB,
		Width:      v4l2TestWidth,
		Height:     v4l2TestHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  v4l2TestFrameRate,
	})

	if err := src.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("second close should not fail: %v", err)
	}
}