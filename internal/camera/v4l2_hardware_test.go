//go:build hardware
// +build hardware

package camera

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	hwWidth       = 640
	hwHeight      = 480
	hwFrameRate   = 30
	hwYUYVSize    = hwWidth * hwHeight * 2
	hwZ16Size     = hwWidth * hwHeight * 2
	hwReadTimeout = 5 * time.Second
)

func TestV4L2HardwareRGBStreamLifecycle(t *testing.T) {
	rgbDevice := requireHardwareEnv(t, "ORBBEC_RGB_DEVICE")

	requireDeviceExists(t, rgbDevice)

	src := NewV4L2Source(V4L2Config{
		DevicePath: rgbDevice,
		Kind:       FrameKindRGB,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), hwReadTimeout)
	defer cancel()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start RGB V4L2 source failed: %v", err)
	}
	defer src.Close()

	frame, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read RGB frame failed: %v", err)
	}

	assertHardwareFrame(t, frame, FrameKindRGB, PixelFormatYUYV, hwYUYVSize)

	if err := src.Close(); err != nil {
		t.Fatalf("close RGB V4L2 source failed: %v", err)
	}
}

func TestV4L2HardwareDepthStreamLifecycle(t *testing.T) {
	depthDevice := requireHardwareEnv(t, "ORBBEC_DEPTH_DEVICE")

	requireDeviceExists(t, depthDevice)

	src := NewV4L2Source(V4L2Config{
		DevicePath: depthDevice,
		Kind:       FrameKindDepth,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatZ16,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), hwReadTimeout)
	defer cancel()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start Depth V4L2 source failed: %v", err)
	}
	defer src.Close()

	frame, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read Depth frame failed: %v", err)
	}

	assertHardwareFrame(t, frame, FrameKindDepth, PixelFormatZ16, hwZ16Size)

	if err := src.Close(); err != nil {
		t.Fatalf("close Depth V4L2 source failed: %v", err)
	}
}

func TestV4L2HardwareRGBSerialIncrements(t *testing.T) {
	rgbDevice := requireHardwareEnv(t, "ORBBEC_RGB_DEVICE")

	requireDeviceExists(t, rgbDevice)

	src := NewV4L2Source(V4L2Config{
		DevicePath: rgbDevice,
		Kind:       FrameKindRGB,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), hwReadTimeout)
	defer cancel()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start RGB V4L2 source failed: %v", err)
	}
	defer src.Close()

	first, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read first RGB frame failed: %v", err)
	}

	second, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read second RGB frame failed: %v", err)
	}

	if second.Serial != first.Serial+1 {
		t.Fatalf("expected RGB serial to increment: first=%d second=%d", first.Serial, second.Serial)
	}
}

func TestV4L2HardwareDepthSerialIncrements(t *testing.T) {
	depthDevice := requireHardwareEnv(t, "ORBBEC_DEPTH_DEVICE")

	requireDeviceExists(t, depthDevice)

	src := NewV4L2Source(V4L2Config{
		DevicePath: depthDevice,
		Kind:       FrameKindDepth,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatZ16,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), hwReadTimeout)
	defer cancel()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start Depth V4L2 source failed: %v", err)
	}
	defer src.Close()

	first, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read first Depth frame failed: %v", err)
	}

	second, err := src.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("read second Depth frame failed: %v", err)
	}

	if second.Serial != first.Serial+1 {
		t.Fatalf("expected Depth serial to increment: first=%d second=%d", first.Serial, second.Serial)
	}
}

func TestV4L2HardwareReadAfterCloseFails(t *testing.T) {
	rgbDevice := requireHardwareEnv(t, "ORBBEC_RGB_DEVICE")

	requireDeviceExists(t, rgbDevice)

	src := NewV4L2Source(V4L2Config{
		DevicePath: rgbDevice,
		Kind:       FrameKindRGB,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), hwReadTimeout)
	defer cancel()

	if err := src.Start(ctx); err != nil {
		t.Fatalf("start RGB V4L2 source failed: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("close RGB V4L2 source failed: %v", err)
	}

	_, err := src.ReadFrame(ctx)
	if err == nil {
		t.Fatal("expected ReadFrame after Close to fail")
	}
}

func TestV4L2HardwareStartWithMissingDeviceFails(t *testing.T) {
	src := NewV4L2Source(V4L2Config{
		DevicePath: "/dev/orbbec-does-not-exist",
		Kind:       FrameKindRGB,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), hwReadTimeout)
	defer cancel()

	err := src.Start(ctx)
	if err == nil {
		t.Fatal("expected Start with missing V4L2 device to fail")
	}
}

func TestV4L2HardwareContextCanceledBeforeStartFails(t *testing.T) {
	rgbDevice := requireHardwareEnv(t, "ORBBEC_RGB_DEVICE")

	requireDeviceExists(t, rgbDevice)

	src := NewV4L2Source(V4L2Config{
		DevicePath: rgbDevice,
		Kind:       FrameKindRGB,
		Width:      hwWidth,
		Height:     hwHeight,
		PixelFmt:   PixelFormatYUYV,
		FrameRate:  hwFrameRate,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := src.Start(ctx)
	if err == nil {
		t.Fatal("expected Start with canceled context to fail")
	}
}

func assertHardwareFrame(
	t *testing.T,
	frame Frame,
	expectedKind FrameKind,
	expectedPixelFmt PixelFormat,
	expectedSize int,
) {
	t.Helper()

	if frame.Kind != expectedKind {
		t.Fatalf("expected frame kind %v, got %v", expectedKind, frame.Kind)
	}

	if frame.Width != hwWidth {
		t.Fatalf("expected width %d, got %d", hwWidth, frame.Width)
	}

	if frame.Height != hwHeight {
		t.Fatalf("expected height %d, got %d", hwHeight, frame.Height)
	}

	if frame.PixelFmt != expectedPixelFmt {
		t.Fatalf("expected pixel format %q, got %q", expectedPixelFmt, frame.PixelFmt)
	}

	if len(frame.Data) != expectedSize {
		t.Fatalf("expected frame size %d, got %d", expectedSize, len(frame.Data))
	}

	if frame.Serial == 0 {
		t.Fatal("expected frame serial to be non-zero")
	}

	if frame.Timestamp.IsZero() {
		t.Fatal("expected frame timestamp to be set")
	}
}

func requireHardwareEnv(t *testing.T, key string) string {
	t.Helper()

	value := os.Getenv(key)
	if value == "" {
		t.Skipf("%s is not set; skipping V4L2 hardware test", key)
	}

	return value
}

func requireDeviceExists(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Skipf("V4L2 device %s is not available: %v", path, err)
	}

	if info.IsDir() {
		t.Fatalf("V4L2 device path %s is a directory", path)
	}
}