#!/usr/bin/env bash
#
# compare-schedulers.sh — fair multipath-scheduler comparison harness.
#
# Runs the Jetson client with each scheduler in turn, repeats N times, and reports
# per-scheduler throughput (frames delivered) with mean and standard deviation.
# Everything except the --scheduler flag is held constant, so the comparison
# isolates the scheduler's effect (addresses reviewer comments R1.10, R3.4, R3.8).
#
# The server must already be running on $SERVER. For a handover scenario, impair
# one path between runs with `tc netem` (see IMPAIR_CMD below) — left to the
# operator since it needs privileges on the path under test.
#
# Usage:
#   scripts/compare-schedulers.sh [--runs N] [--dur SECONDS] \
#       [--schedulers "pqi min-rtt round-robin"] [--server IP:PORT] [--path1 IP:PORT]
set -euo pipefail

RUNS=5
DUR=20
SCHEDULERS="pqi min-rtt round-robin"
SERVER="192.168.0.80:4433"
PATH1="192.168.0.80:4433"
JETSON="jetson@192.168.0.13"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519_mes}"
REMOTE_DIR="/home/jetson/mp-quic-go"
FPS=5

while [ $# -gt 0 ]; do
  case "$1" in
    --runs) RUNS="$2"; shift 2;;
    --dur) DUR="$2"; shift 2;;
    --schedulers) SCHEDULERS="$2"; shift 2;;
    --server) SERVER="$2"; shift 2;;
    --path1) PATH1="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

ssh_run() { ssh -o BatchMode=yes -i "$SSH_KEY" "$JETSON" "$1"; }

# One run: returns "<frames> <errors>" parsed from the client output.
one_run() {
  local sched="$1"
  local out
  out=$(ssh_run "cd $REMOTE_DIR && timeout $DUR ./bin/jetson \
      --addr $SERVER --path1 $PATH1 --fps $FPS --scheduler $sched 2>&1" || true)
  local frames errors
  frames=$(printf '%s\n' "$out" | grep -oE '\[depth\] sent #[0-9]+' | grep -oE '[0-9]+' | sort -n | tail -1)
  frames=${frames:-0}
  errors=$(printf '%s\n' "$out" | grep -ciE 'error|EOF|panic' || true)
  echo "$frames $errors"
}

printf '%-14s %8s %8s %8s %8s\n' "scheduler" "mean" "stddev" "min" "errors"
printf '%-14s %8s %8s %8s %8s\n' "---------" "----" "------" "---" "------"

for sched in $SCHEDULERS; do
  vals=()
  errsum=0
  for i in $(seq 1 "$RUNS"); do
    read -r f e < <(one_run "$sched")
    vals+=("$f")
    errsum=$((errsum + e))
    sleep 2   # let the previous connection fully close
  done
  # mean / stddev / min via awk
  stats=$(printf '%s\n' "${vals[@]}" | awk '
    { x[NR]=$1; s+=$1; if(NR==1||$1<mn)mn=$1 }
    END { m=s/NR; for(i=1;i<=NR;i++){d=x[i]-m; v+=d*d}
          printf "%.1f %.1f %d", m, (NR>1? sqrt(v/(NR-1)):0), mn }')
  printf '%-14s %8s %8s %8s %8d\n' "$sched" $stats "$errsum"
done
