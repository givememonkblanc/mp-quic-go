# QUIC Server - Go Project Structure

This follows standard Go project organization with modular, testable components.

```
.
├── go.mod              # Go module definition
├── go.sum              # Dependency checksums
├── Makefile            # Common development commands
├── Taskfile.yml        # Task runner commands
├── README.md           # Project overview
├── cmd/                # Command-line applications
│   └── server/         # Main server application
│       └── main.go
├── bin/                # Local build outputs (gitignored)
│   └── server
├── third_party/        # Local dependency forks
│   └── quic-go/        # Forked quic-go for wire-level MP-QUIC work
├── internal/           # Private application code (not importable by others)
│   ├── mpquic/         # Draft-21-oriented multipath architecture
│   │   ├── session/    # Connection/session orchestration
│   │   │   └── session.go
│   │   ├── path/       # Path state and lifecycle management
│   │   │   ├── path.go
│   │   │   ├── manager.go
│   │   │   └── manager_test.go
│   │   ├── cid/        # Per-path connection ID tracking
│   │   │   └── registry.go
│   │   ├── transport/  # Transport parameter modeling
│   │   │   ├── parameters.go
│   │   │   └── parameters_test.go
│   │   ├── scheduler/  # Path scheduler hooks
│   │   │   └── scheduler.go
│   │   └── frame/      # Draft frame taxonomy
│   │       ├── types.go
│   │       ├── codec.go
│   │       ├── varint.go
│   │       └── codec_test.go
│   ├── server/         # QUIC server implementation
│   │   ├── server.go
│   │   └── config.go
│   ├── handler/        # Request/response handlers
│   │   ├── handler.go
│   │   └── echo.go     # Example handler
│   ├── conn/           # QUIC connection handling
│   │   └── stream.go
│   └── logger/         # Logging infrastructure
│       └── logger.go
├── pkg/                # Public library code (can be imported by others)
│   └── protocols/      # Protocol implementations
│       └── quic.go
├── config/             # Configuration files
│   ├── config.yaml     # Default config
│   ├── config.example.yaml
│   └── tls/            # Local development TLS assets
│       ├── cert.pem
│       └── key.pem
├── scripts/            # Build/deployment scripts
│   └── build.sh
├── tests/              # Integration/test files
│   └── integration/
│       └── server_test.go
└── docs/               # Documentation
    ├── api.md
    └── reference/
        └── draft-ietf-quic-multipath-21.txt
```

## Key Design Principles

1. **internal/ vs pkg/**
   - `internal/` - Private implementation details, cannot be imported by other projects
   - `pkg/` - Public API packages, can be imported by external projects

2. **cmd/ separate**
   - Each executable gets its own subdirectory
   - Keeps main.go files clean and focused

3. **Separation of concerns**
   - `server/` - QUIC listener lifecycle and process bootstrap
   - `mpquic/session/` - Per-connection orchestration boundary
   - `mpquic/path/` - Path state, path lifecycle, packet-number-space modeling
   - `mpquic/cid/` - Per-path connection ID inventory
   - `mpquic/transport/` - draft transport parameters such as `initial_max_path_id`
   - `mpquic/scheduler/` - path selection policy hooks
   - `mpquic/frame/` - draft-defined frame vocabulary and binary codec
   - `conn/` - Stream I/O handling
   - `handler/` - Application payload handling
   - `logger/` - Logging infrastructure

4. **Configuration externalized**
   - YAML config files in `config/`
   - Easy to modify without recompiling

5. **Test isolation**
   - Integration tests in `tests/`
   - Separate from unit tests in `internal/` packages

## Getting Started

```bash
# Install dependencies
go mod tidy

# Build the server
go build -o ./bin/server ./cmd/server

# Or via task runners
make build
task build

# Run with config
./bin/server --config config/config.yaml
```

## Dependencies (typical for QUIC Go)

```go
require (
    github.com/quic-go/quic-go v0.48.0
)
```
