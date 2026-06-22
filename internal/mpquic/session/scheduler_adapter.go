package session

import (
	"github.com/quic-go/quic-go"
	"mp-quic-go/internal/mpquic/path"
	"mp-quic-go/internal/mpquic/scheduler"
)

// NewQuicPathSelector adapts the session's scheduler to quic-go's PathSelector interface.
func NewQuicPathSelector(sched scheduler.Scheduler) quic.PathSelector {
	if sched == nil {
		return nil
	}
	return &quicPathSelectorAdapter{sched: sched}
}

type quicPathSelectorAdapter struct {
	sched scheduler.Scheduler
}

func (a *quicPathSelectorAdapter) SelectPath(qp []quic.PathState) quic.PathID {
	// Convert from quic.PathState to the session's path.State
	states := make([]path.State, len(qp))
	for i, ps := range qp {
		state := path.State{ID: path.ID(ps.ID)}
		if ps.Available {
			state.Status = path.StatusActive
		}
		if ps.HasRSSI {
			state.RSSI = ps.RSSI
			state.HasRSSI = true
		}
		states[i] = state
	}

	selected, ok := a.sched.SelectPath(states)
	if !ok {
		return 0 // fallback to main path
	}
	return quic.PathID(selected.ID)
}

func (a *quicPathSelectorAdapter) Name() string {
	return a.sched.Name()
}
