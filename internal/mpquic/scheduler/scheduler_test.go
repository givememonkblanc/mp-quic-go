package scheduler

import (
	"testing"

	"mp-quic-go/internal/mpquic/path"
)

func TestPrimaryPathSchedulerSelectsHighestRSSI(t *testing.T) {
	s := NewPrimaryPathScheduler()
	paths := []path.State{
		state(1, path.StatusAvailable, true, -70, true),
		state(2, path.StatusActive, true, -45, true),
		state(3, path.StatusActive, true, -60, true),
	}
	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected a selected path")
	}
	if selected.ID != 2 {
		t.Fatalf("expected highest RSSI path, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerFallsBackWithoutRSSI(t *testing.T) {
	s := NewPrimaryPathScheduler()
	paths := []path.State{
		state(3, path.StatusAvailable, true, 0, false),
		state(2, path.StatusActive, true, 0, false),
		state(1, path.StatusBackup, true, 0, false),
	}
	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected a selected path")
	}
	if selected.ID != 2 {
		t.Fatalf("expected active fallback path, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerBreaksRSSITiesDeterministically(t *testing.T) {
	s := NewPrimaryPathScheduler()
	paths := []path.State{
		state(5, path.StatusAvailable, true, -50, true),
		state(4, path.StatusActive, true, -50, true),
		state(3, path.StatusActive, true, -50, true),
	}
	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected a selected path")
	}
	if selected.ID != 3 {
		t.Fatalf("expected lowest-ID active path on tie, got %d", selected.ID)
	}
}
