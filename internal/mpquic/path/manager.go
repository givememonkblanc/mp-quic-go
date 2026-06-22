package path

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type Manager struct {
	mu        sync.RWMutex
	maxPathID uint32
	paths     map[ID]State
}

func NewManager(maxPathID uint32) *Manager {
	return &Manager{
		maxPathID: maxPathID,
		paths:     make(map[ID]State),
	}
}

func (m *Manager) EnsureInitialPath(remoteAddr string) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ensureInitialPathLocked(remoteAddr)
}

func (m *Manager) ensureInitialPathLocked(remoteAddr string) State {
	if state, ok := m.paths[0]; ok {
		return state
	}

	now := time.Now()
	state := State{
		ID:         0,
		RemoteAddr: remoteAddr,
		Validated:  true,
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	m.paths[0] = state
	return state
}

func (m *Manager) RegisterPath(id ID, remoteAddr string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == 0 {
		return m.ensureInitialPathLocked(remoteAddr), nil
	}
	if uint32(id) > m.maxPathID {
		return State{}, fmt.Errorf("path id %d exceeds max path id %d", id, m.maxPathID)
	}
	if _, exists := m.paths[id]; exists {
		return State{}, fmt.Errorf("path id %d already registered", id)
	}

	now := time.Now()
	state := State{
		ID:         id,
		RemoteAddr: remoteAddr,
		Status:     StatusAvailable,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	m.paths[id] = state
	return state, nil
}

func (m *Manager) UpdateStatus(id ID, status Status, validated bool) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.paths[id]
	if !ok {
		return State{}, fmt.Errorf("path id %d not registered", id)
	}

	state.Status = status
	state.Validated = validated
	state.UpdatedAt = time.Now()
	m.paths[id] = state
	return state, nil
}

func (m *Manager) UpdateRSSI(id ID, rssi int) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.paths[id]
	if !ok {
		return State{}, fmt.Errorf("path id %d not registered", id)
	}
	state.RSSI = rssi
	state.HasRSSI = true
	state.UpdatedAt = time.Now()
	m.paths[id] = state
	return state, nil
}

func (m *Manager) UpdateMaxPathID(maxPathID uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if maxPathID > m.maxPathID {
		m.maxPathID = maxPathID
	}
}

func (m *Manager) MaxPathID() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.maxPathID
}

func (m *Manager) Get(id ID) (State, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.paths[id]
	return state, ok
}

func (m *Manager) NextUnusedPathID() (ID, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for candidate := ID(1); candidate <= ID(m.maxPathID); candidate++ {
		if _, exists := m.paths[candidate]; !exists {
			return candidate, true
		}
	}
	return 0, false
}

func (m *Manager) SetLocalStatus(id ID, status Status) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.paths[id]
	if !ok {
		return State{}, fmt.Errorf("path id %d not registered", id)
	}
	state.LocalStatusSeq++
	state.Status = status
	state.UpdatedAt = time.Now()
	m.paths[id] = state
	return state, nil
}

func (m *Manager) ApplyPeerStatus(id ID, status Status, sequence uint64) (State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.paths[id]
	if !ok {
		return State{}, false, fmt.Errorf("path id %d not registered", id)
	}
	if sequence <= state.PeerStatusSeq {
		return state, false, nil
	}
	state.PeerStatusSeq = sequence
	state.Status = status
	state.UpdatedAt = time.Now()
	m.paths[id] = state
	return state, true, nil
}

func (m *Manager) Abandon(id ID) error {
	_, err := m.UpdateStatus(id, StatusAbandoned, false)
	return err
}

func (m *Manager) Snapshot() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make([]State, 0, len(m.paths))
	for _, state := range m.paths {
		states = append(states, state)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].ID < states[j].ID
	})

	return states
}
