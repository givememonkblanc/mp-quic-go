package conn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type recordingPayloadHandler struct {
	calls   int
	payload []byte
	err     error
}

func (h *recordingPayloadHandler) Handle(ctx context.Context, payload []byte) ([]byte, error) {
	h.calls++
	h.payload = append([]byte(nil), payload...)

	if h.err != nil {
		return nil, h.err
	}

	return nil, nil
}

func TestNewStreamHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	downstream := &recordingPayloadHandler{}

	h := NewStreamHandler(logger, downstream)
	if h == nil {
		t.Fatal("expected non-nil stream handler")
	}
}

func TestStreamHandlerDispatchesPayload(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	downstream := &recordingPayloadHandler{}

	h := NewStreamHandler(logger, downstream)

	payload := []byte("hello stream payload")

	err := h.HandleReader(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("handle reader failed: %v", err)
	}

	if downstream.calls != 1 {
		t.Fatalf("expected downstream handler to be called once, got %d", downstream.calls)
	}

	if !bytes.Equal(downstream.payload, payload) {
		t.Fatalf("unexpected downstream payload: got=%q want=%q", downstream.payload, payload)
	}
}

func TestStreamHandlerPropagatesDownstreamError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	expectedErr := errors.New("downstream failed")

	downstream := &recordingPayloadHandler{
		err: expectedErr,
	}

	h := NewStreamHandler(logger, downstream)

	err := h.HandleReader(context.Background(), bytes.NewReader([]byte("payload")))
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected downstream error %v, got %v", expectedErr, err)
	}
}

func TestStreamHandlerPropagatesReadError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	downstream := &recordingPayloadHandler{}

	h := NewStreamHandler(logger, downstream)

	expectedErr := errors.New("read failed")
	reader := errReader{err: expectedErr}

	err := h.HandleReader(context.Background(), reader)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected read error %v, got %v", expectedErr, err)
	}

	if downstream.calls != 0 {
		t.Fatalf("expected downstream handler not to be called, got %d calls", downstream.calls)
	}
}

func TestStreamHandlerHandlesEmptyStream(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	downstream := &recordingPayloadHandler{}

	h := NewStreamHandler(logger, downstream)

	err := h.HandleReader(context.Background(), bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected empty stream to fail")
	}

	if downstream.calls != 0 {
		t.Fatalf("expected downstream handler not to be called for empty stream, got %d calls", downstream.calls)
	}
}

func TestStreamHandlerDoesNotMutatePayload(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	downstream := &recordingPayloadHandler{}

	h := NewStreamHandler(logger, downstream)

	payload := []byte("immutable payload")
	original := append([]byte(nil), payload...)

	err := h.HandleReader(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("handle reader failed: %v", err)
	}

	if !bytes.Equal(payload, original) {
		t.Fatalf("input payload was mutated: got=%q want=%q", payload, original)
	}
}


