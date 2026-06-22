# Reviewer Response & Revision Plan

Manuscript: *MP-QUIC for Seamless Handover of Autonomous Mobile Robots in
Heterogeneous Networks* (COMCOM-D-26-01559, rejected by Computer Communications;
revise for a new venue — Computer Communications will not re-consider).

This document tracks **every** reviewer comment, the action taken, its type
(**Code** = repository/system change, **Paper** = manuscript text/figures,
**Eval** = experiment/measurement), and status.

---

## 0. Root-cause finding (read first)

Several of the strongest technical criticisms (R1: "PQI not defined",
"normalization not described", "unclear what in Fig. 3 is implemented"; R3-1/3/5:
"what is novel", "parameters not specified", "which implementation / where is the
scheduler") share one root cause:

> **The PQI scheduler described in the paper was not actually implemented in the
> system.** Before this revision the repository only contained an **RSSI-based**
> `PrimaryPathScheduler` (`internal/mpquic/scheduler/scheduler.go`, name
> `"rssi-primary-path"`). The `internal/mpquic/pqi` package held only placeholder
> tests, `path.State` carried **only RSSI** (no RTT / loss / bandwidth), and there
> was no cost function, normalization, EWMA, trend detection, or hysteresis.

The revision therefore **implements the PQI subsystem for real** so the system
matches the paper, and the paper is rewritten to describe exactly that
implementation. This simultaneously resolves the "reproducibility / parameters"
and "novelty / definition" comments.

### PQI formalization (now both implemented and to be put in the paper)

Per active path *i*, raw metrics: smoothed RTT `r_i`, loss rate `l_i ∈ [0,1]`,
estimated goodput `b_i`, link RSSI `s_i` (dBm).

Min–max normalization across the current set of schedulable paths P (so the cost
is scale-free; |P| ≥ 1, guard against max==min):

```
r̃_i = (r_i − r_min) / (r_max − r_min)      # 0 = best (lowest RTT)
l̃_i = (l_i − l_min) / (l_max − l_min)      # 0 = best (lowest loss)
b̃_i = (b_i − b_min) / (b_max − b_min)      # 1 = best (highest bw)
```

Cost (lower is better), weights α+β+γ = 1:

```
C_i = α·r̃_i + β·l̃_i + γ·(1 − b̃_i)
```

Path Quality Index (higher is better), reported on 0–100:

```
PQI_i = 100 · (1 − C_i)
```

EWMA temporal smoothing (factor λ), evaluated each sampling tick:

```
PQI_i(t) = λ·PQI_raw_i(t) + (1−λ)·PQI_i(t−1)
```

Trend detection: slope of PQI over a sliding window of W samples (negative slope
beyond a threshold = degrading).

Hysteresis handover from the active path *a* to best candidate *c*:

```
switch iff  PQI_a < T_deg                     # active path degraded
       and  PQI_c − PQI_a > Δ_margin          # candidate clearly better (safety margin)
       and  the above held for ≥ T_stable      # stability interval (anti-flapping)
```

Default parameters (configurable in `config.yaml`, reported in the paper):

| Symbol | Meaning | Default |
| --- | --- | --- |
| α, β, γ | RTT / loss / bandwidth weights | 0.5, 0.3, 0.2 |
| λ | EWMA smoothing factor | 0.3 |
| W | sliding-window size (samples) | 10 |
| T_deg | degradation threshold (PQI) | 40 |
| Δ_margin | handover safety margin (PQI) | 10 |
| T_stable | stability interval | 500 ms |

---

## 1. Reviewer #1

| # | Comment | Action | Type | Status |
| --- | --- | --- | --- | --- |
| 1.1 | §2.2 does not say MP-QUIC is a draft (not a standard), v21 Mar 2026 | State explicitly it is an IETF draft (draft-ietf-quic-multipath-21, Mar 2026), summarize the 2017→ standardization history | Paper | TODO |
| 1.2 | "standard MP scheduler fails" — but scheduler is implementation-specific (draft §5.5) | Reword: the *default/reference* scheduler is implementation-specific; we propose a specific scheduler; cite §5.5 | Paper | TODO |
| 1.3 | Missing related work on MP-QUIC schedulers | Add related-work subsection surveying MP-QUIC schedulers (ECF, BLEST, minRTT, Peekaboo, reinforcement-learning schedulers, etc.) | Paper | TODO |
| 1.4 | Present QUIC connection migration (RFC 9000 §9): path probing, similar scope | Add a paragraph on connection migration / path validation and how it differs (no simultaneous multipath sensing) | Paper | TODO |
| 1.5 | Fig. 3 components must be elaborated; which are technically implemented? | Implement the PQI subsystem so the figure maps 1:1 to code modules; annotate Fig. 3 with the implementing package | Code + Paper | IN PROGRESS |
| 1.6 | Min–max normalization not mathematically described | Defined above (§0); implemented in `pqi` package | Code + Paper | IN PROGRESS |
| 1.7 | PQI not explicitly defined | Defined above (§0, `PQI = 100·(1−C)`); implemented | Code + Paper | IN PROGRESS |
| 1.8 | Path probing strategy undefined (how are per-path metrics obtained?) | Document probing: per-path RTT from the QUIC sent-packet handler (now per-path), loss from per-path ACK/PATH_ACK, goodput from delivered bytes/Δt, RSSI via the SSH `iw dev link` provider sampled every interval | Code + Paper | IN PROGRESS |
| 1.9 | Video framing: how does the transport layer know frame boundaries? | Document the application framing: each frame is length-delimited on its own stream with a 1-byte `StreamType` tag (`[StreamType][FrameData]`, depth=0x00 / RGB=0x01); QUIC streams preserve boundaries via explicit length prefixes (`internal/handler`) | Code + Paper | TODO (text) |
| 1.10 | Evaluation lacks comparison with default scheduler and SP-QUIC + connection migration; SP-QUIC in Fig. 4 unfairly cannot use 5G | Add baselines: (a) default/round-robin MP-QUIC scheduler, (b) SP-QUIC **with** RFC 9000 connection migration to 5G; ensure SP-QUIC can fail over to 5G for fairness | Code + Eval | TODO |

## 2. Reviewer #2

| # | Comment | Action | Type | Status |
| --- | --- | --- | --- | --- |
| 2.1 | §2.1: replace "question" with "matter" | Edit | Paper | TODO |
| 2.2 | §2.2: don't frame QUIC as a TCP "competitor"/strictly better | Reword to balanced trade-offs | Paper | TODO |
| 2.3 | §2.2: with MP-QUIC both links are active → strictly not "handover"; clarify soft-handover vs compensating Wi-Fi's no-handover | Define terminology: we use MP-QUIC for *soft handover* / make-before-break; clarify the problem statement (Wi-Fi↔5G transition, not Wi-Fi no-handover) consistently | Paper | TODO |
| 2.4 | §2.2: clarify why 5G packets are "faster", why HoL blocking arises (QUIC avoids HoL intrinsically) | Clarify: per-stream delivery avoids HoL *within* a path; the issue is cross-path scheduling/reordering at handover, not classic TCP HoL | Paper | TODO |
| 2.5 | §3.1: consider 3GPP N3IWF for 5G/Wi-Fi hybrid handover (cite in §1/§2) | Add N3IWF / ATSSS discussion and contrast with the transport-layer approach | Paper | TODO |
| 2.6 | §4.2.2/Fig.5: which application is tested? Is total delay = file-transfer time? | State the workload (video frame stream); define "total transmission delay" precisely | Paper | TODO |
| 2.7 | §4.2.3: define "cross traffic" | Define the contending background traffic (rate, direction, generator) | Paper + Eval | TODO |
| 2.8 | §4.2.3: "no traffic, no delay?"; how many handovers? just one? | Clarify the scenario; report number of handover events per run | Paper + Eval | TODO |
| 2.9 | Are 50/150 Mbps rates realistic for AMR? Required rate is much less | Justify rates (e.g., multi-camera 3D/point-cloud) or add an AMR-realistic rate; discuss | Paper + Eval | TODO |
| 2.10 | Fig. 8: 5.5 s handover is very long | Investigate/repro handover latency with the new PQI hysteresis; report improved value or explain the 5.5 s (Wi-Fi disconnect detection vs. transport reaction) | Code + Eval | TODO |

## 3. Reviewer #3

| # | Comment | Action | Type | Status |
| --- | --- | --- | --- | --- |
| 3.1 | What is novel: metric, trend detection, hysteresis, or MP-QUIC/AMR integration? | State the contribution precisely: PQI is a normalized multi-metric index **plus** EWMA trend + hysteresis handover **integrated into a draft-21 MP-QUIC stack** for AMR Wi-Fi/5G soft handover | Paper | TODO |
| 3.2 | "virtually eliminates service outages" too strong | Soften to measured claim (e.g., "reduces outage duration from X to Y") | Paper | TODO |
| 3.3 | Parameters (α,β,γ, T_deg, Δ_margin, T_stable, λ, W) unspecified → reproducibility | All defined (§0), implemented as config, reported in a table | Code + Paper | IN PROGRESS |
| 3.4 | Comparison does not isolate the PQI benefit; compare with standard MP-QUIC (quic-go, xquic) | Add same-stack baselines (default round-robin / minRTT scheduler in our fork) so only the scheduler differs | Code + Eval | TODO |
| 3.5 | State which MP-QUIC impl, how modified, which draft, where PQI is implemented | Documented: forked `quic-go` (`third_party/quic-go`), draft-ietf-quic-multipath-21; PQI in `internal/mpquic/pqi` + `internal/mpquic/scheduler`; per-path transport (CID/PN/nonce/PATH_ACK) in the fork | Code + Paper | IN PROGRESS |
| 3.6 | Handover real (AMR moving across APs) or emulated (Wi-Fi disconnect)? Give AP layout, robot speed, #runs, 5G type, disconnect duration | Specify the testbed precisely; if emulated, say so and justify | Paper + Eval | TODO |
| 3.7 | §4.2.4: 70 ms "hesitation" after link fails ⇒ reactive failover, not proactive | Reconcile terminology: distinguish proactive (PQI-trend pre-emptive) vs reactive (post-failure) handover; the PQI trend path is proactive, the 70 ms case is the reactive fallback | Code + Paper | TODO |
| 3.8 | Report #repetitions, mean, variance/CI, statistical significance | Re-run with N repetitions; report mean ± CI; significance test | Eval | TODO |
| 3.9 | Why is MP-QUIC normal-state throughput lower than SP-QUIC (Table 2)? | Investigate (scheduler overhead / reordering / dup acks); explain or fix | Code + Eval | TODO |
| 3.10 | Writing/figures: inconsistent "Wi-Fi" spelling; low-res figures; number equations | Standardize "Wi-Fi"; regenerate figures; number all equations | Paper | TODO |

---

## 4. Work breakdown by owner

- **Code (this repo)** — implement PQI subsystem (§0): metrics in `path.State`,
  `pqi` package (normalization/cost/PQI/EWMA/trend/hysteresis), `PQIScheduler`,
  config parameters, real tests; add baseline schedulers (round-robin, minRTT)
  for fair comparison; investigate handover latency (2.10) and normal-state
  throughput (3.9).
- **Paper** — items marked Paper above (terminology, related work, migration /
  N3IWF, contribution framing, parameter table, claims softening, Wi-Fi spelling,
  equation numbering, figures).
- **Eval** — baseline comparisons (1.10, 3.4), N-run statistics with CI (3.8),
  testbed specification (3.6), cross-traffic / rate justification (2.7–2.9).

Status legend: TODO · IN PROGRESS · DONE.

---

## 5. Implementation progress (code)

**DONE — PQI subsystem now implemented and unit-tested**:

- `internal/mpquic/pqi/normalization.go` — `MinMaxNormalize` (formal min-max).
- `internal/mpquic/pqi/estimator.go` — `Metrics`, `Weights`, `ComputePQI`
  (cost `α·RTT~ + β·loss~ + γ·(1−bw~)`, `PQI = 100·(1−C)`), `Estimator` (EWMA +
  sliding-window trend).
- `internal/mpquic/pqi/recovery.go` — `HandoverController` (degradation
  threshold + safety margin + stability interval hysteresis).
- `internal/mpquic/scheduler/pqi_scheduler.go` — `PQIScheduler` implementing the
  `Scheduler` interface (cold-start best, sticky-while-healthy, hysteresis
  handover); configurable `PQIConfig`.
- `internal/mpquic/path/path.go` — `State` gains `RTT`/`LossRate`/`Bandwidth`/
  `HasMetrics`.
- Real tests replace the placeholder stubs; all pass.

This closes the **code** side of R1.6, R1.7, R3.3, R3.5 and the implementable
part of R1.5. The per-path transport already provides the inputs (per-path RTT /
congestion from the fork; loss via PATH_ACK; bandwidth from delivered bytes).

**DONE — PQI is now live (R1.8):**

- `quic.PathState` carries `Validated` + per-path `RTT`/`Bandwidth`/`HasMetrics`;
  `connection.pathStates()` fills them from each path's RTT estimator and
  sent-packet handler (`bandwidth = cwnd / smoothed RTT`;
  `sentPacketHandler.GetCongestionWindow()` added).
- The session scheduler adapter maps these into `path.State`, so `PQIScheduler`
  ranks paths on live metrics (previously paths were never marked `Validated`, so
  every scheduler silently fell back to path 0).
- Server uses `scheduler.NewPQIScheduler(...)`; the Jetson client uses it too via
  the session adapter (round-robin selector kept as a baseline).
- Verified on hardware: 2-path streaming under the live PQI scheduler, 0
  decryption failures / 0 protocol violations, frames delivered; PQI selects and
  holds the best path (soft handover) rather than aggregating.

**Still TODO (code/eval):**

- Per-path loss rate is not yet measured (currently 0 = neutral in the cost);
  RTT and bandwidth are live. Add a per-path loss counter to the sent-packet
  handler to make all three cost terms live.
- Add the remaining baseline modes for fair comparison: minRTT scheduler and an
  SP-QUIC-with-connection-migration mode (round-robin baseline already present).
  (R1.10, R3.4)
- Wire PQI parameters from `config.yaml` (defaults are used now).
- Investigate handover latency (R2.10) and normal-state throughput gap (R3.9).

**Paper/Eval items** remain as listed in §1–§3 (terminology, related work,
migration/N3IWF, parameter table, claim softening, Wi-Fi spelling, equation
numbering, figures, N-run statistics, testbed spec).
