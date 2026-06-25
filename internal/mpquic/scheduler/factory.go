package scheduler

import "fmt"

// Names of the available schedulers, for selection from flags/config.
const (
	NamePQI        = "pqi"
	NameMinRTT     = "min-rtt"
	NameRoundRobin = "round-robin"
	NameRSSI       = "rssi"
	NameRSSIAware  = "rssi-aware"
)

// New constructs a scheduler by name. This lets the same binary run different
// schedulers so evaluation can compare them with everything else held constant
// (only the scheduler differs). An empty name defaults to PQI.
func New(name string) (Scheduler, error) {
	switch name {
	case "", NamePQI:
		return NewPQIScheduler(DefaultPQIConfig()), nil
	case NameMinRTT:
		return NewMinRTTScheduler(), nil
	case NameRoundRobin:
		return NewRoundRobinScheduler(), nil
	case NameRSSI:
		return NewPrimaryPathScheduler(), nil
	case NameRSSIAware:
		return NewRSSIAwareScheduler(DefaultRSSIAwareConfig()), nil
	default:
		return nil, fmt.Errorf("unknown scheduler %q (want %s|%s|%s|%s|%s)",
			name, NamePQI, NameMinRTT, NameRoundRobin, NameRSSI, NameRSSIAware)
	}
}
