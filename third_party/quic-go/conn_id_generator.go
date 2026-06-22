package quic

import (
	"fmt"

	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/qerr"
	"github.com/quic-go/quic-go/internal/wire"
)

type connIDGenerator struct {
	generator  ConnectionIDGenerator
	highestSeq uint64

	activeSrcConnIDs        map[uint64]protocol.ConnectionID
	initialClientDestConnID *protocol.ConnectionID // nil for the client

	// Per-path source connection IDs (draft-21 multipath). Connection IDs issued
	// for a non-zero path are tracked separately so that, on receipt, a packet's
	// Destination Connection ID resolves to the path it was issued for (and thus
	// to that path's packet number space and AEAD nonce path ID). Path 0 keeps
	// using activeSrcConnIDs / NewConnectionIDFrame above.
	highestSeqByPath map[PathID]uint64                          // per-path sequence numbering (§4.4)
	srcConnIDsByPath map[PathID]map[uint64]protocol.ConnectionID // path -> seq -> CID
	pathForSrcConnID map[protocol.ConnectionID]PathID            // demux: CID -> path

	addConnectionID        func(protocol.ConnectionID)
	getStatelessResetToken func(protocol.ConnectionID) protocol.StatelessResetToken
	removeConnectionID     func(protocol.ConnectionID)
	retireConnectionID     func(protocol.ConnectionID)
	replaceWithClosed      func([]protocol.ConnectionID, []byte)
	queueControlFrame      func(wire.Frame)
}

func newConnIDGenerator(
	initialConnectionID protocol.ConnectionID,
	initialClientDestConnID *protocol.ConnectionID, // nil for the client
	addConnectionID func(protocol.ConnectionID),
	getStatelessResetToken func(protocol.ConnectionID) protocol.StatelessResetToken,
	removeConnectionID func(protocol.ConnectionID),
	retireConnectionID func(protocol.ConnectionID),
	replaceWithClosed func([]protocol.ConnectionID, []byte),
	queueControlFrame func(wire.Frame),
	generator ConnectionIDGenerator,
) *connIDGenerator {
	m := &connIDGenerator{
		generator:              generator,
		activeSrcConnIDs:       make(map[uint64]protocol.ConnectionID),
		highestSeqByPath:       make(map[PathID]uint64),
		srcConnIDsByPath:       make(map[PathID]map[uint64]protocol.ConnectionID),
		pathForSrcConnID:       make(map[protocol.ConnectionID]PathID),
		addConnectionID:        addConnectionID,
		getStatelessResetToken: getStatelessResetToken,
		removeConnectionID:     removeConnectionID,
		retireConnectionID:     retireConnectionID,
		replaceWithClosed:      replaceWithClosed,
		queueControlFrame:      queueControlFrame,
	}
	m.activeSrcConnIDs[0] = initialConnectionID
	m.initialClientDestConnID = initialClientDestConnID
	return m
}

func (m *connIDGenerator) SetMaxActiveConnIDs(limit uint64) error {
	if m.generator.ConnectionIDLen() == 0 {
		return nil
	}
	// The active_connection_id_limit transport parameter is the number of
	// connection IDs the peer will store. This limit includes the connection ID
	// used during the handshake, and the one sent in the preferred_address
	// transport parameter.
	// We currently don't send the preferred_address transport parameter,
	// so we can issue (limit - 1) connection IDs.
	for i := uint64(len(m.activeSrcConnIDs)); i < min(limit, protocol.MaxIssuedConnectionIDs); i++ {
		if err := m.issueNewConnID(); err != nil {
			return err
		}
	}
	return nil
}

func (m *connIDGenerator) Retire(seq uint64, sentWithDestConnID protocol.ConnectionID) error {
	if seq > m.highestSeq {
		return &qerr.TransportError{
			ErrorCode:    qerr.ProtocolViolation,
			ErrorMessage: fmt.Sprintf("retired connection ID %d (highest issued: %d)", seq, m.highestSeq),
		}
	}
	connID, ok := m.activeSrcConnIDs[seq]
	// We might already have deleted this connection ID, if this is a duplicate frame.
	if !ok {
		return nil
	}
	if connID == sentWithDestConnID {
		return &qerr.TransportError{
			ErrorCode:    qerr.ProtocolViolation,
			ErrorMessage: fmt.Sprintf("retired connection ID %d (%s), which was used as the Destination Connection ID on this packet", seq, connID),
		}
	}
	m.retireConnectionID(connID)
	delete(m.activeSrcConnIDs, seq)
	// Don't issue a replacement for the initial connection ID.
	if seq == 0 {
		return nil
	}
	return m.issueNewConnID()
}

