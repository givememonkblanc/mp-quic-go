#!/usr/bin/env bash
# Runs the WHOLE experiment matrix LOCALLY on the AMR (Jetson), so per-run
# orchestration never depends on the (flaky) control-channel SSH. The ryzen only
# has to launch this once and pull the results dir at the end. Parameters arrive
# via the params file sourced as $1; it must define SERVER_ADDR, PRIMARY_IFACE,
# SECONDARY_IFACE, BIN, OUT_BASE, RUN_SECONDS, FPS, REPEATS, EXP_GROUPS, SCENARIOS,
# scenario knobs, and the cross-traffic knobs (see experiments/config.sh).
set -u
PARAMS="${1:?params file}"; source "$PARAMS"
HERE="$(cd "$(dirname "$0")" && pwd)"
: "${PW:?}" "${BIN:?}" "${OUT_BASE:?}" "${SERVER_ADDR:?}" "${PRIMARY_IFACE:?}" "${SECONDARY_IFACE:?}"
: "${RUN_SECONDS:=40}" "${FPS:=30}" "${REPEATS:=10}" "${BACKUP_PROBE:=1s}"
: "${RSSI_INTERVAL:=250ms}" "${RSSI_ALPHA:=0.3}"

group_flags() {
  case "$1" in
    g1_sp_wifi)       echo "--addr $SERVER_ADDR --path0-iface $PRIMARY_IFACE --scheduler rssi --fps $FPS" ;;
    g2_sp_migrate)    echo "--addr $SERVER_ADDR --path0-iface $PRIMARY_IFACE --scheduler rssi --fps $FPS" ;;
    g3_mp_default)    echo "--addr $SERVER_ADDR --path1 $SERVER_ADDR --path0-iface $PRIMARY_IFACE --path1-iface $SECONDARY_IFACE --scheduler round-robin --fps $FPS" ;;
    g4_mp_failure)    echo "--addr $SERVER_ADDR --path1 $SERVER_ADDR --path0-iface $PRIMARY_IFACE --path1-iface $SECONDARY_IFACE --scheduler rssi --backup-probe $BACKUP_PROBE --fps $FPS" ;;
    g5_mp_rssi_aware) echo "--addr $SERVER_ADDR --path1 $SERVER_ADDR --path0-iface $PRIMARY_IFACE --path1-iface $SECONDARY_IFACE --scheduler rssi-aware --rssi-collect --rssi-interval $RSSI_INTERVAL --rssi-alpha $RSSI_ALPHA --backup-probe $BACKUP_PROBE --fps $FPS" ;;
  esac
}
uses_rssi()  { [[ "$1" == g5_mp_rssi_aware ]]; }
is_migrate() { [[ "$1" == g2_sp_migrate ]]; }

total=0; done_n=0
for g in $EXP_GROUPS; do for s in $SCENARIOS; do for ((r=1;r<=REPEATS;r++)); do total=$((total+1)); done; done; done
echo "ALLBEGIN total=$total $(date +%s)" > "$OUT_BASE/progress"

for g in $EXP_GROUPS; do
  for sc in $SCENARIOS; do
    for ((r=1;r<=REPEATS;r++)); do
      out="$OUT_BASE/$g/$sc/run$r"; mkdir -p "$out"
      ur=0; im=0; cross=0
      uses_rssi "$g" && ur=1
      is_migrate "$g" && im=1
      [[ "$sc" == 5_crosstraffic && -n "${CROSS_TARGET:-}" ]] && cross=1
      cross_if="$PRIMARY_IFACE"; [[ "${CROSS_PATH:-primary}" == secondary ]] && cross_if="$SECONDARY_IFACE"
      cat > "$out/env" <<EOF
PW='$PW'
BIN='$BIN'
OUT='$out'
PRIMARY='$PRIMARY_IFACE'
SECONDARY='$SECONDARY_IFACE'
SCENARIO='$sc'
FLAGS="$(group_flags "$g")"
RUN_SECONDS=$RUN_SECONDS
FAIL_AT=${FAIL_AT:-15}
RESTORE_AT=${RESTORE_AT:-28}
USE_RSSI=$ur
IS_MIGRATE=$im
RSSI_FILE='$( [[ $ur == 1 ]] && echo "$out/rssi" )'
GRAD_RSSI_START=${GRAD_RSSI_START:--50}
GRAD_RSSI_END=${GRAD_RSSI_END:--85}
GRAD_NETEM_LOSS_END=${GRAD_NETEM_LOSS_END:-15}
GRAD_NETEM_DELAY_END=${GRAD_NETEM_DELAY_END:-60}
CROSS_ENABLE=$cross
CROSS_PROTO='${CROSS_PROTO:-udp}'
CROSS_RATE='${CROSS_RATE:-30M}'
CROSS_TARGET='${CROSS_TARGET:-}'
CROSS_IFACE='$cross_if'
EOF
      # kill any stray client, then run this single run to completion (local).
      echo "$PW" | sudo -S pkill -9 -f "$BIN" 2>/dev/null; sleep 1
      bash "$HERE/orin_runner.sh" "$out/env" >"$out/runner.out" 2>&1 || true
      done_n=$((done_n+1))
      echo "PROGRESS $done_n/$total $g/$sc/run$r $(date +%s)" >> "$OUT_BASE/progress"
    done
  done
done
echo "ALLDONE $(date +%s)" > "$OUT_BASE/ALLDONE"
