package scheduler

import (
	"fmt"
	"os"
	"time"

	"mp-quic-go/internal/mpquic/path"
)

// WiFiState is the coarse link-quality state derived from the (EWMA-smoothed)
// Wi-Fi RSSI and path validation status. Converting the continuous RSSI into a
// small set of named states (rather than feeding raw dBm into a cost metric)
// keeps the policy explainable and its parameters reviewable/reproducible.
type WiFiState string

const (
	StateGood       WiFiState = "GOOD"       // RSSI >= GoodThreshold
	StateWarning    WiFiState = "WARNING"    // WarningThreshold <= RSSI < GoodThreshold
	StateDegraded   WiFiState = "DEGRADED"   // DegradedThreshold <= RSSI < WarningThreshold
	StateFailed     WiFiState = "FAILED"     // RSSI < DegradedThreshold OR path validation failed
	StateRecovering WiFiState = "RECOVERING" // on backup, Wi-Fi climbing back but recovery hold not yet met
)

// RSSIAwareConfig holds the (reviewer-facing, reproducible) parameters of the
// RSSI-aware scheduler. Defaults match the values documented for the proposed
// algorithm. RSSI values are in dBm (negative; higher is better).
type RSSIAwareConfig struct {
	// State-machine thresholds (dBm).
	GoodThreshold     int // >= this -> GOOD          (default -60)
	WarningThreshold  int // >= this -> WARNING        (default -70)
	DegradedThreshold int // >= this -> DEGRADED; < this -> FAILED (default -80)

	// Switching hysteresis (separate from the state bands so transient dips do
	// not cause ping-pong).
	DownThreshold int           // Wi-Fi->5G considered when EWMA < this (default -72)
	DownHold      time.Duration // ...sustained for at least this long      (default 500ms)
	UpThreshold   int           // 5G->Wi-Fi considered when EWMA > this    (default -65)
	UpHold        time.Duration // ...sustained for at least this long      (default 2000ms)
	MinDwell      time.Duration // minimum time on a path after a switch    (default 1000ms)

	// PrimaryID is the Wi-Fi (active) path; any other validated path is treated
	// as the 5G backup. Default 0.
	PrimaryID path.ID
}

// DefaultRSSIAwareConfig returns the documented default parameters.
func DefaultRSSIAwareConfig() RSSIAwareConfig {
	return RSSIAwareConfig{
		GoodThreshold:     -60,
		WarningThreshold:  -70,
		DegradedThreshold: -80,
		DownThreshold:     -72,
		DownHold:          500 * time.Millisecond,
		UpThreshold:       -65,
		UpHold:            2000 * time.Millisecond,
		MinDwell:          1000 * time.Millisecond,
		PrimaryID:         0,
	}
}

// RSSIAwareScheduler implements a simple, explainable rule-based path policy:
//
//	GOOD or WARNING            -> use Wi-Fi
//	DEGRADED + down-hold met   -> switch to 5G
//	FAILED                     -> switch to 5G immediately (bypasses dwell)
//	RECOVERING + up-hold met   -> switch back to Wi-Fi
//	otherwise                  -> keep current path
//
// It is stateful across calls (it remembers the active path, the hold timers and
// the last switch time), so one instance serves one connection. It is not
// goroutine-safe; SelectPath is expected to be driven from the connection's send
// loop.
type RSSIAwareScheduler struct {
	cfg RSSIAwareConfig

	active    path.ID
	hasActive bool
	state     WiFiState

	belowSince time.Time // when EWMA first dropped below DownThreshold (zero = not below)
	aboveSince time.Time // when EWMA first rose above UpThreshold (zero = not above)
	lastSwitch time.Time

	switches       int
	pingPongs      int
	lastSwitchFrom path.ID
	hadPrevSwitch  bool

	now func() time.Time
	log bool
}

// NewRSSIAwareScheduler creates an RSSI-aware scheduler with the given config.
func NewRSSIAwareScheduler(cfg RSSIAwareConfig) *RSSIAwareScheduler {
	return &RSSIAwareScheduler{
		cfg:   cfg,
		state: StateGood,
		now:   time.Now,
		log:   os.Getenv("MPQUIC_SCHED_LOG") != "",
	}
}

// Name identifies the scheduler.
func (s *RSSIAwareScheduler) Name() string { return "rssi-aware" }

// Switches returns the number of path switches performed so far.
func (s *RSSIAwareScheduler) Switches() int { return s.switches }

// PingPongs returns the number of switches that reversed the immediately
// preceding switch (A->B->A), i.e. ping-pong events.
func (s *RSSIAwareScheduler) PingPongs() int { return s.pingPongs }

// State returns the most recently classified Wi-Fi state (for telemetry).
func (s *RSSIAwareScheduler) State() WiFiState { return s.state }

// classify maps the (EWMA) RSSI and primary availability into a coarse state.
// RECOVERING is decided in SelectPath since it depends on the active path.
func (s *RSSIAwareScheduler) classify(rssi int, primaryUp bool) WiFiState {
	switch {
	case !primaryUp || rssi < s.cfg.DegradedThreshold:
		return StateFailed
	case rssi >= s.cfg.GoodThreshold:
		return StateGood
	case rssi >= s.cfg.WarningThreshold:
		return StateWarning
	default:
		return StateDegraded
	}
}

