# mp-quic-go

Minimal QUIC server scaffold for experimenting with multipath-oriented transport handling in Go.

This repository now includes a repo-level draft-21 MP-QUIC implementation layer for:

- path state and per-path packet-number-space modeling
- per-path connection ID lifecycle tracking
- draft transport parameter modeling
- draft `PATH_*` / `MAX_PATH_ID` / blocked-frame codec support
- session-level path lifecycle and scheduler hooks

It now includes a **local `quic-go` fork** for the first wire-level slice: draft-21 transport-parameter plumbing and native `internal/wire` multipath frame parsing/serialization.

It is still **not** a complete wire-compatible full stack until packet protection, per-path packet number spaces in transport internals, and actual multi-path packet transmission are implemented in the fork.

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

Still missing in the fork:

- modified AEAD nonce calculation with path ID
- per-path packet number spaces in the real transport pipeline
- native connection/path lifecycle enforcement across multiple paths
- real per-path packet transmission across multiple active paths

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
