package handler

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const maxFrames = 30

type JetsonHandler struct {
	baseDir string
	mu      sync.Mutex
	rgbSerial    uint64
	depthSerial  uint64
}

func NewJetsonHandler(baseDir string) (*JetsonHandler, error) {
	if err := os.MkdirAll(filepath.Join(baseDir, "rgb"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create rgb directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "depth"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create depth directory: %w", err)
	}

	return &JetsonHandler{
		baseDir:     baseDir,
		rgbSerial:   1,
		depthSerial: 1,
	}, nil
}

func (h *JetsonHandler) nextSerial(kind FrameKind) uint64 {
	switch kind {
	case FrameKindRGB:
		serial := h.rgbSerial
		h.rgbSerial++
		return serial
	case FrameKindDepth:
		serial := h.depthSerial
		h.depthSerial++
		return serial
	}
	return 1
}

func (h *JetsonHandler) cleanupOldFrames(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Frames are named "<serial>.jpg". Sort by the NUMERIC serial, not lexically:
	// without zero-padding, a string sort orders "1000" < "999" (because '1'<'9'),
	// so it would delete the newest high-serial frames and keep stale low-serial
	// ones — the bug that left old footage on disk while new frames vanished.
	type fileSerial struct {
		name   string
		serial int
	}
	files := make([]fileSerial, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jpg" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".jpg"))
		if err != nil {
			continue
		}
		files = append(files, fileSerial{e.Name(), n})
	}

	if len(files) <= maxFrames {
		return nil
	}

	sort.Slice(files, func(i, j int) bool { return files[i].serial < files[j].serial })

	for i := 0; i < len(files)-maxFrames; i++ {
		os.Remove(filepath.Join(dir, files[i].name))
	}

	return nil
}

func (h *JetsonHandler) HandleJetsonStream(r io.Reader, kind FrameKind) error {
	payload, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read payload: %w", err)
	}

	if len(payload) < 1 {
		return fmt.Errorf("payload too short")
	}

	frameData := payload[1:]

	dir := filepath.Join(h.baseDir, "rgb")
	if kind == FrameKindDepth {
		dir = filepath.Join(h.baseDir, "depth")
	}

	h.mu.Lock()
	serial := h.nextSerial(kind)
	h.mu.Unlock()

	filename := fmt.Sprintf("%d.jpg", serial)
	filepath := filepath.Join(dir, filename)

	fmt.Printf("[JetsonHandler] Writing frame: kind=%v, serial=%d, size=%d, path=%s\n", kind, serial, len(frameData), filepath)
	if err := os.WriteFile(filepath, frameData, 0644); err != nil {
		return fmt.Errorf("failed to write frame file: %w", err)
	}

	if err := h.cleanupOldFrames(dir); err != nil {
		fmt.Printf("[JetsonHandler] Cleanup error (non-critical): %v\n", err)
	}

	fmt.Printf("[JetsonHandler] Frame saved successfully\n")
	return nil
}

func (h *JetsonHandler) HandleJetsonRawStream(frameData []byte, kind FrameKind) error {
	if len(frameData) < 1 {
		return fmt.Errorf("frame data too short")
	}

	dir := filepath.Join(h.baseDir, "rgb")
	if kind == FrameKindDepth {
		dir = filepath.Join(h.baseDir, "depth")
	}

	h.mu.Lock()
	serial := h.nextSerial(kind)
	h.mu.Unlock()

	filename := fmt.Sprintf("%d.jpg", serial)
	filepath := filepath.Join(dir, filename)

	fmt.Printf("[JetsonHandler] Writing frame: kind=%v, serial=%d, size=%d, path=%s\n", kind, serial, len(frameData), filepath)
	if err := os.WriteFile(filepath, frameData, 0644); err != nil {
		return fmt.Errorf("failed to write frame file: %w", err)
	}

	if err := h.cleanupOldFrames(dir); err != nil {
		fmt.Printf("[JetsonHandler] Cleanup error (non-critical): %v\n", err)
	}

	fmt.Printf("[JetsonHandler] Frame saved successfully\n")
	return nil
}
