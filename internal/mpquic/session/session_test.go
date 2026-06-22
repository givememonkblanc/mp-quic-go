package session

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"mp-quic-go/internal/conn"
	"mp-quic-go/internal/handler"
	"mp-quic-go/internal/mpquic/frame"
	"mp-quic-go/internal/mpquic/path"
	"mp-quic-go/internal/mpquic/scheduler"
	"mp-quic-go/internal/mpquic/transport"
)

type stubRSSIProvider struct {
	rssiMap map[string]int
	err     error
}

func (s stubRSSIProvider) FetchRSSI(context.Context) (map[string]int, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.rssiMap == nil {
		return nil, nil
	}
	return s.rssiMap, nil
}

func (s stubRSSIProvider) Source() string {
	return "stub"
}

func newTestSession(t *testing.T) *Session {
	t.Helper()

	return newTestSessionWithRSSIProvider(t, nil)
}

func newTestSessionWithRSSIProvider(t *testing.T, provider interface {
	FetchRSSI(context.Context) (map[string]int, error)
	Source() string
}) *Session {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	streamHandler := conn.NewStreamHandler(logger, handler.NewEchoHandler())

	session, err := New(
		logger,
		streamHandler,
		transport.Parameters{
			InitialMaxPathID: 1,
			MaxPathID:        4,
		},
		scheduler.NewPrimaryPathScheduler(),
		provider,
	)
	if err != nil {
		t.Fatalf("new session failed: %v", err)
	}

	session.Bootstrap("peer0")
	return session
}

func TestSessionBootstrapInitializesInitialPath(t *testing.T) {
	session := newTestSession(t)

	state, ok := session.paths.Get(0)
	if !ok {
		t.Fatal("expected initial path 0 to exist")
	}

	if state.ID != 0 {
		t.Fatalf("expected path ID 0, got %d", state.ID)
	}
	if state.RemoteAddr != "peer0" {
		t.Fatalf("expected remote addr peer0, got %q", state.RemoteAddr)
	}
	if !state.Validated {
		t.Fatal("expected initial path to be validated")
	}
	if state.Status != path.StatusActive {
		t.Fatalf("expected initial path to be active, got %v", state.Status)
	}
}

func TestSessionOpenPathRequiresRemoteCID(t *testing.T) {
	session := newTestSession(t)

	_, frames, err := session.OpenPath("peer1")
	if err == nil {
		t.Fatal("expected open path to fail without remote cid")
	}

	if len(frames) != 1 {
		t.Fatalf("expected a single blocked frame, got %d", len(frames))
	}

	blocked, ok := frames[0].(frame.PathCIDsBlockedFrame)
	if !ok {
		t.Fatalf("unexpected response frame type %T", frames[0])
	}

	if blocked.PathID != 1 {
		t.Fatalf("expected blocked frame for path ID 1, got %d", blocked.PathID)
	}
}

func TestSessionRegistersRemoteCIDAndOpensPath(t *testing.T) {
	session := newTestSession(t)

	registerRemoteCID(t, session, 1, 0)

	state, frames, err := session.OpenPath("peer1")
	if err != nil {
		t.Fatalf("open path failed: %v", err)
	}

	if state.ID != 1 {
		t.Fatalf("expected opened path ID 1, got %d", state.ID)
	}
	if state.RemoteAddr != "peer1" {
		t.Fatalf("expected remote addr peer1, got %q", state.RemoteAddr)
	}
	if state.Status != path.StatusAvailable {
		t.Fatalf("expected opened path status available, got %v", state.Status)
	}
	if state.Validated {
		t.Fatal("newly opened additional path should not be validated yet")
	}

	if len(frames) != 1 {
		t.Fatalf("expected one status frame, got %d", len(frames))
	}

	statusFrame, ok := frames[0].(frame.PathStatusAvailableFrame)
	if !ok {
		t.Fatalf("unexpected status frame type %T", frames[0])
	}
	if statusFrame.PathID != 1 {
		t.Fatalf("expected status frame path ID 1, got %d", statusFrame.PathID)
	}
	if statusFrame.SequenceNumber != 1 {
		t.Fatalf("expected status sequence number 1, got %d", statusFrame.SequenceNumber)
	}
}

