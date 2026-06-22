package handler

import "context"

// EchoHandler returns the input payload unchanged.
type EchoHandler struct{}

func NewEchoHandler() *EchoHandler {
	return &EchoHandler{}
}

func (h *EchoHandler) Handle(_ context.Context, payload []byte) ([]byte, error) {
	response := make([]byte, len(payload))
	copy(response, payload)
	return response, nil
}
