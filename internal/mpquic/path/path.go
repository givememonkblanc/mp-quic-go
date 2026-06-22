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
	LocalStatusSeq    uint64
	PeerStatusSeq     uint64
	PacketNumberSpace PacketNumberSpace
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
