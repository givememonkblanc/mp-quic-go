package path

import (
	"testing"
	"time"
)

func TestManagerEnsureInitialPath(t *testing.T) {
	mgr := NewManager(4)

	state := mgr.EnsureInitialPath("peer0")

	if state.ID != 0 {
		t.Fatalf("expected initial path ID 0, got %d", state.ID)
	}
	if state.RemoteAddr != "peer0" {
		t.Fatalf("expected remote addr peer0, got %q", state.RemoteAddr)
	}
	if !state.Validated {
		t.Fatal("expected initial path to be validated")
	}
	if state.Status != StatusActive {
		t.Fatalf("expected initial path status active, got %v", state.Status)
	}
	assertTimeSet(t, "CreatedAt", state.CreatedAt)
	assertTimeSet(t, "UpdatedAt", state.UpdatedAt)

	// EnsureInitialPath must be idempotent.
	again := mgr.EnsureInitialPath("peer0-changed")
	if again.ID != state.ID {
		t.Fatalf("expected same path ID, got %d", again.ID)
	}
	if again.RemoteAddr != "peer0" {
		t.Fatalf("expected existing remote addr to remain peer0, got %q", again.RemoteAddr)
	}

	snapshot := mgr.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected exactly one path in snapshot, got %d", len(snapshot))
	}
}

func TestManagerRegisterPath(t *testing.T) {
	mgr := NewManager(4)

	state, err := mgr.RegisterPath(1, "peer1")
	if err != nil {
		t.Fatalf("register path failed: %v", err)
	}

	if state.ID != 1 {
		t.Fatalf("expected path ID 1, got %d", state.ID)
	}
	if state.RemoteAddr != "peer1" {
		t.Fatalf("expected remote addr peer1, got %q", state.RemoteAddr)
	}
	if state.Validated {
		t.Fatal("newly registered non-initial path should not be validated")
	}
	if state.Status != StatusAvailable {
		t.Fatalf("expected status available, got %v", state.Status)
	}
	assertTimeSet(t, "CreatedAt", state.CreatedAt)
	assertTimeSet(t, "UpdatedAt", state.UpdatedAt)
}

func TestManagerRegisterPathIDZeroEnsuresInitialPath(t *testing.T) {
	mgr := NewManager(4)

	state, err := mgr.RegisterPath(0, "peer0")
	if err != nil {
		t.Fatalf("register path 0 failed: %v", err)
	}

	if state.ID != 0 {
		t.Fatalf("expected path ID 0, got %d", state.ID)
	}
	if !state.Validated {
		t.Fatal("expected path 0 to be validated")
	}
	if state.Status != StatusActive {
		t.Fatalf("expected path 0 to be active, got %v", state.Status)
	}
}

func TestManagerRegisterPathRejectsDuplicate(t *testing.T) {
	mgr := NewManager(4)

	if _, err := mgr.RegisterPath(1, "peer1"); err != nil {
		t.Fatalf("first register path failed: %v", err)
	}

	if _, err := mgr.RegisterPath(1, "peer1-duplicate"); err == nil {
		t.Fatal("expected duplicate path registration to fail")
	}
}

func TestManagerRegisterPathRejectsPathIDAboveMax(t *testing.T) {
	mgr := NewManager(1)

	if _, err := mgr.RegisterPath(2, "peer2"); err == nil {
		t.Fatal("expected path ID above maxPathID to fail")
	}
}

func TestManagerNextUnusedPathID(t *testing.T) {
	mgr := NewManager(4)

	mgr.EnsureInitialPath("peer0")

	if _, err := mgr.RegisterPath(1, "peer1"); err != nil {
		t.Fatalf("register path 1 failed: %v", err)
	}

	next, ok := mgr.NextUnusedPathID()
	if !ok {
		t.Fatal("expected next unused path ID to exist")
	}
	if next != 2 {
		t.Fatalf("expected next unused path ID 2, got %d", next)
	}

	for _, id := range []ID{2, 3, 4} {
		if _, err := mgr.RegisterPath(id, "peer"); err != nil {
			t.Fatalf("register path %d failed: %v", id, err)
		}
	}

	next, ok = mgr.NextUnusedPathID()
	if ok {
		t.Fatalf("expected no unused path ID, got %d", next)
	}
}

