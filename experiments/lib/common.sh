#!/usr/bin/env bash
# Shared helpers for the experiment harness. Source after config.sh.

# Robust SSH to the Jetson. The tailscale path can flap, so callers should keep
# the per-call work short (push a self-contained script, launch detached, poll).
jssh() {
  local tries="${JSSH_TRIES:-8}" i
  for ((i=1;i<=tries;i++)); do
    if SSHPASS="$JETSON_PW" sshpass -e ssh \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=6 \
        -o ServerAliveInterval=4 -o ServerAliveCountMax=2 \
        "$JETSON_USER@$JETSON_SSH" "$@" 2>/dev/null; then
      return 0
    fi
    sleep 2
  done
  return 1
}

# Push stdin to a file on the Jetson (retry until the bytes land).
jpush() {  # jpush <remote_path>
  local remote="$1" data; data="$(cat)"
  local i
  for ((i=1;i<=10;i++)); do
    if printf '%s' "$data" | SSHPASS="$JETSON_PW" sshpass -e ssh \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=6 \
        "$JETSON_USER@$JETSON_SSH" "cat > '$remote' && test -s '$remote'" 2>/dev/null; then
      return 0
    fi
    sleep 2
  done
  return 1
}

# Fetch a remote file to a local path (retry).
jpull() {  # jpull <remote_path> <local_path>
  local remote="$1" local="$2" i
  for ((i=1;i<=10;i++)); do
    if SSHPASS="$JETSON_PW" sshpass -e ssh \
        -o StrictHostKeyChecking=accept-new -o ConnectTimeout=6 \
        "$JETSON_USER@$JETSON_SSH" "cat '$remote'" > "$local" 2>/dev/null && [[ -s "$local" ]]; then
      return 0
    fi
    sleep 2
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
