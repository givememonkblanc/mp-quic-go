package cid

import (
	"fmt"
	"sort"

	"mp-quic-go/internal/mpquic/path"
)

type Entry struct {
	SequenceNumber      uint64
	ConnectionID        []byte
	StatelessResetToken [16]byte
	Retired             bool
}

type Registry struct {
	local  map[path.ID][]Entry
	remote map[path.ID][]Entry
}

func NewRegistry() *Registry {
	return &Registry{
		local:  make(map[path.ID][]Entry),
		remote: make(map[path.ID][]Entry),
	}
}

func (r *Registry) IssueLocal(pathID path.ID, connectionID []byte, token [16]byte, retirePriorTo uint64) (Entry, error) {
	next := r.NextLocalSequence(pathID)
	entry := Entry{SequenceNumber: next, ConnectionID: append([]byte(nil), connectionID...), StatelessResetToken: token}
	r.local[pathID] = append(retirePriorToLocal(r.local[pathID], retirePriorTo), entry)
	sortEntries(r.local[pathID])
	return entry, nil
}

func (r *Registry) RegisterRemote(pathID path.ID, sequenceNumber uint64, retirePriorTo uint64, connectionID []byte, token [16]byte) error {
	if retirePriorTo > sequenceNumber {
		return fmt.Errorf("retire_prior_to %d exceeds sequence number %d", retirePriorTo, sequenceNumber)
	}
	entries := retirePriorToLocal(r.remote[pathID], retirePriorTo)
	entries = append(entries, Entry{SequenceNumber: sequenceNumber, ConnectionID: append([]byte(nil), connectionID...), StatelessResetToken: token})
	r.remote[pathID] = entries
	sortEntries(r.remote[pathID])
	return nil
}

func (r *Registry) RetireLocal(pathID path.ID, sequenceNumber uint64) error {
	entries, ok := r.local[pathID]
	if !ok {
		return fmt.Errorf("local path id %d not registered", pathID)
	}
	r.local[pathID] = retire(entries, sequenceNumber)
	return nil
}

func (r *Registry) RetireRemote(pathID path.ID, sequenceNumber uint64) error {
	entries, ok := r.remote[pathID]
	if !ok {
		return fmt.Errorf("remote path id %d not registered", pathID)
	}
	r.remote[pathID] = retire(entries, sequenceNumber)
	return nil
}

func (r *Registry) Remote(pathID path.ID) []Entry {
	return append([]Entry(nil), r.remote[pathID]...)
}

func (r *Registry) Local(pathID path.ID) []Entry {
	return append([]Entry(nil), r.local[pathID]...)
}

func (r *Registry) NextLocalSequence(pathID path.ID) uint64 {
	return nextSequence(r.local[pathID])
}

func (r *Registry) NextRemoteSequence(pathID path.ID) uint64 {
	return nextSequence(r.remote[pathID])
}

func (r *Registry) HasUsableRemote(pathID path.ID) bool {
	for _, entry := range r.remote[pathID] {
		if !entry.Retired {
			return true
		}
	}
	return false
}

func (r *Registry) RetirePath(pathID path.ID) {
	for i := range r.local[pathID] {
		r.local[pathID][i].Retired = true
	}
	for i := range r.remote[pathID] {
		r.remote[pathID][i].Retired = true
	}
}

func nextSequence(entries []Entry) uint64 {
	var max uint64
	for i, entry := range entries {
		if i == 0 || entry.SequenceNumber >= max {
			max = entry.SequenceNumber + 1
		}
	}
	return max
}

func retire(entries []Entry, sequenceNumber uint64) []Entry {
	for i := range entries {
		if entries[i].SequenceNumber == sequenceNumber {
			entries[i].Retired = true
		}
	}
	return entries
}

func retirePriorToLocal(entries []Entry, retirePriorTo uint64) []Entry {
	for i := range entries {
		if entries[i].SequenceNumber < retirePriorTo {
			entries[i].Retired = true
		}
	}
	return entries
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SequenceNumber < entries[j].SequenceNumber
	})
}
