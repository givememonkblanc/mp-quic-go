package wire

import (
	"fmt"
	"io"

	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/quicvarint"
)

const (
	pathAckFrameType                = 0x3e
	pathAckECNFrameType             = 0x3f
	pathAbandonFrameType            = 0x3e75
	pathStatusBackupFrameType       = 0x3e76
	pathStatusAvailableFrameType    = 0x3e77
	pathNewConnectionIDFrameType    = 0x3e78
	pathRetireConnectionIDFrameType = 0x3e79
	maxPathIDFrameType              = 0x3e7a
	pathsBlockedFrameType           = 0x3e7b
	pathCIDsBlockedFrameType        = 0x3e7c
)

type PathAckFrame struct {
	PathID uint64
	AckFrame
}

func parsePathAckFrame(frame *PathAckFrame, b []byte, typ uint64, ackDelayExponent uint8, v protocol.Version) (int, error) {
	pathID, l, err := quicvarint.Parse(b)
	if err != nil {
		return 0, replaceUnexpectedEOF(err)
	}
	frame.PathID = pathID
	frame.AckFrame.Reset()
	read, err := parseAckFrame(&frame.AckFrame, b[l:], typ-pathAckFrameType+ackFrameType, ackDelayExponent, v)
	return l + read, err
}

func (f *PathAckFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	hasECN := f.ECT0 > 0 || f.ECT1 > 0 || f.ECNCE > 0
	if hasECN {
		b = append(b, pathAckECNFrameType)
	} else {
		b = append(b, pathAckFrameType)
	}
	b = quicvarint.Append(b, f.PathID)
	b = quicvarint.Append(b, uint64(f.LargestAcked()))
	b = quicvarint.Append(b, encodeAckDelay(f.DelayTime))
	numRanges := f.numEncodableAckRanges()
	b = quicvarint.Append(b, uint64(numRanges-1))
	_, firstRange := f.encodeAckRange(0)
	b = quicvarint.Append(b, firstRange)
	for i := 1; i < numRanges; i++ {
		gap, ln := f.encodeAckRange(i)
		b = quicvarint.Append(b, gap)
		b = quicvarint.Append(b, ln)
	}
	if hasECN {
		b = quicvarint.Append(b, f.ECT0)
		b = quicvarint.Append(b, f.ECT1)
		b = quicvarint.Append(b, f.ECNCE)
	}
	return b, nil
}

func (f *PathAckFrame) Length(v protocol.Version) protocol.ByteCount {
	return 1 + protocol.ByteCount(quicvarint.Len(f.PathID)) + f.AckFrame.Length(v) - 1
}

type PathAbandonFrame struct{ PathID, ErrorCode uint64 }

func parsePathAbandonFrame(b []byte, _ protocol.Version) (*PathAbandonFrame, int, error) {
	pathID, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	code, l2, err := quicvarint.Parse(b[l:])
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	return &PathAbandonFrame{PathID: pathID, ErrorCode: code}, l + l2, nil
}

func (f *PathAbandonFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = quicvarint.Append(b, pathAbandonFrameType)
	b = quicvarint.Append(b, f.PathID)
	b = quicvarint.Append(b, f.ErrorCode)
	return b, nil
}

func (f *PathAbandonFrame) Length(protocol.Version) protocol.ByteCount {
	return protocol.ByteCount(quicvarint.Len(pathAbandonFrameType) + quicvarint.Len(f.PathID) + quicvarint.Len(f.ErrorCode))
}

type PathStatusFrame struct {
	PathID         uint64
	SequenceNumber uint64
	Backup         bool
}

func parsePathStatusFrame(b []byte, typ uint64, _ protocol.Version) (*PathStatusFrame, int, error) {
	pathID, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	seq, l2, err := quicvarint.Parse(b[l:])
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	return &PathStatusFrame{PathID: pathID, SequenceNumber: seq, Backup: typ == pathStatusBackupFrameType}, l + l2, nil
}

func (f *PathStatusFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	if f.Backup {
		b = quicvarint.Append(b, pathStatusBackupFrameType)
	} else {
		b = quicvarint.Append(b, pathStatusAvailableFrameType)
	}
	b = quicvarint.Append(b, f.PathID)
	b = quicvarint.Append(b, f.SequenceNumber)
	return b, nil
}

func (f *PathStatusFrame) Length(protocol.Version) protocol.ByteCount {
	typ := uint64(pathStatusAvailableFrameType)
	if f.Backup {
		typ = uint64(pathStatusBackupFrameType)
	}
	return protocol.ByteCount(quicvarint.Len(typ) + quicvarint.Len(f.PathID) + quicvarint.Len(f.SequenceNumber))
}

type PathNewConnectionIDFrame struct {
	PathID              uint64
	SequenceNumber      uint64
	RetirePriorTo       uint64
	ConnectionID        protocol.ConnectionID
	StatelessResetToken protocol.StatelessResetToken
}

