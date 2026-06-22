# Quick Start Guide

## Build binaries
```bash
make build      # server  -> ./bin/server  (native, this host)
make jetson     # client  -> ./bin/jetson  (native, this host)
```

> Note: `make jetson` builds a **native** binary on the current host (good for
> local loopback testing via `test_jetson.sh`). The Jetson client uses CGO +
> OpenCV (`internal/camera/provider_opencv.go`), so it is **not** cross-compiled
> from this AMD64 host — build it on the Jetson device itself (see below).

## Run server
```bash
./bin/server --config ./config/config.yaml
```

## Build & run client on the Jetson (ARM64)
The repo is checked out on the device at `/home/jetson/mp-quic-go`. Build there
so the OpenCV/CGO camera provider links against the device's libraries.

```bash
# Sync source to the Jetson (if changed)
rsync -az --exclude bin --exclude .git ./ jetson@192.168.0.13:/home/jetson/mp-quic-go/

# Build natively on the Jetson and run
ssh jetson@192.168.0.13 \
  "cd /home/jetson/mp-quic-go && make jetson && \
   ./bin/jetson --addr 192.168.0.80:4433 --fps 5"
```

## Local loopback test (server + client on this host)
```bash
./test_jetson.sh   # builds nothing; expects ./bin/server and ./bin/jetson present
```

## Debug
```bash
# Server logs
tail -f /tmp/quic-server.log

# Jetson client logs
ssh jetson@192.168.0.13 "journalctl -u jetson-client -f"
```
