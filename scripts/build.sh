#!/usr/bin/env bash

set -euo pipefail

GO_BIN="${GO:-go}"

mkdir -p ./bin
"${GO_BIN}" build -o ./bin/server ./cmd/server
"${GO_BIN}" test ./...
