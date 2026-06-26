# RSSI-aware MP-QUIC — experiment harness

Reproducible evaluation of the proposed RSSI-aware MP-QUIC path scheduler against
baselines, across five AMR mobility scenarios, reporting the reviewer-requested
metrics as **mean ± std with 95% confidence intervals over N runs**.

## System under test

The proposed scheduler is implemented in the codebase (not in these scripts):

| Component | Where | Notes |
|---|---|---|
| RSSI collector (raw + EWMA + invalid handling) | `internal/rssi/collector.go` | local `iw dev <iface> link`; or a scripted RSSI file (`MPQUIC_RSSI_FILE`) for reproducible scenarios |
| Wi-Fi state machine + hysteresis + dwell | `internal/mpquic/scheduler/rssi_aware.go` | states GOOD/WARNING/DEGRADED/FAILED/RECOVERING |
| Rule-based RSSI-aware scheduler | same | `--scheduler rssi-aware` |
| Backup (5G) path probing | `third_party/quic-go` `Config.BackupProbeInterval` | keeps the standby validated/warm even idle |
| Fast fail-over (reinject + standby send) | `third_party/quic-go/connection.go` | the earlier liveness/failover fix |

### Parameters (defaults; reproducible)

```
State machine:  GOOD >= -60 dBm | WARNING -70..-60 | DEGRADED -80..-70 | FAILED < -80 or validation-fail
Hysteresis:     Wi-Fi -> 5G : EWMA < -72 dBm sustained >= 500 ms
                5G -> Wi-Fi : EWMA > -65 dBm sustained >= 2000 ms
                min dwell   : 1000 ms after any switch
RSSI:           EWMA alpha 0.3, sampled every 250 ms
Backup probe:   1000 ms PING on the 5G path
```

Unit tests cover the state machine and hysteresis (no hardware needed):
`go test ./internal/mpquic/scheduler/ -run RSSIAware` and `go test ./internal/rssi/`.

## Comparison groups (config.sh `GROUPS`)

To separate "gains from adding 5G" from "gains from the RSSI-aware policy":

| id | description | how |
|---|---|---|
| `g1_sp_wifi` | SP-QUIC, Wi-Fi only | single path, no `--path1` |
| `g2_sp_migrate` | SP-QUIC, migrate to 5G on Wi-Fi failure | single path; harness relaunches on 5G when it stalls (app-level migration) |
| `g3_mp_default` | MP-QUIC default scheduler | `--scheduler round-robin` |
| `g4_mp_failure` | MP-QUIC failure-based backup | `--scheduler rssi` (primary until PTO-liveness failure) |
| `g5_mp_rssi_aware` | **proposed** RSSI-aware MP-QUIC | `--scheduler rssi-aware --rssi-collect --backup-probe 1s` |

## Scenarios (config.sh `SCENARIOS`)

| id | condition | how it is induced |
|---|---|---|
| `1_normal` | stable Wi-Fi | no impairment; verify g5 does **not** switch to 5G |
| `2_gradual` | coverage-edge degradation | RSSI ramps `-50 -> -85` (scripted file) **and** `tc netem` loss/delay ramp on Wi-Fi |
| `3_sudden` | Wi-Fi failure | `nmcli device disconnect` Wi-Fi at `FAIL_AT`, restore at `RESTORE_AT` |
| `4_recovery` | Wi-Fi returns | same down/up; metrics focus on the hysteresis-gated return |
| `5_crosstraffic` | background load | `iperf3` on a path — **type & rate logged** (`CROSS_PROTO`/`CROSS_RATE`) |

Gradual degradation is driven by a **scripted RSSI file** so the scheduler sees a
deterministic, reproducible signal; `tc netem` applies the matching physical
impairment so *all* groups experience the same scenario (fair comparison).

## Metrics (lib/metrics.py)

Per run, written as JSON; aggregated to mean ± std ± 95% CI over N runs.

| metric | definition |
|---|---|
| `service_interruption_ms` | largest inter-frame delivery gap |
| `path_switching_delay_ms` | trigger event → first frame after the stall |
| `frame_latency_ms_mean/_p95` | per-frame send→ack latency |
| `frame_loss_rate` | `1 − delivered / (fps·duration·2 streams)` |
| `packet_loss_transition_proxy` | frame-level loss in a 2 s window after the trigger (proxy; true packet loss needs qlog) |
| `throughput_mbps` | delivered bytes·8 / span |
| `jitter_ms` | stddev of inter-arrival gaps |
| `recovery_time_ms` | `wifi_up` → switch back to Wi-Fi (path 0) |
| `num_path_switches` | scheduler switches (rssi-aware SCHED log) or fail-over count |
| `ping_pong_count` | reverse switches (A→B→A) |
| `cpu_pct_*`, `mem_mb_*` | client (and server) process overhead |

Telemetry the metrics rely on is emitted by the client when run with
`MPQUIC_SCHED_LOG=1` (path switches/state) and `MPQUIC_FRAME_LOG=1` (per-frame
records) — the harness sets both.

## Running

Prerequisites on the **AMR/Jetson**: `iw`, `tc` (iproute2), `nmcli`, `iperf3`
(scenario 5), sudo, and the built `bin/jetson`. On the **server host**: built
`bin/server`, `sshpass`, `python3`.

```bash
go build -o bin/server ./cmd/server          # server host
# build bin/jetson natively on the Jetson (CGO/OpenCV): make jetson

# edit experiments/config.sh: SECONDARY_IFACE (your 5G iface), SERVER_ADDR,
# JETSON_SSH, and CROSS_TARGET (an iperf3 server) for scenario 5.

./experiments/run.sh                          # all groups x scenarios x REPEATS
./experiments/run.sh -g g5_mp_rssi_aware -s 3_sudden -n 10   # one cell, 10 runs
```

Results: `experiments/results/<group>/<scenario>/run<r>/metrics.json` and a
per-cell `aggregate.txt` / `aggregate.json`.

## Caveats / honesty notes

- **5G interface name varies** (`enx<mac>`, renames on reconnect); set
  `SECONDARY_IFACE` per session. `eno1` (wired) is a stable stand-in for bring-up.
- **SSH stability**: tailscale can flap; the harness retries every step. A direct
  LAN IP is better if the AP does not isolate clients.
- **Migration baseline (g2)** is app-level reconnect-on-failure, not seamless
  RFC 9000 0-RTT migration; it intentionally exposes the interruption that
  multipath avoids.
- **`packet_loss_transition_proxy`** is a frame-level proxy; wire `QLOGDIR` and a
  qlog parser for true QUIC packet-loss accounting.
- Workload is ack-synchronous frame streaming → latency-bound; multipath's value
  here is **resilience**, not raw throughput aggregation (see project notes).
