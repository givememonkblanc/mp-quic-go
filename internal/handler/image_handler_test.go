package handler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImageFrameHandlerCreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()

	h, err := NewImageFrameHandler(tmpDir)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "rgb")); err != nil {
		t.Fatalf("rgb directory not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "depth")); err != nil {
		t.Fatalf("depth directory not created: %v", err)
	}

	_ = h
}

func TestImageFrameHandlerSavesRGBFrame(t *testing.T) {
	tmpDir := t.TempDir()

	h, err := NewImageFrameHandler(tmpDir)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	frame := Frame{
		Kind:      FrameKindRGB,
		Serial:    1,
		Timestamp: time.Now(),
		Width:     640,
		Height:    480,
		PixelFmt:  "YUYV",
		Data:      make([]byte, 640*480*2),
	}

	if err := h.StoreFrame(frame); err != nil {
		t.Fatalf("failed to store frame: %v", err)
	}

	files, err := os.ReadDir(filepath.Join(tmpDir, "rgb"))
	if err != nil {
		t.Fatalf("failed to read rgb dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	_ = h
}

func TestImageFrameHandlerSavesDepthFrame(t *testing.T) {
	tmpDir := t.TempDir()

	h, err := NewImageFrameHandler(tmpDir)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	frame := Frame{
		Kind:      FrameKindDepth,
		Serial:    2,
		Timestamp: time.Now(),
		Width:     640,
		Height:    480,
		PixelFmt:  "Z16",
		Data:      make([]byte, 640*480*2),
	}

	if err := h.StoreFrame(frame); err != nil {
		t.Fatalf("failed to store frame: %v", err)
	}

	files, err := os.ReadDir(filepath.Join(tmpDir, "depth"))
	if err != nil {
		t.Fatalf("failed to read depth dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	_ = h
}

func TestImageFrameHandlerSnapshots(t *testing.T) {
	tmpDir := t.TempDir()

	h, err := NewImageFrameHandler(tmpDir)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	rgbFrame := Frame{
		Kind:      FrameKindRGB,
		Serial:    1,
		Timestamp: time.Now(),
		Width:     640,
		Height:    480,
		PixelFmt:  "YUYV",
		Data:      make([]byte, 640*480*2),
	}
	depthFrame := Frame{
		Kind:      FrameKindDepth,
		Serial:    2,
		Timestamp: time.Now(),
		Width:     640,
		Height:    480,
		PixelFmt:  "Z16",
		Data:      make([]byte, 640*480*2),
	}

	h.StoreFrame(rgbFrame)
	h.StoreFrame(depthFrame)

	snapshot := h.Snapshot()
	if snapshot.RGB == nil {
		t.Fatal("expected RGB frame in snapshot")
	}
	if snapshot.Depth == nil {
		t.Fatal("expected depth frame in snapshot")
	}
	if snapshot.RGB.Kind != FrameKindRGB {
		t.Fatalf("expected RGB kind, got %d", snapshot.RGB.Kind)
	}
	if snapshot.Depth.Kind != FrameKindDepth {
		t.Fatalf("expected depth kind, got %d", snapshot.Depth.Kind)
	}
}
