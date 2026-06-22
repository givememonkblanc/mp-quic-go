package pqi

import "time"

// HysteresisParams configures the handover decision so that paths are not
// switched on transient noise (anti-flapping).
type HysteresisParams struct {
	// DegradationThreshold (T_deg): the active path's PQI must fall below this
	// for a handover to be considered.
	DegradationThreshold float64
	// SafetyMargin (Δ_margin): the candidate path's PQI must exceed the active
	// path's by at least this margin.
	SafetyMargin float64
	// StabilityInterval (T_stable): the handover condition must hold continuously
	// for at least this long before the switch is committed.
	StabilityInterval time.Duration
}

// DefaultHysteresis returns the default anti-flapping parameters.
func DefaultHysteresis() HysteresisParams {
	return HysteresisParams{
		DegradationThreshold: 40,
		SafetyMargin:         10,
		StabilityInterval:    500 * time.Millisecond,
	}
}

// HandoverController decides whether to switch from the active path to a better
// candidate. It combines a degradation threshold (only hand over when the active
// path is actually bad), a safety margin (the candidate must be clearly better),
// and a stability interval (the condition must persist), implementing the
// hysteresis described in the paper.
type HandoverController struct {
	p HysteresisParams

	hasPending    bool
	pendingTarget int
	pendingSince  time.Time
}

// NewHandoverController creates a controller with the given parameters.
func NewHandoverController(p HysteresisParams) *HandoverController {
	return &HandoverController{p: p}
}

// Decide returns the path index that should be active. active is the current
// active index; pqis holds the smoothed PQI per path in the same order; now is
// the current time. The active path is kept unless the handover condition
// (active degraded AND a candidate better by the safety margin) has held
// continuously for the stability interval, at which point the candidate is
// returned. A change of best candidate restarts the stability timer.
func (c *HandoverController) Decide(active int, pqis []float64, now time.Time) int {
	if active < 0 || active >= len(pqis) || len(pqis) == 0 {
		return active
	}
	best := active
	for i, v := range pqis {
		if v > pqis[best] {
			best = i
		}
	}

	cond := pqis[active] < c.p.DegradationThreshold &&
		best != active &&
		pqis[best]-pqis[active] > c.p.SafetyMargin

	if !cond {
		c.hasPending = false
		return active
	}
	if !c.hasPending || c.pendingTarget != best {
		// Start (or restart) the stability timer for this candidate.
		c.hasPending = true
		c.pendingTarget = best
		c.pendingSince = now
		return active
	}
	if now.Sub(c.pendingSince) >= c.p.StabilityInterval {
		c.hasPending = false
		return best
	}
	return active
}
