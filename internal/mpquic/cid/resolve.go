package cid

import (
	"bytes"

	"mp-quic-go/internal/mpquic/path"
)

// PathForLocalConnectionID resolves which path a locally-issued connection ID
// belongs to. This is the receive-side demultiplexing primitive for draft-21
// multipath: an incoming 1-RTT packet carries one of our issued connection IDs
// as its Destination Connection ID, and that connection ID is associated with a
// specific path ID (draft-ietf-quic-multipath-21 §3.1, §4.4). The resolved path
// ID selects the packet number space and the AEAD nonce path ID for decryption.
//
// Retired connection IDs still resolve, since packets already in flight may
// legitimately arrive on a connection ID shortly after it is retired.
func (r *Registry) PathForLocalConnectionID(connectionID []byte) (path.ID, bool) {
	for pathID, entries := range r.local {
		for i := range entries {
			if bytes.Equal(entries[i].ConnectionID, connectionID) {
				return pathID, true
			}
		}
	}
	return 0, false
}

// UsableRemote returns a non-retired peer-issued connection ID to use as the
// Destination Connection ID when sending on the given path. Per draft §3.1, an
// endpoint MUST use a connection ID associated with the path it sends on, so a
// path can only carry traffic once the peer has provided a usable connection ID
// for that path via a PATH_NEW_CONNECTION_ID frame. The lowest-sequence usable
// entry is returned for determinism.
func (r *Registry) UsableRemote(pathID path.ID) (Entry, bool) {
	for _, entry := range r.remote[pathID] {
		if !entry.Retired {
			return entry, true
		}
	}
	return Entry{}, false
}
