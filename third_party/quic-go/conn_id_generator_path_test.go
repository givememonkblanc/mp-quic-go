package quic

import (
	"testing"

	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/wire"

	"github.com/stretchr/testify/require"
)

func newTestPathGenerator() (g *connIDGenerator, added *[]protocol.ConnectionID, frames *[]wire.Frame) {
	initialConnID := protocol.ParseConnectionID([]byte{1, 2, 3, 4, 5, 6, 7})
	initialClientDestConnID := protocol.ParseConnectionID([]byte{0xa, 0xb, 0xc, 0xd, 0xe})
	var addedConnIDs []protocol.ConnectionID
	var queuedFrames []wire.Frame
	tokenFor := func(c protocol.ConnectionID) protocol.StatelessResetToken {
		b := c.Bytes()[0]
		return protocol.StatelessResetToken{b, b, b, b, b, b, b, b, b, b, b, b, b, b, b, b}
	}
	g = newConnIDGenerator(
		initialConnID,
		&initialClientDestConnID,
		func(c protocol.ConnectionID) { addedConnIDs = append(addedConnIDs, c) },
		tokenFor,
		func(c protocol.ConnectionID) {},
		func(c protocol.ConnectionID) {},
		func(cs []protocol.ConnectionID, _ []byte) {},
		func(f wire.Frame) { queuedFrames = append(queuedFrames, f) },
		&protocol.DefaultConnectionIDGenerator{ConnLen: initialConnID.Len()},
	)
	return g, &addedConnIDs, &queuedFrames
}

// TestIssueConnIDForPathEmitsFrames verifies that issuing per-path connection IDs
// registers them for routing and emits PATH_NEW_CONNECTION_ID frames with
// per-path sequence numbering (draft-21 §4.4).
func TestIssueConnIDForPathEmitsFrames(t *testing.T) {
	g, added, frames := newTestPathGenerator()

	require.NoError(t, g.IssueConnIDForPath(1, 2))
	require.NoError(t, g.IssueConnIDForPath(2, 1))

	// Every issued CID must be registered for routing.
	require.Len(t, *added, 3)

	// Three PATH_NEW_CONNECTION_ID frames, with per-path sequence numbers.
	var pncids []*wire.PathNewConnectionIDFrame
	for _, f := range *frames {
		if p, ok := f.(*wire.PathNewConnectionIDFrame); ok {
			pncids = append(pncids, p)
		}
	}
	require.Len(t, pncids, 3)
	require.Equal(t, uint64(1), pncids[0].PathID)
	require.Equal(t, uint64(0), pncids[0].SequenceNumber)
	require.Equal(t, uint64(1), pncids[1].PathID)
	require.Equal(t, uint64(1), pncids[1].SequenceNumber)
	require.Equal(t, uint64(2), pncids[2].PathID)
	require.Equal(t, uint64(0), pncids[2].SequenceNumber, "per-path sequence restarts at 0 for each path")

	// The stateless reset token must match the issued connection ID.
	require.Equal(t, pncids[0].ConnectionID.Bytes()[0], pncids[0].StatelessResetToken[0])
}

// TestPathForConnIDDemux verifies the receive-side demux: a packet's Destination
// Connection ID resolves to the path it was issued for, path 0 for handshake
// CIDs, and not-found for unknown CIDs.
func TestPathForConnIDDemux(t *testing.T) {
	g, _, frames := newTestPathGenerator()
	require.NoError(t, g.IssueConnIDForPath(1, 1))
	require.NoError(t, g.IssueConnIDForPath(2, 1))

	var path1CID, path2CID protocol.ConnectionID
	for _, f := range *frames {
		p, ok := f.(*wire.PathNewConnectionIDFrame)
		if !ok {
			continue
		}
		switch p.PathID {
		case 1:
			path1CID = p.ConnectionID
		case 2:
			path2CID = p.ConnectionID
		}
	}

	got, ok := g.PathForConnID(path1CID)
	require.True(t, ok)
	require.Equal(t, PathID(1), got)

	got, ok = g.PathForConnID(path2CID)
	require.True(t, ok)
	require.Equal(t, PathID(2), got)

	// The initial (path 0) connection ID resolves to path 0.
	got, ok = g.PathForConnID(protocol.ParseConnectionID([]byte{1, 2, 3, 4, 5, 6, 7}))
	require.True(t, ok)
	require.Equal(t, PathID(0), got)

	// An unknown connection ID does not resolve.
	_, ok = g.PathForConnID(protocol.ParseConnectionID([]byte{9, 9, 9, 9, 9, 9, 9}))
	require.False(t, ok)
}

// TestIssueConnIDForPathZeroRejected ensures path 0 cannot be issued via the
// per-path API (it uses the standard NEW_CONNECTION_ID path).
func TestIssueConnIDForPathZeroRejected(t *testing.T) {
	g, _, _ := newTestPathGenerator()
	require.Error(t, g.IssueConnIDForPath(0, 1))
}
