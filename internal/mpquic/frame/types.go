package frame

import "fmt"

type Type uint64

const (
	PathACKNoECNType           Type   = 0x3e
	PathACKECNType             Type   = 0x3f
	PathAbandonType            Type   = 0x3e75
	PathStatusBackupType       Type   = 0x3e76
	PathStatusAvailableType    Type   = 0x3e77
	PathNewConnectionIDType    Type   = 0x3e78
	PathRetireConnectionIDType Type   = 0x3e79
	MaxPathIDType              Type   = 0x3e7a
	PathsBlockedType           Type   = 0x3e7b
	PathCIDsBlockedType        Type   = 0x3e7c
	ApplicationAbandonPath     uint64 = 0x3e
	PathResourceLimitReached   uint64 = 0x3e75
	PathUnstableOrPoor         uint64 = 0x3e76
	NoCIDAvailableForPath      uint64 = 0x3e77
	NoError                    uint64 = 0x0
)

type Frame interface {
	Type() Type
}

type ACKRange struct {
	Gap      uint64
	ACKRange uint64
}

type ECNCounts struct {
	ECT0 uint64
	ECT1 uint64
	CE   uint64
}

type PathACKFrame struct {
	UseECN              bool
	PathID              uint64
	LargestAcknowledged uint64
	ACKDelay            uint64
	FirstACKRange       uint64
	ACKRanges           []ACKRange
	ECNCounts           *ECNCounts
}

func (f PathACKFrame) Type() Type {
	if f.UseECN {
		return PathACKECNType
	}
	return PathACKNoECNType
}

type PathAbandonFrame struct {
	PathID    uint64
	ErrorCode uint64
}

func (f PathAbandonFrame) Type() Type { return PathAbandonType }

type PathStatusAvailableFrame struct {
	PathID         uint64
	SequenceNumber uint64
}

func (f PathStatusAvailableFrame) Type() Type { return PathStatusAvailableType }

type PathStatusBackupFrame struct {
	PathID         uint64
	SequenceNumber uint64
}

func (f PathStatusBackupFrame) Type() Type { return PathStatusBackupType }

type PathNewConnectionIDFrame struct {
	PathID              uint64
	SequenceNumber      uint64
	RetirePriorTo       uint64
	ConnectionID        []byte
	StatelessResetToken [16]byte
	Retired             bool
}

func (f PathNewConnectionIDFrame) Type() Type { return PathNewConnectionIDType }

type PathRetireConnectionIDFrame struct {
	PathID         uint64
	SequenceNumber uint64
}

func (f PathRetireConnectionIDFrame) Type() Type { return PathRetireConnectionIDType }

type MaxPathIDFrame struct {
	MaximumPathID uint64
}

func (f MaxPathIDFrame) Type() Type { return MaxPathIDType }

type PathsBlockedFrame struct {
	MaximumPathID uint64
}

func (f PathsBlockedFrame) Type() Type { return PathsBlockedType }

type PathCIDsBlockedFrame struct {
	PathID             uint64
	NextSequenceNumber uint64
}

func (f PathCIDsBlockedFrame) Type() Type { return PathCIDsBlockedType }

func All() []Type {
	return []Type{
		PathACKNoECNType,
		PathACKECNType,
		PathAbandonType,
		PathStatusBackupType,
		PathStatusAvailableType,
		PathNewConnectionIDType,
		PathRetireConnectionIDType,
		MaxPathIDType,
		PathsBlockedType,
		PathCIDsBlockedType,
	}
}

func Name(t Type) string {
	switch t {
	case PathACKNoECNType, PathACKECNType:
		return "PATH_ACK"
	case PathAbandonType:
		return "PATH_ABANDON"
	case PathStatusBackupType:
		return "PATH_STATUS_BACKUP"
	case PathStatusAvailableType:
		return "PATH_STATUS_AVAILABLE"
	case PathNewConnectionIDType:
		return "PATH_NEW_CONNECTION_ID"
	case PathRetireConnectionIDType:
		return "PATH_RETIRE_CONNECTION_ID"
	case MaxPathIDType:
		return "MAX_PATH_ID"
	case PathsBlockedType:
		return "PATHS_BLOCKED"
	case PathCIDsBlockedType:
		return "PATH_CIDS_BLOCKED"
	default:
		return fmt.Sprintf("UNKNOWN(0x%x)", uint64(t))
	}
}
