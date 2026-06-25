#!/usr/bin/env bash
# On-AMR experiment runner: launches the FULL matrix on the Jetson once (so the
# flaky control-channel SSH is out of the per-run loop) and only pulls results at
# the end. Use this when the tailscale path to the AMR keeps flapping.
#
#   ./experiments/run_on_orin.sh [-g groups] [-s scenarios] [-n repeats]
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"; cd "$HERE/.."
source "$HERE/config.sh"; source "$HERE/lib/common.sh"

GSEL="$(IFS=,; echo "${EXP_GROUPS[*]}")"; SSEL="$(IFS=,; echo "${SCENARIOS[*]}")"
while getopts "g:s:n:" o; do case "$o" in g) GSEL="$OPTARG";; s) SSEL="$OPTARG";; n) REPEATS="$OPTARG";; esac; done
GROUPS_SP="${GSEL//,/ }"; SCEN_SP="${SSEL//,/ }"

command -v sshpass >/dev/null || { echo "need sshpass"; exit 1; }
[[ -x "$SERVER_BIN" ]] || { echo "build server: go build -o bin/server ./cmd/server"; exit 1; }
server_start || { echo "server not listening on :4433"; exit 1; }
jconnect || { echo "cannot reach AMR at $JETSON_SSH"; exit 1; }

OUT_BASE="$JETSON_WORK/results"
log "pushing harness + params to AMR ($OUT_BASE)"
jssh "mkdir -p '$OUT_BASE'" || exit 1
jpush "$JETSON_WORK/orin_runner.sh" < "$HERE/lib/orin_runner.sh" || exit 1
jpush "$JETSON_WORK/orin_driver.sh" < "$HERE/lib/orin_driver.sh" || exit 1
jssh "chmod +x '$JETSON_WORK/orin_runner.sh' '$JETSON_WORK/orin_driver.sh'"

paramsf="$(mktemp)"
cat > "$paramsf" <<EOF
PW='$JETSON_PW'
BIN='$JETSON_BIN'
OUT_BASE='$OUT_BASE'
SERVER_ADDR='$SERVER_ADDR'
PRIMARY_IFACE='$PRIMARY_IFACE'
SECONDARY_IFACE='$SECONDARY_IFACE'
RUN_SECONDS=$RUN_SECONDS
FPS=$FPS
REPEATS=$REPEATS
BACKUP_PROBE='$BACKUP_PROBE'
RSSI_INTERVAL='$RSSI_INTERVAL'
RSSI_ALPHA='$RSSI_ALPHA'
EXP_GROUPS='$GROUPS_SP'
SCENARIOS='$SCEN_SP'
FAIL_AT=$FAIL_AT
RESTORE_AT=$RESTORE_AT
GRAD_RSSI_START=$GRAD_RSSI_START
GRAD_RSSI_END=$GRAD_RSSI_END
GRAD_NETEM_LOSS_END=$GRAD_NETEM_LOSS_END
GRAD_NETEM_DELAY_END=$GRAD_NETEM_DELAY_END
CROSS_PATH='$CROSS_PATH'
CROSS_PROTO='$CROSS_PROTO'
CROSS_RATE='$CROSS_RATE'
CROSS_TARGET='$CROSS_TARGET'
EOF
jpush "$OUT_BASE/params" < "$paramsf"; rm -f "$paramsf"

# Estimate runtime so we know how long to poll.
ng=$(echo $GROUPS_SP|wc -w); ns=$(echo $SCEN_SP|wc -w)
total=$(( ng*ns*REPEATS )); per=$(( RUN_SECONDS + 14 )); est=$(( total*per ))
log "matrix: $ng groups x $ns scenarios x $REPEATS = $total runs (~$((est/60)) min)"

# Launch the driver once, detached, and verify it started.
started=0
for a in 1 2 3 4 5; do
  jssh "rm -f '$OUT_BASE/ALLDONE' '$OUT_BASE/progress'; setsid nohup '$JETSON_WORK/orin_driver.sh' '$OUT_BASE/params' >'$OUT_BASE/driver.out' 2>&1 </dev/null & echo go" >/dev/null
  sleep 4
  jssh "test -s '$OUT_BASE/progress' && echo y" 2>/dev/null | grep -q y && { started=1; break; }
  log "  (driver launch attempt $a failed; retrying)"
done
[[ $started == 1 ]] || { echo "driver failed to start"; exit 1; }
log "driver running on AMR; polling for completion"

# Poll for ALLDONE (tolerant of dropped polls), with a generous cap.
waited=0; cap=$(( est*2 + 120 ))
while (( waited < cap )); do
  if jssh "test -f '$OUT_BASE/ALLDONE' && echo y" 2>/dev/null | grep -q y; then break; fi
  prog="$(jssh "tail -1 '$OUT_BASE/progress' 2>/dev/null" 2>/dev/null)"
  [[ -n "$prog" ]] && log "  $prog"
  sleep 15; waited=$((waited+15))
done

log "pulling results"
mkdir -p "$OUT_ROOT"
for i in 1 2 3 4 5; do
  SSHPASS="$JETSON_PW" sshpass -e rsync -az -e "ssh -o ControlPath=$JCTL -o StrictHostKeyChecking=accept-new" \
    "$JETSON_USER@$JETSON_SSH:$OUT_BASE/" "$OUT_ROOT/_orin/" 2>/dev/null && break
  jconnect; sleep 2
done

# Analyse locally.
for g in $GROUPS_SP; do for sc in $SCEN_SP; do
  base="$OUT_ROOT/_orin/$g/$sc"; [[ -d "$base" ]] || continue
  shopt -s nullglob; runs=()
  for rd in "$base"/run*; do
    [[ -f "$rd/client.log" ]] || continue
    python3 "$HERE/lib/metrics.py" --client "$rd/client.log" --events "$rd/events.log" \
        --cpu "$rd/cpu.csv" --fps "$FPS" --duration "$RUN_SECONDS" > "$rd/metrics.json" 2>/dev/null && runs+=("$rd/metrics.json")
  done
  shopt -u nullglob
  (( ${#runs[@]} )) && python3 "$HERE/lib/aggregate.py" --label "$g / $sc" --json "$base/aggregate.json" "${runs[@]}" | tee "$base/aggregate.txt"
done; done
log "done. results under $OUT_ROOT/_orin/"