func TestSessionOpenPathAllocatesNextUnusedPathID(t *testing.T) {
	session := newTestSession(t)

	if _, err := session.HandleFrame(frame.MaxPathIDFrame{MaximumPathID: 4}); err != nil {
		t.Fatalf("handle max path id failed: %v", err)
	}

	registerRemoteCID(t, session, 1, 0)
	registerRemoteCID(t, session, 2, 0)

	first, _, err := session.OpenPath("peer1")
	if err != nil {
		t.Fatalf("open first path failed: %v", err)
	}
	if first.ID != 1 {
		t.Fatalf("expected first opened path ID 1, got %d", first.ID)
	}

	second, _, err := session.OpenPath("peer2")
	if err != nil {
		t.Fatalf("open second path failed: %v", err)
	}
	if second.ID != 2 {
		t.Fatalf("expected second opened path ID 2, got %d", second.ID)
	}
}

func TestSessionHandleMaxPathIDUpdatesPeerLimit(t *testing.T) {
	session := newTestSession(t)

	if _, err := session.HandleFrame(frame.MaxPathIDFrame{MaximumPathID: 3}); err != nil {
		t.Fatalf("handle max path id failed: %v", err)
	}

	if session.peerMaxPathID != 3 {
		t.Fatalf("unexpected peer max path id: %d", session.peerMaxPathID)
	}
}

func TestSessionRejectsMaxPathIDBelowInitialLimit(t *testing.T) {
	session := newTestSession(t)

	_, err := session.HandleFrame(frame.MaxPathIDFrame{MaximumPathID: 0})
	if err == nil {
		t.Fatal("expected invalid max path id error")
	}
}

func TestSessionHandleAbandonPath(t *testing.T) {
	session := newTestSession(t)

	registerRemoteCID(t, session, 1, 0)

	if _, _, err := session.OpenPath("peer1"); err != nil {
		t.Fatalf("open path failed: %v", err)
	}

	responses, err := session.HandleFrame(frame.PathAbandonFrame{
		PathID:    1,
		ErrorCode: frame.ApplicationAbandonPath,
	})
	if err != nil {
		t.Fatalf("handle path abandon failed: %v", err)
	}

	if len(responses) != 1 {
		t.Fatalf("expected one abandon response, got %d", len(responses))
	}

	state, ok := session.paths.Get(1)
	if !ok {
		t.Fatal("expected path 1 to exist after abandon")
	}
	if state.Status != path.StatusAbandoned {
		t.Fatalf("expected path to be abandoned, got %v", state.Status)
	}
	if state.Validated {
		t.Fatal("expected abandoned path to be unvalidated")
	}
}

func TestSessionHandleAbandonUnknownPathFails(t *testing.T) {
	session := newTestSession(t)

	_, err := session.HandleFrame(frame.PathAbandonFrame{
		PathID:    99,
		ErrorCode: frame.ApplicationAbandonPath,
	})
	if err == nil {
		t.Fatal("expected abandoning unknown path to fail")
	}
}

func TestSessionAppliesPeerPathStatusInSequenceOrder(t *testing.T) {
	session := newTestSession(t)

	registerRemoteCID(t, session, 1, 0)

	if _, _, err := session.OpenPath("peer1"); err != nil {
		t.Fatalf("open path failed: %v", err)
	}

	if _, err := session.HandleFrame(frame.PathStatusBackupFrame{
		PathID:         1,
		SequenceNumber: 2,
	}); err != nil {
		t.Fatalf("apply backup status failed: %v", err)
	}

	state, ok := session.paths.Get(1)
	if !ok {
		t.Fatal("expected path 1 to exist")
	}
	if state.Status != path.StatusBackup {
		t.Fatalf("expected backup status, got %v", state.Status)
	}
	if state.PeerStatusSeq != 2 {
		t.Fatalf("expected peer status seq 2, got %d", state.PeerStatusSeq)
	}

	if _, err := session.HandleFrame(frame.PathStatusAvailableFrame{
		PathID:         1,
		SequenceNumber: 3,
	}); err != nil {
		t.Fatalf("apply available status failed: %v", err)
	}

	state, ok = session.paths.Get(1)
	if !ok {
		t.Fatal("expected path 1 to exist")
	}
	if state.Status != path.StatusAvailable {
		t.Fatalf("expected available status, got %v", state.Status)
	}
	if state.PeerStatusSeq != 3 {
		t.Fatalf("expected peer status seq 3, got %d", state.PeerStatusSeq)
	}
}

