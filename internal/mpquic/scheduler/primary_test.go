package scheduler

import (
	"testing"

	"mp-quic-go/internal/mpquic/path"
)

func TestPrimaryPathSchedulerSelectsActiveFallbackWithoutRSSI(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(2, path.StatusAvailable, true, 0, false),
		state(1, path.StatusActive, true, 0, false),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a fallback path")
	}

	if selected.ID != 1 {
		t.Fatalf("expected active path ID 1 to be selected, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerSelectsLowestIDFallbackWhenStatusesAreEqual(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(3, path.StatusAvailable, true, 0, false),
		state(1, path.StatusAvailable, true, 0, false),
		state(2, path.StatusAvailable, true, 0, false),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a fallback path")
	}

	if selected.ID != 1 {
		t.Fatalf("expected lowest path ID 1 to be selected, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerSelectsHighestRSSIPath(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusActive, true, -70, true),
		state(1, path.StatusAvailable, true, -55, true),
		state(2, path.StatusAvailable, true, -65, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a path")
	}

	if selected.ID != 1 {
		t.Fatalf("expected path ID 1 with strongest RSSI to be selected, got %d", selected.ID)
	}

	if selected.RSSI != -55 {
		t.Fatalf("expected selected RSSI -55, got %d", selected.RSSI)
	}
}

func TestPrimaryPathSchedulerPrefersRSSIPathOverFallback(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusActive, true, 0, false),
		state(1, path.StatusAvailable, true, -60, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a path")
	}

	if selected.ID != 1 {
		t.Fatalf("expected RSSI-bearing path ID 1 to be selected, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerTieBreaksRSSIByActiveStatus(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(1, path.StatusAvailable, true, -60, true),
		state(2, path.StatusActive, true, -60, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a path")
	}

	if selected.ID != 2 {
		t.Fatalf("expected active path ID 2 to win RSSI tie, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerTieBreaksRSSIByLowestPathID(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(3, path.StatusAvailable, true, -60, true),
		state(1, path.StatusAvailable, true, -60, true),
		state(2, path.StatusAvailable, true, -60, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a path")
	}

	if selected.ID != 1 {
		t.Fatalf("expected lowest path ID 1 to win RSSI tie, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerIgnoresInactivePaths(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusAbandoned, true, -40, true),
		state(1, path.StatusAvailable, true, -70, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select available path")
	}

	if selected.ID != 1 {
		t.Fatalf("expected available path ID 1 to be selected, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerReturnsFalseWhenNoUsablePathExists(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusAbandoned, false, -40, true),
		state(1, path.StatusAbandoned, false, -50, true),
	}

	selected, ok := s.SelectPath(paths)
	if ok {
		t.Fatalf("expected no path to be selected, got %#v", selected)
	}
}

func TestPrimaryPathSchedulerName(t *testing.T) {
	s := NewPrimaryPathScheduler()

	if got := s.Name(); got != "rssi-primary-path" {
		t.Fatalf("unexpected scheduler name: %q", got)
	}
}

// This test describes the desired safety behavior.
// Current implementation will likely fail this test because SelectPath does not check Validated.
// Keep this test if the next step is to harden scheduler selection policy.
func TestPrimaryPathSchedulerIgnoresUnvalidatedPath(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusActive, true, -70, true),
		state(1, path.StatusAvailable, false, -40, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a validated path")
	}

	if selected.ID != 0 {
		t.Fatalf("expected validated path ID 0 to be selected, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerIgnoresRSSIOnInactivePath(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusActive, true, -80, true),
		state(1, path.StatusAbandoned, true, -30, true),
	}

	selected, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select active path")
	}

	if selected.ID != 0 {
		t.Fatalf("expected active path ID 0 to be selected, got %d", selected.ID)
	}
}

func TestPrimaryPathSchedulerDoesNotMutateInputStates(t *testing.T) {
	s := NewPrimaryPathScheduler()

	paths := []path.State{
		state(0, path.StatusActive, true, -70, true),
		state(1, path.StatusAvailable, true, -50, true),
	}

	before := append([]path.State(nil), paths...)

	_, ok := s.SelectPath(paths)
	if !ok {
		t.Fatal("expected scheduler to select a path")
	}

	for i := range paths {
		if paths[i] != before[i] {
			t.Fatalf("scheduler mutated input state at index %d: before=%#v after=%#v", i, before[i], paths[i])
		}
	}
}

