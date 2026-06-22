package qlogviewer

import (
	"net"
	"time"
)

// EventType categorises a trace event for the frontend.
type EventType string

const (
	EventConnStarted       EventType = "conn_started"
	EventConnClosed        EventType = "conn_closed"
	EventPacketSent        EventType = "packet_sent"
	EventPacketRecv        EventType = "packet_recv"
	EventPathRSSI          EventType = "path_rssi"
	EventSchedulerSelect   EventType = "scheduler_select"
	EventPathState         EventType = "path_state"
	EventUpdatedMetrics    EventType = "metrics"
	EventPacketAcked       EventType = "packet_acked"
	EventPacketLost        EventType = "packet_lost"
)

// Event is the common envelope pushed to WebSocket clients.
type Event struct {
	Time    float64     `json:"t"`     // ms since reference
	Type    EventType   `json:"type"`
	PathID  uint32      `json:"path,omitempty"`
	Payload interface{} `json:"payload,omitempty"`
}

// referenceTime is set once when the collector starts.
var referenceTime = time.Now()

func msSinceRef() float64 {
	return float64(time.Since(referenceTime).Microseconds()) / 1000.0
}

// PacketInfo describes a single QUIC packet for the frontend.
type PacketInfo struct {
	PacketNumber int64  `json:"pn"`
	Size         int64  `json:"size"`
	EncLevel     string `json:"enc_level,omitempty"`
	FrameCount   int    `json:"frames"`
	DestConnID   string `json:"dcid,omitempty"`
}

// MetricsInfo carries congestion / RTT state.
type MetricsInfo struct {
	SmoothedRTT    float64 `json:"srtt"`
	CongestionWind int64   `json:"cwnd"`
	BytesInFlight  int64   `json:"bytes_in_flight"`
	PacketsInFlight int    `json:"packets_in_flight"`
}

// PathStateInfo describes a path state change.
type PathStateInfo struct {
	RemoteAddr string `json:"remote_addr,omitempty"`
	Status     string `json:"status,omitempty"`
	RSSI       int    `json:"rssi,omitempty"`
	HasRSSI    bool   `json:"has_rssi"`
	Validated  bool   `json:"validated"`
}

// ConnInfo is sent once when a connection starts.
type ConnInfo struct {
	LocalAddr  net.Addr `json:"local"`
	RemoteAddr net.Addr `json:"remote"`
	SrcConnID  string   `json:"src_cid"`
	DestConnID string   `json:"dst_cid"`
}
