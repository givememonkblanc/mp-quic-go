package rssi

import (
	"context"
	"math"
	"testing"
)

func TestParseSignal(t *testing.T) {
	out := `Connected to aa:bb:cc:dd:ee:ff (on wlP1p1s0)
	SSID: solfac 220 5G
	freq: 5180
	signal: -67 dBm
	tx bitrate: 130.0 MBit/s`
	v, err := ParseSignal(out)
	if err != nil || v != -67 {
		t.Fatalf("ParseSignal=%d,%v want -67,nil", v, err)
	}
	if _, err := ParseSignal("Not connected."); err == nil {
		t.Fatalf("expected error on not-connected output")
	}
}

func TestCollector_EWMA(t *testing.T) {
	seq := []struct {
		v  int
		ok bool
	}{{-60, true}, {-80, true}, {-80, true}, {-80, true}}
	i := 0
	c := NewLocalCollector("wlan0", 0.5)
	c.read = func(_ context.Context, _ string) (int, bool) {
		s := seq[i]
		i++
		return s.v, s.ok
	}
	got := []float64{}
	for range seq {
		got = append(got, c.Sample(context.Background()).EWMA)
	}
	// alpha=0.5: -60, -70, -75, -77.5
	want := []float64{-60, -70, -75, -77.5}
	for j := range want {
		if math.Abs(got[j]-want[j]) > 1e-9 {
			t.Fatalf("EWMA[%d]=%v want %v", j, got[j], want[j])
		}
	}
}

// An invalid read must hold the EWMA (not poison it) and report Valid=false.
func TestCollector_InvalidHoldsEWMA(t *testing.T) {
	vals := []struct {
		v  int
		ok bool
	}{{-65, true}, {0, false}, {-65, true}}
	i := 0
	c := NewLocalCollector("wlan0", 0.3)
	c.read = func(_ context.Context, _ string) (int, bool) {
		s := vals[i]
		i++
		return s.v, s.ok
	}
	s0 := c.Sample(context.Background())
	if !s0.Valid || s0.EWMA != -65 {
		t.Fatalf("s0=%+v", s0)
	}
	s1 := c.Sample(context.Background())
	if s1.Valid || s1.EWMA != -65 {
		t.Fatalf("invalid sample must hold EWMA and be invalid: %+v", s1)
	}
	s2 := c.Sample(context.Background())
	if !s2.Valid || s2.EWMA != -65 {
		t.Fatalf("s2=%+v", s2)
	}
	if v, ok := c.EWMAInt(); !ok || v != -65 {
		t.Fatalf("EWMAInt=%d,%v want -65,true", v, ok)
	}
}
