package frame

import (
	"bytes"
	"fmt"
)

func Marshal(f Frame) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	if err := writeVarint(buf, uint64(f.Type())); err != nil {
		return nil, err
	}

	switch frame := f.(type) {
	case PathACKFrame:
		if err := marshalPathACK(buf, frame); err != nil {
			return nil, err
		}
	case PathAbandonFrame:
		writeVarints(buf, frame.PathID, frame.ErrorCode)
	case PathStatusAvailableFrame:
		writeVarints(buf, frame.PathID, frame.SequenceNumber)
	case PathStatusBackupFrame:
		writeVarints(buf, frame.PathID, frame.SequenceNumber)
	case PathNewConnectionIDFrame:
		if err := marshalPathNewConnectionID(buf, frame); err != nil {
			return nil, err
		}
	case PathRetireConnectionIDFrame:
		writeVarints(buf, frame.PathID, frame.SequenceNumber)
	case MaxPathIDFrame:
		writeVarints(buf, frame.MaximumPathID)
	case PathsBlockedFrame:
		writeVarints(buf, frame.MaximumPathID)
	case PathCIDsBlockedFrame:
		writeVarints(buf, frame.PathID, frame.NextSequenceNumber)
	default:
		return nil, fmt.Errorf("unsupported frame type %T", f)
	}

	return buf.Bytes(), nil
}

func Unmarshal(data []byte) (Frame, error) {
	r := bytes.NewReader(data)
	typeValue, err := readVarint(r)
	if err != nil {
		return nil, err
	}

	switch Type(typeValue) {
	case PathACKNoECNType, PathACKECNType:
		return unmarshalPathACK(r, Type(typeValue) == PathACKECNType)
	case PathAbandonType:
		pathID, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		errorCode, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		return PathAbandonFrame{PathID: pathID, ErrorCode: errorCode}, nil
	case PathStatusAvailableType:
		return unmarshalPathStatusAvailable(r)
	case PathStatusBackupType:
		return unmarshalPathStatusBackup(r)
	case PathNewConnectionIDType:
		return unmarshalPathNewConnectionID(r)
	case PathRetireConnectionIDType:
		pathID, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		sequenceNumber, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		return PathRetireConnectionIDFrame{PathID: pathID, SequenceNumber: sequenceNumber}, nil
	case MaxPathIDType:
		maxPathID, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		return MaxPathIDFrame{MaximumPathID: maxPathID}, nil
	case PathsBlockedType:
		maxPathID, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		return PathsBlockedFrame{MaximumPathID: maxPathID}, nil
	case PathCIDsBlockedType:
		pathID, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		nextSequence, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		return PathCIDsBlockedFrame{PathID: pathID, NextSequenceNumber: nextSequence}, nil
	default:
		return nil, fmt.Errorf("unknown frame type 0x%x", typeValue)
	}
}

func marshalPathACK(buf *bytes.Buffer, frame PathACKFrame) error {
	writeVarints(buf, frame.PathID, frame.LargestAcknowledged, frame.ACKDelay, uint64(len(frame.ACKRanges)), frame.FirstACKRange)
	for _, ackRange := range frame.ACKRanges {
		writeVarints(buf, ackRange.Gap, ackRange.ACKRange)
	}
	if frame.UseECN {
		if frame.ECNCounts == nil {
			return fmt.Errorf("path ack frame with ECN must provide ECN counts")
		}
		writeVarints(buf, frame.ECNCounts.ECT0, frame.ECNCounts.ECT1, frame.ECNCounts.CE)
	}
	return nil
}

func unmarshalPathACK(r *bytes.Reader, useECN bool) (Frame, error) {
	pathID, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	largestAcked, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	ackDelay, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	ackRangeCount, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	firstAckRange, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	ackRanges := make([]ACKRange, 0, ackRangeCount)
	for i := uint64(0); i < ackRangeCount; i++ {
		gap, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		ackRange, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		ackRanges = append(ackRanges, ACKRange{Gap: gap, ACKRange: ackRange})
	}

	frame := PathACKFrame{
		UseECN:              useECN,
		PathID:              pathID,
		LargestAcknowledged: largestAcked,
		ACKDelay:            ackDelay,
		FirstACKRange:       firstAckRange,
		ACKRanges:           ackRanges,
	}

	if useECN {
		ect0, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		ect1, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		ce, err := readVarint(r)
		if err != nil {
			return nil, err
		}
		frame.ECNCounts = &ECNCounts{ECT0: ect0, ECT1: ect1, CE: ce}
	}

	return frame, nil
}

func unmarshalPathStatusAvailable(r *bytes.Reader) (Frame, error) {
	pathID, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	sequenceNumber, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	return PathStatusAvailableFrame{PathID: pathID, SequenceNumber: sequenceNumber}, nil
}

func unmarshalPathStatusBackup(r *bytes.Reader) (Frame, error) {
	pathID, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	sequenceNumber, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	return PathStatusBackupFrame{PathID: pathID, SequenceNumber: sequenceNumber}, nil
}

func marshalPathNewConnectionID(buf *bytes.Buffer, frame PathNewConnectionIDFrame) error {
	if len(frame.ConnectionID) == 0 || len(frame.ConnectionID) > 20 {
		return fmt.Errorf("connection id length must be between 1 and 20 bytes")
	}
	writeVarints(buf, frame.PathID, frame.SequenceNumber, frame.RetirePriorTo)
	buf.WriteByte(byte(len(frame.ConnectionID)))
	buf.Write(frame.ConnectionID)
	buf.Write(frame.StatelessResetToken[:])
	return nil
}

func unmarshalPathNewConnectionID(r *bytes.Reader) (Frame, error) {
	pathID, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	sequenceNumber, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	retirePriorTo, err := readVarint(r)
	if err != nil {
		return nil, err
	}
	connectionIDLength, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if connectionIDLength == 0 || connectionIDLength > 20 {
		return nil, fmt.Errorf("invalid connection id length %d", connectionIDLength)
	}
	connectionID := make([]byte, connectionIDLength)
	if _, err := r.Read(connectionID); err != nil {
		return nil, err
	}
	var token [16]byte
	if _, err := r.Read(token[:]); err != nil {
		return nil, err
	}
	return PathNewConnectionIDFrame{
		PathID:              pathID,
		SequenceNumber:      sequenceNumber,
		RetirePriorTo:       retirePriorTo,
		ConnectionID:        connectionID,
		StatelessResetToken: token,
	}, nil
}