func parsePathNewConnectionIDFrame(b []byte, _ protocol.Version) (*PathNewConnectionIDFrame, int, error) {
	startLen := len(b)
	pathID, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l:]
	seq, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l:]
	ret, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l:]
	if ret > seq {
		return nil, 0, fmt.Errorf("Retire Prior To value (%d) larger than Sequence Number (%d)", ret, seq)
	}
	if len(b) == 0 {
		return nil, 0, io.EOF
	}
	connIDLen := int(b[0])
	b = b[1:]
	if connIDLen == 0 {
		return nil, 0, fmt.Errorf("invalid zero-length connection ID")
	}
	if connIDLen > protocol.MaxConnIDLen {
		return nil, 0, protocol.ErrInvalidConnectionIDLen
	}
	if len(b) < connIDLen {
		return nil, 0, io.EOF
	}
	frame := &PathNewConnectionIDFrame{
		PathID:         pathID,
		SequenceNumber: seq,
		RetirePriorTo:  ret,
		ConnectionID:   protocol.ParseConnectionID(b[:connIDLen]),
	}
	b = b[connIDLen:]
	if len(b) < len(frame.StatelessResetToken) {
		return nil, 0, io.EOF
	}
	copy(frame.StatelessResetToken[:], b)
	return frame, startLen - len(b) + len(frame.StatelessResetToken), nil
}

func (f *PathNewConnectionIDFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = quicvarint.Append(b, pathNewConnectionIDFrameType)
	b = quicvarint.Append(b, f.PathID)
	b = quicvarint.Append(b, f.SequenceNumber)
	b = quicvarint.Append(b, f.RetirePriorTo)
	connIDLen := f.ConnectionID.Len()
	if connIDLen > protocol.MaxConnIDLen {
		return nil, fmt.Errorf("invalid connection ID length: %d", connIDLen)
	}
	b = append(b, uint8(connIDLen))
	b = append(b, f.ConnectionID.Bytes()...)
	b = append(b, f.StatelessResetToken[:]...)
	return b, nil
}

func (f *PathNewConnectionIDFrame) Length(protocol.Version) protocol.ByteCount {
	return protocol.ByteCount(quicvarint.Len(pathNewConnectionIDFrameType)+quicvarint.Len(f.PathID)+quicvarint.Len(f.SequenceNumber)+quicvarint.Len(f.RetirePriorTo)+1+f.ConnectionID.Len()) + 16
}

type PathRetireConnectionIDFrame struct{ PathID, SequenceNumber uint64 }

func parsePathRetireConnectionIDFrame(b []byte, _ protocol.Version) (*PathRetireConnectionIDFrame, int, error) {
	pathID, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	seq, l2, err := quicvarint.Parse(b[l:])
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	return &PathRetireConnectionIDFrame{PathID: pathID, SequenceNumber: seq}, l + l2, nil
}

func (f *PathRetireConnectionIDFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = quicvarint.Append(b, pathRetireConnectionIDFrameType)
	b = quicvarint.Append(b, f.PathID)
	b = quicvarint.Append(b, f.SequenceNumber)
	return b, nil
}

func (f *PathRetireConnectionIDFrame) Length(protocol.Version) protocol.ByteCount {
	return protocol.ByteCount(quicvarint.Len(pathRetireConnectionIDFrameType) + quicvarint.Len(f.PathID) + quicvarint.Len(f.SequenceNumber))
}

type MaxPathIDFrame struct{ MaximumPathID uint64 }

func parseMaxPathIDFrame(b []byte, _ protocol.Version) (*MaxPathIDFrame, int, error) {
	v, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	return &MaxPathIDFrame{MaximumPathID: v}, l, nil
}

func (f *MaxPathIDFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = quicvarint.Append(b, maxPathIDFrameType)
	b = quicvarint.Append(b, f.MaximumPathID)
	return b, nil
}

func (f *MaxPathIDFrame) Length(protocol.Version) protocol.ByteCount {
	return protocol.ByteCount(quicvarint.Len(maxPathIDFrameType) + quicvarint.Len(f.MaximumPathID))
}

type PathsBlockedFrame struct{ MaximumPathID uint64 }

func parsePathsBlockedFrame(b []byte, _ protocol.Version) (*PathsBlockedFrame, int, error) {
	v, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	return &PathsBlockedFrame{MaximumPathID: v}, l, nil
}

func (f *PathsBlockedFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = quicvarint.Append(b, pathsBlockedFrameType)
	b = quicvarint.Append(b, f.MaximumPathID)
	return b, nil
}

func (f *PathsBlockedFrame) Length(protocol.Version) protocol.ByteCount {
	return protocol.ByteCount(quicvarint.Len(pathsBlockedFrameType) + quicvarint.Len(f.MaximumPathID))
}

type PathCIDsBlockedFrame struct{ PathID, SequenceNumber uint64 }

func parsePathCIDsBlockedFrame(b []byte, _ protocol.Version) (*PathCIDsBlockedFrame, int, error) {
	pathID, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	seq, l2, err := quicvarint.Parse(b[l:])
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	return &PathCIDsBlockedFrame{PathID: pathID, SequenceNumber: seq}, l + l2, nil
}

func (f *PathCIDsBlockedFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = quicvarint.Append(b, pathCIDsBlockedFrameType)
	b = quicvarint.Append(b, f.PathID)
	b = quicvarint.Append(b, f.SequenceNumber)
	return b, nil
}

func (f *PathCIDsBlockedFrame) Length(protocol.Version) protocol.ByteCount {
	return protocol.ByteCount(quicvarint.Len(pathCIDsBlockedFrameType) + quicvarint.Len(f.PathID) + quicvarint.Len(f.SequenceNumber))
}
