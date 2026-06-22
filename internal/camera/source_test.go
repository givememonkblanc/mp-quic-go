package camera

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	testCameraWidth       = 640
	testCameraHeight      = 480
	testCameraBytesPerPix = 2
	testCameraFrameSize   = testCameraWidth * testCameraHeight * testCameraBytesPerPix
)

func TestTestPatternSourceImplementsSource(t *testing.T) {
	var _ Source = NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})
}

func TestTestPatternSourceLifecycle(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}

	frame, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read frame failed: %v", err)
	}

	assertValidCameraFrame(t, frame, FrameKindRGB, 1)

	if err := src.Close(); err != nil {
		t.Fatalf("close source failed: %v", err)
	}
}

func TestTestPatternSourceReadBeforeStartFails(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	_, err := src.ReadFrame(context.Background())
	if err == nil {
		t.Fatal("expected ReadFrame before Start to fail")
	}
}

func TestTestPatternSourceSerialIncrements(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}
	defer src.Close()

	first, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read first frame failed: %v", err)
	}

	second, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read second frame failed: %v", err)
	}

	if second.Serial != first.Serial+1 {
		t.Fatalf("expected serial to increment: first=%d second=%d", first.Serial, second.Serial)
	}
}

func TestTestPatternSourceProducesExpectedFrameSize(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}
	defer src.Close()

	frame, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read frame failed: %v", err)
	}

	if frame.Width != testCameraWidth {
		t.Fatalf("expected width %d, got %d", testCameraWidth, frame.Width)
	}
	if frame.Height != testCameraHeight {
		t.Fatalf("expected height %d, got %d", testCameraHeight, frame.Height)
	}
	if frame.PixelFmt != PixelFormatYUYV {
		t.Fatalf("expected pixel format %q, got %q", PixelFormatYUYV, frame.PixelFmt)
	}
	if len(frame.Data) != testCameraFrameSize {
		t.Fatalf("expected frame size %d, got %d", testCameraFrameSize, len(frame.Data))
	}
}

func TestTestPatternSourceReadAfterCloseFails(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("close source failed: %v", err)
	}

	_, err := src.ReadFrame(ctx)
	if err == nil {
		t.Fatal("expected ReadFrame after Close to fail")
	}
}

func TestTestPatternSourceCloseIsIdempotent(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("second close should not fail: %v", err)
	}
}

func TestTestPatternSourceHonorsCanceledContextOnStart(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := src.Start(ctx)
	if err == nil {
		t.Fatal("expected Start with canceled context to fail")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTestPatternSourceHonorsCanceledContextOnRead(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}
	defer src.Close()

	readCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.ReadFrame(readCtx)
	if err == nil {
		t.Fatal("expected ReadFrame with canceled context to fail")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTestPatternSourceRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  TestPatternConfig
	}{
		{
			name: "zero width",
			cfg: TestPatternConfig{
				Width:     0,
				Height:    testCameraHeight,
				PixelFmt:  PixelFormatYUYV,
				FrameRate: 30,
			},
		},
		{
			name: "zero height",
			cfg: TestPatternConfig{
				Width:     testCameraWidth,
				Height:    0,
				PixelFmt:  PixelFormatYUYV,
				FrameRate: 30,
			},
		},
		{
			name: "empty pixel format",
			cfg: TestPatternConfig{
				Width:     testCameraWidth,
				Height:    testCameraHeight,
				PixelFmt:  "",
				FrameRate: 30,
			},
		},
		{
			name: "zero frame rate",
			cfg: TestPatternConfig{
				Width:     testCameraWidth,
				Height:    testCameraHeight,
				PixelFmt:  PixelFormatYUYV,
				FrameRate: 0,
			},
		},
		{
			name: "unsupported pixel format",
			cfg: TestPatternConfig{
				Width:     testCameraWidth,
				Height:    testCameraHeight,
				PixelFmt:  PixelFormat("UNKNOWN"),
				FrameRate: 30,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := NewTestPatternSource(tt.cfg)

			err := src.Start(context.Background())
			if err == nil {
				t.Fatal("expected invalid config to fail on Start")
			}
		})
	}
}

func TestTestPatternSourceTimestampIsSet(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}
	defer src.Close()

	frame, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read frame failed: %v", err)
	}

	if frame.Timestamp.IsZero() {
		t.Fatal("expected frame timestamp to be set")
	}

	if time.Since(frame.Timestamp) > time.Second {
		t.Fatalf("expected recent timestamp, got %v", frame.Timestamp)
	}
}

func TestTestPatternSourceDataChangesAcrossFrames(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Width:     testCameraWidth,
		Height:    testCameraHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: 30,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}
	defer src.Close()

	first, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read first frame failed: %v", err)
	}

	second, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read second frame failed: %v", err)
	}

	if len(first.Data) != len(second.Data) {
		t.Fatalf("frame size changed: first=%d second=%d", len(first.Data), len(second.Data))
	}

	if string(first.Data) == string(second.Data) {
		t.Fatal("expected test pattern data to change across frames")
	}
}

func assertValidCameraFrame(t *testing.T, frame Frame, expectedKind FrameKind, expectedSerial uint64) {
	t.Helper()

	if frame.Kind != expectedKind {
		t.Fatalf("expected frame kind %v, got %v", expectedKind, frame.Kind)
	}
	if frame.Serial != expectedSerial {
		t.Fatalf("expected serial %d, got %d", expectedSerial, frame.Serial)
	}
	if frame.Width != testCameraWidth {
		t.Fatalf("expected width %d, got %d", testCameraWidth, frame.Width)
	}
	if frame.Height != testCameraHeight {
		t.Fatalf("expected height %d, got %d", testCameraHeight, frame.Height)
	}
	if frame.PixelFmt != PixelFormatYUYV {
		t.Fatalf("expected pixel format %q, got %q", PixelFormatYUYV, frame.PixelFmt)
	}
	if len(frame.Data) != testCameraFrameSize {
		t.Fatalf("expected data size %d, got %d", testCameraFrameSize, len(frame.Data))
	}
	if frame.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be set")
	}
}