# AMR performance-validation runbook

Copy-paste commands and expected outcomes for validating the RSSI-aware MP-QUIC
scheduler on the AMR. Read `README.md` for the design/metric definitions.

## 0. One-time prerequisites

**Server host (ryzen):**
```bash
go build -o bin/server ./cmd/server
go test ./internal/mpquic/scheduler/ ./internal/rssi/        # must pass
```

**AMR (Jetson) — build the client natively (CGO/OpenCV):**
```bash
export PATH=/home/jetson/gopath/go1.22/bin:$PATH
cd /home/jetson/mp-quic-go && make jetson
./bin/jetson --help | grep -E 'rssi-aware|rssi-collect|backup-probe'   # 3+ lines
```

**Set the backup path in `experiments/config.sh`:**
```bash
SECONDARY_IFACE=<your 5G/cellular iface>   # e.g. enxXXXX (USB tether) / wwan0 / ppp0
                                           # eno1 (wired) is only a bring-up stand-in
CROSS_TARGET="<iperf3 host -p 5201>"       # for scenario 5 only
```

Tools on the AMR: `iw tc nmcli iperf3 python3 sudo` (verify with the checklist).

## 1. Sanity smoke (≈1 min) — confirm the pipeline end-to-end

```bash
RUN_SECONDS=30 REPEATS=1 ./experiments/run_on_orin.sh -g g5_mp_rssi_aware -s 1_normal -n 1
cat experiments/results/_orin/g5_mp_rssi_aware/1_normal/aggregate.txt
```
Expect: `num_path_switches 0`, `ping_pong_count 0`, non-zero `frames_delivered`,
client log shows `Using path scheduler: rssi-aware` + `RSSI collector started`.

## 2. Full evaluation (all groups × scenarios × N)

> Use `run_on_orin.sh` (runs the matrix ON the AMR, immune to control-SSH flaps).
> `run.sh` is the equivalent host-driven variant for a stable LAN.

```bash
# full: 5 groups x 5 scenarios x 10 runs  (~3 h; tune REPEATS/RUN_SECONDS)
REPEATS=10 RUN_SECONDS=40 ./experiments/run_on_orin.sh

# a single cell, 10 runs
./experiments/run_on_orin.sh -g g5_mp_rssi_aware -s 3_sudden -n 10

# one scenario across all groups
./experiments/run_on_orin.sh -s 2_gradual -n 10
```
Results: `experiments/results/_orin/<group>/<scenario>/aggregate.txt` (and `.json`)
with **mean ± std, 95% CI, N**. Per-run raw logs under `run<r>/`.

## 3. What each cell should show (acceptance criteria)

| scenario | proposed g5 expected behaviour |
|---|---|
| 1 normal | **0 path switches**, throughput ≈ Wi-Fi baseline (no needless 5G use) |
| 2 gradual | switches to 5G **before** link failure (DEGRADED + down-hold); lower `service_interruption` than g4 |
| 3 sudden | fails over to 5G immediately; **smallest `service_interruption` / `path_switching_delay`** of all groups; g1 (Wi-Fi-only) drops the stream, g2 (migrate) shows a large interruption |
| 4 recovery | returns to Wi-Fi only after the recovery hold; **`ping_pong_count` ≈ 0** vs a no-hysteresis baseline |
| 5 crosstraffic | maintains delivery under background load; `CROSS_PROTO/CROSS_RATE` logged in events |

| group | role in the comparison |
|---|---|
| g1_sp_wifi | single-link floor (worst under Wi-Fi loss) |
| g2_sp_migrate | RFC-9000-style migrate-on-failure (shows the interruption multipath avoids) |
| g3_mp_default | round-robin MP-QUIC (uses both paths; no quality awareness) |
| g4_mp_failure | failure-based backup (switches only after the path dies, not on degradation) |
| **g5_mp_rssi_aware** | **proposed** — switches on RSSI degradation with hysteresis |

The 4 baselines isolate the gain: g5 vs g1 = "multipath+policy"; g5 vs g3/g4 =
"the RSSI-aware policy specifically, holding multipath constant".

## 4. Reading multipath vs migration

- **Multipath active** (g3/g4/g5): the server receives from **two NAT source
  ports** (Wi-Fi vs 5G); `num_path_switches`/`SCHED path_switch` records moves.
- **Migration** (g2): single path; on failure the runner relaunches on 5G and
  logs `migrate_begin`/`migrate_done` in `events.log` — the gap between them is
  the migration service interruption.

## 5. Quick capability checks (no full matrix)

```bash
# multipath carries both paths (round-robin):
./experiments/run_on_orin.sh -g g3_mp_default -s 1_normal -n 1
#   -> server log: two distinct source ports from the AMR's NAT IP

# RSSI-aware switches on degradation:
./experiments/run_on_orin.sh -g g5_mp_rssi_aware -s 2_gradual -n 1
#   -> client log SCHED path_switch from=0 to=1 reason=degraded

# migration baseline interrupts then recovers on 5G:
./experiments/run_on_orin.sh -g g2_sp_migrate -s 3_sudden -n 1
#   -> events.log: wifi_down ... migrate_begin ... migrate_done
```