// SelectPath chooses the path for this send opportunity.
func (s *RSSIAwareScheduler) SelectPath(paths []path.State) (path.State, bool) {
	now := s.now()

	// Locate the Wi-Fi (primary) path and the best available 5G (backup) path.
	var primary, backup path.State
	var havePrimary, haveBackup bool
	for _, p := range paths {
		if p.ID == s.cfg.PrimaryID {
			primary, havePrimary = p, true
			continue
		}
		if !p.Validated {
			continue
		}
		if p.Status != path.StatusActive && p.Status != path.StatusAvailable {
			continue
		}
		if !haveBackup || p.ID < backup.ID {
			backup, haveBackup = p, true
		}
	}

	// No usable path at all.
	if !havePrimary && !haveBackup {
		return path.State{}, false
	}

	primaryUp := havePrimary && primary.Validated &&
		(primary.Status == path.StatusActive || primary.Status == path.StatusAvailable)

	rssi := primary.RSSI // fed as EWMA-smoothed dBm by the RSSI collector
	if !primary.HasRSSI {
		// Without an RSSI sample we cannot run the state machine on quality; fall
		// back to availability only (treat as GOOD while up, FAILED while down).
		if primaryUp {
			rssi = s.cfg.GoodThreshold
		} else {
			rssi = s.cfg.DegradedThreshold - 1
		}
	}

	prevState := s.state
	s.state = s.classify(rssi, primaryUp)

	// Maintain the sustained-below / sustained-above timers used for hysteresis.
	if rssi < s.cfg.DownThreshold {
		if s.belowSince.IsZero() {
			s.belowSince = now
		}
	} else {
		s.belowSince = time.Time{}
	}
	if rssi > s.cfg.UpThreshold {
		if s.aboveSince.IsZero() {
			s.aboveSince = now
		}
	} else {
		s.aboveSince = time.Time{}
	}

	// Cold start: pin to the primary if it is usable, else the backup.
	if !s.hasActive {
		if primaryUp {
			s.setActive(primary.ID, now, "init")
		} else if haveBackup {
			s.setActive(backup.ID, now, "init")
		} else {
			s.setActive(primary.ID, now, "init")
		}
	}

	onPrimary := s.active == s.cfg.PrimaryID

	// FAILED -> immediate failover to 5G, bypassing the minimum-dwell guard.
	if s.state == StateFailed && haveBackup && onPrimary {
		s.setActive(backup.ID, now, "failover")
		return backup, true
	}

	// Minimum dwell: do not switch again until we have stayed put long enough
	// (prevents ping-pong on noisy RSSI). FAILED already handled above.
	if !s.lastSwitch.IsZero() && now.Sub(s.lastSwitch) < s.cfg.MinDwell {
		return s.current(primary, backup, havePrimary, haveBackup, prevState)
	}

	if onPrimary {
		// Wi-Fi -> 5G when EWMA has stayed below DownThreshold for DownHold.
		if haveBackup && !s.belowSince.IsZero() && now.Sub(s.belowSince) >= s.cfg.DownHold {
			s.setActive(backup.ID, now, "degraded")
			return backup, true
		}
		return s.current(primary, backup, havePrimary, haveBackup, prevState)
	}

	// Currently on 5G (backup): consider recovery back to Wi-Fi.
	if primaryUp && rssi > s.cfg.UpThreshold {
		s.state = StateRecovering
	}
	if primaryUp && !s.aboveSince.IsZero() && now.Sub(s.aboveSince) >= s.cfg.UpHold {
		s.setActive(primary.ID, now, "recovered")
		return primary, true
	}
	return s.current(primary, backup, havePrimary, haveBackup, prevState)
}

// current returns the currently active path's state, or any usable fallback.
func (s *RSSIAwareScheduler) current(primary, backup path.State, havePrimary, haveBackup bool, prevState WiFiState) (path.State, bool) {
	if s.log && s.state != prevState {
		fmt.Fprintf(os.Stderr, "SCHED ts=%d state %s->%s active=%d rssi=%d\n",
			s.now().UnixMilli(), prevState, s.state, s.active, primaryRSSI(primary))
	}
	if s.active == s.cfg.PrimaryID && havePrimary {
		return primary, true
	}
	if haveBackup {
		return backup, true
	}
	if havePrimary {
		return primary, true
	}
	return path.State{}, false
}

func (s *RSSIAwareScheduler) setActive(id path.ID, now time.Time, reason string) {
	if s.hasActive && s.active == id {
		return
	}
	if s.hasActive {
		s.switches++
		if s.hadPrevSwitch && id == s.lastSwitchFrom {
			s.pingPongs++
		}
		s.lastSwitchFrom = s.active
		s.hadPrevSwitch = true
		s.lastSwitch = now
		if s.log {
			fmt.Fprintf(os.Stderr, "SCHED ts=%d path_switch from=%d to=%d reason=%s state=%s n_switch=%d ping_pong=%d\n",
				now.UnixMilli(), s.active, id, reason, s.state, s.switches, s.pingPongs)
		}
	}
	s.active = id
	s.hasActive = true
}

func primaryRSSI(p path.State) int {
	if p.HasRSSI {
		return p.RSSI
	}
	return 0
}