func TestSessionIgnoresStalePeerPathStatus(t *testing.T) {
	session := newTestSession(t)

	registerRemoteCID(t, session, 1, 0)

	if _, _, err := session.OpenPath("peer1"); err != nil {
		t.Fatalf("open path failed: %v", err)
	}

	if _, err := session.HandleFrame(frame.PathStatusBackupFrame{
		PathID:         1,
		SequenceNumber: 2,
	}); err != nil {
		t.Fatalf("apply backup status failed: %v", err)
	}

	state, ok := session.paths.Get(1)
	if !ok || state.Status != path.StatusBackup || state.PeerStatusSeq != 2 {
		t.Fatalf("unexpected state after backup: %#v", state)
	}

	if _, err := session.HandleFrame(frame.PathStatusAvailableFrame{
		PathID:         1,
		SequenceNumber: 1,
	}); err != nil {
		t.Fatalf("stale status should be ignored without error: %v", err)
	}

	state, ok = session.paths.Get(1)
	if !ok {
		t.Fatal("expected path 1 to exist")
	}
	if state.Status != path.StatusBackup {
		t.Fatalf("stale status should not overwrite latest state, got %v", state.Status)
	}
	if state.PeerStatusSeq != 2 {
		t.Fatalf("expected peer status seq to remain 2, got %d", state.PeerStatusSeq)
	}
}

func TestSessionRejectsPeerStatusForUnknownPath(t *testing.T) {
	session := newTestSession(t)

	_, err := session.HandleFrame(frame.PathStatusAvailableFrame{
		PathID:         99,
		SequenceNumber: 1,
	})
	if err == nil {
		t.Fatal("expected peer status for unknown path to fail")
	}
}

func TestSessionIssuesAndRetiresLocalConnectionID(t *testing.T) {
	session := newTestSession(t)

	token := testToken(0x01)

	issued, err := session.IssueLocalConnectionID(0, []byte{0xaa, 0xbb, 0xcc}, token, 0)
	if err != nil {
		t.Fatalf("issue local connection id failed: %v", err)
	}

	if issued.SequenceNumber != 0 {
		t.Fatalf("unexpected sequence number: %d", issued.SequenceNumber)
	}
	if issued.Retired {
		t.Fatal("newly issued local connection ID should not be retired")
	}

	if _, err := session.HandleFrame(frame.PathRetireConnectionIDFrame{
		PathID:         0,
		SequenceNumber: 0,
	}); err != nil {
		t.Fatalf("retire local connection id failed: %v", err)
	}

	entries := session.connIDs.Local(0)
	if len(entries) != 1 {
		t.Fatalf("expected one local connection ID entry, got %d", len(entries))
	}
	if !entries[0].Retired {
		t.Fatalf("expected local connection id to be retired: %#v", entries)
	}
}

func TestSessionRejectsRetireUnknownLocalConnectionID(t *testing.T) {
	session := newTestSession(t)

	_, err := session.HandleFrame(frame.PathRetireConnectionIDFrame{
		PathID:         0,
		SequenceNumber: 99,
	})
	if err == nil {
		t.Fatal("expected retiring unknown local connection ID to fail")
	}
}

func TestSessionRejectsPathCIDsBlockedPastLocalSequence(t *testing.T) {
	session := newTestSession(t)

	_, err := session.HandleFrame(frame.PathCIDsBlockedFrame{
		PathID:             0,
		NextSequenceNumber: 1,
	})
	if err == nil {
		t.Fatal("expected path_cids_blocked validation error")
	}
}

