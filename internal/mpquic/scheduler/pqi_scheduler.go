package scheduler

import (
	"time"

	"mp-quic-go/internal/mpquic/path"
	"mp-quic-go/internal/mpquic/pqi"
)

// PQIConfig holds the tunable parameters of the PQI scheduler. Defaults mirror
// the values reported in the paper and can be overridden from configuration.
type PQIConfig struct {
	Weights pqi.Weights          // alpha/beta/gamma for RTT/loss/bandwidth
	Lambda  float64              // EWMA smoothing factor
	Window  int                  // trend sliding-window size (samples)
	Hyst    pqi.HysteresisParams // degradation threshold / safety margin / stability interval
}

// DefaultPQIConfig returns the default PQI scheduler configuration.
func DefaultPQIConfig() PQIConfig {
	return PQIConfig{
		Weights: pqi.DefaultWeights(),
		Lambda:  0.3,
		Window:  10,
		Hyst:    pqi.DefaultHysteresis(),
	}
}

// PQIScheduler ranks paths by the Path Quality Index and applies EWMA smoothing
// plus hysteresis so handovers happen when a path genuinely and persistently
// degrades, rather than on transient noise. It is stateful across calls (it
// remembers the active path and each path's smoothed PQI), so a single instance
// must be used for one connection.
type PQIScheduler struct {
	cfg        PQIConfig
	estimators map[path.ID]*pqi.Estimator
	controller *pqi.HandoverController
	active     path.ID
	hasActive  bool
	now        func() time.Time
}

// NewPQIScheduler creates a PQI scheduler with the given configuration.
func NewPQIScheduler(cfg PQIConfig) *PQIScheduler {
	return &PQIScheduler{
		cfg:        cfg,
		estimators: make(map[path.ID]*pqi.Estimator),
		controller: pqi.NewHandoverController(cfg.Hyst),
		now:        time.Now,
	}
}

// SelectPath chooses the path to use. It computes each schedulable path's PQI,
// smooths it (EWMA) per path, and returns the active path unless the hysteresis
// controller decides a persistently-better candidate warrants a handover.
func (s *PQIScheduler) SelectPath(paths []path.State) (path.State, bool) {
	cand := make([]path.State, 0, len(paths))
	for _, p := range paths {
		if !p.Validated {
			continue
		}
		if p.Status != path.StatusActive && p.Status != path.StatusAvailable {
			continue
		}
		cand = append(cand, p)
	}
	if len(cand) == 0 {
		return path.State{}, false
	}

	metrics := make([]pqi.Metrics, len(cand))
	for i, p := range cand {
		metrics[i] = pqi.Metrics{RTT: p.RTT, LossRate: p.LossRate, Bandwidth: p.Bandwidth}
	}
	raw := pqi.ComputePQI(metrics, s.cfg.Weights)

	smoothed := make([]float64, len(cand))
	for i, p := range cand {
		smoothed[i] = s.estimatorFor(p.ID).Update(raw[i])
	}

	// Cold start: with no established active path, pick the best path directly
	// (hysteresis only governs switching away from an already-active path).
	activeIdx := -1
	if s.hasActive {
		for i, p := range cand {
			if p.ID == s.active {
				activeIdx = i
				break
			}
		}
	}
	if activeIdx < 0 {
		activeIdx = 0
		for i := range smoothed {
			if smoothed[i] > smoothed[activeIdx] {
				activeIdx = i
			}
		}
	}

	chosen := s.controller.Decide(activeIdx, smoothed, s.now())
	s.active = cand[chosen].ID
	s.hasActive = true
	return cand[chosen], true
}

// Name identifies the scheduler.
func (s *PQIScheduler) Name() string { return "pqi" }

// PQI returns the current smoothed PQI for a path, if it has been observed.
func (s *PQIScheduler) PQI(id path.ID) (float64, bool) {
	if e, ok := s.estimators[id]; ok {
		return e.Value(), true
	}
	return 0, false
}

func (s *PQIScheduler) estimatorFor(id path.ID) *pqi.Estimator {
	e, ok := s.estimators[id]
	if !ok {
		e = pqi.NewEstimator(s.cfg.Lambda, s.cfg.Window)
		s.estimators[id] = e
	}
	return e
}
