#!/usr/bin/env python3
"""Load agent — reports system metrics as JSON on port 9099.

Runs on every VPS in the pool. The orchestrator polls this every 60 seconds.
Binds to 0.0.0.0:9099 — restrict access with UFW to the orchestrator's IP only.

Zero dependencies. Standard library only.
"""

import json
import time
import os
import subprocess
from http.server import HTTPServer, BaseHTTPRequestHandler


def cpu_pct():
    """Read /proc/stat twice 1s apart, compute CPU usage percentage.

    Reads the aggregate 'cpu ' line (all cores combined).
    Subtracts idle + iowait from total to get busy time.
    """
    def read_cpu():
        with open("/proc/stat") as f:
            for line in f:
                if line.startswith("cpu "):
                    fields = list(map(int, line.split()[1:]))
                    # fields: user nice system idle iowait irq softirq steal ...
                    idle = fields[3] + (fields[4] if len(fields) > 4 else 0)
                    total = sum(fields)
                    return idle, total
        return 0, 0

    idle1, total1 = read_cpu()
    time.sleep(1)
    idle2, total2 = read_cpu()

    if total2 - total1 == 0:
        return 0.0
    return round((1 - (idle2 - idle1) / (total2 - total1)) * 100, 1)


def mem_pct():
    """Read /proc/meminfo, compute used memory percentage.

    Uses MemAvailable rather than MemFree + buffers/cache
    because MemAvailable is what the kernel actually considers
    available for new allocations.
    """
    total = 0
    avail = 0
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemTotal:"):
                total = int(line.split()[1])
            elif line.startswith("MemAvailable:"):
                avail = int(line.split()[1])
    if total == 0:
        return 0.0
    return round((total - avail) / total * 100, 1)


def load_1m():
    """Standard Unix 1-minute load average."""
    return round(os.getloadavg()[0], 2)


def vpn_connections():
    """Count established TCP/UDP connections on client-facing VPN ports only.

    Uses 'ss' with sport filter to count only connections where the VPS
    is the source (i.e., reply traffic from the VPN tunnel). This is a
    better proxy for client count than counting all established connections
    (which would include SSH, outbound NAT, the load agent itself, etc.).
    """
    try:
        out = subprocess.check_output(
            ["ss", "-tun", "state", "established",
             "( sport = :443 or sport = :8443 or sport = :9443 )"],
            timeout=2,
        )
        lines = out.decode().strip().split("\n")
        return max(0, len(lines) - 1)  # subtract header line
    except Exception:
        return 0


def uptime_days():
    """System uptime in days (fractional)."""
    with open("/proc/uptime") as f:
        return round(float(f.read().split()[0]) / 86400, 1)


def get_hostname():
    """Best-effort hostname."""
    try:
        return os.uname().nodename
    except Exception:
        return "unknown"


class Handler(BaseHTTPRequestHandler):
    """Single endpoint: GET / returns JSON metrics."""

    def do_GET(self):
        if self.path != "/":
            self.send_response(404)
            self.end_headers()
            return

        data = {
            "timestamp": time.time(),
            "hostname": get_hostname(),
            "cpu_pct": cpu_pct(),
            "mem_pct": mem_pct(),
            "load_1m": load_1m(),
            "connections": vpn_connections(),
            "uptime_days": uptime_days(),
        }

        body = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass  # suppress access logs to avoid noise


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", 9099), Handler)
    server.serve_forever()
