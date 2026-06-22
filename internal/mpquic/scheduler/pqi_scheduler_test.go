package scheduler

import (
	"testing"
	"time"

	"mp-quic-go/internal/mpquic/path"
)

func metricState(id path.ID, rtt time.Duration, loss, bw float64) path.State {
	s := state(id, path.StatusActive, true, 0, false)
	s.RTT = rtt
	s.LossRate = loss
	s.Bandwidth = bw
	s.HasMetrics = true
	return s
}

// Cold start picks the best path by PQI immediately.
func TestPQISchedulerColdStartPicksBest(t *testing.T) {
	s := NewPQIScheduler(DefaultPQIConfig())
	paths := []path.State{
		metricState(1, 100*time.Millisecond, 0.1, 10e6), // worse
		metricState(2, 10*time.Millisecond, 0.0, 100e6), // better
	}
	got, ok := s.SelectPath(paths)
	if !ok || got.ID != 2 {
		t.Fatalf("cold start should pick best path (2), got id=%d ok=%v", got.ID, ok)
	}
}

// A healthy active path is kept even if another path has a higher PQI
// (no degradation -> no handover).
func TestPQISchedulerStickyWhenHealthy(t *testing.T) {
	s := NewPQIScheduler(DefaultPQIConfig())
	// Establish path 1 as active (it's the only good one initially).
	s.SelectPath([]path.State{metricState(1, 10*time.Millisecond, 0.0, 100e6)})

	// Now path 2 appears slightly better, but path 1 is still healthy.
	paths := []path.State{
		metricState(1, 12*time.Millisecond, 0.0, 95e6),
		metricState(2, 10*time.Millisecond, 0.0, 100e6),
	}
	got, _ := s.SelectPath(paths)
	if got.ID != 1 {
		t.Fatalf("healthy active path must be kept, switched to %d", got.ID)
	}
}

// When the active path degrades below threshold and a candidate is clearly and
// persistently better, hand over after the stability interval.
func TestPQISchedulerHandsOverOnPersistentDegradation(t *testing.T) {
	cfg := DefaultPQIConfig()
	cfg.Lambda = 1.0 // no smoothing, so degradation is immediate in the test
	cfg.Hyst.StabilityInterval = 500 * time.Millisecond
	s := NewPQIScheduler(cfg)

	clk := time.Unix(0, 0)
	s.now = func() time.Time { return clk }

	// Establish path 1 as active and healthy.
	s.SelectPath([]path.State{metricState(1, 10*time.Millisecond, 0.0, 100e6)})

	// Path 1 collapses (high RTT + loss, low bw), path 2 is great.
	degraded := []path.State{
		metricState(1, 200*time.Millisecond, 0.3, 5e6),
		metricState(2, 10*time.Millisecond, 0.0, 100e6),
	}

	// First degraded tick arms the timer; still on path 1.
	if got, _ := s.SelectPath(degraded); got.ID != 1 {
		t.Fatalf("first degraded tick should arm timer, not switch; got %d", got.ID)
	}
	// Before the interval elapses.
	clk = clk.Add(300 * time.Millisecond)
	if got, _ := s.SelectPath(degraded); got.ID != 1 {
		t.Fatalf("must not switch before stability interval; got %d", got.ID)
	}
	// After the interval: hand over to path 2.
	clk = clk.Add(300 * time.Millisecond)
	if got, _ := s.SelectPath(degraded); got.ID != 2 {
		t.Fatalf("must hand over to path 2 after stability interval; got %d", got.ID)
	}
}

func TestPQISchedulerNoSchedulablePaths(t *testing.T) {
	s := NewPQIScheduler(DefaultPQIConfig())
	_, ok := s.SelectPath([]path.State{state(1, path.StatusAbandoned, true, 0, false)})
	if ok {
		t.Fatal("no schedulable paths must return ok=false")
	}
}
