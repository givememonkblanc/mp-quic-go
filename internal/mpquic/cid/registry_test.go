package cid

import (
	"testing"

	"mp-quic-go/internal/mpquic/path"
)

func tok(b byte) [16]byte {
	var t [16]byte
	for i := range t {
		t[i] = b
	}
	return t
}

// TestIssueLocalPerPathSequence checks that sequence numbers are tracked
// per-path: connection IDs on different paths can share a sequence number
// (draft §4.4: "the sequence number applies on a per-path context").
func TestIssueLocalPerPathSequence(t *testing.T) {
	r := NewRegistry()
	e0a, _ := r.IssueLocal(0, []byte{0xc0, 0x00}, tok(1), 0)
	e0b, _ := r.IssueLocal(0, []byte{0xc0, 0x01}, tok(2), 0)
	e1a, _ := r.IssueLocal(1, []byte{0xc1, 0x00}, tok(3), 0)

	if e0a.SequenceNumber != 0 || e0b.SequenceNumber != 1 {
		t.Fatalf("path 0 sequences: got %d,%d want 0,1", e0a.SequenceNumber, e0b.SequenceNumber)
	}
	if e1a.SequenceNumber != 0 {
		t.Fatalf("path 1 first sequence: got %d want 0 (per-path numbering)", e1a.SequenceNumber)
	}
}

// TestPathForLocalConnectionID is the receive-side demux: an incoming DCID maps
// to the path it was issued for.
func TestPathForLocalConnectionID(t *testing.T) {
	r := NewRegistry()
	r.IssueLocal(0, []byte{0xc0, 0xaa}, tok(1), 0)
	r.IssueLocal(1, []byte{0xc1, 0xbb}, tok(2), 0)
	r.IssueLocal(2, []byte{0xc2, 0xcc}, tok(3), 0)

	cases := []struct {
		cid  []byte
		want path.ID
	}{
		{[]byte{0xc0, 0xaa}, 0},
		{[]byte{0xc1, 0xbb}, 1},
		{[]byte{0xc2, 0xcc}, 2},
	}
	for _, c := range cases {
		got, ok := r.PathForLocalConnectionID(c.cid)
		if !ok || got != c.want {
			t.Fatalf("PathForLocalConnectionID(%x) = %d,%v want %d,true", c.cid, got, ok, c.want)
		}
	}
	if _, ok := r.PathForLocalConnectionID([]byte{0xff, 0xff}); ok {
		t.Fatal("unknown connection ID must not resolve to a path")
	}
}

// TestUsableRemote is the send-side primitive: pick a non-retired peer CID for a
// path, and report none available before the peer has issued one.
func TestUsableRemote(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.UsableRemote(1); ok {
		t.Fatal("no remote CID issued for path 1 yet, must be unusable")
	}
	if err := r.RegisterRemote(1, 0, 0, []byte{0x51, 0x00}, tok(9)); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterRemote(1, 1, 0, []byte{0x51, 0x01}, tok(10)); err != nil {
		t.Fatal(err)
	}
	e, ok := r.UsableRemote(1)
	if !ok || e.SequenceNumber != 0 {
		t.Fatalf("UsableRemote(1) = seq %d,%v want seq 0,true (lowest usable)", e.SequenceNumber, ok)
	}

	// After retiring seq 0, the next usable one is seq 1.
	if err := r.RetireRemote(1, 0); err != nil {
		t.Fatal(err)
	}
	e, ok = r.UsableRemote(1)
	if !ok || e.SequenceNumber != 1 {
		t.Fatalf("after retiring seq 0: UsableRemote(1) = seq %d,%v want seq 1,true", e.SequenceNumber, ok)
	}
}

// TestRetirePathClearsUsability verifies abandoning a path makes its remote CIDs
// unusable for sending.
func TestRetirePathClearsUsability(t *testing.T) {
	r := NewRegistry()
	r.RegisterRemote(2, 0, 0, []byte{0x52, 0x00}, tok(1))
	if !r.HasUsableRemote(2) {
		t.Fatal("path 2 should have a usable remote CID")
	}
	r.RetirePath(2)
	if r.HasUsableRemote(2) {
		t.Fatal("after RetirePath, path 2 must have no usable remote CID")
	}
	if _, ok := r.UsableRemote(2); ok {
		t.Fatal("UsableRemote(2) must be false after RetirePath")
	}
}
