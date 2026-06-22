package pqi

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMinMaxNormalize(t *testing.T) {
	got := MinMaxNormalize([]float64{10, 20, 30})
	want := []float64{0, 0.5, 1}
	for i := range want {
		if !approx(got[i], want[i]) {
			t.Fatalf("idx %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestMinMaxNormalizeAllEqual(t *testing.T) {
	// When values don't differ, the metric is neutral (all zeros).
	got := MinMaxNormalize([]float64{5, 5, 5})
	for i, v := range got {
		if v != 0 {
			t.Fatalf("idx %d: got %v want 0 (neutral)", i, v)
		}
	}
}

func TestMinMaxNormalizeEmptyAndSingle(t *testing.T) {
	if len(MinMaxNormalize(nil)) != 0 {
		t.Fatal("empty input must return empty")
	}
	got := MinMaxNormalize([]float64{42})
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("single value must normalize to 0, got %v", got)
	}
}
