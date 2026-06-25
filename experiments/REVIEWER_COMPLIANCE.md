# Reviewer feedback → how it is satisfied

| # | Reviewer point | How it is addressed | Where / evidence |
|---|---|---|---|
| 1 | **Don't invent a new abstract metric** (PQI/cost); use a simple, explainable scheduler | Rule-based RSSI-aware policy: `GOOD/WARNING→Wi-Fi`, `DEGRADED+hold→5G`, `FAILED→immediate 5G`, `RECOVERING+hold→Wi-Fi`. No learned/abstract index. | `internal/mpquic/scheduler/rssi_aware.go` |
| 2 | **Clarify parameters / reproducibility** | All thresholds/timers are named constants with documented defaults; scenarios driven by a **scripted RSSI file** for deterministic replay. | `DefaultRSSIAwareConfig`, `README.md` params table, `MPQUIC_RSSI_FILE` |
| 3 | **RSSI Collector** (raw + EWMA + invalid) | Local `iw dev link` reader, EWMA smoothing, invalid reads hold (don't poison) the average. | `internal/rssi/collector.go` (+ tests) |
| 4 | **Wi-Fi State Machine** (named states, not raw dBm) | 5 states from EWMA RSSI + path validation. | `WiFiState` in `rssi_aware.go` |
| 5 | **Hysteresis / dwell** (avoid ping-pong) | Wi-Fi→5G: EWMA < −72 dBm for ≥500 ms; 5G→Wi-Fi: EWMA > −65 dBm for ≥2000 ms; 1000 ms min dwell. Unit-tested incl. ping-pong prevention. | `rssi_aware_test.go` (`TestRSSIAware_MinDwell…`) |
| 6 | **Path probing** (keep 5G backup validated even idle) | Periodic ack-eliciting PING on the standby path keeps it validated and its RTT fresh. | `Config.BackupProbeInterval`, `connection.go` run loop |
| 7 | **Fair comparison groups** — separate "added 5G" from "the policy" | 5 groups: SP-Wi-Fi, SP-migrate-to-5G, MP-default(RR), MP-failure-based, MP-RSSI-aware. g5 vs g3/g4 isolates the policy with multipath held constant. | `config.sh GROUPS`, `RUNBOOK.md §3` |
| 8 | **SP-QUIC migration baseline** (vs RFC 9000) | g2 single path migrates to 5G on failure (app-level reconnect), exposing the interruption multipath avoids; logged via `migrate_begin/done`. | `orin_runner.sh` migrate branch |
| 9 | **Metrics beyond throughput** (12 metrics) | interruption, switch delay, frame latency, frame loss, packet-loss(proxy), throughput, jitter, recovery, #switches, ping-pong, CPU, mem. | `lib/metrics.py` |
| 10 | **mean ± std, 95% CI, N runs** | Aggregation reports mean, std, 95% CI (t-distribution), min/max, n. | `lib/aggregate.py` |
| 11 | **Five scenarios** (normal/gradual/sudden/recovery/cross-traffic) | Each induced with documented impairments (RSSI ramp + `tc netem`, `nmcli` down/up, `iperf3`); cross-traffic type & rate recorded. | `orin_runner.sh`, `config.sh` |

## Notes / honest caveats (state these in the paper)
- `packet_loss_transition_proxy` is a frame-level proxy; for QUIC packet-level
  loss, enable `QLOGDIR` and parse the qlog.
- The migration baseline (g2) is app-level reconnect, not seamless 0-RTT RFC 9000
  migration — intentionally conservative (it shows the worst-case interruption).
- The workload is ack-synchronous frame streaming → latency-bound; multipath's
  measured benefit here is **resilience** (interruption/recovery), not bandwidth
  aggregation. Report it as such.
