#!/usr/bin/env python3
"""Extract the per-run evaluation metrics from one experiment run's logs.

Usage:
  metrics.py --client client.log --events events.log --cpu cpu.csv \
             --fps 30 --duration 40 [--server-cpu scpu.csv] > run.json

Outputs a JSON object of metrics. Values are null when a metric cannot be
derived from the available telemetry (e.g. path switches without the rssi-aware
SCHED log). Several metrics are documented as proxies (frame-level, not QUIC
packet-level) where true measurement would require qlog.
"""
import argparse, json, re, statistics, sys

FRAME = re.compile(r'\[(rgb|depth)\] FRAME serial=(\d+) bytes=(\d+) lat_ms=([\d.]+) ts=(\d+)')
SCHED_SW = re.compile(r'SCHED ts=(\d+) path_switch from=(-?\d+) to=(-?\d+).*n_switch=(\d+) ping_pong=(\d+)')
SCHED_ST = re.compile(r'SCHED ts=(\d+) state (\w+)->(\w+)')
FAILOVER = re.compile(r'main path lost liveness')
EVENT = re.compile(r'EVENT ts=(\d+) name=(\S+)')


def load_frames(path):
    frames = []  # (stream, serial, bytes, lat_ms, ts_ms)
    try:
        for ln in open(path, errors='ignore'):
            m = FRAME.search(ln)
            if m:
                frames.append((m.group(1), int(m.group(2)), int(m.group(3)),
                               float(m.group(4)), int(m.group(5))))
    except FileNotFoundError:
        pass
    frames.sort(key=lambda f: f[4])
    return frames


def load_sched(path):
    switches, pingpong, switch_ts = 0, 0, []
    try:
        for ln in open(path, errors='ignore'):
            m = SCHED_SW.search(ln)
            if m:
                switches = max(switches, int(m.group(4)))
                pingpong = max(pingpong, int(m.group(5)))
                switch_ts.append((int(m.group(1)), int(m.group(2)), int(m.group(3))))
    except FileNotFoundError:
        pass
    return switches, pingpong, switch_ts


def count_failovers(path):
    n = 0
    try:
        for ln in open(path, errors='ignore'):
            if FAILOVER.search(ln):
                n += 1
    except FileNotFoundError:
        pass
    return n


def load_events(path):
    ev = {}
    try:
        for ln in open(path, errors='ignore'):
            m = EVENT.search(ln)
            if m:
                ev.setdefault(m.group(2), int(m.group(1)))  # first occurrence
    except FileNotFoundError:
        pass
    return ev


