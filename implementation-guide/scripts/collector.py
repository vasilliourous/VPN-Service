#!/usr/bin/env python3
"""Collector — polls workers, scores them, writes best candidate to temp file.

Runs on the orchestrator VPS only, via cron every 60 seconds.
Reads /opt/load-balancer/workers.conf for the worker list.
Writes /tmp/least_loaded_vps.json for the activation API to read.

Zero dependencies. Standard library only.
"""

import json
import time
import sys
import os
import urllib.request

# ============================================================
# CONFIGURATION
# ============================================================
WORKERS_FILE = "/opt/load-balancer/workers.conf"
OUTPUT_FILE = "/tmp/least_loaded_vps.json"
TIMEOUT = 5  # seconds per worker HTTP request

# Scoring weights — tune based on production observations
WEIGHT_CPU = 0.35
WEIGHT_MEM = 0.25
WEIGHT_CONNS = 0.25
WEIGHT_LOAD = 0.15

# Connection cap — once a worker reaches this many VPN connections,
# further increases don't raise its score (it's already "fully loaded"
# for that metric). Adjust based on what your VPSes actually handle.
CONN_CAP = 200


# ============================================================
# WORKER LIST
# ============================================================

def read_workers(path):
    """Parse workers.conf. Returns list of 'hostname:port' strings.

    Format: one hostname:port per line. Lines starting with # are ignored.
    Blank lines are ignored.
    """
    workers = []
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                workers.append(line)
    except FileNotFoundError:
        print(f"ERROR: {path} not found", file=sys.stderr)
        sys.exit(1)
    return workers


# ============================================================
# POLLING
# ============================================================

def poll_worker(endpoint):
    """Fetch metrics from a worker's load agent.

    Args:
        endpoint: 'hostname:port' or 'ip:port' string

    Returns:
        dict with metrics, or None if unreachable
    """
    url = f"http://{endpoint}/"
    try:
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            return json.loads(resp.read().decode())
    except Exception as e:
        print(f"  WARN: {endpoint} unreachable — {e}", file=sys.stderr)
        return None


# ============================================================
# SCORING
# ============================================================

def normalize(value, cap):
    """Normalize a metric to 0-100 scale, capped at 'cap'."""
    return min(value, cap) / cap * 100


def score_worker(metrics):
    """Compute a weighted score from worker metrics.

    Lower score = less loaded = better candidate for new clients.
    All input metrics are converted to 0-100 scale and weighted.

    Returns:
        float: score (0 = idle, 100 = fully loaded)
    """
    cpu_norm = metrics["cpu_pct"]                    # already 0-100
    mem_norm = metrics["mem_pct"]                    # already 0-100
    conns_norm = normalize(metrics["connections"], CONN_CAP)
    load_norm = normalize(min(metrics["load_1m"] * 10, 100), 100)

    score = (
        cpu_norm * WEIGHT_CPU +
        mem_norm * WEIGHT_MEM +
        conns_norm * WEIGHT_CONNS +
        load_norm * WEIGHT_LOAD
    )
    return round(score, 1)


# ============================================================
# OUTPUT
# ============================================================

def atomic_write(path, data):
    """Write JSON to a temp file, then rename atomically.

    Prevents readers from seeing a partially-written file.
    """
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(data, f, indent=2)
    os.rename(tmp, path)


# ============================================================
# MAIN
# ============================================================

def main():
    workers = read_workers(WORKERS_FILE)
    if not workers:
        print("ERROR: No workers defined in", WORKERS_FILE, file=sys.stderr)
        sys.exit(1)

    results = []

    for endpoint in workers:
        print(f"Polling {endpoint}...", end=" ", flush=True)
        data = poll_worker(endpoint)

        if data is None:
            results.append({
                "worker": endpoint,
                "score": None,
                "reachable": False,
                "metrics": None,
            })
        else:
            score = score_worker(data)
            print(
                f"score={score} "
                f"(cpu={data['cpu_pct']}%, mem={data['mem_pct']}%, "
                f"conns={data['connections']}, load={data['load_1m']})"
            )
            results.append({
                "worker": endpoint,
                "score": score,
                "reachable": True,
                "metrics": data,
            })

    # Find the reachable worker with the lowest score
    reachable = [r for r in results if r["reachable"]]
    if not reachable:
        print("ERROR: All workers unreachable. Keeping previous output file.", file=sys.stderr)
        sys.exit(1)

    best = min(reachable, key=lambda r: r["score"])

    # Strip the port from the worker endpoint for the selected_worker field.
    # The activation API uses this as a hostname for VPN client configs,
    # and the VPN protocols use their own well-known ports (443, 8443, 9443),
    # not port 9099.
    selected = best["worker"].rsplit(":", 1)[0]

    output = {
        "timestamp": time.time(),
        "selected_worker": selected,
        "score": best["score"],
        "metrics": {
            "cpu_pct": best["metrics"]["cpu_pct"],
            "mem_pct": best["metrics"]["mem_pct"],
            "connections": best["metrics"]["connections"],
            "load_1m": best["metrics"]["load_1m"],
        },
        "all_workers": [
            {
                "worker": r["worker"].rsplit(":", 1)[0],
                "score": r["score"],
                "reachable": r["reachable"],
            }
            for r in results
        ],
    }

    os.makedirs(os.path.dirname(OUTPUT_FILE), exist_ok=True)
    atomic_write(OUTPUT_FILE, output)

    print(f"\nBest worker: {selected} (score={best['score']})")
    print(f"Output written to {OUTPUT_FILE}")


if __name__ == "__main__":
    main()
