# API

## Transport

- Protocol: QUIC
- ALPN: `mp-quic`

## Stream behavior

The current server accepts bidirectional QUIC streams and applies the default echo handler:

1. Read a single payload from the stream.
2. Pass the payload to the internal handler.
3. Write the handler response back to the same stream.

This is scaffolding for future multipath-specific protocol handling.

## MP-QUIC architecture scaffolding

- Session orchestration: `internal/mpquic/session`
- Path state and lifecycle: `internal/mpquic/path`
- Per-path connection IDs: `internal/mpquic/cid`
- Draft transport parameters: `internal/mpquic/transport`
- Scheduling hooks: `internal/mpquic/scheduler`
- Draft frame taxonomy: `internal/mpquic/frame`

Current scope includes repo-level draft-21 frame parsing/encoding, path lifecycle state transitions, per-path CID tracking, and session-level frame handling hooks.

Current limitation: these semantics are modeled above `quic-go`, not yet injected into `quic-go`'s native QUIC transport internals. That means draft-native wire negotiation and packet protection changes are still outside the current implementation boundary.

## Local development assets

- Default config: `config/config.yaml`
- Example config: `config/config.example.yaml`
- Local TLS assets: `config/tls/`
