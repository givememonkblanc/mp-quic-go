#!/usr/bin/env bash
# Runs ONE experiment run on the AMR (Jetson), fully self-contained and detached.
# Parameters arrive via the env file sourced as $1. It applies the scenario
# timeline while running the multipath client, and writes timestamped logs:
#   $OUT/client.log   client stdout (FRAME/SCHED/RSSI/"sent #"/failover lines)
#   $OUT/events.log   scenario timeline  (EVENT ts=<ms> name=<...>)
#   $OUT/cpu.csv      client CPU%/RSS samples
#   $OUT/done         written when finished
set -u
ENVFILE="${1:?env file}"; source "$ENVFILE"
: "${PW:?}" "${BIN:?}" "${OUT:?}" "${PRIMARY:?}" "${SECONDARY:?}" "${SCENARIO:?}" "${FLAGS:?}"
: "${RUN_SECONDS:=40}" "${FAIL_AT:=15}" "${RESTORE_AT:=28}"
: "${RSSI_FILE:=}" "${USE_RSSI:=0}" "${IS_MIGRATE:=0}"
: "${GRAD_RSSI_START:=-50}" "${GRAD_RSSI_END:=-85}" "${GRAD_NETEM_LOSS_END:=15}" "${GRAD_NETEM_DELAY_END:=60}"
: "${CROSS_ENABLE:=0}" "${CROSS_PROTO:=udp}" "${CROSS_RATE:=30M}" "${CROSS_TARGET:=}" "${CROSS_IFACE:=$PRIMARY}"

mkdir -p "$OUT"; : > "$OUT/client.log"; : > "$OUT/events.log"; rm -f "$OUT/done"
nowms() { date +%s%3N; }
ev() { echo "EVENT ts=$(nowms) name=$1 ${2:-}" >> "$OUT/events.log"; }
sudoq() { echo "$PW" | sudo -S "$@" 2>/dev/null; }

cleanup() {
  sudoq tc qdisc del dev "$PRIMARY" root 2>/dev/null
  [[ -n "${IPERF_PID:-}" ]] && kill "$IPERF_PID" 2>/dev/null
  [[ -n "${SAMP_PID:-}" ]] && kill "$SAMP_PID" 2>/dev/null
  sudoq pkill -9 -f "$BIN" 2>/dev/null
  # best-effort restore of the primary link
  sudoq nmcli device connect "$PRIMARY" 2>/dev/null
}
trap cleanup EXIT

# --- client cpu/mem sampler --------------------------------------------------
echo "ts_ms,cpu,rss_kb" > "$OUT/cpu.csv"
( while true; do
    pid="$(pgrep -f "$BIN" | head -1)"
    [[ -n "$pid" ]] && { r="$(ps -p "$pid" -o %cpu=,rss= 2>/dev/null | awk '{print $1","$2}')"; [[ -n "$r" ]] && echo "$(nowms),$r" >> "$OUT/cpu.csv"; }
    sleep 1
  done ) & SAMP_PID=$!

start_client() {  # start_client <label>
  ev client_start "iface0=$1"
  ( echo "$PW" | sudo -S env MPQUIC_SCHED_LOG=1 MPQUIC_FRAME_LOG=1 ${RSSI_FILE:+MPQUIC_RSSI_FILE=$RSSI_FILE} \
      timeout "$REMAIN" "$BIN" $CLIENT_FLAGS >> "$OUT/client.log" 2>&1 ) &
  CLIENT_PID=$!
}

# initial RSSI (good) so the scheduler starts on Wi-Fi
[[ "$USE_RSSI" == 1 && -n "$RSSI_FILE" ]] && echo "${GRAD_RSSI_START}" > "$RSSI_FILE"

REMAIN="$RUN_SECONDS"
CLIENT_FLAGS="$FLAGS"
ev run_begin "scenario=$SCENARIO group_migrate=$IS_MIGRATE"
START="$(nowms)"
start_client "$PRIMARY"

# --- background cross traffic (scenario 5) ----------------------------------
if [[ "$CROSS_ENABLE" == 1 && -n "$CROSS_TARGET" ]]; then
  ev cross_start "proto=$CROSS_PROTO rate=$CROSS_RATE iface=$CROSS_IFACE target=$CROSS_TARGET"
  if [[ "$CROSS_PROTO" == udp ]]; then
    iperf3 -c $CROSS_TARGET -u -b "$CROSS_RATE" -t "$RUN_SECONDS" -B "$(ip -br addr show $CROSS_IFACE|awk '{print $3}'|cut -d/ -f1)" >> "$OUT/iperf.log" 2>&1 & IPERF_PID=$!
  else
    iperf3 -c $CROSS_TARGET -t "$RUN_SECONDS" -B "$(ip -br addr show $CROSS_IFACE|awk '{print $3}'|cut -d/ -f1)" >> "$OUT/iperf.log" 2>&1 & IPERF_PID=$!
  fi
