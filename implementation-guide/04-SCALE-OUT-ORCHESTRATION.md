# VPN Monetised Service — Scale-Out Orchestration

> **When you outgrow a single VPS or the two-VPS HA setup, this is how you scale horizontally without adding latency, complexity, or per-client overhead.**

---

## Table of Contents

1. [The Model — Placement Service, Not Load Balancer](#1-the-model--placement-service-not-load-balancer)
2. [Architecture Diagram](#2-architecture-diagram)
3. [The Load Agent — `/opt/load-balancer/load-agent.py`](#3-the-load-agent--optload-balancerload-agentpy)
4. [The Collector — `/opt/load-balancer/collector.py`](#4-the-collector--optload-balancercollectorpy)
5. [The Scoring Formula](#5-the-scoring-formula)
6. [The Activation Flow With Orchestration](#6-the-activation-flow-with-orchestration)
7. [The Migration Endpoint — Surviving Worker Death](#7-the-migration-endpoint--surviving-worker-death)
8. [Day-2 Operations — Adding & Removing Workers](#8-day-2-operations--adding--removing-workers)
9. [Failure Mode Reference](#9-failure-mode-reference)
10. [Orchestrator SPOF Mitigation](#10-orchestrator-spof-mitigation)
11. [Integration With the Base Implementation Guide](#11-integration-with-the-base-implementation-guide)
12. [Client App Changes — Auto-Migration](#12-client-app-changes--auto-migration)

---

## 1. The Model — Placement Service, Not Load Balancer

**This is NOT a load balancer.** A load balancer sits in the data path and forwards every packet. That would double latency for every client, make the orchestrator VPS a bottleneck, and create a single point of failure for all traffic.

This is a **placement service** — it decides where a new client goes once (at activation time) and then gets out of the way. After activation, the client connects directly to the assigned worker VPS. The orchestrator never sees another byte of that client's traffic.

**Analogy:** A restaurant host. The host greets you, checks which tables are open, seats you at one, and never interacts with you again. The waiter (worker VPS) handles everything from that point. If the host steps out, the people already seated keep eating — you just can't seat new people.

### What the orchestrator does every 60 seconds:
1. Read `/opt/load-balancer/workers.conf` — a text file listing all worker hostnames
2. Poll each worker's load agent at `http://<hostname>:9099/` with a 5-second timeout
3. Parse the JSON response (CPU%, memory%, load average, connection count, uptime)
4. Compute a weighted score for each reachable worker
5. Write the lowest-scoring worker's hostname to `/tmp/least_loaded_vps.json`
6. Log warnings for any unreachable workers

### What the orchestrator does on every activation request:
1. Receive POST with activation code and device ID
2. Validate the code in the SQLite database
3. Read `/tmp/least_loaded_vps.json` for the current best worker
4. Generate a config with that worker's hostname as the server address
5. Return the config — client connects directly to the worker

---

## 2. Architecture Diagram

```
                        ┌─────────────────────────────┐
                        │      ORCHESTRATOR VPS        │
                        │  (also a worker — runs all    │
                        │   protocols + serves its own  │
                        │   share of clients)           │
                        │                               │
                        │  ├─ Activation API            │
                        │  ├─ Collector (cron, 60s)     │
                        │  ├─ workers.conf              │
                        │  ├─ /tmp/least_loaded_vps.json│
                        │  ├─ All 4 protocol backends   │
                        │  └─ Load agent :9099          │
                        └──────┬───────────────────────┘
                               │
            ┌──────────────────┼──────────────────┐
            │                  │                  │
            ▼                  ▼                  ▼
    ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
    │  WORKER VPS  │  │  WORKER VPS  │  │  WORKER VPS  │
    │  (provider A)│  │  (provider B)│  │  (provider C)│
    │              │  │              │  │              │
    │ ├─ Hysteria2 │  │ ├─ Hysteria2 │  │ ├─ Hysteria2 │
    │ ├─ VLESS     │  │ ├─ VLESS     │  │ ├─ VLESS     │
    │ ├─ TUIC V5   │  │ ├─ TUIC V5   │  │ ├─ TUIC V5   │
    │ ├─ AmneziaWG │  │ ├─ AmneziaWG │  │ ├─ AmneziaWG │
    │ ├─ NAT       │  │ ├─ NAT       │  │ ├─ NAT       │
    │ └─ Load agent│  │ └─ Load agent│  │ └─ Load agent│
    │    :9099     │  │    :9099     │  │    :9099     │
    └──────────────┘  └──────────────┘  └──────────────┘
         ▲                  ▲                  ▲
         │                  │                  │
         └──────────────────┼──────────────────┘
                            │
                    CLIENT TRAFFIC
              (direct to assigned worker,
               bypasses orchestrator entirely)
```

**Key points:**
- Every VPS is identical at the protocol/NAT/security level — they all run the same implementation guide
- The orchestrator is just a worker that also happens to run the activation API, collector, and `workers.conf`
- Client traffic never touches the orchestrator after activation
- Workers can be at different cloud providers, different regions, different price points

---

## 3. The Load Agent — `/opt/load-balancer/load-agent.py`

**Purpose:** Answer one question — "how busy are you?" Runs on every VPS in the pool, including the orchestrator.

**Binding:** Listens on `0.0.0.0:9099` but UFW restricts access to the orchestrator's IP only. This is critical — the spec originally said `127.0.0.1` but that prevents remote access from the orchestrator. The correct approach is bind to all interfaces, lock down with firewall.

### 3.1 Metrics Collected

| Metric | Source | Meaning | Range |
|--------|--------|---------|-------|
| CPU usage % | `/proc/stat` — read twice, 1s apart, compute delta of idle vs total | How saturated the processor is | 0.0 – 100.0 |
| Memory usage % | `/proc/meminfo` — `(total - available) / total * 100` | How much RAM is in use | 0.0 – 100.0 |
| 1-min load average | `os.getloadavg()` | System pressure including I/O wait | 0.0 – N |
| Active VPN connections | `ss -tun state established '( sport = :443 or sport = :8443 or sport = :9443 )'` — counts only client-facing ports | Rough estimate of concurrent VPN clients | 0 – thousands |
| Uptime in days | `/proc/uptime` | How long since last reboot | 0.0 – N |

**The connection count only counts VPN ports** (443, 8443, 9443), not SSH, not the load agent itself, not outbound NAT connections. This makes it a much better proxy for client load.

### 3.2 Install on Every VPS

```bash
mkdir -p /opt/load-balancer

cat > /opt/load-balancer/load-agent.py << 'PYEOF'
#!/usr/bin/env python3
"""Load agent — reports system metrics as JSON on port 9099."""

import json, time, os, subprocess
from http.server import HTTPServer, BaseHTTPRequestHandler

def cpu_pct():
    """Read /proc/stat twice 1s apart, compute CPU usage."""
    def read_cpu():
        with open("/proc/stat") as f:
            for line in f:
                if line.startswith("cpu "):
                    fields = list(map(int, line.split()[1:]))
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
    """Read /proc/meminfo, compute used percentage."""
    total = avail = 0
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
    return round(os.getloadavg()[0], 2)

def vpn_connections():
    """Count established connections on client-facing VPN ports only."""
    try:
        out = subprocess.check_output(
            ["ss", "-tun", "state", "established",
             "( sport = :443 or sport = :8443 or sport = :9443 )"],
            timeout=2
        )
        # Output has a header line; count the rest
        lines = out.decode().strip().split("\n")
        return max(0, len(lines) - 1)  # subtract header
    except Exception:
        return 0

def uptime_days():
    with open("/proc/uptime") as f:
        return round(float(f.read().split()[0]) / 86400, 1)

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/":
            self.send_response(404)
            self.end_headers()
            return
        try:
            hostname = os.uname().nodename
        except Exception:
            hostname = "unknown"
        data = {
            "timestamp": time.time(),
            "hostname": hostname,
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
        pass  # suppress access logs

if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", 9099), Handler)
    server.serve_forever()
PYEOF

chmod +x /opt/load-balancer/load-agent.py

# Systemd service
cat > /etc/systemd/system/load-agent.service << 'EOF'
[Unit]
Description=VPN Load Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/python3 /opt/load-balancer/load-agent.py
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now load-agent
```

### 3.3 Firewall — Restrict to Orchestrator Only

On every worker VPS, allow port 9099 **only from the orchestrator's IP**. If the orchestrator is `vps-a.yourdomain.com` with IP `1.2.3.4`:

```bash
# On each worker (NOT the orchestrator — unless you want to restrict the
# orchestrator's own load agent to itself):
ufw allow from 1.2.3.4 to any port 9099 proto tcp comment "Load agent — orchestrator polling only"
```

On the orchestrator, if it also runs the load agent (for self-polling via `127.0.0.1:9099` in `workers.conf`), no firewall change needed — localhost traffic is always allowed.

### 3.4 Verify

```bash
# From the orchestrator — should return valid JSON
curl http://<worker-hostname>:9099/
# {"timestamp": 1718000000.0, "hostname": "vps-b", "cpu_pct": 12.0, ...}

# From any other machine — should be blocked by UFW
curl --connect-timeout 5 http://<worker-hostname>:9099/
# Connection timed out (as expected)
```

---

## 4. The Collector — `/opt/load-balancer/collector.py`

**Purpose:** Poll all workers every 60 seconds, score them, and write the winner to a temp file.

**Runs only on the orchestrator VPS**, via cron.

### 4.1 The Workers File — `/opt/load-balancer/workers.conf`

```bash
cat > /opt/load-balancer/workers.conf << 'EOF'
# VPN Worker Pool — last updated YYYY-MM-DD
# Format: hostname:port   (one per line, # for comments)
#
# IMPORTANT: Use hostnames that resolve to the worker's PUBLIC IP, NOT
# Cloudflare-proxied hostnames. The collector polls port 9099, which is
# outside Cloudflare's proxy. Use a separate A record with grey cloud
# (DNS only, not proxied) or use direct IP addresses.
#
# Example with direct IPs:
#   1.2.3.4:9099
#   5.6.7.8:9099
#   9.10.11.12:9099

vps-a.yourdomain.com:9099
vps-b.yourdomain.com:9099
# vps-c.yourdomain.com:9099   (commented out = removed from pool)
127.0.0.1:9099
EOF
```

**ⓘ DNS note:** The `vps-a.yourdomain.com` hostnames used here should resolve to the worker's **direct public IP**, NOT through Cloudflare's proxy (orange cloud). Cloudflare doesn't proxy arbitrary TCP ports like 9099. Create separate A records with the grey cloud icon (DNS only) or use bare IPs. Alternatively, use a dedicated non-proxied subdomain like `direct-a.yourdomain.com`.

### 4.2 The Collector Script

```bash
cat > /opt/load-balancer/collector.py << 'PYEOF'
#!/usr/bin/env python3
"""Collector — polls workers, scores them, writes best candidate to temp file."""

import json, time, sys, urllib.request, os

WORKERS_FILE = "/opt/load-balancer/workers.conf"
OUTPUT_FILE = "/tmp/least_loaded_vps.json"
TIMEOUT = 5  # seconds per worker

# ============================================================
# SCORING WEIGHTS (tune based on production data)
# ============================================================
WEIGHT_CPU = 0.35
WEIGHT_MEM = 0.25
WEIGHT_CONNS = 0.25
WEIGHT_LOAD = 0.15

# Connection cap — once a worker has this many VPN connections,
# further increases don't raise its score.
CONN_CAP = 200

# ============================================================
# WORKER LIST
# ============================================================
def read_workers(path):
    workers = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            workers.append(line)
    return workers

# ============================================================
# POLLING
# ============================================================
def poll_worker(endpoint):
    """Fetch metrics from a worker's load agent. Returns dict or None."""
    url = f"http://{endpoint}/"
    try:
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            data = json.loads(resp.read().decode())
            return data
    except Exception as e:
        print(f"  WARN: {endpoint} unreachable — {e}", file=sys.stderr)
        return None

# ============================================================
# SCORING
# ============================================================
def normalize(value, cap):
    """Normalize a metric to 0-100, capped at 'cap'."""
    return min(value, cap) / cap * 100

def score_worker(metrics):
    """Compute weighted score. Lower = better."""
    cpu_norm = metrics["cpu_pct"]                     # already 0-100
    mem_norm = metrics["mem_pct"]                     # already 0-100
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
            results.append({"worker": endpoint, "score": None, "reachable": False, "metrics": None})
        else:
            score = score_worker(data)
            print(f"score={score} (cpu={data['cpu_pct']}%, mem={data['mem_pct']}%, "
                  f"conns={data['connections']}, load={data['load_1m']})")
            results.append({"worker": endpoint, "score": score, "reachable": True, "metrics": data})

    # Find the reachable worker with the lowest score
    reachable = [r for r in results if r["reachable"]]
    if not reachable:
        print("ERROR: All workers unreachable. Keeping previous output file.", file=sys.stderr)
        sys.exit(1)

    best = min(reachable, key=lambda r: r["score"])

    # Strip port from worker endpoint for the selected_worker field
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
    with open(OUTPUT_FILE + ".tmp", "w") as f:
        json.dump(output, f, indent=2)
    os.rename(OUTPUT_FILE + ".tmp", OUTPUT_FILE)  # atomic write

    print(f"\nBest worker: {selected} (score={best['score']})")
    print(f"Output written to {OUTPUT_FILE}")

if __name__ == "__main__":
    main()
PYEOF

chmod +x /opt/load-balancer/collector.py

# Cron — runs every 60 seconds
echo "* * * * * root /opt/load-balancer/collector.py 2>&1 | logger -t load-collector" > /etc/cron.d/load-collector

# Run it once now to verify
/opt/load-balancer/collector.py
```

---

## 5. The Scoring Formula

### 5.1 Normalization

| Raw Metric | Normalized To 0-100 | Formula |
|------------|---------------------|---------|
| CPU % (0-100) | Already 0-100 | `cpu_pct` |
| Memory % (0-100) | Already 0-100 | `mem_pct` |
| Connections (0-N) | 0-100, capped at `CONN_CAP` | `min(connections, CONN_CAP) / CONN_CAP * 100` |
| Load avg (0-N) | 0-100, scaled | `min(load_1m * 10, 100)` |

### 5.2 Weights

| Metric | Weight | Rationale |
|--------|--------|-----------|
| CPU | 0.35 | First bottleneck in encryption-heavy workloads |
| Memory | 0.25 | Important but protocol backends are memory-efficient |
| Connections | 0.25 | Good proxy for concurrent client load |
| Load average | 0.15 | Lagging indicator; overlaps with CPU |

### 5.3 Final Score

```
score = cpu_norm * 0.35 + mem_norm * 0.25 + conns_norm * 0.25 + load_norm * 0.15
```

Lower = better. 0 = completely idle. 50 = moderately loaded. 80+ = shouldn't get new clients.

### 5.4 Worked Example

| Worker | CPU | Memory | Connections | Load | Score |
|--------|-----|--------|-------------|------|-------|
| vps-a | 45% | 60% | 120 | 2.1 | 48.9 |
| **vps-b** | **12%** | **30%** | **45** | **0.5** | **18.1** ← winner |
| vps-c | 75% | 80% | 190 | 3.8 | 75.7 |

vps-b is clearly the least loaded — it gets the next activation.

### 5.5 What Happens When a Worker Is Unreachable

The collector skips unreachable workers entirely. They get no score, they're not candidates. The temp file's `all_workers` list marks them as `"reachable": false` for diagnostics. The best worker among the *reachable* ones gets the placement.

If zero workers respond, the collector logs an error and does NOT overwrite the temp file. The previous file stays intact. The activation API continues to use the last known good worker. Activations don't break because of one bad collector cycle.

---

## 6. The Activation Flow With Orchestration

### 6.1 Step by Step

1. User opens the VPN client app, enters activation code `VPN-A1B2C3D4`
2. App POSTs to `https://api.yourdomain.com/api/v1/activate` with code and device ID
3. The orchestrator's activation API validates the code against the SQLite DB
4. If valid, the API reads `/tmp/least_loaded_vps.json` and extracts `selected_worker`
5. If the temp file doesn't exist (first boot, before collector has run), falls back to the orchestrator's own hostname
6. The API calls the tier-specific config generator, passing the selected worker's hostname as the server address
7. The config generator builds a JSON config with that worker as the server
8. API marks the code as used, creates a device record, returns the config
9. App imports the config and connects directly to the worker
10. From this point forward: client ↔ worker, orchestrator out of the path

### 6.2 Activation API Changes From Single-VPS Mode

The base implementation guide's activation API needs these modifications. The full updated code is in Section 11.

**Change A — Config generators accept a `server` parameter:**
```python
def generate_lite_config(device_id: str, server: str) -> dict:
    return {
        "server": server,          # ← dynamic, not from env
        "port": 8443,
        ...
    }
```

**Change B — `activate()` endpoint reads the temp file:**
```python
@app.post("/api/v1/activate")
async def activate(req: ActivateRequest):
    # ... validate code ...
    
    # Read best worker from collector output
    try:
        with open("/tmp/least_loaded_vps.json") as f:
            lb_data = json.load(f)
            server = lb_data["selected_worker"]
    except (FileNotFoundError, json.JSONDecodeError, KeyError):
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")  # fallback
    
    config = generators[code.tier](req.device_id, server)
    # ... rest unchanged ...
```

**Change C — New `/api/v1/migrate` endpoint** (see Section 7).

---

## 7. The Migration Endpoint — Surviving Worker Death

### 7.1 The Problem

Worker `vps-b` dies. The 35 clients pinned to it lose internet. The collector detects the failure within 60 seconds and stops assigning new clients there. But existing clients are stuck.

### 7.2 The Solution — `POST /api/v1/migrate`

A client calls the migrate endpoint (manually or via auto-detection in the client app), which reissues a config pointing to a live worker.

```python
@app.post("/api/v1/migrate")
async def migrate(req: MigrateRequest):
    """Reissue config for an existing device, pointing to the current best worker."""
    db = SessionLocal()
    device = db.query(Device).filter(
        Device.device_id == req.device_id,
        Device.active == True
    ).first()
    
    if not device:
        db.close()
        raise HTTPException(status_code=404, detail="Device not found or subscription expired")
    
    # Read best worker
    try:
        with open("/tmp/least_loaded_vps.json") as f:
            server = json.load(f)["selected_worker"]
    except (FileNotFoundError, json.JSONDecodeError, KeyError):
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")
    
    generators = {
        "lite": generate_lite_config,
        "standard": generate_standard_config,
        "premium": generate_premium_config,
    }
    config = generators[device.tier](device.device_id, server)
    
    db.close()
    return {
        "success": True,
        "tier": device.tier,
        "expires_at": device.subscription_expiry.isoformat(),
        "config": config,
    }
```

**Important:** Migration does NOT modify the device record. Same tier, same expiry date, same device ID. Only the server address in the config changes.

### 7.3 Request Model

```python
class MigrateRequest(BaseModel):
    device_id: str
```

---

## 8. Day-2 Operations — Adding & Removing Workers

### 8.1 When to Add a Worker

The collector's output tells you. Signs you need more capacity:
- The lowest score in the pool consistently exceeds 40 during peak hours
- Any single metric (CPU, memory, connections) exceeds 60% on multiple workers
- The orchestrator itself is scoring poorly and absorbing too many new clients

### 8.2 How to Add a Worker

1. **Provision** a new Ubuntu 24.04 VPS at a different provider than your existing VPSes
2. **Run the base implementation guide** (Sections 2-6, 10-11 of `01-MASTER-IMPLEMENTATION.md`) — all four protocols, kernel tuning, NAT, nginx fallback, firewall, fail2ban, unattended-upgrades
3. **Install the load agent** (Section 3 of this document)
4. **Open UFW port 9099** to the orchestrator's IP only:
   ```bash
   ufw allow from <ORCHESTRATOR_IP> to any port 9099 proto tcp
   ```
5. **Verify** from the orchestrator: `curl http://<new-worker-hostname>:9099/` should return JSON
6. **Add one line** to `/opt/load-balancer/workers.conf` on the orchestrator:
   ```
   new-worker.yourdomain.com:9099
   ```
7. **Wait 60 seconds** for the next collector cycle
8. **Check** `journalctl -t load-collector --since "2 min ago"` — the new worker should appear in the poll results
9. **Done.** The next activation will consider the new worker.

**What does NOT change:** No existing clients need to reconnect. No configs regenerated. No databases migrated. No other workers restarted. The new worker simply joins the pool.

### 8.3 How to Remove a Worker

1. **Edit** `/opt/load-balancer/workers.conf` on the orchestrator
2. **Comment out** the worker's line (add `#` at the start) or delete it
3. The collector stops polling it within 60 seconds
4. New clients stop being assigned to it
5. Existing clients on that worker keep working — they're not kicked off
6. Over weeks, their subscriptions expire naturally, or they migrate manually

**Graceful decommissioning:** If you want to cleanly remove a worker while it still has clients, leave it in the pool (commented out) for 30 days. After 30 days, all non-renewed subscriptions on it have expired. Then you can shut it down.

---

## 9. Failure Mode Reference

### Worker Dies Completely

| What | Details |
|------|---------|
| Collector sees | HTTP timeout after 5s, logs warning, skips worker |
| New clients | Assigned to next-best reachable worker |
| Existing clients on dead worker | Lose internet |
| Recovery (automatic) | Client detects sustained failure, calls `/migrate`, reconnects — ~20-30 sec |
| Recovery (manual) | User contacts you, you issue a new config or they call `/migrate` manually |

### Orchestrator Dies

| What | Details |
|------|---------|
| Collector | Stops running (was on the orchestrator) |
| Existing clients | **Unaffected** — already connected directly to their workers |
| New activations | **Down** — API is on the orchestrator |
| Migrations | **Down** |
| Recovery | Restore orchestrator or promote a worker (Section 10) |

### Both Orchestrator and a Worker Die

| What | Details |
|------|---------|
| Clients on surviving workers | Unaffected |
| Clients on dead worker | Offline |
| Activations | Down until orchestrator is restored |
| Recovery | Promoted worker becomes new orchestrator within 15-30 min |

### Network Partition (Orchestrator Can't Reach Workers)

| What | Details |
|------|---------|
| Collector sees | All workers time out |
| Temp file | NOT overwritten — keeps last known good worker |
| Activations | Still work — use stale temp file |
| New clients | Assigned to last known worker; may fail to connect if partition persists |
| Recovery | Fix networking between VPSes. If using private networking (VLAN/VPC), consider switching to public internet for collector traffic. |

---

## 10. Orchestrator SPOF Mitigation

The orchestrator is the single point of failure for *new activations and migrations*. Existing clients are unaffected.

### 10.1 Hot Standby (Recommended)

Run a second copy of the activation API on another worker. Use Cloudflare Load Balancer (free tier, 2 origins) with health checks on `api.yourdomain.com`:

```
Cloudflare LB → Primary orchestrator (api on port 443)
              → Standby worker (identical API, shared or synced DB)
```

The standby needs:
- A copy of the activation DB (synced every 5 minutes via `rsync` or `scp` from the primary)
- The activation API installed and running
- `/opt/load-balancer/workers.conf` (same file as the primary)
- The collector running (so it can take over if the primary dies)

If the primary dies, Cloudflare LB flips to the standby within 2-3 minutes. The standby already has the collector running and a recent copy of the DB.

### 10.2 Cold Standby / Worker Promotion

If you didn't set up a hot standby, promote any surviving worker:

1. Copy the latest backup DB from your backup location to the worker:
   ```bash
   scp /root/vpn-backups/vpn-backup-latest.tar.gz root@surviving-worker:/tmp/
   ssh root@surviving-worker "cd / && tar xzf /tmp/vpn-backup-latest.tar.gz"
   ```
2. Install the activation API on it (Section 9 of `01-MASTER-IMPLEMENTATION.md`)
3. Install the collector (Section 4 of this document)
4. Create `workers.conf` (remove the dead orchestrator, add the new one)
5. Start the API and collector
6. Update Cloudflare DNS: point `api.yourdomain.com` to the promoted worker's IP
7. Recovery time: ~15-30 minutes from backup

### 10.3 Frequent Activation DB Backups

The 5-minute health check script should include an activation DB backup:
```bash
# In vpn-health-check.sh or a separate cron job
cp /opt/activation-api/activation.db /root/vpn-backups/activation-$(date +%H%M).db
```
This ensures you lose at most 5 minutes of activation data if the orchestrator dies without a recent backup.

---

## 11. Integration With the Base Implementation Guide

### 11.1 What Changes in `01-MASTER-IMPLEMENTATION.md`

**Section 9 (Activation API) — Three changes:**

1. Config generators accept a `server` parameter instead of using `os.getenv("VPS_DOMAIN")`
2. `activate()` endpoint reads `/tmp/least_loaded_vps.json` with fallback
3. New `POST /api/v1/migrate` endpoint

**Section 1.2 (Variables) — One addition:**
```bash
# ===== ORCHESTRATOR MODE =====
# Set to "true" if this VPS is the orchestrator (runs collector + activation API)
# Set to "false" for pure workers (just protocols + load agent)
ORCHESTRATOR_MODE="true"   # Only ONE VPS should have this set to true
```

When `ORCHESTRATOR_MODE=false`, the activation API, collector, and `workers.conf` are not installed. The VPS runs only protocols, NAT, nginx, and the load agent.

### 11.2 What Every VPS Runs (Worker or Orchestrator)

| Component | Worker | Orchestrator |
|-----------|--------|--------------|
| All 4 protocol backends | ✅ | ✅ |
| iptables NAT | ✅ | ✅ |
| Nginx fallback | ✅ | ✅ |
| Security hardening | ✅ | ✅ |
| Load agent (:9099) | ✅ | ✅ |
| Activation API | ❌ | ✅ |
| Collector (cron) | ❌ | ✅ |
| `/opt/load-balancer/workers.conf` | ❌ | ✅ |
| Marzban panel | ❌ | ✅ (or separate) |

---

## 12. Client App Contract — What the App Needs from the API

The app specifications have not been finalized yet. This section documents the **API contract** — what the server provides, which any client app can call. The app implementation is a separate concern.

### 12.1 Activation

```
POST https://api.<domain>/api/v1/activate
Body: {"activation_code": "VPN-XXXXXXXX", "device_id": "<unique>", "device_name": "..."}
Response: {"success": true, "tier": "premium", "expires_at": "...", "config": {...}}
```

The returned `config` object contains all protocol settings (server hostname, port, auth tokens, protocol-specific parameters). The app imports this config and connects directly to the assigned worker.

### 12.2 Migration (When a Worker Dies)

```
POST https://api.<domain>/api/v1/migrate
Body: {"device_id": "<unique>"}
Response: {"success": true, "tier": "premium", "expires_at": "...", "config": {...}}
```

Same response shape as activation. The only difference: `config.server` now points to a different (live) worker. The app replaces its old config and reconnects. No subscription data changes.

### 12.3 App Requirements (Implementation-Agnostic)

Whatever client app you build or modify should:
- Call `/activate` on first launch with a user-entered code and a persistent device ID
- Store the returned config and connect to the specified server
- If the connection fails for a sustained period (not a brief blip), call `/migrate` to get a new server assignment
- Handle the case where `/migrate` returns the same worker (if it came back online) or a different one (if it's dead)

The specific client — whether a de-branded Hiddify fork, a custom Flutter app, or something else — is out of scope for this server-side document.

---

## Appendix: Complete File Inventory

| File | Machine | Purpose |
|------|---------|---------|
| `/opt/load-balancer/load-agent.py` | Every VPS | HTTP server on :9099 reporting system metrics |
| `/opt/load-balancer/collector.py` | Orchestrator only | Polls workers, scores them, writes temp file |
| `/opt/load-balancer/workers.conf` | Orchestrator only | List of worker hostnames, one per line |
| `/tmp/least_loaded_vps.json` | Orchestrator only | Written by collector, read by activation API |
| `/etc/systemd/system/load-agent.service` | Every VPS | systemd unit for load agent |
| `/etc/cron.d/load-collector` | Orchestrator only | Cron entry for collector (every 60s) |

---

*Document prepared for the scale-out phase. Deploy the base single-VPS architecture first (01-MASTER-IMPLEMENTATION.md), validate it works for your initial clients, then add the orchestrator layer when you need to scale beyond one or two VPSes.*
