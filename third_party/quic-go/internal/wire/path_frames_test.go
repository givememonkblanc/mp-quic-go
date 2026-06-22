package wire

import (
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestPathNewConnectionIDFrameRoundTrip(t *testing.T) {
	frame := &PathNewConnectionIDFrame{
		PathID:         2,
		SequenceNumber: 4,
		RetirePriorTo:  1,
		ConnectionID:   protocol.ParseConnectionID([]byte{0xde, 0xad, 0xbe, 0xef}),
	}
	b, err := frame.Append(nil, protocol.Version1)
	require.NoError(t, err)
	parser := NewFrameParser(false)
	_, parsed, err := parser.ParseNext(b, protocol.Encryption1RTT, protocol.Version1)
	require.NoError(t, err)
	p := parsed.(*PathNewConnectionIDFrame)
	require.Equal(t, frame.PathID, p.PathID)
	require.Equal(t, frame.SequenceNumber, p.SequenceNumber)
	require.Equal(t, frame.RetirePriorTo, p.RetirePriorTo)
	require.Equal(t, frame.ConnectionID, p.ConnectionID)
}

func TestPathAckFrameRoundTrip(t *testing.T) {
	frame := &PathAckFrame{
		PathID: 3,
		AckFrame: AckFrame{
			AckRanges: []AckRange{{Smallest: 8, Largest: 10}},
			DelayTime: 5 * time.Millisecond,
		},
	}
	b, err := frame.Append(nil, protocol.Version1)
	require.NoError(t, err)
	parser := NewFrameParser(false)
	_, parsed, err := parser.ParseNext(b, protocol.Encryption1RTT, protocol.Version1)
	require.NoError(t, err)
	p := parsed.(*PathAckFrame)
	require.Equal(t, uint64(3), p.PathID)
	require.Equal(t, protocol.PacketNumber(10), p.LargestAcked())
}

func TestPathStatusFrameRoundTrip(t *testing.T) {
	frame := &PathStatusFrame{PathID: 4, SequenceNumber: 9, Backup: true}
	b, err := frame.Append(nil, protocol.Version1)
	require.NoError(t, err)
	parser := NewFrameParser(false)
	_, parsed, err := parser.ParseNext(b, protocol.Encryption1RTT, protocol.Version1)
	require.NoError(t, err)
	p := parsed.(*PathStatusFrame)
	require.Equal(t, uint64(4), p.PathID)
	require.Equal(t, uint64(9), p.SequenceNumber)
	require.True(t, p.Backup)
}

func TestMaxPathIDFrameRoundTrip(t *testing.T) {
	frame := &MaxPathIDFrame{MaximumPathID: 7}
	b, err := frame.Append(nil, protocol.Version1)
	require.NoError(t, err)
	parser := NewFrameParser(false)
	_, parsed, err := parser.ParseNext(b, protocol.Encryption1RTT, protocol.Version1)
	require.NoError(t, err)
	p := parsed.(*MaxPathIDFrame)
	require.Equal(t, uint64(7), p.MaximumPathID)
}

func TestPathCIDsBlockedFrameRoundTrip(t *testing.T) {
	frame := &PathCIDsBlockedFrame{PathID: 2, SequenceNumber: 11}
	b, err := frame.Append(nil, protocol.Version1)
	require.NoError(t, err)
	parser := NewFrameParser(false)
	_, parsed, err := parser.ParseNext(b, protocol.Encryption1RTT, protocol.Version1)
	require.NoError(t, err)
	p := parsed.(*PathCIDsBlockedFrame)
	require.Equal(t, uint64(2), p.PathID)
	require.Equal(t, uint64(11), p.SequenceNumber)
}
