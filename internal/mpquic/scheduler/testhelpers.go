package scheduler

import (
	"time"

	"mp-quic-go/internal/mpquic/path"
)

func state(id path.ID, status path.Status, validated bool, rssi int, hasRSSI bool) path.State {
	now := time.Now()

	return path.State{
		ID:         id,
		RemoteAddr: "peer",
		Validated:  validated,
		Status:     status,
		RSSI:       rssi,
		HasRSSI:    hasRSSI,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func primaryState(id path.ID, status path.Status, validated bool, rssi int, hasRSSI bool) path.State {
	return state(id, status, validated, rssi, hasRSSI)
}
