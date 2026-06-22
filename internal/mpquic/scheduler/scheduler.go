package scheduler

import "mp-quic-go/internal/mpquic/path"

type Scheduler interface {
	SelectPath([]path.State) (path.State, bool)
	Name() string
}

type PrimaryPathScheduler struct{}

func NewPrimaryPathScheduler() *PrimaryPathScheduler {
	return &PrimaryPathScheduler{}
}

func (s *PrimaryPathScheduler) SelectPath(paths []path.State) (path.State, bool) {
	var (
		best        path.State
		bestSet     bool
		fallback    path.State
		fallbackSet bool
	)

	for _, candidate := range paths {
		// Skip unvalidated paths
		if !candidate.Validated {
			continue
		}

		if candidate.Status != path.StatusActive && candidate.Status != path.StatusAvailable {
			continue
		}

		if !fallbackSet || betterFallback(candidate, fallback) {
			fallback = candidate
			fallbackSet = true
		}

		if !candidate.HasRSSI {
			continue
		}

		if !bestSet || betterRSSI(candidate, best) {
			best = candidate
			bestSet = true
		}
	}

	if bestSet {
		return best, true
	}
	if fallbackSet {
		return fallback, true
	}
	return path.State{}, false
}

func (s *PrimaryPathScheduler) Name() string {
	return "rssi-primary-path"
}

func betterRSSI(candidate, current path.State) bool {
	if candidate.RSSI != current.RSSI {
		return candidate.RSSI > current.RSSI
	}
	return betterFallback(candidate, current)
}

func betterFallback(candidate, current path.State) bool {
	if candidate.Status != current.Status {
		return candidate.Status == path.StatusActive
	}
	return candidate.ID < current.ID
}
