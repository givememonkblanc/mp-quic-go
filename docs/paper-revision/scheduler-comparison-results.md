# Scheduler Comparison — Preliminary Results

Produced by `scripts/compare-schedulers.sh`. Everything except the `--scheduler`
flag is held constant (same client, server, paths, fps, duration), so the
comparison isolates the scheduler. Addresses reviewer comments R1.10, R3.4
(fair, same-stack comparison) and R3.8 (mean / variance reporting).

## Run 1 — steady state, clean LAN (no path impairment)

- Setup: Jetson client → server, two paths sharing the 4-tuple (distinguished by
  connection ID), 5 fps, 18 s per run, 3 repetitions per scheduler.
- Metric: frames delivered (max `depth sent #N`) — a sustained-throughput proxy.

| scheduler   | mean | stddev | min | errors |
| ----------- | ---- | ------ | --- | ------ |
| pqi         | 43.0 | 1.0    | 42  | 0      |
| min-rtt     | 44.0 | 0.0    | 44  | 0      |
| round-robin | 42.7 | 1.5    | 41  | 0      |

### Interpretation

- With **no path degradation**, all three schedulers deliver essentially the
  same throughput (≈ 42–44 frames / 18 s) with **zero errors**. This is the
  expected, honest steady-state result: when both paths are healthy, the choice
  of path-selection policy barely matters.
- `min-rtt` is marginally highest with the lowest variance; `pqi` is within noise
  of it; `round-robin` is slightly lower and more variable (consistent with it
  spreading load onto a path that is not always the best).
- Importantly, **PQI does not reduce steady-state throughput** versus the
  baselines — relevant to R3.9 (the manuscript's claim that MP-QUIC has lower
  normal-state throughput is not reproduced here when only the scheduler varies).

### What this does *not* yet show

The PQI scheduler's benefit is at **handover**, which a clean LAN does not
exercise. The differentiating experiment degrades one path mid-run (e.g.
`tc qdisc add dev <wifi> root netem delay 200ms loss 10%`, or bring Wi-Fi down)
and measures outage duration / frames lost during the transition. That scenario
(and an SP-QUIC-with-connection-migration baseline) is the next evaluation step;
the harness is structured to drop an impairment command between runs.
