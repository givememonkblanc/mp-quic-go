package handler

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

const (
	testWidth     = 640
	testHeight    = 480
	testFrameSize = testWidth * testHeight * 2
)

func TestNewMultiStreamHandler(t *testing.T) {
	h := NewMultiStreamHandler()
	if h == nil {
		t.Fatal("expected non-nil MultiStreamHandler")
	}
}

func TestMultiStreamHandlerUpdatesRGBFrame(t *testing.T) {
	h := NewMultiStreamHandler()

	frame := testFrame(FrameKindRGB, 1, testFrameSize)

	if err := h.StoreFrame(frame); err != nil {
		t.Fatalf("handle rgb frame failed: %v", err)
	}

	snapshot := h.Snapshot()

	if snapshot.RGB == nil {
		t.Fatal("expected latest RGB frame to be set")
	}
	if snapshot.RGB.Serial != 1 {
		t.Fatalf("expected RGB serial 1, got %d", snapshot.RGB.Serial)
	}
	if len(snapshot.RGB.Data) != testFrameSize {
		t.Fatalf("expected RGB frame size %d, got %d", testFrameSize, len(snapshot.RGB.Data))
	}
}

func TestMultiStreamHandlerUpdatesDepthFrame(t *testing.T) {
	h := NewMultiStreamHandler()

	frame := testFrame(FrameKindDepth, 1, testFrameSize)

	if err := h.StoreFrame(frame); err != nil {
		t.Fatalf("handle depth frame failed: %v", err)
	}

	snapshot := h.Snapshot()

	if snapshot.Depth == nil {
		t.Fatal("expected latest Depth frame to be set")
	}
	if snapshot.Depth.Serial != 1 {
		t.Fatalf("expected Depth serial 1, got %d", snapshot.Depth.Serial)
	}
	if len(snapshot.Depth.Data) != testFrameSize {
		t.Fatalf("expected Depth frame size %d, got %d", testFrameSize, len(snapshot.Depth.Data))
	}
}

func TestMultiStreamHandlerKeepsRGBAndDepthIndependently(t *testing.T) {
	h := NewMultiStreamHandler()

	rgb := testFrame(FrameKindRGB, 10, testFrameSize)
	depth := testFrame(FrameKindDepth, 20, testFrameSize)

	if err := h.StoreFrame(rgb); err != nil {
		t.Fatalf("handle rgb frame failed: %v", err)
	}
	if err := h.StoreFrame(depth); err != nil {
		t.Fatalf("handle depth frame failed: %v", err)
	}

	snapshot := h.Snapshot()

	if snapshot.RGB == nil {
		t.Fatal("expected RGB frame to be set")
	}
	if snapshot.Depth == nil {
		t.Fatal("expected Depth frame to be set")
	}

	if snapshot.RGB.Serial != 10 {
		t.Fatalf("expected RGB serial 10, got %d", snapshot.RGB.Serial)
	}
	if snapshot.Depth.Serial != 20 {
		t.Fatalf("expected Depth serial 20, got %d", snapshot.Depth.Serial)
	}
}

func TestMultiStreamHandlerOverwritesLatestFrame(t *testing.T) {
	h := NewMultiStreamHandler()

	first := testFrame(FrameKindRGB, 1, testFrameSize)
	second := testFrame(FrameKindRGB, 2, testFrameSize)

	if err := h.StoreFrame(first); err != nil {
		t.Fatalf("handle first rgb frame failed: %v", err)
	}
	if err := h.StoreFrame(second); err != nil {
		t.Fatalf("handle second rgb frame failed: %v", err)
	}

	snapshot := h.Snapshot()

	if snapshot.RGB == nil {
		t.Fatal("expected RGB frame to be set")
	}
	if snapshot.RGB.Serial != 2 {
		t.Fatalf("expected latest RGB serial 2, got %d", snapshot.RGB.Serial)
	}
	if !bytes.Equal(snapshot.RGB.Data, second.Data) {
		t.Fatal("expected latest RGB data to match second frame")
	}
}

func TestMultiStreamHandlerRejectsInvalidFrameKind(t *testing.T) {
	h := NewMultiStreamHandler()

	frame := testFrame(FrameKind(255), 1, testFrameSize)

	if err := h.StoreFrame(frame); err == nil {
		t.Fatal("expected invalid frame kind to fail")
	}
}

func TestMultiStreamHandlerRejectsInvalidFrameSize(t *testing.T) {
	h := NewMultiStreamHandler()

	frame := testFrame(FrameKindRGB, 1, testFrameSize-1)

	if err := h.StoreFrame(frame); err == nil {
		t.Fatal("expected invalid frame size to fail")
	}
}

func TestMultiStreamHandlerConcurrentUpdates(t *testing.T) {
	h := NewMultiStreamHandler()

	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)

		go func(worker int) {
			defer wg.Done()

			for i := 0; i < 1000; i++ {
				kind := FrameKindRGB
				if i%2 == 0 {
					kind = FrameKindDepth
				}

				frame := testFrame(kind, uint64(worker*1000+i), testFrameSize)
				_ = h.StoreFrame(frame)
				_ = h.Snapshot()
			}
		}(worker)
	}

	wg.Wait()
}

func testFrame(kind FrameKind, serial uint64, size int) Frame {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(serial + uint64(i))
	}

	return Frame{
		Kind:      kind,
		Serial:    serial,
		Timestamp: time.Now(),
		Width:     testWidth,
		Height:    testHeight,
		PixelFmt:  "YUYV",
		Data:      data,
	}
}