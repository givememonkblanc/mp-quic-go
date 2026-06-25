#!/usr/bin/env bash
# Central configuration for the RSSI-aware MP-QUIC experiment harness.
# Override any value via the environment before invoking run.sh.

# ---------------------------------------------------------------------------
# Hosts / addressing
# ---------------------------------------------------------------------------
# QUIC server: runs on this (the orchestrating) host. Both AMR paths must be
# able to reach SERVER_ADDR — use the public/edge IP, not a LAN-only address.
SERVER_BIN="${SERVER_BIN:-$PWD/bin/server}"
SERVER_CONFIG="${SERVER_CONFIG:-$PWD/config/config.yaml}"
SERVER_ADDR="${SERVER_ADDR:-165.229.169.120:4433}"
FRAMES_DIR="${FRAMES_DIR:-$PWD/frames}"

# AMR / Jetson client (where the multipath client runs).
# Prefer a STABLE ssh path. Tailscale (100.x) survives address churn but can
# flap; a direct LAN IP is faster if the AP does not isolate clients.
JETSON_SSH="${JETSON_SSH:-100.109.159.8}"
JETSON_USER="${JETSON_USER:-jetson}"
JETSON_PW="${JETSON_PW:-ts4430!@}"
JETSON_BIN="${JETSON_BIN:-/home/jetson/mp-quic-go/bin/jetson}"
JETSON_WORK="${JETSON_WORK:-/tmp/mpq-exp}"   # scratch dir on the Jetson

# Interfaces on the AMR.
PRIMARY_IFACE="${PRIMARY_IFACE:-wlP1p1s0}"     # Wi-Fi (primary)
SECONDARY_IFACE="${SECONDARY_IFACE:-eno1}"     # 5G / cellular backup (set to your 5G iface)

# ---------------------------------------------------------------------------
# Workload
# ---------------------------------------------------------------------------
FPS="${FPS:-30}"
RUN_SECONDS="${RUN_SECONDS:-40}"               # streaming duration per run
REPEATS="${REPEATS:-10}"                       # repetitions per (group,scenario)

# ---------------------------------------------------------------------------
# Scheduler parameters (proposed RSSI-aware scheduler). Documented here so the
# experiment is reproducible; the binary's defaults match these.
# ---------------------------------------------------------------------------
RSSI_ALPHA="${RSSI_ALPHA:-0.3}"
RSSI_INTERVAL="${RSSI_INTERVAL:-250ms}"
BACKUP_PROBE="${BACKUP_PROBE:-1s}"

# ---------------------------------------------------------------------------
# Comparison groups. Each is a set of jetson flags (telemetry env is added by
# the runner). RSSI_FILE=1 marks groups whose scheduler consumes scripted RSSI.
#   g1_sp_wifi          SP-QUIC, Wi-Fi only (baseline single link)
#   g2_sp_migrate       SP-QUIC, app-level migration to 5G on Wi-Fi failure
#   g3_mp_default       MP-QUIC, default scheduler (round-robin)
#   g4_mp_failure       MP-QUIC, failure-based backup (rssi-primary + PTO liveness)
#   g5_mp_rssi_aware    MP-QUIC, proposed RSSI-aware scheduler
# ---------------------------------------------------------------------------
GROUPS=(g1_sp_wifi g2_sp_migrate g3_mp_default g4_mp_failure g5_mp_rssi_aware)

group_flags() {
  local g="$1"
  case "$g" in
    g1_sp_wifi)
      echo "--addr $SERVER_ADDR --path0-iface $PRIMARY_IFACE --scheduler rssi --fps $FPS" ;;
    g2_sp_migrate)
      # single path; the runner relaunches it on $SECONDARY_IFACE upon Wi-Fi failure
      echo "--addr $SERVER_ADDR --path0-iface $PRIMARY_IFACE --scheduler rssi --fps $FPS" ;;
    g3_mp_default)
      echo "--addr $SERVER_ADDR --path1 $SERVER_ADDR --path0-iface $PRIMARY_IFACE --path1-iface $SECONDARY_IFACE --scheduler round-robin --fps $FPS" ;;
    g4_mp_failure)
      echo "--addr $SERVER_ADDR --path1 $SERVER_ADDR --path0-iface $PRIMARY_IFACE --path1-iface $SECONDARY_IFACE --scheduler rssi --backup-probe $BACKUP_PROBE --fps $FPS" ;;
    g5_mp_rssi_aware)
      echo "--addr $SERVER_ADDR --path1 $SERVER_ADDR --path0-iface $PRIMARY_IFACE --path1-iface $SECONDARY_IFACE --scheduler rssi-aware --rssi-collect --rssi-interval $RSSI_INTERVAL --rssi-alpha $RSSI_ALPHA --backup-probe $BACKUP_PROBE --fps $FPS" ;;
    *) echo "" ; return 1 ;;
  esac
}

# Whether a group consumes the scripted RSSI file (needs MPQUIC_RSSI_FILE).
group_uses_rssi() { case "$1" in g5_mp_rssi_aware) return 0 ;; *) return 1 ;; esac ; }
# Whether a group migrates a single path to 5G on failure (orchestrated).
group_is_migrate() { case "$1" in g2_sp_migrate) return 0 ;; *) return 1 ;; esac ; }

# ---------------------------------------------------------------------------
# Scenarios. Timelines are interpreted by lib/orin_runner.sh.
#   1 normal        stable RSSI, no impairment
#   2 gradual       RSSI ramps -50 -> -85 dBm; matching tc netem loss/delay ramp
#   3 sudden        Wi-Fi interface down at mid-run, restored near end
#   4 recovery      down then up; verify hysteresis-gated return
#   5 crosstraffic  iperf3 background load on a path (type/rate below)
# ---------------------------------------------------------------------------
SCENARIOS=(1_normal 2_gradual 3_sudden 4_recovery 5_crosstraffic)

# Scenario 2 (gradual) ramp.
GRAD_RSSI_START="${GRAD_RSSI_START:--50}"
GRAD_RSSI_END="${GRAD_RSSI_END:--85}"
GRAD_NETEM_LOSS_END="${GRAD_NETEM_LOSS_END:-15}"   # % packet loss at the edge
GRAD_NETEM_DELAY_END="${GRAD_NETEM_DELAY_END:-60}" # ms one-way delay at the edge

# Scenario 3/4 trigger/restore offsets (seconds into the run).
FAIL_AT="${FAIL_AT:-15}"
RESTORE_AT="${RESTORE_AT:-28}"

# Scenario 5 cross traffic (DEFINE EXPLICITLY for the record).
#   CROSS_PATH   which interface carries the background load (primary|secondary)
#   CROSS_PROTO  udp|tcp
#   CROSS_RATE   e.g. 30M (UDP) ; ignored for tcp (uses full window)
#   CROSS_TARGET iperf3 server reachable over that path  (host:port form host -p port)
CROSS_PATH="${CROSS_PATH:-primary}"
CROSS_PROTO="${CROSS_PROTO:-udp}"
CROSS_RATE="${CROSS_RATE:-30M}"
CROSS_TARGET="${CROSS_TARGET:-}"    # e.g. "165.229.169.120 -p 5201" ; empty disables

OUT_ROOT="${OUT_ROOT:-$PWD/experiments/results}"