func (m *connIDGenerator) issueNewConnID() error {
	connID, err := m.generator.GenerateConnectionID()
	if err != nil {
		return err
	}
	m.activeSrcConnIDs[m.highestSeq+1] = connID
	m.addConnectionID(connID)
	m.queueControlFrame(&wire.NewConnectionIDFrame{
		SequenceNumber:      m.highestSeq + 1,
		ConnectionID:        connID,
		StatelessResetToken: m.getStatelessResetToken(connID),
	})
	m.highestSeq++
	return nil
}

// IssueConnIDForPath generates and issues `count` new source connection IDs for
// the given non-zero path, sending each to the peer in a PATH_NEW_CONNECTION_ID
// frame (draft-21 §4.4). Each issued connection ID is registered for routing
// (addConnectionID) and recorded so that incoming packets carrying it resolve to
// this path. Sequence numbers are per-path. Path 0 must use issueNewConnID.
func (m *connIDGenerator) IssueConnIDForPath(pathID PathID, count int) error {
	if pathID == 0 {
		return fmt.Errorf("path 0 uses issueNewConnID, not IssueConnIDForPath")
	}
	if m.generator.ConnectionIDLen() == 0 {
		return nil
	}
	for i := 0; i < count; i++ {
		connID, err := m.generator.GenerateConnectionID()
		if err != nil {
			return err
		}
		if m.srcConnIDsByPath[pathID] == nil {
			m.srcConnIDsByPath[pathID] = make(map[uint64]protocol.ConnectionID)
		}
		seq := m.nextSeqForPath(pathID)
		m.srcConnIDsByPath[pathID][seq] = connID
		m.pathForSrcConnID[connID] = pathID
		m.addConnectionID(connID)
		m.queueControlFrame(&wire.PathNewConnectionIDFrame{
			PathID:              uint64(pathID),
			SequenceNumber:      seq,
			RetirePriorTo:       0,
			ConnectionID:        connID,
			StatelessResetToken: m.getStatelessResetToken(connID),
		})
	}
	return nil
}

// PathForConnID resolves which path a locally-issued connection ID was issued
// for. Returns (0, true) for path-0 / handshake connection IDs and the recorded
// path for per-path connection IDs. The second return is false if the
// connection ID is unknown to this generator.
// RetireForPath retires a locally-issued per-path connection ID in response to a
// PATH_RETIRE_CONNECTION_ID frame (draft-21 §4.5), removing it from routing and
// from the demux mapping.
func (m *connIDGenerator) RetireForPath(pathID PathID, seq uint64) error {
	seqs, ok := m.srcConnIDsByPath[pathID]
	if !ok {
		return nil
	}
	connID, ok := seqs[seq]
	if !ok {
		return nil // duplicate or already retired
	}
	m.retireConnectionID(connID)
	delete(m.pathForSrcConnID, connID)
	delete(seqs, seq)
	return nil
}

func (m *connIDGenerator) PathForConnID(connID protocol.ConnectionID) (PathID, bool) {
	if pathID, ok := m.pathForSrcConnID[connID]; ok {
		return pathID, true
	}
	if m.initialClientDestConnID != nil && connID == *m.initialClientDestConnID {
		return 0, true
	}
	for _, c := range m.activeSrcConnIDs {
		if c == connID {
			return 0, true
		}
	}
	return 0, false
}

func (m *connIDGenerator) nextSeqForPath(pathID PathID) uint64 {
	seq, ok := m.highestSeqByPath[pathID]
	if !ok {
		m.highestSeqByPath[pathID] = 0
		return 0
	}
	m.highestSeqByPath[pathID] = seq + 1
	return seq + 1
}

func (m *connIDGenerator) SetHandshakeComplete() {
	if m.initialClientDestConnID != nil {
		m.retireConnectionID(*m.initialClientDestConnID)
		m.initialClientDestConnID = nil
	}
}

func (m *connIDGenerator) RemoveAll() {
	if m.initialClientDestConnID != nil {
		m.removeConnectionID(*m.initialClientDestConnID)
	}
	for _, connID := range m.activeSrcConnIDs {
		m.removeConnectionID(connID)
	}
	for _, seqs := range m.srcConnIDsByPath {
		for _, connID := range seqs {
			m.removeConnectionID(connID)
		}
	}
}

func (m *connIDGenerator) ReplaceWithClosed(connClose []byte) {
	connIDs := make([]protocol.ConnectionID, 0, len(m.activeSrcConnIDs)+1)
	if m.initialClientDestConnID != nil {
		connIDs = append(connIDs, *m.initialClientDestConnID)
	}
	for _, connID := range m.activeSrcConnIDs {
		connIDs = append(connIDs, connID)
	}
	for _, seqs := range m.srcConnIDsByPath {
		for _, connID := range seqs {
			connIDs = append(connIDs, connID)
		}
	}
	m.replaceWithClosed(connIDs, connClose)
}
