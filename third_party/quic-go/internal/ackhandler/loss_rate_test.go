package ackhandler

import "testing"

func TestSentPacketHandlerLossRate(t *testing.T) {
	h := &sentPacketHandler{}
	if h.LossRate() != 0 {
		t.Fatalf("loss rate with no packets sent must be 0, got %v", h.LossRate())
	}
	h.appDataSent = 100
	h.appDataLost = 5
	if got := h.LossRate(); got != 0.05 {
		t.Fatalf("loss rate = lost/sent: got %v want 0.05", got)
	}
	h.appDataLost = 100
	if got := h.LossRate(); got != 1.0 {
		t.Fatalf("full loss must be 1.0, got %v", got)
	}
}
