#!/usr/bin/env python3
"""Aggregate per-run metric JSONs into mean +/- std with 95% confidence
intervals and the number of runs.

Usage:
  aggregate.py run1.json run2.json ...           # text table to stdout
  aggregate.py --json out.json run*.json         # also write machine JSON
"""
import argparse, json, math, statistics, sys

# Two-sided t critical values at 95% for small samples (df = n-1). Falls back to
# the normal approximation (1.96) for larger n / unknown df.
T95 = {1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571, 6: 2.447, 7: 2.365,
       8: 2.306, 9: 2.262, 10: 2.228, 11: 2.201, 12: 2.179, 13: 2.160,
       14: 2.145, 15: 2.131, 20: 2.086, 25: 2.060, 29: 2.045}


def tcrit(n):
    df = n - 1
    if df <= 0:
        return float('nan')
    if df in T95:
        return T95[df]
    keys = sorted(T95)
    for k in keys:
        if df <= k:
            return T95[k]
    return 1.96


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('runs', nargs='+')
    ap.add_argument('--json', default='')
    ap.add_argument('--label', default='')
    a = ap.parse_args()

    data = []
    for p in a.runs:
        try:
            data.append(json.load(open(p)))
        except Exception as e:
            print(f"warn: skip {p}: {e}", file=sys.stderr)
    if not data:
        sys.exit("no run data")

    keys = []
    for d in data:
        for k in d:
            if k not in keys:
                keys.append(k)

    out = {'label': a.label, 'n_runs': len(data), 'metrics': {}}
    rows = []
    for k in keys:
        vals = [d[k] for d in data if isinstance(d.get(k), (int, float))]
        if not vals:
            out['metrics'][k] = {'n': 0}
            rows.append((k, 'n/a', 'n/a', 'n/a', 0))
            continue
        n = len(vals)
        mean = statistics.mean(vals)
        std = statistics.stdev(vals) if n > 1 else 0.0
        ci = tcrit(n) * std / math.sqrt(n) if n > 1 else 0.0
        out['metrics'][k] = {'mean': mean, 'std': std, 'ci95': ci, 'n': n,
                             'min': min(vals), 'max': max(vals)}
        rows.append((k, f"{mean:.3f}", f"{std:.3f}", f"+/-{ci:.3f}", n))

    if a.label:
        print(f"== {a.label}  (n={len(data)} runs) ==")
    w = max(len(r[0]) for r in rows)
    print(f"{'metric'.ljust(w)}  {'mean':>12}  {'std':>10}  {'95% CI':>12}  {'n':>3}")
    print("-" * (w + 44))
    for k, mean, std, ci, n in rows:
        print(f"{k.ljust(w)}  {mean:>12}  {std:>10}  {ci:>12}  {n:>3}")

    if a.json:
        json.dump(out, open(a.json, 'w'), indent=2)


if __name__ == '__main__':
    main()