func TestManagerUpdateStatus(t *testing.T) {
	mgr := NewManager(4)

	state, err := mgr.RegisterPath(1, "peer1")
	if err != nil {
		t.Fatalf("register path failed: %v", err)
	}

	updated, err := mgr.UpdateStatus(1, StatusActive, true)
	if err != nil {
		t.Fatalf("update status failed: %v", err)
	}

	if updated.Status != StatusActive {
		t.Fatalf("expected active status, got %v", updated.Status)
	}
	if !updated.Validated {
		t.Fatal("expected path to be validated after status update")
	}
	if updated.UpdatedAt.Before(state.UpdatedAt) {
		t.Fatalf("expected UpdatedAt to be updated, old=%v new=%v", state.UpdatedAt, updated.UpdatedAt)
	}

	if _, err := mgr.UpdateStatus(99, StatusActive, true); err == nil {
		t.Fatal("expected updating unknown path to fail")
	}
}

func TestManagerUpdateRSSI(t *testing.T) {
	mgr := NewManager(4)

	mgr.EnsureInitialPath("peer0")

	state, err := mgr.UpdateRSSI(0, -55)
	if err != nil {
		t.Fatalf("update rssi failed: %v", err)
	}

	if !state.HasRSSI {
		t.Fatal("expected HasRSSI to be true")
	}
	if state.RSSI != -55 {
		t.Fatalf("expected RSSI -55, got %d", state.RSSI)
	}
	assertTimeSet(t, "UpdatedAt", state.UpdatedAt)

	if _, err := mgr.UpdateRSSI(99, -70); err == nil {
		t.Fatal("expected updating RSSI for unknown path to fail")
	}
}

func TestManagerSetLocalStatus(t *testing.T) {
	mgr := NewManager(4)

	if _, err := mgr.RegisterPath(1, "peer1"); err != nil {
		t.Fatalf("register path failed: %v", err)
	}

	state, err := mgr.SetLocalStatus(1, StatusBackup)
	if err != nil {
		t.Fatalf("set local status failed: %v", err)
	}

	if state.Status != StatusBackup {
		t.Fatalf("expected backup status, got %v", state.Status)
	}
	if state.LocalStatusSeq != 1 {
		t.Fatalf("expected local status seq 1, got %d", state.LocalStatusSeq)
	}

	state, err = mgr.SetLocalStatus(1, StatusActive)
	if err != nil {
		t.Fatalf("second set local status failed: %v", err)
	}

	if state.Status != StatusActive {
		t.Fatalf("expected active status, got %v", state.Status)
	}
	if state.LocalStatusSeq != 2 {
		t.Fatalf("expected local status seq 2, got %d", state.LocalStatusSeq)
	}

	if _, err := mgr.SetLocalStatus(99, StatusActive); err == nil {
		t.Fatal("expected setting local status for unknown path to fail")
	}
}

func TestManagerApplyPeerStatusSequence(t *testing.T) {
	mgr := NewManager(4)

	if _, err := mgr.RegisterPath(1, "peer1"); err != nil {
		t.Fatalf("register path failed: %v", err)
	}

	state, applied, err := mgr.ApplyPeerStatus(1, StatusBackup, 1)
	if err != nil {
		t.Fatalf("apply peer status failed: %v", err)
	}
	if !applied {
		t.Fatal("expected first peer status update to be applied")
	}
	if state.Status != StatusBackup {
		t.Fatalf("expected backup status, got %v", state.Status)
	}
	if state.PeerStatusSeq != 1 {
		t.Fatalf("expected peer status seq 1, got %d", state.PeerStatusSeq)
	}

	state, applied, err = mgr.ApplyPeerStatus(1, StatusAvailable, 1)
	if err != nil {
		t.Fatalf("apply stale peer status failed: %v", err)
	}
	if applied {
		t.Fatal("expected stale peer sequence to be ignored")
	}
	if state.Status != StatusBackup {
		t.Fatalf("expected status to remain backup after stale update, got %v", state.Status)
	}
	if state.PeerStatusSeq != 1 {
		t.Fatalf("expected peer status seq to remain 1, got %d", state.PeerStatusSeq)
	}

	state, applied, err = mgr.ApplyPeerStatus(1, StatusActive, 2)
	if err != nil {
		t.Fatalf("apply newer peer status failed: %v", err)
	}
	if !applied {
		t.Fatal("expected newer peer status update to be applied")
	}
	if state.Status != StatusActive {
		t.Fatalf("expected active status, got %v", state.Status)
	}
	if state.PeerStatusSeq != 2 {
		t.Fatalf("expected peer status seq 2, got %d", state.PeerStatusSeq)
	}

	if _, _, err := mgr.ApplyPeerStatus(99, StatusActive, 1); err == nil {
		t.Fatal("expected applying peer status to unknown path to fail")
	}
}

