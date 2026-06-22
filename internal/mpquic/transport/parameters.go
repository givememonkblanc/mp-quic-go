package transport

import "fmt"

const InitialMaxPathIDParameterID uint64 = 0x3e

type Parameters struct {
	InitialMaxPathID uint32
	MaxPathID        uint32
}

func DefaultParameters() Parameters {
	return Parameters{
		InitialMaxPathID: 1,
		MaxPathID:        1,
	}
}

func (p Parameters) Validate() error {
	if p.MaxPathID < p.InitialMaxPathID {
		return fmt.Errorf("max path id %d must be >= initial max path id %d", p.MaxPathID, p.InitialMaxPathID)
	}
	return nil
}
