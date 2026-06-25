#!/usr/bin/env bash
# RSSI-aware MP-QUIC experiment driver.
#
#   ./experiments/run.sh [-g group[,group...]] [-s scenario[,...]] [-n repeats]
#
# Defaults: all groups x all scenarios x $REPEATS. Results land under
# $OUT_ROOT/<group>/<scenario>/ as run<r>.json plus an aggregate.txt/json.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE/.."
source "$HERE/config.sh"
source "$HERE/lib/common.sh"

GSEL="$(IFS=,; echo "${GROUPS[*]}")"
SSEL="$(IFS=,; echo "${SCENARIOS[*]}")"
while getopts "g:s:n:" o; do case "$o" in
  g) GSEL="$OPTARG" ;; s) SSEL="$OPTARG" ;; n) REPEATS="$OPTARG" ;;
esac; done
IFS=, read -r -a USE_GROUPS <<< "$GSEL"
IFS=, read -r -a USE_SCEN  <<< "$SSEL"

command -v sshpass >/dev/null || { echo "need sshpass"; exit 1; }
[[ -x "$SERVER_BIN" ]] || { echo "build the server first: go build -o bin/server ./cmd/server"; exit 1; }
server_start || { echo "server failed to listen on :4433 (see /tmp/mpq-exp-server.log)"; exit 1; }
log "server listening; results -> $OUT_ROOT"

# Push the runner once per driver invocation.
jconnect || { echo "cannot reach Jetson at $JETSON_SSH (ssh master)"; exit 1; }
jssh "mkdir -p '$JETSON_WORK'" || { echo "cannot reach Jetson at $JETSON_SSH"; exit 1; }
jpush "$JETSON_WORK/orin_runner.sh" < "$HERE/lib/orin_runner.sh" || { echo "cannot push runner to Jetson"; exit 1; }
jssh "chmod +x '$JETSON_WORK/orin_runner.sh'" || true

cross_iface() { case "$CROSS_PATH" in secondary) echo "$SECONDARY_IFACE" ;; *) echo "$PRIMARY_IFACE" ;; esac; }

one_run() {  # one_run <group> <scenario> <rep> <local_out_dir>
  local g="$1" sc="$2" r="$3" lout="$4"
  local rout="$JETSON_WORK/$g/$sc/run$r"
  local flags; flags="$(group_flags "$g")"
  local use_rssi=0 is_migrate=0 cross=0
  group_uses_rssi "$g" && use_rssi=1
  group_is_migrate "$g" && is_migrate=1
  [[ "$sc" == 5_crosstraffic && -n "$CROSS_TARGET" ]] && cross=1

  # Build the per-run env file for the Jetson runner.
  local envf; envf="$(mktemp)"
  cat > "$envf" <<EOF
PW='$JETSON_PW'
BIN='$JETSON_BIN'
OUT='$rout'
PRIMARY='$PRIMARY_IFACE'
SECONDARY='$SECONDARY_IFACE'
SCENARIO='$sc'
FLAGS="$flags"
RUN_SECONDS=$RUN_SECONDS
FAIL_AT=$FAIL_AT
RESTORE_AT=$RESTORE_AT
USE_RSSI=$use_rssi
IS_MIGRATE=$is_migrate
RSSI_FILE='$( [[ $use_rssi == 1 ]] && echo "$rout/rssi" )'
GRAD_RSSI_START=$GRAD_RSSI_START
GRAD_RSSI_END=$GRAD_RSSI_END
GRAD_NETEM_LOSS_END=$GRAD_NETEM_LOSS_END
GRAD_NETEM_DELAY_END=$GRAD_NETEM_DELAY_END
CROSS_ENABLE=$cross
CROSS_PROTO='$CROSS_PROTO'
CROSS_RATE='$CROSS_RATE'
CROSS_TARGET='$CROSS_TARGET'
CROSS_IFACE='$(cross_iface)'
EOF

  jssh "mkdir -p '$rout'" || return 1
  jpush "$rout/env" < "$envf" || { rm -f "$envf"; return 1; }
  rm -f "$envf"

  # server-side cpu sampling (local)
  local scpu="$lout/server_cpu.csv"
  local sampid; sampid="$(sample_proc "$SERVER_BIN" "$scpu" 1)"

  # launch detached, verifying the runner actually came up (the ssh handshake can
  # fail mid-flap, leaving nothing started) and retrying the launch if not.
  local started=0 a
  for a in 1 2 3 4 5; do
    jssh "echo '$JETSON_PW' | sudo -S pkill -9 -f bin/jetson 2>/dev/null; rm -f '$rout/done' '$rout/events.log'; setsid nohup '$JETSON_WORK/orin_runner.sh' '$rout/env' >'$rout/runner.out' 2>&1 </dev/null & echo started" >/dev/null
    sleep 3
    if jssh "test -s '$rout/events.log' && echo y" 2>/dev/null | grep -q y; then started=1; break; fi
    log "    (launch attempt $a did not start the runner; retrying)"
  done
  [[ $started == 1 ]] || { log "    runner failed to start after retries"; stop_sampler "$sampid"; return 1; }
  local waited=0 maxw=$(( RUN_SECONDS + 40 ))
  while (( waited < maxw )); do
    if jssh "test -f '$rout/done' && echo y" 2>/dev/null | grep -q y; then break; fi
    sleep 3; waited=$((waited+3))
  done
  stop_sampler "$sampid"

  # collect
  jpull "$rout/client.log" "$lout/client.log" || true
  jpull "$rout/events.log" "$lout/events.log" || true
  jpull "$rout/cpu.csv"    "$lout/cpu.csv"    || true

  # per-run metrics
  python3 "$HERE/lib/metrics.py" --client "$lout/client.log" \
      --events "$lout/events.log" --cpu "$lout/cpu.csv" --server-cpu "$scpu" \
      --fps "$FPS" --duration "$RUN_SECONDS" > "$lout/metrics.json" 2>"$lout/metrics.err" \
      || { echo "  metrics.py failed (see $lout/metrics.err)"; return 1; }
  local fr; fr="$(python3 -c "import json;print(json.load(open('$lout/metrics.json')).get('frames_delivered'))" 2>/dev/null)"
  log "    $g/$sc run$r: frames=$fr"
}

for g in "${USE_GROUPS[@]}"; do
  for sc in "${USE_SCEN[@]}"; do
    base="$OUT_ROOT/$g/$sc"; mkdir -p "$base"
    log "=== $g / $sc  (x$REPEATS) ==="
    for ((r=1;r<=REPEATS;r++)); do
      lout="$base/run$r"; mkdir -p "$lout"
      one_run "$g" "$sc" "$r" "$lout" || log "    run$r failed"
    done
    # aggregate this (group,scenario)
    shopt -s nullglob
    runs=( "$base"/run*/metrics.json )
    shopt -u nullglob
    if (( ${#runs[@]} )); then
      python3 "$HERE/lib/aggregate.py" --label "$g / $sc" --json "$base/aggregate.json" \
          "${runs[@]}" | tee "$base/aggregate.txt"
    fi
  done
done

log "done. summaries: $OUT_ROOT/<group>/<scenario>/aggregate.txt"