func TestManagerAbandon(t *testing.T) {
	mgr := NewManager(4)

	if _, err := mgr.RegisterPath(1, "peer1"); err != nil {
		t.Fatalf("register path failed: %v", err)
	}

	if err := mgr.Abandon(1); err != nil {
		t.Fatalf("abandon path failed: %v", err)
	}

	state, ok := mgr.Get(1)
	if !ok {
		t.Fatal("expected abandoned path to still exist")
	}
	if state.Status != StatusAbandoned {
		t.Fatalf("expected abandoned status, got %v", state.Status)
	}
	if state.Validated {
		t.Fatal("expected abandoned path to be unvalidated")
	}
}

func TestManagerUpdateMaxPathIDOnlyIncreases(t *testing.T) {
	mgr := NewManager(2)

	mgr.UpdateMaxPathID(1)
	if got := mgr.MaxPathID(); got != 2 {
		t.Fatalf("expected maxPathID not to decrease, got %d", got)
	}

	mgr.UpdateMaxPathID(5)
	if got := mgr.MaxPathID(); got != 5 {
		t.Fatalf("expected maxPathID to increase to 5, got %d", got)
	}
}

func TestManagerSnapshotSortedByPathID(t *testing.T) {
	mgr := NewManager(4)

	mgr.EnsureInitialPath("peer0")

	for _, id := range []ID{3, 1, 4, 2} {
		if _, err := mgr.RegisterPath(id, "peer"); err != nil {
			t.Fatalf("register path %d failed: %v", id, err)
		}
	}

	snapshot := mgr.Snapshot()
	expectedIDs := []ID{0, 1, 2, 3, 4}

	if len(snapshot) != len(expectedIDs) {
		t.Fatalf("expected snapshot len %d, got %d", len(expectedIDs), len(snapshot))
	}

	for i, expectedID := range expectedIDs {
		if snapshot[i].ID != expectedID {
			t.Fatalf("expected snapshot[%d].ID = %d, got %d", i, expectedID, snapshot[i].ID)
		}
	}
}

func TestManagerGetReturnsCopy(t *testing.T) {
	mgr := NewManager(4)

	mgr.EnsureInitialPath("peer0")

	state, ok := mgr.Get(0)
	if !ok {
		t.Fatal("expected path 0 to exist")
	}

	state.Status = StatusAbandoned

	stored, ok := mgr.Get(0)
	if !ok {
		t.Fatal("expected path 0 to still exist")
	}
	if stored.Status != StatusActive {
		t.Fatalf("expected stored state not to be mutated by external copy, got %v", stored.Status)
	}
}

func TestManagerSnapshotReturnsCopy(t *testing.T) {
	mgr := NewManager(4)

	mgr.EnsureInitialPath("peer0")

	snapshot := mgr.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected snapshot len 1, got %d", len(snapshot))
	}

	snapshot[0].Status = StatusAbandoned

	stored, ok := mgr.Get(0)
	if !ok {
		t.Fatal("expected path 0 to exist")
	}
	if stored.Status != StatusActive {
		t.Fatalf("expected stored state not to be mutated by snapshot copy, got %v", stored.Status)
	}
}

func assertTimeSet(t *testing.T, name string, value time.Time) {
	t.Helper()

	if value.IsZero() {
		t.Fatalf("expected %s to be set", name)
	}
}