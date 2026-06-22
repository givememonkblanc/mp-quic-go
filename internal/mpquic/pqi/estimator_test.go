package pqi

import (
	"testing"
	"time"
)

// TestComputePQIRanksBetterPathHigher: a path with lower RTT, lower loss, and
// higher bandwidth must get a strictly higher PQI.
func TestComputePQIRanksBetterPathHigher(t *testing.T) {
	good := Metrics{RTT: 10 * time.Millisecond, LossRate: 0.0, Bandwidth: 100e6}
	bad := Metrics{RTT: 100 * time.Millisecond, LossRate: 0.1, Bandwidth: 10e6}
	pqi := ComputePQI([]Metrics{good, bad}, DefaultWeights())
	if !(pqi[0] > pqi[1]) {
		t.Fatalf("good path PQI %.1f must exceed bad path PQI %.1f", pqi[0], pqi[1])
	}
	// The best path on every normalized metric has cost 0 -> PQI 100.
	if pqi[0] < 99.999 {
		t.Fatalf("dominant path should reach PQI ~100, got %.3f", pqi[0])
	}
}

// TestComputePQIWeightsMatter: shifting all weight to loss makes the lossy path
// rank lowest regardless of RTT.
func TestComputePQIWeightsMatter(t *testing.T) {
	lowRTThighLoss := Metrics{RTT: 10 * time.Millisecond, LossRate: 0.2, Bandwidth: 50e6}
	highRTTnoLoss := Metrics{RTT: 80 * time.Millisecond, LossRate: 0.0, Bandwidth: 50e6}
	w := Weights{Alpha: 0, Beta: 1, Gamma: 0}
	pqi := ComputePQI([]Metrics{lowRTThighLoss, highRTTnoLoss}, w)
	if !(pqi[1] > pqi[0]) {
		t.Fatalf("with loss-only weighting the lossless path must win: %.1f vs %.1f", pqi[1], pqi[0])
	}
}

// TestEstimatorEWMASmoothing: a step change is approached gradually.
func TestEstimatorEWMASmoothing(t *testing.T) {
	e := NewEstimator(0.5, 10)
	e.Update(0)
	v := e.Update(100)
	if v <= 0 || v >= 100 {
		t.Fatalf("EWMA after step should be strictly between old and new, got %.1f", v)
	}
	if !approx(v, 50) {
		t.Fatalf("lambda=0.5 step 0->100 gives 50, got %.3f", v)
	}
}

// TestEstimatorTrend: a steadily falling PQI yields a negative trend.
func TestEstimatorTrend(t *testing.T) {
	e := NewEstimator(1.0, 5) // lambda 1 = no smoothing, value == raw
	for _, v := range []float64{90, 80, 70, 60, 50} {
		e.Update(v)
	}
	if e.Trend() >= 0 {
		t.Fatalf("declining series must have negative trend, got %.2f", e.Trend())
	}
	if e.Value() != 50 {
		t.Fatalf("lambda=1 value should equal last raw, got %.1f", e.Value())
	}
}
