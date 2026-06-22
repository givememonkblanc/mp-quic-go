package quic

import (
	"testing"

	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/wire"

	"github.com/stretchr/testify/require"
)

// TestManagerAddPathAndGet verifies the peer-side per-path connection ID store:
// a CID received via PATH_NEW_CONNECTION_ID becomes usable as the DCID for that
// path, paths are independent, and the stateless reset token is registered.
func TestManagerAddPathAndGet(t *testing.T) {
	var tokens []protocol.StatelessResetToken
	m := newConnIDManager(
		protocol.ParseConnectionID([]byte{0, 0, 0, 0}),
		func(tk protocol.StatelessResetToken) { tokens = append(tokens, tk) },
		func(tk protocol.StatelessResetToken) {},
		func(f wire.Frame) {},
	)

	// No CID for a path until the peer issues one.
	_, ok := m.GetForPath(1)
	require.False(t, ok)

	cid1 := protocol.ParseConnectionID([]byte{0x51, 0x01, 0x02, 0x03})
	require.NoError(t, m.AddPath(&wire.PathNewConnectionIDFrame{
		PathID: 1, SequenceNumber: 0, ConnectionID: cid1,
		StatelessResetToken: protocol.StatelessResetToken{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
	}))

	got, ok := m.GetForPath(1)
	require.True(t, ok)
	require.Equal(t, cid1, got)
	require.Len(t, tokens, 1, "stateless reset token must be registered")

	// A different path is independent.
	_, ok = m.GetForPath(2)
	require.False(t, ok)

	// Duplicate (same path+seq) is ignored.
	require.NoError(t, m.AddPath(&wire.PathNewConnectionIDFrame{
		PathID: 1, SequenceNumber: 0, ConnectionID: cid1,
		StatelessResetToken: protocol.StatelessResetToken{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9},
	}))
	require.Len(t, tokens, 1, "duplicate must not re-register a token")
}

// TestManagerAddPathZeroRejected verifies PATH_NEW_CONNECTION_ID for path 0 is a
// protocol violation (path 0 uses NEW_CONNECTION_ID; draft §4.4).
func TestManagerAddPathZeroRejected(t *testing.T) {
	m := newConnIDManager(
		protocol.ParseConnectionID([]byte{0, 0, 0, 0}),
		func(protocol.StatelessResetToken) {},
		func(protocol.StatelessResetToken) {},
		func(wire.Frame) {},
	)
	require.Error(t, m.AddPath(&wire.PathNewConnectionIDFrame{PathID: 0, SequenceNumber: 0}))
}
