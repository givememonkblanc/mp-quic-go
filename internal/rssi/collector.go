package rssi

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RSSIFileEnv, when set, makes the collector read the current RSSI (a single
// integer dBm) from that file instead of `iw`. This gives experiments a
// reproducible, scriptable RSSI source (e.g. a scenario writing a ramp), which
// is what makes the gradual-degradation / recovery scenarios deterministic.
const RSSIFileEnv = "MPQUIC_RSSI_FILE"

// Sample is one RSSI observation for a Wi-Fi interface.
type Sample struct {
	Raw   int       // raw RSSI in dBm (valid only if Valid)
	EWMA  float64   // exponentially-weighted moving average of raw, in dBm
	Valid bool      // false when the link is down / not associated / unreadable
	Time  time.Time // when the sample was taken
}

// signalRe extracts the RSSI from `iw dev <iface> link` output, e.g.
//
//	signal: -67 dBm
var signalRe = regexp.MustCompile(`signal:\s*(-?\d+)\s*dBm`)

// LocalCollector periodically reads the RSSI of a local Wi-Fi interface (via
// `iw dev <iface> link`), maintains an EWMA, and exposes the latest sample. It is
// the RSSI source the AMR feeds into the RSSI-aware scheduler; smoothing here
// keeps the scheduler's state machine off raw per-packet noise.
//
// Invalid reads (link down, not associated, command error) do NOT poison the
// EWMA: the smoothed value is held and the sample is marked Valid=false so the
// scheduler treats the path by its validation state instead.
type LocalCollector struct {
	iface string
	alpha float64

	ewma float64
	has  bool

	// read returns (rssiDbm, ok). Overridable in tests.
	read func(ctx context.Context, iface string) (int, bool)
}

// NewLocalCollector creates a collector for iface with the given EWMA smoothing
// factor alpha in (0,1]. alpha<=0 defaults to 0.3.
func NewLocalCollector(iface string, alpha float64) *LocalCollector {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3
	}
	c := &LocalCollector{iface: iface, alpha: alpha, read: readSignalIW}
	if f := os.Getenv(RSSIFileEnv); f != "" {
		c.read = func(context.Context, string) (int, bool) { return readSignalFile(f) }
	}
	return c
}

// readSignalFile reads a single integer dBm from a scenario-controlled file.
// A missing/empty/invalid file reads as invalid (holds the EWMA).
func readSignalFile(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return v, true
}

// Iface returns the interface name.
func (c *LocalCollector) Iface() string { return c.iface }

// Sample reads the interface once, updates the EWMA on a valid read, and returns
// the resulting sample.
func (c *LocalCollector) Sample(ctx context.Context) Sample {
	now := time.Now()
	raw, ok := c.read(ctx, c.iface)
	if !ok {
		return Sample{Raw: 0, EWMA: c.ewma, Valid: false, Time: now}
	}
	if !c.has {
		c.ewma = float64(raw)
		c.has = true
	} else {
		c.ewma = c.alpha*float64(raw) + (1-c.alpha)*c.ewma
	}
	return Sample{Raw: raw, EWMA: c.ewma, Valid: true, Time: now}
}

// EWMAInt returns the current EWMA rounded to the nearest dBm, plus whether any
// valid sample has been recorded. Suitable for feeding UpdatePathRSSI (int).
func (c *LocalCollector) EWMAInt() (int, bool) {
	if !c.has {
		return 0, false
	}
	return int(math.Round(c.ewma)), true
}

// readSignalIW runs `iw dev <iface> link` and parses the signal line. Returns
// ok=false when the interface is not associated or the command fails.
func readSignalIW(ctx context.Context, iface string) (int, bool) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "iw", "dev", iface, "link").CombinedOutput()
	if err != nil {
		return 0, false
	}
	if strings.Contains(string(out), "Not connected") {
		return 0, false
	}
	m := signalRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, false
	}
	return v, true
}

// ParseSignal extracts the signal dBm from `iw dev ... link` output. Exposed for
// reuse/testing.
func ParseSignal(iwLinkOutput string) (int, error) {
	m := signalRe.FindStringSubmatch(iwLinkOutput)
	if m == nil {
		return 0, fmt.Errorf("no signal line in output")
	}
	return strconv.Atoi(m[1])
}
