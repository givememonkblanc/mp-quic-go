package handshake

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/utils"

	"github.com/stretchr/testify/require"
)

// TestMultipathNonceDraftVector verifies the path-ID AEAD nonce calculation
// against the official example from draft-ietf-quic-multipath-21, Section 2.4
// (Figure 3):
//
//	IV:    6b26114b9cba2b63a9e8dd4f
//	PPN:   00000003000000000000d431   (path ID 3, packet number 54321 = 0xd431)
//	Nonce: 6b2611489cba2b63a9e8097e
//
// buildNonce produces the 96-bit Path-and-Packet-Number (PPN); the xorNonceAEAD
// then XORs it with the IV to form the nonce. Here we reproduce that XOR with
// the draft's IV and assert the resulting nonce matches the draft exactly.
func TestMultipathNonceDraftVector(t *testing.T) {
	iv := mustHex(t, "6b26114b9cba2b63a9e8dd4f")
	wantPPN := mustHex(t, "00000003000000000000d431")
	wantNonce := mustHex(t, "6b2611489cba2b63a9e8097e")

	a := &updatableAEAD{nonceBuf: make([]byte, aeadNonceLength)}
	a.buildNonce(3, protocol.PacketNumber(54321))

	require.Equal(t, wantPPN, a.nonceBuf, "PPN must be path ID (32) || zeroes (2) || packet number (62)")

	nonce := make([]byte, len(iv))
	for i := range nonce {
		nonce[i] = iv[i] ^ a.nonceBuf[i]
	}
	require.Equal(t, wantNonce, nonce, "nonce = IV xor PPN (draft §2.4, Figure 3)")
}

// TestMultipathNoncePath0IsStandard verifies that path ID 0 yields the standard
// single-path nonce (PPN high 4 bytes are zero), i.e. the multipath nonce is
// backward compatible with the primary path.
func TestMultipathNoncePath0IsStandard(t *testing.T) {
	a := &updatableAEAD{nonceBuf: make([]byte, aeadNonceLength)}
	a.buildNonce(0, protocol.PacketNumber(0xd431))
	require.Equal(t, mustHex(t, "00000000000000000000d431"), a.nonceBuf)
}

// TestMultipathSealForPathBindsPathID proves the path ID is cryptographically
// bound into the AEAD: a packet sealed for one path cannot be opened as a
// different path (the nonce differs, so authentication fails), while opening
// with the matching path ID succeeds.
func TestMultipathSealForPathBindsPathID(t *testing.T) {
	client, server, _ := setupEndpoints(t, &utils.RTTStats{})

	const pn = protocol.PacketNumber(54321)
	plaintext := []byte(msg)
	additional := []byte(ad)

	sealed := client.SealForPath(nil, plaintext, 1, pn, additional)

	// Matching path ID opens successfully.
	opened, err := server.OpenForPath(nil, sealed, time.Now(), 1, pn, protocol.KeyPhaseZero, additional)
	require.NoError(t, err)
	require.Equal(t, plaintext, opened)

	// Wrong path IDs must fail to authenticate.
	for _, badPath := range []uint32{0, 2, 7} {
		_, err := server.OpenForPath(nil, sealed, time.Now(), badPath, pn, protocol.KeyPhaseZero, additional)
		require.Errorf(t, err, "opening a path-1 packet as path %d must fail", badPath)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}
