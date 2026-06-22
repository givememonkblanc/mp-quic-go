package pqi

import (
	"testing"
	"time"
)

func TestHandoverRequiresDegradation(t *testing.T) {
	c := NewHandoverController(DefaultHysteresis())
	t0 := time.Unix(0, 0)
	// Active path (idx 0) is healthy (PQI 80) even though path 1 is higher.
	// No handover: the active path is not degraded.
	for i := 0; i < 10; i++ {
		got := c.Decide(0, []float64{80, 95}, t0.Add(time.Duration(i)*time.Second))
		if got != 0 {
			t.Fatalf("healthy active path must not hand over, got %d", got)
		}
	}
}

func TestHandoverRequiresMargin(t *testing.T) {
	c := NewHandoverController(DefaultHysteresis()) // margin 10
	t0 := time.Unix(0, 0)
	// Active degraded (30) but candidate only 5 better -> below safety margin.
	for i := 0; i < 10; i++ {
		got := c.Decide(0, []float64{30, 35}, t0.Add(time.Duration(i)*time.Second))
		if got != 0 {
			t.Fatalf("sub-margin improvement must not hand over, got %d", got)
		}
	}
}

func TestHandoverCommitsAfterStability(t *testing.T) {
	p := HysteresisParams{DegradationThreshold: 40, SafetyMargin: 10, StabilityInterval: 500 * time.Millisecond}
	c := NewHandoverController(p)
	t0 := time.Unix(0, 0)
	pqis := []float64{30, 90} // active degraded, candidate clearly better

	// First evaluation starts the stability timer, still on the active path.
	if got := c.Decide(0, pqis, t0); got != 0 {
		t.Fatalf("first tick must arm the timer, not switch: got %d", got)
	}
	// Before the interval elapses: still active.
	if got := c.Decide(0, pqis, t0.Add(400*time.Millisecond)); got != 0 {
		t.Fatalf("must not switch before stability interval, got %d", got)
	}
	// After the interval: commit handover to path 1.
	if got := c.Decide(0, pqis, t0.Add(600*time.Millisecond)); got != 1 {
		t.Fatalf("must hand over after stability interval, got %d", got)
	}
}

// TestHandoverAntiFlapping: a candidate that only momentarily looks best (then
// the condition clears) must not trigger a switch.
func TestHandoverAntiFlapping(t *testing.T) {
	p := HysteresisParams{DegradationThreshold: 40, SafetyMargin: 10, StabilityInterval: 500 * time.Millisecond}
	c := NewHandoverController(p)
	t0 := time.Unix(0, 0)

	c.Decide(0, []float64{30, 90}, t0)                          // arm
	c.Decide(0, []float64{70, 60}, t0.Add(200*time.Millisecond)) // condition clears (active recovered)
	if got := c.Decide(0, []float64{30, 90}, t0.Add(300*time.Millisecond)); got != 0 {
		t.Fatalf("timer must reset when condition clears; should not switch yet, got %d", got)
	}
}
