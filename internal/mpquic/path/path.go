package path

import "time"

type ID uint32

type Status string

const (
	StatusAvailable Status = "available"
	StatusActive    Status = "active"
	StatusBackup    Status = "backup"
	StatusAbandoned Status = "abandoned"
)

type PacketNumberSpace struct {
	NextSendPacketNumber uint64
	LargestRecvPacketNum uint64
}

type State struct {
	ID                ID
	RemoteAddr        string
	Validated         bool
	Status            Status
	RSSI              int
	HasRSSI           bool
	// Per-path transport metrics feeding the Path Quality Index (PQI). RTT is the
	// path's smoothed round-trip time, LossRate the packet loss fraction in
	// [0,1], and Bandwidth the estimated goodput in bytes/s. HasMetrics is set
	// once at least one sample has been observed for the path.
	RTT               time.Duration
	LossRate          float64
	Bandwidth         float64
	HasMetrics        bool
	LocalStatusSeq    uint64
	PeerStatusSeq     uint64
	PacketNumberSpace PacketNumberSpace
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
