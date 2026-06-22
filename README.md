# mp-quic-go

Minimal QUIC server scaffold for experimenting with multipath-oriented transport handling in Go.

This repository now includes a repo-level draft-21 MP-QUIC implementation layer for:

- path state and per-path packet-number-space modeling
- per-path connection ID lifecycle tracking
- draft transport parameter modeling
- draft `PATH_*` / `MAX_PATH_ID` / blocked-frame codec support
- session-level path lifecycle and scheduler hooks

It now includes a **local `quic-go` fork** for the first wire-level slice: draft-21 transport-parameter plumbing and native `internal/wire` multipath frame parsing/serialization.

The fork transmits over multiple paths simultaneously using the **draft-21
mechanisms**: per-path connection IDs, per-path packet number spaces, the
path-ID-mixed AEAD nonce (§2.4), and PATH_ACK frames (§4.1). Paths are
identified by connection ID and may share a 4-tuple. This is verified working
end-to-end on hardware — see "Current implementation boundary" below for exactly
what is implemented versus simplified.

## Layout

- `cmd/server` — executable entrypoint
- `internal/server` — server lifecycle and configuration defaults
- `internal/mpquic/session` — connection-level MP-QUIC session orchestration
- `internal/mpquic/path` — path state and per-path packet number space tracking
- `internal/mpquic/cid` — per-path connection ID registry
- `internal/mpquic/transport` — draft transport parameter modeling
- `internal/mpquic/scheduler` — path selection hooks
- `internal/mpquic/frame` — draft frame taxonomy and binary codec
- `internal/conn` — stream lifecycle handling
- `internal/handler` — payload handlers
- `internal/logger` — logging setup
- `pkg/protocols` — exported protocol constants
- `third_party/quic-go` — local fork for wire-level MP-QUIC work
- `config` — runtime configuration and local TLS assets
- `docs` — project documentation
- `tests/integration` — integration-oriented tests

## Draft reference

- `docs/reference/draft-ietf-quic-multipath-21.txt`

## Current implementation boundary

Implemented in-repo:

- frame structs + marshal/unmarshal logic for draft-21 multipath frames
- path open/backup/available/abandon state transitions
- per-path CID issue/retire tracking
- session reactions to `MAX_PATH_ID`, `PATH_NEW_CONNECTION_ID`, `PATH_RETIRE_CONNECTION_ID`, `PATH_STATUS_*`, `PATH_ABANDON`, and blocked frames
- RSSI-aware path selection with deterministic fallback when no RSSI is available

Partially transport-native in the local fork:

- `initial_max_path_id` transport parameter wiring
- native `internal/wire` parsing/serialization for draft multipath frames

Draft-21 per-path multipath (implemented in the fork, verified on hardware):

- **path-ID AEAD nonce** (§2.4): 1-RTT packets on a non-zero path are sealed and
  opened with the path ID mixed into the nonce. Verified against the draft's
  official test vector (`internal/handshake/multipath_nonce_test.go`); path 0 is
  byte-identical to standard QUIC.
- **per-path packet number spaces** (§2.4): each path has its own send/receive
  packet number space (`PathHandler.SentPH`/`RecvPH`); a packet's path is
  resolved from its Destination Connection ID.
- **per-path connection IDs** (§3.1, §4.4): each endpoint issues a source
  connection ID per negotiated path via `PATH_NEW_CONNECTION_ID` after the
  handshake. The DCID identifies the path on receipt (`PathForConnID`) and the
  peer's per-path CID is used as the DCID when sending (`GetForPath`).
- **PATH_ACK frames** (§4.1): per-path acknowledgements, generated from each
  path's receive handler and bundled into 1-RTT packets (non-ack-eliciting,
  never retransmitted); the path-0 ACK is suppressed on non-zero paths.
- additional client paths via `Connection.AddPath`; client-side `PathSelector`
  (e.g. round-robin) distributes packets across paths; paths may share a 4-tuple
  and be distinguished purely by connection ID (§5.2).
- **independent per-path loss recovery & congestion control** (§5.3/§5.4/§5.6/§5.7):
  each path has its own RTT estimator, congestion controller, and loss-detection
  timer (driven from the run loop); `sendOnPath` respects the path's own
  congestion window.

Verified end-to-end on hardware: a Jetson client streams depth+RGB over two
paths (same 4-tuple, distinguished by DCID) to the server, which decrypts path-1
packets with `OpenForPath` and returns `PATH_ACK[PathID=1]`, with no decryption
failures and no stall. Bidirectional `PATH_NEW_CONNECTION_ID` exchange confirmed.

Still missing / simplified in the fork:

- server-initiated per-path **data** transmission is not exercised by the
  current app (the server only returns PATH_ACK on path 0); the capability is
  present once the peer has issued a per-path CID
- a single-PN-space fallback path (`returnPaths`, `IP_PKTINFO` source-address
  control) remains for the distinct-local-address scenario and does not conflict
  with the per-path-CID model
- full path lifecycle enforcement (PATH_ABANDON / PATH_STATUS state machine in
  the transport)

RSSI input boundary right now:

- RSSI is modeled as externally injected path metadata.
- The server now includes an SSH-based RSSI provider that loads `/home/ryzen395/mpquic/.env`.
- It connects to the configured edge host and runs a wireless RSSI command over SSH.
- The fetched RSSI is currently applied to the initial path (`path 0`) and refreshed periodically.
- The scheduler prefers the highest RSSI (higher / less negative dBm wins).
- If no schedulable path has RSSI, selection falls back to active-before-available and then lowest path ID.

## Build

```bash
./scripts/build.sh
make build test
task build
```

## Common commands

```bash
make build          # build ./bin/server
make test           # run Go tests
make run            # build and run with default config
make clean          # remove ./bin

task build          # same as make build
task test           # same as make test
task run            # same as make run
```
