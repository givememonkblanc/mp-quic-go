package scheduler

import (
	"testing"
	"time"

	"mp-quic-go/internal/mpquic/path"
)

func TestMinRTTSchedulerPicksLowestRTT(t *testing.T) {
	s := NewMinRTTScheduler()
	paths := []path.State{
		metricState(1, 50*time.Millisecond, 0, 0),
		metricState(2, 12*time.Millisecond, 0, 0),
		metricState(3, 30*time.Millisecond, 0, 0),
	}
	got, ok := s.SelectPath(paths)
	if !ok || got.ID != 2 {
		t.Fatalf("min-rtt should pick path 2 (12ms), got id=%d ok=%v", got.ID, ok)
	}
}

func TestMinRTTSchedulerSkipsUnvalidated(t *testing.T) {
	s := NewMinRTTScheduler()
	lowButUnvalidated := metricState(1, 5*time.Millisecond, 0, 0)
	lowButUnvalidated.Validated = false
	paths := []path.State{lowButUnvalidated, metricState(2, 40*time.Millisecond, 0, 0)}
	got, ok := s.SelectPath(paths)
	if !ok || got.ID != 2 {
		t.Fatalf("must ignore unvalidated low-RTT path, got id=%d ok=%v", got.ID, ok)
	}
}

func TestRoundRobinSchedulerCycles(t *testing.T) {
	s := NewRoundRobinScheduler()
	paths := []path.State{
		metricState(1, 10*time.Millisecond, 0, 0),
		metricState(2, 10*time.Millisecond, 0, 0),
	}
	var seq []path.ID
	for i := 0; i < 4; i++ {
		got, ok := s.SelectPath(paths)
		if !ok {
			t.Fatal("round-robin must select a path")
		}
		seq = append(seq, got.ID)
	}
	want := []path.ID{1, 2, 1, 2}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("round-robin sequence %v, want %v", seq, want)
		}
	}
}

func TestSchedulerFactory(t *testing.T) {
	cases := map[string]string{
		"":            "pqi",
		"pqi":         "pqi",
		"min-rtt":     "min-rtt",
		"round-robin": "round-robin",
		"rssi":        "rssi-primary-path",
	}
	for name, wantName := range cases {
		s, err := New(name)
		if err != nil {
			t.Fatalf("New(%q) error: %v", name, err)
		}
		if s.Name() != wantName {
			t.Fatalf("New(%q).Name() = %q, want %q", name, s.Name(), wantName)
		}
	}
	if _, err := New("bogus"); err == nil {
		t.Fatal("unknown scheduler must error")
	}
}