fi

# --- scenario timeline -------------------------------------------------------
case "$SCENARIO" in
  1_normal)
    sleep "$RUN_SECONDS" ;;

  2_gradual)
    # Ramp RSSI (for the scheduler) and tc netem (physical, for all groups) from
    # t=3s to t=RUN-5s.
    steps=20; t0=3; t1=$((RUN_SECONDS-5)); span=$((t1-t0)); [[ $span -lt 1 ]] && span=1
    sleep "$t0"; ev degrade_begin
    sudoq tc qdisc add dev "$PRIMARY" root netem loss 0% delay 0ms 2>/dev/null
    for ((i=1;i<=steps;i++)); do
      rssi=$(( GRAD_RSSI_START + (GRAD_RSSI_END-GRAD_RSSI_START)*i/steps ))
      loss=$(( GRAD_NETEM_LOSS_END*i/steps ))
      delay=$(( GRAD_NETEM_DELAY_END*i/steps ))
      [[ "$USE_RSSI" == 1 && -n "$RSSI_FILE" ]] && echo "$rssi" > "$RSSI_FILE"
      sudoq tc qdisc change dev "$PRIMARY" root netem loss ${loss}% delay ${delay}ms 2>/dev/null
      ev degrade_step "rssi=$rssi loss=${loss}% delay=${delay}ms"
      sleep "$(awk "BEGIN{print $span/$steps}")"
    done
    ev degrade_end
    sleep 5 ;;

  3_sudden|4_recovery)
    sleep "$FAIL_AT"
    ev wifi_down
    [[ "$USE_RSSI" == 1 && -n "$RSSI_FILE" ]] && echo "-100" > "$RSSI_FILE"
    # Simulate sudden Wi-Fi failure by blackholing the Wi-Fi path (100% loss)
    # rather than downing the interface: this keeps the IP/route/tailscale and the
    # driver intact (downing the iface disrupts control + hangs nmcli reconnect),
    # while the QUIC path still dies (PTO liveness) and fails over to 5G.
    sudoq tc qdisc add dev "$PRIMARY" root netem loss 100% 2>/dev/null \
      || sudoq tc qdisc change dev "$PRIMARY" root netem loss 100%
    # migrate group: detect the stall and re-establish the single path on 5G
    if [[ "$IS_MIGRATE" == 1 ]]; then
      last=$(grep -c "sent #" "$OUT/client.log")
      stall=0
      while :; do
        sleep 1; cur=$(grep -c "sent #" "$OUT/client.log")
        if [[ "$cur" -le "$last" ]]; then stall=$((stall+1)); else stall=0; fi
        last=$cur
        if [[ $stall -ge 2 ]]; then
          ev migrate_begin "to=$SECONDARY"
          sudoq pkill -9 -f "$BIN" 2>/dev/null; sleep 1
          REMAIN=$(( RUN_SECONDS - (FAIL_AT+stall) )); [[ $REMAIN -lt 3 ]] && REMAIN=3
          CLIENT_FLAGS="${FLAGS/--path0-iface $PRIMARY/--path0-iface $SECONDARY}"
          start_client "$SECONDARY"
          ev migrate_done
          break
        fi
        [[ $stall -gt 12 ]] && break
      done
    fi
    rem=$(( RESTORE_AT - FAIL_AT )); [[ $rem -gt 0 ]] && sleep "$rem"
    ev wifi_up
    [[ "$USE_RSSI" == 1 && -n "$RSSI_FILE" ]] && echo "${GRAD_RSSI_START}" > "$RSSI_FILE"
    sudoq tc qdisc del dev "$PRIMARY" root 2>/dev/null
    rem=$(( RUN_SECONDS - RESTORE_AT )); [[ $rem -gt 0 ]] && sleep "$rem" ;;

  5_crosstraffic)
    sleep "$RUN_SECONDS" ;;

  *) sleep "$RUN_SECONDS" ;;
esac

ev run_end
wait "$CLIENT_PID" 2>/dev/null
cleanup
trap - EXIT
echo "$(nowms)" > "$OUT/done"