def cpu_stats(path):
    cpu, rss = [], []
    try:
        for ln in open(path, errors='ignore'):
            p = ln.strip().split(',')
            if len(p) == 3 and p[0].isdigit():
                try:
                    cpu.append(float(p[1])); rss.append(float(p[2]))
                except ValueError:
                    pass
    except FileNotFoundError:
        pass
    f = lambda xs, g: (g(xs) if xs else None)
    return (f(cpu, statistics.mean), f(cpu, max),
            f(rss, statistics.mean), f(rss, max))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--client', required=True)
    ap.add_argument('--events', default='')
    ap.add_argument('--cpu', default='')
    ap.add_argument('--server-cpu', default='')
    ap.add_argument('--fps', type=float, required=True)
    ap.add_argument('--duration', type=float, required=True)
    a = ap.parse_args()

    frames = load_frames(a.client)
    switches, pingpong, switch_ts = load_sched(a.client)
    failovers = count_failovers(a.client)
    ev = load_events(a.events) if a.events else {}

    m = {}

    # --- throughput / latency / jitter --------------------------------------
    if frames:
        t0, t1 = frames[0][4], frames[-1][4]
        span_s = max((t1 - t0) / 1000.0, 1e-3)
        total_bytes = sum(f[2] for f in frames)
        m['throughput_mbps'] = total_bytes * 8 / span_s / 1e6
        lats = [f[3] for f in frames]
        m['frame_latency_ms_mean'] = statistics.mean(lats)
        m['frame_latency_ms_p95'] = sorted(lats)[int(0.95 * (len(lats) - 1))]
        # jitter: stddev of inter-arrival gaps, per stream then averaged
        jit = []
        for st in ('rgb', 'depth'):
            ts = [f[4] for f in frames if f[0] == st]
            if len(ts) > 2:
                gaps = [ts[i + 1] - ts[i] for i in range(len(ts) - 1)]
                jit.append(statistics.pstdev(gaps))
        m['jitter_ms'] = statistics.mean(jit) if jit else None
        # service interruption = largest inter-frame gap (any stream)
        allts = [f[4] for f in frames]
        gaps = [allts[i + 1] - allts[i] for i in range(len(allts) - 1)]
        m['service_interruption_ms'] = max(gaps) if gaps else 0
        # frame loss rate = 1 - delivered / expected (per-stream fps over span)
        expected = a.fps * span_s * 2  # rgb + depth
        m['frame_loss_rate'] = max(0.0, 1 - len(frames) / expected) if expected else None
        # delivered frame count
        m['frames_delivered'] = len(frames)
    else:
        for k in ('throughput_mbps', 'frame_latency_ms_mean', 'frame_latency_ms_p95',
                  'jitter_ms', 'service_interruption_ms', 'frame_loss_rate'):
            m[k] = None
        m['frames_delivered'] = 0

    # --- transition-window metrics (need a trigger event) -------------------
    trig = ev.get('wifi_down') or ev.get('degrade_begin')
    resume = None
    if trig and frames:
        after = [f for f in frames if f[4] >= trig]
        gap_before = [f for f in frames if f[4] < trig]
        # first frame whose preceding gap straddles the trigger = resume point
        # path switching delay: trigger -> first frame delivered after the stall
        if after:
            # find the largest gap that starts before/at the resume and ends after trig
            resume = after[0][4]
            # better: first frame after the longest post-trigger gap
            m['path_switching_delay_ms'] = max(0, after[0][4] - trig)
        # packet loss during transition (PROXY): missing frames in [trig, trig+2s]
        win = [f for f in frames if trig <= f[4] <= trig + 2000]
        exp_win = a.fps * 2 * 2  # 2 streams, 2 seconds
        m['packet_loss_transition_proxy'] = max(0.0, 1 - len(win) / exp_win) if exp_win else None
    else:
        m['path_switching_delay_ms'] = None
        m['packet_loss_transition_proxy'] = None

    # --- recovery time ------------------------------------------------------
    up = ev.get('wifi_up')
    if up:
        # prefer the scheduler's switch back to the primary (path 0)
        back = [ts for (ts, frm, to) in switch_ts if ts >= up and to == 0]
        if back:
            m['recovery_time_ms'] = back[0] - up
        elif frames:
            aft = [f[4] for f in frames if f[4] >= up]
            m['recovery_time_ms'] = (aft[0] - up) if aft else None
        else:
            m['recovery_time_ms'] = None
    else:
        m['recovery_time_ms'] = None

    # --- path switching counts ----------------------------------------------
    # rssi-aware reports via SCHED; other schedulers via failover log lines.
    m['num_path_switches'] = switches if switches else failovers
    m['ping_pong_count'] = pingpong

    # --- cpu / memory -------------------------------------------------------
    cpu_mean, cpu_max, rss_mean, rss_max = cpu_stats(a.cpu) if a.cpu else (None,) * 4
    m['cpu_pct_mean'] = cpu_mean
    m['cpu_pct_max'] = cpu_max
    m['mem_mb_mean'] = rss_mean / 1024.0 if rss_mean is not None else None
    m['mem_mb_max'] = rss_max / 1024.0 if rss_max is not None else None
    if a.server_cpu:
        sc_mean, sc_max, sr_mean, sr_max = cpu_stats(a.server_cpu)
        m['server_cpu_pct_mean'] = sc_mean
        m['server_mem_mb_mean'] = sr_mean / 1024.0 if sr_mean is not None else None

    json.dump(m, sys.stdout, indent=2)
    print()


if __name__ == '__main__':
    main()
