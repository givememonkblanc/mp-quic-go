package camera

import (
	"bytes"
	"context"
	"testing"
)

const (
	tpWidth       = 640
	tpHeight      = 480
	tpYUYVSize    = tpWidth * tpHeight * 2
	tpZ16Size     = tpWidth * tpHeight * 2
	tpRGB24Size   = tpWidth * tpHeight * 3
	tpDefaultFPS  = 30
)

func TestTestPatternSourceProducesRGBYUYVFrame(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
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

	if frame.Kind != FrameKindRGB {
		t.Fatalf("expected RGB frame, got %v", frame.Kind)
	}
	if frame.Width != tpWidth {
		t.Fatalf("expected width %d, got %d", tpWidth, frame.Width)
	}
	if frame.Height != tpHeight {
		t.Fatalf("expected height %d, got %d", tpHeight, frame.Height)
	}
	if frame.PixelFmt != PixelFormatYUYV {
		t.Fatalf("expected pixel format %q, got %q", PixelFormatYUYV, frame.PixelFmt)
	}
	if len(frame.Data) != tpYUYVSize {
		t.Fatalf("expected YUYV frame size %d, got %d", tpYUYVSize, len(frame.Data))
	}
	if frame.Serial != 1 {
		t.Fatalf("expected first serial 1, got %d", frame.Serial)
	}
	if frame.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be set")
	}
}

func TestTestPatternSourceProducesDepthZ16Frame(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindDepth,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatZ16,
		FrameRate: tpDefaultFPS,
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

	if frame.Kind != FrameKindDepth {
		t.Fatalf("expected depth frame, got %v", frame.Kind)
	}
	if frame.PixelFmt != PixelFormatZ16 {
		t.Fatalf("expected pixel format %q, got %q", PixelFormatZ16, frame.PixelFmt)
	}
	if len(frame.Data) != tpZ16Size {
		t.Fatalf("expected Z16 frame size %d, got %d", tpZ16Size, len(frame.Data))
	}
}

func TestTestPatternSourceProducesRGB24FrameWhenConfigured(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatRGB24,
		FrameRate: tpDefaultFPS,
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

	if frame.Kind != FrameKindRGB {
		t.Fatalf("expected RGB frame, got %v", frame.Kind)
	}
	if frame.PixelFmt != PixelFormatRGB24 {
		t.Fatalf("expected pixel format %q, got %q", PixelFormatRGB24, frame.PixelFmt)
	}
	if len(frame.Data) != tpRGB24Size {
		t.Fatalf("expected RGB24 frame size %d, got %d", tpRGB24Size, len(frame.Data))
	}
}

func TestTestPatternSourceSerialStartsAtOne(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
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

	if frame.Serial != 1 {
		t.Fatalf("expected first frame serial 1, got %d", frame.Serial)
	}
}

func TestTestPatternSourceSerialIncrementsMonotonically(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
	})

	ctx := context.Background()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start source failed: %v", err)
	}
	defer src.Close()

	for expected := uint64(1); expected <= 5; expected++ {
		frame, err := src.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("read frame %d failed: %v", expected, err)
		}

		if frame.Serial != expected {
			t.Fatalf("expected serial %d, got %d", expected, frame.Serial)
		}
	}
}

func TestTestPatternSourceFrameDataChangesBySerial(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
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

	if first.Serial == second.Serial {
		t.Fatalf("expected different serials, got %d and %d", first.Serial, second.Serial)
	}
	if bytes.Equal(first.Data, second.Data) {
		t.Fatal("expected test pattern data to change across frames")
	}
}

func TestTestPatternSourceReturnsIndependentFrameBuffers(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
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

	firstOriginal := append([]byte(nil), first.Data...)

	for i := range first.Data {
		first.Data[i] = 0xff
	}

	second, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read second frame failed: %v", err)
	}

	if bytes.Equal(second.Data, first.Data) {
		t.Fatal("expected second frame buffer to be independent from mutated first frame")
	}

	if bytes.Equal(first.Data, firstOriginal) {
		t.Fatal("test setup failed: expected first frame data to be mutated")
	}
}

func TestTestPatternSourceMultipleSourcesDoNotShareSerialState(t *testing.T) {
	srcA := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
	})

	srcB := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindDepth,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatZ16,
		FrameRate: tpDefaultFPS,
	})

	ctx := context.Background()

	if err := srcA.Start(ctx); err != nil {
		t.Fatalf("start source A failed: %v", err)
	}
	defer srcA.Close()

	if err := srcB.Start(ctx); err != nil {
		t.Fatalf("start source B failed: %v", err)
	}
	defer srcB.Close()

	frameA, err := srcA.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read source A failed: %v", err)
	}

	frameB, err := srcB.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read source B failed: %v", err)
	}

	if frameA.Serial != 1 {
		t.Fatalf("expected source A first serial 1, got %d", frameA.Serial)
	}
	if frameB.Serial != 1 {
		t.Fatalf("expected source B first serial 1, got %d", frameB.Serial)
	}
}

func TestTestPatternSourceRejectsRGBKindWithDepthFormat(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindRGB,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatZ16,
		FrameRate: tpDefaultFPS,
	})

	err := src.Start(context.Background())
	if err == nil {
		t.Fatal("expected RGB source with Z16 pixel format to fail")
	}
}

func TestTestPatternSourceRejectsDepthKindWithRGBFormat(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKindDepth,
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
	})

	err := src.Start(context.Background())
	if err == nil {
		t.Fatal("expected depth source with YUYV pixel format to fail")
	}
}

func TestTestPatternSourceRejectsUnknownFrameKind(t *testing.T) {
	src := NewTestPatternSource(TestPatternConfig{
		Kind:      FrameKind(255),
		Width:     tpWidth,
		Height:    tpHeight,
		PixelFmt:  PixelFormatYUYV,
		FrameRate: tpDefaultFPS,
	})

	err := src.Start(context.Background())
	if err == nil {
		t.Fatal("expected unknown frame kind to fail")
	}
}