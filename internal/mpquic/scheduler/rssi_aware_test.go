package scheduler

import (
	"testing"
	"time"

	"mp-quic-go/internal/mpquic/path"
)

// clock is a manually-advanced time source for deterministic hysteresis tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newAware() (*RSSIAwareScheduler, *clock) {
	s := NewRSSIAwareScheduler(DefaultRSSIAwareConfig())
	c := &clock{t: time.Unix(1_000_000, 0)}
	s.now = c.now
	return s, c
}

// wifi is the primary (path 0) Wi-Fi path state with the given EWMA RSSI (dBm).
// up=false models a validation/link failure (no active/available status).
func wifi(rssi int, up bool) path.State {
	st := path.StatusActive
	if !up {
		st = path.Status("")
	}
	return path.State{ID: 0, Validated: true, Status: st, RSSI: rssi, HasRSSI: true}
}

// cell is the 5G backup path (path 1), validated and available.
func cell() path.State {
	return path.State{ID: 1, Validated: true, Status: path.StatusActive}
}

func sel(t *testing.T, s *RSSIAwareScheduler, p ...path.State) path.ID {
	t.Helper()
	st, ok := s.SelectPath(p)
	if !ok {
		t.Fatalf("SelectPath returned ok=false")
	}
	return st.ID
}

// Scenario A: stable, strong Wi-Fi must never switch to 5G.
func TestRSSIAware_StableGood_NoSwitch(t *testing.T) {
	s, c := newAware()
	for i := 0; i < 50; i++ {
		if id := sel(t, s, wifi(-55, true), cell()); id != 0 {
			t.Fatalf("iter %d: selected path %d, want 0 (Wi-Fi)", i, id)
		}
		c.advance(100 * time.Millisecond)
	}
	if s.Switches() != 0 {
		t.Fatalf("switches=%d, want 0", s.Switches())
	}
}

// Scenario B: gradual degradation switches to 5G only after the down-hold, not
// before, and not on a transient dip.
func TestRSSIAware_GradualDegradation_SwitchAfterHold(t *testing.T) {
	s, c := newAware()
	sel(t, s, wifi(-55, true), cell()) // establish primary

	// RSSI drops below DownThreshold (-72) but hold (500ms) not yet met.
	if id := sel(t, s, wifi(-75, true), cell()); id != 0 {
		t.Fatalf("at drop: path %d, want 0 (hold not met)", id)
	}
	c.advance(300 * time.Millisecond)
	if id := sel(t, s, wifi(-75, true), cell()); id != 0 {
		t.Fatalf("at +300ms: path %d, want 0 (hold not met)", id)
	}
	// Cross the 500ms down-hold -> switch to 5G.
	c.advance(250 * time.Millisecond)
	if id := sel(t, s, wifi(-75, true), cell()); id != 1 {
		t.Fatalf("at +550ms: path %d, want 1 (5G)", id)
	}
	if s.Switches() != 1 {
		t.Fatalf("switches=%d, want 1", s.Switches())
	}
}

func TestRSSIAware_TransientDip_NoSwitch(t *testing.T) {
	s, c := newAware()
	sel(t, s, wifi(-55, true), cell())
	c.advance(100 * time.Millisecond)
	sel(t, s, wifi(-75, true), cell()) // dip below -72
	c.advance(300 * time.Millisecond) // < 500ms hold
	sel(t, s, wifi(-55, true), cell()) // recovered before hold
	c.advance(2 * time.Second)
	if id := sel(t, s, wifi(-55, true), cell()); id != 0 {
		t.Fatalf("path %d, want 0 (transient dip should not switch)", id)
	}
	if s.Switches() != 0 {
		t.Fatalf("switches=%d, want 0", s.Switches())
	}
}

// Scenario C: sudden Wi-Fi failure fails over to 5G immediately, even within the
// minimum dwell window.
func TestRSSIAware_SuddenFailure_ImmediateSwitch(t *testing.T) {
	s, c := newAware()
	sel(t, s, wifi(-55, true), cell())
	c.advance(100 * time.Millisecond)
	// Wi-Fi link down -> FAILED -> immediate switch.
	if id := sel(t, s, wifi(-55, false), cell()); id != 1 {
		t.Fatalf("on failure: path %d, want 1 (immediate 5G)", id)
	}
	if s.State() != StateFailed {
		t.Fatalf("state=%s, want FAILED", s.State())
	}
	if s.Switches() != 1 {
		t.Fatalf("switches=%d, want 1", s.Switches())
	}
}

// Scenario D: after recovery, switch back to Wi-Fi only once the (longer)
// recovery hold is satisfied.
func TestRSSIAware_Recovery_SwitchBackAfterHold(t *testing.T) {
	s, c := newAware()
	sel(t, s, wifi(-55, true), cell())
	// Fail over to 5G.
	c.advance(100 * time.Millisecond)
	sel(t, s, wifi(-55, false), cell())
	if s.active != 1 {
		t.Fatalf("expected to be on 5G")
	}
	// Wi-Fi recovers strongly (> UpThreshold -65) and stays up.
	c.advance(100 * time.Millisecond)
	sel(t, s, wifi(-50, true), cell()) // aboveSince starts here
	// Before the 2000ms up-hold: stay on 5G.
	c.advance(1500 * time.Millisecond)
	if id := sel(t, s, wifi(-50, true), cell()); id != 1 {
		t.Fatalf("at +1500ms: path %d, want 1 (recovery hold not met)", id)
	}
	// Cross the 2000ms up-hold -> back to Wi-Fi.
	c.advance(600 * time.Millisecond)
	if id := sel(t, s, wifi(-50, true), cell()); id != 0 {
		t.Fatalf("at +2100ms: path %d, want 0 (recovered)", id)
	}
	if s.Switches() != 2 {
		t.Fatalf("switches=%d, want 2", s.Switches())
	}
}

// Minimum dwell prevents an immediate reverse switch (ping-pong) right after a
// degradation switch, even if RSSI momentarily looks recoverable.
func TestRSSIAware_MinDwell_NoImmediateReverse(t *testing.T) {
	s, c := newAware()
	sel(t, s, wifi(-55, true), cell())
	// Degrade and switch to 5G.
	sel(t, s, wifi(-75, true), cell())
	c.advance(600 * time.Millisecond)
	if id := sel(t, s, wifi(-75, true), cell()); id != 1 {
		t.Fatalf("expected switch to 5G, got %d", id)
	}
	// Immediately strong Wi-Fi, but within MinDwell (1000ms): must stay on 5G.
	c.advance(200 * time.Millisecond)
	if id := sel(t, s, wifi(-50, true), cell()); id != 1 {
		t.Fatalf("within dwell: path %d, want 1 (no reverse)", id)
	}
	if s.Switches() != 1 {
		t.Fatalf("switches=%d, want 1 (no ping-pong)", s.Switches())
	}
}

// When no backup exists, a degraded/failed Wi-Fi path is still returned (better
// than nothing) and no spurious switch is counted.
func TestRSSIAware_NoBackup_StaysOnWiFi(t *testing.T) {
	s, c := newAware()
	sel(t, s, wifi(-55, true))
	c.advance(1 * time.Second)
	if id := sel(t, s, wifi(-85, true)); id != 0 {
		t.Fatalf("path %d, want 0 (no backup available)", id)
	}
	if s.Switches() != 0 {
		t.Fatalf("switches=%d, want 0", s.Switches())
	}
}
