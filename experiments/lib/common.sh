#!/usr/bin/env bash
# Shared helpers for the experiment harness. Source after config.sh.

# ---------------------------------------------------------------------------
# SSH to the AMR via a persistent multiplexed master connection.
#
# Root cause of the control-channel instability: the solfac AP isolates clients,
# so tailscale cannot hold a direct LAN path and flaps between a NAT-punched
# direct path and the DERP relay; each transition kills *new* TCP handshakes
# (the ssh 255 storms). A single long-lived master connection rides over the
# stable DERP relay and is reused by every command (no per-call handshake = no
# failure point), surviving the underlay churn. The experiment data path
# (AMR -> server) is independent of tailscale, so this only affects control.
# ---------------------------------------------------------------------------
JCTL="${JCTL:-$HOME/.ssh/cm/mpq-orin}"
mkdir -p "$(dirname "$JCTL")" 2>/dev/null; chmod 700 "$(dirname "$JCTL")" 2>/dev/null

_jbase=( -o ControlPath="$JCTL" -o StrictHostKeyChecking=accept-new
         -o ConnectTimeout=12 -o ServerAliveInterval=8 -o ServerAliveCountMax=4 )

# Ensure the master connection is up (one password handshake, then persists).
# Running a trivial command with ControlMaster=auto makes that first connection
# the master; ControlPersist keeps it alive after the command exits. (sshpass is
# incompatible with `ssh -f -N -M`, so we bootstrap via a real command instead.)
jconnect() {
  ssh -O check "${_jbase[@]}" "$JETSON_USER@$JETSON_SSH" 2>/dev/null && return 0
  local i
  for ((i=1;i<=8;i++)); do
    SSHPASS="$JETSON_PW" sshpass -e ssh -o ControlMaster=auto -o ControlPersist=900 \
        "${_jbase[@]}" "$JETSON_USER@$JETSON_SSH" true 2>/dev/null
    if ssh -O check "${_jbase[@]}" "$JETSON_USER@$JETSON_SSH" 2>/dev/null; then
      return 0
    fi
    sleep 2
  done
  return 1
}

# Run a command on the AMR over the master (no handshake / no password).
jssh() {
  jconnect || return 1
  local i
  for ((i=1;i<=3;i++)); do
    if ssh -o BatchMode=yes "${_jbase[@]}" "$JETSON_USER@$JETSON_SSH" "$@" 2>/dev/null; then
      return 0
    fi
    jconnect || return 1
  done
  return 1
}

# Push stdin to a file on the AMR (retry until the bytes land).
jpush() {  # jpush <remote_path>
  local remote="$1" data; data="$(cat)"; jconnect || return 1
  local i
  for ((i=1;i<=5;i++)); do
    if printf '%s' "$data" | ssh -o BatchMode=yes "${_jbase[@]}" \
        "$JETSON_USER@$JETSON_SSH" "cat > '$remote' && test -s '$remote'" 2>/dev/null; then
      return 0
    fi
    jconnect; sleep 1
  done
  return 1
}

# Fetch a remote file to a local path (retry).
jpull() {  # jpull <remote_path> <local_path>
  local remote="$1" local="$2"; jconnect || return 1
  local i
  for ((i=1;i<=5;i++)); do
    if ssh -o BatchMode=yes "${_jbase[@]}" "$JETSON_USER@$JETSON_SSH" \
        "cat '$remote'" > "$local" 2>/dev/null && [[ -s "$local" ]]; then
      return 0
    fi
    jconnect; sleep 1
  done
  return 1
}

# --- server lifecycle (local) ------------------------------------------------
server_running() { ss -uln 2>/dev/null | grep -q ':4433'; }

server_start() {
  if server_running; then return 0; fi
  mkdir -p "$FRAMES_DIR/rgb" "$FRAMES_DIR/depth"
  ( cd "$(dirname "$SERVER_BIN")/.." && exec "$SERVER_BIN" --config "$SERVER_CONFIG" ) \
      > /tmp/mpq-exp-server.log 2>&1 &
  echo $! > /tmp/mpq-exp-server.pid
  sleep 2
  server_running
}

# Count frames the server has on disk (delivery confirmation).
server_frame_count() { ls "$FRAMES_DIR/rgb" 2>/dev/null | wc -l | tr -d ' '; }

# --- cpu/mem sampling --------------------------------------------------------
# Sample a process's CPU% and RSS(KB) every interval into a CSV (ts_ms,cpu,rss).
# Runs in the background; returns the sampler PID. pattern matches `pgrep -f`.
sample_proc() {  # sample_proc <pgrep_pattern> <out_csv> <interval_sec>
  local pat="$1" out="$2" iv="${3:-1}"
  echo "ts_ms,cpu,rss_kb" > "$out"
  (
    while true; do
      local pid; pid="$(pgrep -f "$pat" | grep -v $$ | head -1)"
      if [[ -n "$pid" ]]; then
        # %cpu and rss from ps (rss in KB)
        local row; row="$(ps -p "$pid" -o %cpu=,rss= 2>/dev/null | awk '{print $1","$2}')"
        [[ -n "$row" ]] && echo "$(date +%s%3N),$row" >> "$out"
      fi
      sleep "$iv"
    done
  ) &
  echo $!
}
stop_sampler() { kill "$1" 2>/dev/null; }

ts_ms() { date +%s%3N; }
log() { echo "[$(date +%H:%M:%S)] $*" >&2; }
