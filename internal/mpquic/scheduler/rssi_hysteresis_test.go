package scheduler

import (
	"testing"

	"mp-quic-go/internal/mpquic/path"
)

func TestNewPrimaryPathScheduler(t *testing.T) {
	s := NewPrimaryPathScheduler()

	if s == nil {
		t.Fatal("expected scheduler instance, got nil")
	}

	if s.Name() != "rssi-primary-path" {
		t.Fatalf("unexpected scheduler name: %q", s.Name())
	}
}

func TestPrimaryPathSchedulerImplementsSchedulerInterface(t *testing.T) {
	var _ Scheduler = NewPrimaryPathScheduler()
}

func TestPrimaryPathSchedulerSelectPathWithNilSlice(t *testing.T) {
	s := NewPrimaryPathScheduler()

	selected, ok := s.SelectPath(nil)
	if ok {
		t.Fatalf("expected no path to be selected from nil slice, got %#v", selected)
	}
}

func TestPrimaryPathSchedulerSelectPathWithEmptySlice(t *testing.T) {
	s := NewPrimaryPathScheduler()

	selected, ok := s.SelectPath([]path.State{})
	if ok {
		t.Fatalf("expected no path to be selected from empty slice, got %#v", selected)
	}
}

func TestBetterRSSIPrefersHigherRSSI(t *testing.T) {
	candidate := primaryState(1, path.StatusAvailable, true, -50, true)
	current := primaryState(0, path.StatusActive, true, -70, true)

	if !betterRSSI(candidate, current) {
		t.Fatalf("expected candidate with higher RSSI to be better: candidate=%#v current=%#v", candidate, current)
	}
}

func TestBetterRSSIRejectsLowerRSSI(t *testing.T) {
	candidate := primaryState(1, path.StatusAvailable, true, -80, true)
	current := primaryState(0, path.StatusActive, true, -60, true)

	if betterRSSI(candidate, current) {
		t.Fatalf("expected candidate with lower RSSI not to be better: candidate=%#v current=%#v", candidate, current)
	}
}

func TestBetterRSSITieUsesFallbackRules(t *testing.T) {
	candidate := primaryState(1, path.StatusActive, true, -60, true)
	current := primaryState(0, path.StatusAvailable, true, -60, true)

	if !betterRSSI(candidate, current) {
		t.Fatalf("expected active candidate to win RSSI tie: candidate=%#v current=%#v", candidate, current)
	}
}

func TestBetterFallbackPrefersActiveOverAvailable(t *testing.T) {
	candidate := primaryState(2, path.StatusActive, true, 0, false)
	current := primaryState(1, path.StatusAvailable, true, 0, false)

	if !betterFallback(candidate, current) {
		t.Fatalf("expected active path to be preferred over available path: candidate=%#v current=%#v", candidate, current)
	}
}

func TestBetterFallbackRejectsAvailableWhenCurrentIsActive(t *testing.T) {
	candidate := primaryState(2, path.StatusAvailable, true, 0, false)
	current := primaryState(1, path.StatusActive, true, 0, false)

	if betterFallback(candidate, current) {
		t.Fatalf("expected available path not to beat active current path: candidate=%#v current=%#v", candidate, current)
	}
}

func TestBetterFallbackPrefersLowerPathIDWhenStatusEqual(t *testing.T) {
	candidate := primaryState(1, path.StatusAvailable, true, 0, false)
	current := primaryState(2, path.StatusAvailable, true, 0, false)

	if !betterFallback(candidate, current) {
		t.Fatalf("expected lower path ID to be preferred: candidate=%#v current=%#v", candidate, current)
	}
}

func TestBetterFallbackRejectsHigherPathIDWhenStatusEqual(t *testing.T) {
	candidate := primaryState(3, path.StatusAvailable, true, 0, false)
	current := primaryState(2, path.StatusAvailable, true, 0, false)

	if betterFallback(candidate, current) {
		t.Fatalf("expected higher path ID not to be preferred: candidate=%#v current=%#v", candidate, current)
	}
}

func TestPrimaryPathSchedulerSelectPathUsesFallbackWhenNoRSSIExists(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		primaryState(3, path.StatusAvailable, true, 0, false),
		primaryState(2, path.StatusAvailable, true, 0, false),
		primaryState(1, path.StatusActive, true, 0, false),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected fallback path to be selected")
	}

	if selected.ID != 1 {
		t.Fatalf("expected active fallback path ID 1, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerSelectPathUsesRSSIWhenAvailable(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		primaryState(0, path.StatusActive, true, 0, false),
		primaryState(1, path.StatusAvailable, true, -55, true),
		primaryState(2, path.StatusAvailable, true, -65, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected RSSI path to be selected")
	}

	if selected.ID != 1 {
		t.Fatalf("expected path ID 1 with strongest RSSI, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerSelectPathIgnoresUnavailableStatuses(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		primaryState(0, path.StatusAbandoned, true, -30, true),
		primaryState(1, path.StatusAbandoned, true, -40, true),
	}

	selected, ok := s.SelectPath(paths)
	if ok {
		t.Fatalf("expected no usable path, got %#v", selected)
	}
}

func TestPrimaryPathSchedulerSelectPathDeterministicRegardlessOfInputOrder(t *testing.T) {
	s := NewPrimaryPathScheduler()

	pathsA := []path.State{
		primaryState(2, path.StatusAvailable, true, -60, true),
		primaryState(1, path.StatusAvailable, true, -60, true),
		primaryState(3, path.StatusAvailable, true, -60, true),
	}

	pathsB := []path.State{
		primaryState(3, path.StatusAvailable, true, -60, true),
		primaryState(2, path.StatusAvailable, true, -60, true),
		primaryState(1, path.StatusAvailable, true, -60, true),
	}

	selectedA, okA := s.SelectPath(pathsA)
	selectedB, okB := s.SelectPath(pathsB)

	if !okA || !okB {
		t.Fatalf("expected both selections to succeed: okA=%v okB=%v", okA, okB)
	}

	if selectedA.ID != selectedB.ID {
		t.Fatalf("expected deterministic selection regardless of order, got A=%d B=%d", selectedA.ID, selectedB.ID)
	}

	if selectedA.ID != 1 {
		t.Fatalf("expected lowest path ID 1 to win tie, got %d", selectedA.ID)
	}
}

