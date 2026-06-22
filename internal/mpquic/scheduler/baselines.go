package scheduler

import "mp-quic-go/internal/mpquic/path"

// schedulable reports whether a path can carry traffic (validated and in an
// active/available state). Shared by the baseline schedulers.
func schedulable(p path.State) bool {
	if !p.Validated {
		return false
	}
	return p.Status == path.StatusActive || p.Status == path.StatusAvailable
}

// MinRTTScheduler always sends on the schedulable path with the lowest smoothed
// RTT. It is a classic MP-QUIC baseline (e.g. quic-go's default lowest-RTT
// policy) used to isolate the benefit of the PQI scheduler in evaluation: only
// the scheduler differs, the rest of the stack is identical.
type MinRTTScheduler struct{}

// NewMinRTTScheduler creates a lowest-RTT scheduler.
func NewMinRTTScheduler() *MinRTTScheduler { return &MinRTTScheduler{} }

func (s *MinRTTScheduler) SelectPath(paths []path.State) (path.State, bool) {
	var best path.State
	found := false
	for _, p := range paths {
		if !schedulable(p) {
			continue
		}
		switch {
		case !found:
			best, found = p, true
		case p.HasMetrics && (!best.HasMetrics || p.RTT < best.RTT):
			best = p
		case !best.HasMetrics && !p.HasMetrics && p.ID < best.ID:
			best = p
		}
	}
	return best, found
}

func (s *MinRTTScheduler) Name() string { return "min-rtt" }

// RoundRobinScheduler cycles through the schedulable paths, distributing send
// opportunities evenly. It is the "default / naive" MP-QUIC baseline requested
// by the reviewers (no quality awareness).
type RoundRobinScheduler struct {
	next int
}

// NewRoundRobinScheduler creates a round-robin scheduler.
func NewRoundRobinScheduler() *RoundRobinScheduler { return &RoundRobinScheduler{} }

func (s *RoundRobinScheduler) SelectPath(paths []path.State) (path.State, bool) {
	cand := make([]path.State, 0, len(paths))
	for _, p := range paths {
		if schedulable(p) {
			cand = append(cand, p)
		}
	}
	if len(cand) == 0 {
		return path.State{}, false
	}
	p := cand[s.next%len(cand)]
	s.next++
	return p, true
}

func (s *RoundRobinScheduler) Name() string { return "round-robin" }
