# Deploying to the AMR

Step-by-step to bring the RSSI-aware MP-QUIC system up on the AMR (Jetson) for
real performance validation. Includes the gotchas found during bring-up.

## 0. Topology
- **Server** runs on a host with a **public/edge IP** reachable from BOTH the
  AMR's Wi-Fi and 5G (e.g. `165.229.169.120:4433`). NOT the AMR.
- **Client** (the multipath sender + camera) runs **on the AMR**.
- The experiment **data path** (AMR → server over Wi-Fi/5G) is independent of any
  SSH/tailscale control link.

## 1. Get the code (both machines)
```bash
git clone <repo> mp-quic-go && cd mp-quic-go
git checkout rssi-aware-mpquic        # branch with all fixes (PR #2)
# or on an existing checkout:  git fetch && git checkout rssi-aware-mpquic && git pull
```
The vendored fork lives in `third_party/quic-go` (a `replace` in go.mod) — keep it.

## 2. Build
**Server host:**
```bash
go build -o bin/server ./cmd/server
go test ./internal/mpquic/scheduler/ ./internal/rssi/ ./internal/handler/   # must pass
```
**AMR (native build — CGO + OpenCV for the camera):**
```bash
export PATH=/home/jetson/gopath/go1.22/bin:$PATH      # go is NOT on the default PATH
cd ~/mp-quic-go && make jetson
./bin/jetson --help | grep -E 'rssi-aware|rssi-collect|backup-probe'
```
> Both the camera fix (`CAP_PROP_BUFFERSIZE=1`) and the server frame-cleanup fix
> are in this branch — rebuild both binaries so they take effect.

## 3. Network on the AMR
- **Primary Wi-Fi** up and associated; `iw dev <wifi> link` shows a `signal:` line.
- **5G/cellular** up. Find its iface: `ip -br addr | grep -E 'enx|wwan|ppp|172.20'`.
  > The USB-tether name (`enx<mac>`) **changes on every reconnect** — re-check it
  > each session and update config.
- Both reach the server: `ping -I <iface> <server-ip>` for each.

## 4. Configure `experiments/config.sh`
```bash
SERVER_ADDR=<edge-ip>:4433
PRIMARY_IFACE=<wifi iface>          # e.g. wlP1p1s0
SECONDARY_IFACE=<5G iface>          # the real cellular iface (NOT a wired stand-in)
# if driving the AMR remotely over ssh:
JETSON_SSH=<amr ip/tailscale> ; JETSON_USER=<user> ; JETSON_PW=<pw>
JETSON_BIN=<path to bin/jetson on the AMR>
CROSS_TARGET="<iperf3 host -p 5201>"   # scenario 5 only
```

## 5. Smoke test (1 min)
```bash
RUN_SECONDS=30 REPEATS=1 ./experiments/run_on_orin.sh -g g5_mp_rssi_aware -s 1_normal -n 1
cat experiments/results/_orin/g5_mp_rssi_aware/1_normal/aggregate.txt
```
Expect `num_path_switches 0`, non-zero `frames_delivered`, client log shows
`Using path scheduler: rssi-aware` + `RSSI collector started`.

## 6. Run the evaluation
```bash
REPEATS=10 RUN_SECONDS=40 ./experiments/run_on_orin.sh        # full 5x5x10
```
See `RUNBOOK.md` for per-cell commands and acceptance criteria, and
`REVIEWER_COMPLIANCE.md` for the feedback mapping.

## Gotchas learned during bring-up
- **Run orchestration ON the AMR** (`run_on_orin.sh`), not host-driven, if the
  control link (tailscale) flaps. The AP's client-isolation prevents a stable
  direct path; a wired host↔AMR link or disabling AP isolation removes the flaps.
  For remote ssh, the harness uses a persistent ControlMaster (see common.sh).
- **`go` not on PATH** on the AMR → `export PATH=…/go1.22/bin:$PATH` before `make`.
- **5G iface renames** on reconnect → update `SECONDARY_IFACE`.
- **Real mobility uses real RSSI** (`--rssi-collect`, on by default for g5). The
  scripted-RSSI file (`MPQUIC_RSSI_FILE`) is only for deterministic lab replay of
  scenarios 2/4.
- **Sudden-failure scenario** uses `tc netem loss 100%` (not interface down) so it
  doesn't disrupt routing/control or hang reconnect.
- **Camera**: needs `/dev/video0` (depth) + `/dev/video4` (RGB); ~8 s init per run.
