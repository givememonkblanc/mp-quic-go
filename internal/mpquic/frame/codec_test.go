package frame

import (
	"bytes"
	"testing"
)

func TestMarshalUnmarshalPathNewConnectionIDFrame(t *testing.T) {
	var token [16]byte
	copy(token[:], []byte("0123456789abcdef"))

	original := PathNewConnectionIDFrame{
		PathID:              3,
		SequenceNumber:      7,
		RetirePriorTo:       2,
		ConnectionID:        []byte{0xde, 0xad, 0xbe, 0xef},
		StatelessResetToken: token,
	}

	encoded, err := Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	decodedFrame, err := Unmarshal(encoded)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	decoded, ok := decodedFrame.(PathNewConnectionIDFrame)
	if !ok {
		t.Fatalf("unexpected frame type %T", decodedFrame)
	}

	if decoded.PathID != original.PathID || decoded.SequenceNumber != original.SequenceNumber || decoded.RetirePriorTo != original.RetirePriorTo {
		t.Fatalf("decoded frame mismatch: %#v", decoded)
	}
	if !bytes.Equal(decoded.ConnectionID, original.ConnectionID) {
		t.Fatalf("connection id mismatch: %x != %x", decoded.ConnectionID, original.ConnectionID)
	}
	if decoded.StatelessResetToken != original.StatelessResetToken {
		t.Fatal("stateless reset token mismatch")
	}
}

func TestMarshalUnmarshalPathACKFrameWithECN(t *testing.T) {
	original := PathACKFrame{
		UseECN:              true,
		PathID:              1,
		LargestAcknowledged: 42,
		ACKDelay:            5,
		FirstACKRange:       10,
		ACKRanges: []ACKRange{
			{Gap: 1, ACKRange: 2},
			{Gap: 3, ACKRange: 4},
		},
		ECNCounts: &ECNCounts{ECT0: 9, ECT1: 8, CE: 7},
	}

	encoded, err := Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	decodedFrame, err := Unmarshal(encoded)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	decoded, ok := decodedFrame.(PathACKFrame)
	if !ok {
		t.Fatalf("unexpected frame type %T", decodedFrame)
	}

	if !decoded.UseECN || decoded.PathID != original.PathID || decoded.LargestAcknowledged != original.LargestAcknowledged {
		t.Fatalf("decoded ack mismatch: %#v", decoded)
	}
	if len(decoded.ACKRanges) != 2 || decoded.ACKRanges[1].ACKRange != 4 {
		t.Fatalf("unexpected ack ranges: %#v", decoded.ACKRanges)
	}
	if decoded.ECNCounts == nil || decoded.ECNCounts.CE != 7 {
		t.Fatalf("unexpected ecn counts: %#v", decoded.ECNCounts)
	}
}

func TestMarshalRejectsInvalidConnectionIDLength(t *testing.T) {
	_, err := Marshal(PathNewConnectionIDFrame{ConnectionID: make([]byte, 21)})
	if err == nil {
		t.Fatal("expected error for invalid connection id length")
	}
}