func TestSessionUpdatePathRSSI(t *testing.T) {
	session := newTestSession(t)

	state, err := session.UpdatePathRSSI(0, -48)
	if err != nil {
		t.Fatalf("update path rssi failed: %v", err)
	}

	if !state.HasRSSI {
		t.Fatal("expected HasRSSI to be true")
	}
	if state.RSSI != -48 {
		t.Fatalf("expected RSSI -48, got %d", state.RSSI)
	}
}

func TestSessionUpdatePathRSSIRejectsUnknownPath(t *testing.T) {
	session := newTestSession(t)

	_, err := session.UpdatePathRSSI(99, -48)
	if err == nil {
		t.Fatal("expected updating RSSI for unknown path to fail")
	}
}

func TestSessionRefreshPathRSSIWithoutProviderIsNoop(t *testing.T) {
	session := newTestSession(t)

	if err := session.refreshPathRSSI(context.Background()); err != nil {
		t.Fatalf("refresh path rssi without provider should not fail: %v", err)
	}

	state, ok := session.paths.Get(0)
	if !ok {
		t.Fatal("expected path 0 to exist")
	}
	if state.HasRSSI {
		t.Fatalf("expected RSSI to remain unset without provider: %#v", state)
	}
}

func TestSessionRefreshPathRSSIWithEmptyProviderIsNoop(t *testing.T) {
	session := newTestSessionWithRSSIProvider(t, stubRSSIProvider{
		rssiMap: map[string]int{},
	})

	if err := session.refreshPathRSSI(context.Background()); err != nil {
		t.Fatalf("refresh path rssi failed: %v", err)
	}

	state, ok := session.paths.Get(0)
	if !ok {
		t.Fatal("expected path 0 to exist")
	}
	if state.HasRSSI {
		t.Fatalf("expected RSSI to remain unset with empty provider: %#v", state)
	}
}

func TestSessionRefreshInitialPathRSSI(t *testing.T) {
	session := newTestSessionWithRSSIProvider(t, stubRSSIProvider{
		rssiMap: map[string]int{
			"wlan0": -51,
		},
	})

	if err := session.refreshPathRSSI(context.Background()); err != nil {
		t.Fatalf("refresh path rssi failed: %v", err)
	}

	state, ok := session.paths.Get(0)
	if !ok {
		t.Fatal("expected path 0 to exist")
	}
	if !state.HasRSSI {
		t.Fatal("expected RSSI to be set")
	}
	if state.RSSI != -51 {
		t.Fatalf("expected RSSI -51, got %d", state.RSSI)
	}

	if iface, exists := session.InterfaceForPath(0); !exists || iface != "wlan0" {
		t.Fatalf("expected interface mapping for path 0: got iface=%q, exists=%v", iface, exists)
	}
}

func TestSessionRefreshPathRSSIPropagatesProviderError(t *testing.T) {
	expectedErr := errStubRSSIFailed{}

	session := newTestSessionWithRSSIProvider(t, stubRSSIProvider{
		err: expectedErr,
	})

	err := session.refreshPathRSSI(context.Background())
	if err == nil {
		t.Fatal("expected RSSI provider error")
	}
}

func TestSessionInterfaceForPathReturnsFalseForUnknownMapping(t *testing.T) {
	session := newTestSession(t)

	if iface, exists := session.InterfaceForPath(99); exists {
		t.Fatalf("expected no interface mapping for path 99, got %q", iface)
	}
}

func registerRemoteCID(t *testing.T, session *Session, pathID path.ID, seq uint64) {
	t.Helper()

	token := testToken(byte(pathID) + 1)

	_, err := session.HandleFrame(frame.PathNewConnectionIDFrame{
		PathID:              uint64(pathID),
		SequenceNumber:      seq,
		RetirePriorTo:       0,
		ConnectionID:        []byte{byte(pathID), 0xaa, 0xbb},
		StatelessResetToken: token,
	})
	if err != nil {
		t.Fatalf("register remote cid for path %d failed: %v", pathID, err)
	}
}

func testToken(seed byte) [16]byte {
	var token [16]byte
	for i := range token {
		token[i] = seed
	}
	return token
}

type errStubRSSIFailed struct{}

func (errStubRSSIFailed) Error() string {
	return "stub rssi failed"
}