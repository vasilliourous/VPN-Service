# Modular VPS Setup — MyVPN

## Design Philosophy

The original VPS setup script was a single 280-line bash file. This modular
design splits it into independent, idempotent modules — each doing one thing
well. The goals:

1. **Instant VPS switching** — Spin up a new VPS, run `restore.sh`,
   point DNS at it, and you're live in minutes. Great for disaster recovery,
   provider migration, or scaling to multiple schools.

2. **Idempotent modules** — Each module can be re-run safely. If `03-brutal.sh`
   fails because kernel headers are missing, fix the issue and re-run just that
   module. The other 7 modules don't care.

3. **Testability** — Each module is a pure function of the system state.
   Test them in isolation on a throwaway VPS.

4. **Readability** — A module named `02-shadowsocks.sh` installs Shadowsocks.
   You don't need to read 280 lines of script to understand the structure.

---

## Architecture

```
setup.sh ──┬── modules/00-env.sh        # Environment validation
           ├── modules/01-bbr.sh        # BBR + kernel tuning
           ├── modules/02-shadowsocks.sh # ssserver × 3 instances
           ├── modules/03-brutal.sh     # tcp-brutal kernel module + LD_PRELOAD wrapper
           ├── modules/04-tc.sh         # Traffic shaping (Eco 5M, Strike 200M)
           ├── modules/05-caddy.sh      # Caddy + rate limiting plugin
           ├── modules/06-pocketbase.sh # PocketBase + JS hooks
           ├── modules/07-backups.sh    # B2 hourly backups
           └── modules/08-firewall.sh   # UFW rules

restore.sh ─── Restores PocketBase from B2 backup onto a fresh VPS
```

---

## Module Details

### 00-env.sh — Environment Validation

Validates prerequisites before any provisioning:
- **OS check**: Ubuntu 22.04+ (required for kernel headers and package compatibility)
- **Architecture**: x86_64 only (shadowsocks-rust binaries are x86_64)
- **Root check**: Must run as root
- **Domain**: `DOMAIN` env var must be set
- **DNS resolution**: Warns if domain doesn't resolve (setup still works)
- **Disk space**: Minimum 2GB free
- **Memory**: Minimum 1GB (2GB recommended)
- **Existing installation**: Checks for previous installation (modules handle idempotently)

### 01-bbr.sh — BBR + Kernel Tuning

Enables BBR as the system default congestion control algorithm:
- Loads `tcp_bbr` kernel module
- Persists module load via `/etc/modules-load.d/tcp_bbr.conf`
- Sets `net.core.default_qdisc = fq` (fair queueing required by BBR)
- Sets `net.ipv4.tcp_congestion_control = bbr`
- Persists via `/etc/sysctl.d/90-bbr.conf`
- **Additional TCP optimizations** in `/etc/sysctl.d/91-tcp-tune.conf`:
  - Buffer auto-tuning: rmem/wmem up to 128MB
  - TCP Fast Open (client + server)
  - Reduced TIME_WAIT (`tcp_fin_timeout = 15`)
  - Increased backlog (`netdev_max_backlog = 5000`, `somaxconn = 4096`)
  - Keepalive: 300s interval, 5 probes, 15s between probes

### 02-shadowsocks.sh — Shadowsocks Server × 3 Instances

Downloads and installs `shadowsocks-rust` ssserver, creates three instances:

| Instance | Port | Mode | Service File |
|----------|:----:|:----:|--------------|
| Eco | 8443 | TCP only | `shadowsocks-eco.service` |
| Stealth | 8444 | TCP only (+ Brutal LD_PRELOAD) | `shadowsocks-stealth.service` |
| Strike | 8445 | TCP + UDP | `shadowsocks-strike.service` |

Key details:
- Downloads the latest release from GitHub (falls back to API if version fails)
- Generates random 32-char hex passwords stored in `/root/.tier_passwords` (chmod 600)
- Configs stored in `/etc/shadowsocks/{eco,stealth,strike}.json`
- Creates Brutal LD_PRELOAD drop-in at `/etc/systemd/system/shadowsocks-stealth.service.d/brutal.conf`
- Services are enabled but NOT started until the orchestrator runs (Brutal module must complete first)

### 03-brutal.sh — TCP Brutal Kernel Module + LD_PRELOAD Wrapper

**IMPORTANT**: Brutal CC runs on the SERVER only. The client OS is irrelevant — it uses its default TCP stack and receives data faster.

This module:
1. Clones the [tcp-brutal](https://github.com/apernet/tcp-brutal) kernel module (repo was renamed from `apernet/tcp-brutal-ng` which returns 404)
2. Builds the kernel module with `KDIR=/lib/modules/$(uname -r)/build`
3. Compiles a custom LD_PRELOAD wrapper from bundled C source (`/usr/local/src/brutal-wrap.c`)
4. Loads the module and registers "brutal" as an available congestion control
5. Registers with DKMS for auto-rebuild on kernel updates
6. Persists module load on boot via `/etc/modules-load.d/tcp_brutal.conf`

**LD_PRELOAD wrapper** (`brutal-wrap.c`):
- Intercepts `accept()` and `accept4()` system calls
- On each accepted connection, sets `TCP_CONGESTION=brutal` and `TCP_BRUTAL_PARAMS` with target rate (48 Mbps)
- Necessary because ssserver does not set TCP congestion control explicitly (uses system default)
- The wrapper is compiled from source bundled in the module — no external dependencies

**If the wrapper fails to compile**, the Stealth tier falls back to BBR gracefully.
The LD_PRELOAD drop-in is removed so the service starts cleanly.

**Target rate**: 48 Mbps (configurable via `TARGET_RATE_MBITS` variable in the module)

### 04-tc.sh — Traffic Shaping

Applies bandwidth caps using tc HTB (Hierarchical Token Bucket) qdisc:

| Tier | Port | Class | Rate | Enforcement |
|------|:----:|:-----:|:----:|-------------|
| Eco | 8443 | 1:10 | 5 Mbps | Source port match |
| Strike | 8445 | 1:30 | 200 Mbps | Source port match |

Stealth has no tc cap — Brutal CC manages its own rate via the kernel module.

**Architecture**: Uses a dedicated helper script at `/usr/local/bin/myvpn-tc-apply.sh` to avoid shell escaping bugs in systemd `ExecStart` directives. The helper is called by systemd oneshot services:
- `tc-eco-cap.service` (runs before `shadowsocks-eco.service`)
- `tc-strike-cap.service` (runs before `shadowsocks-strike.service`)

### 05-caddy.sh — Caddy Reverse Proxy

Downloads and configures Caddy with the `caddy-ratelimit` plugin:

**Installation**:
1. Installs base Caddy from official APT repo (for systemd integration)
2. Checks if `http.handlers.rate_limit` module exists
3. If missing, downloads a pre-built Caddy from `caddyserver.com/api/download?p=github.com/mholt/caddy-ratelimit`
4. Replaces the binary with the custom build

**Caddyfile structure**:
- Global: `order rate_limit after basic_auth` (directive ordering)
- Site-level: `rate_limit` handler directive with three zones:
  - `activation_zone`: 5 events per 10m per IP (rate limits activation attempts)
  - `heartbeat_zone`: 1 event per 10s per IP (prevents heartbeat abuse)
  - `default_zone`: 100 events per 10s per IP (general rate limit)
- Security headers: `-Server`, `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`
- Reverse proxies to PocketBase at `127.0.0.1:8090`
- TLS via Let's Encrypt (auto-certificate management)
- Forces HTTPS automatically

**Important Caddyfile syntax notes**:
- `rate_limit` is a **handler directive**, NOT a global option
- Each zone can have only ONE `key` directive
- Use `{remote_host}` for IP-based key (most compatible)
- `alps` is NOT a valid TLS subdirective (removed in recent Caddy versions)

### 06-pocketbase.sh — PocketBase Admin Backend

Installs and configures PocketBase as the admin backend:

- Downloads PocketBase v0.22.21 for linux_amd64
- Creates dedicated `pocketbase` system user
- Directory structure: `/opt/pocketbase/{pocketbase, pb_data, pb_hooks}`
- Deploys JS hooks from `modular-vps/pb_hooks/`
- Creates systemd service (`pocketbase.service`) with security hardening:
  - `NoNewPrivileges=true`, `PrivateTmp=true`, `ProtectHome=true`, `ProtectSystem=full`
- Initializes SQLite with WAL mode via a JS pragma hook:
  - `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`, `foreign_keys=ON`
- Binds to `127.0.0.1:8090` (only accessible through Caddy reverse proxy)

**Prerequisites**: `unzip` and `wget` (automatically installed if missing)

### 07-backups.sh — Backblaze B2 Backups

Sets up hourly offsite backups of the PocketBase SQLite database:

**Backup script** (`/usr/local/bin/myvpn-backup.sh`):
1. Creates a safe SQLite backup using `.backup` command (safe for live databases)
2. Compresses with gzip
3. Generates SHA256 checksum
4. Uploads to Backblaze B2
5. Uploads SHA256 file for verification
6. Verifies upload by downloading and checking checksum

**Systemd timer**: `pocketbase-backup.timer` — runs hourly with 5min randomized delay.
Companion service: `pocketbase-backup.service`

**Credentials**: Stored in `/root/.b2-creds` (sourced with `set -a` for auto-export):
```
B2_APPLICATION_KEY_ID=your_key_id
B2_APPLICATION_KEY=your_key
B2_BUCKET=your-bucket-name
```

**B2 lifecycle rule**: Set 7-day retention on the bucket to auto-prune old backups.

### 08-firewall.sh — UFW Firewall

Configures Uncomplicated Firewall with the following rules:

| Direction | Protocol | Port | Purpose |
|:---------:|:--------:|:----:|---------|
| In | TCP | 22 | SSH (rate-limited: 6 conn/30s/IP) |
| In | TCP | 80 | HTTP (Let's Encrypt validation) |
| In | TCP | 443 | HTTPS (Caddy) |
| In | TCP | 8443 | Shadowsocks Eco |
| In | TCP | 8444 | Shadowsocks Stealth |
| In | TCP | 8445 | Shadowsocks Strike (TCP) |
| In | UDP | 8445 | Shadowsocks Strike (UDP) |

Default policy: deny incoming, allow outgoing.

---

## Flow: Fresh VPS

```mermaid
flowchart LR
    A[Start] --> B[00-env: Validate]
    B --> C[01-bbr: BBR + sysctl]
    C --> D[02-shadowsocks: 3 instances]
    D --> E[03-brutal: Kernel module]
    E --> F[04-tc: Traffic shaping]
    F --> G[05-caddy: Reverse proxy]
    G --> H[06-pocketbase: DB + hooks]
    H --> I[07-backups: B2 cron]
    I --> J[08-firewall: UFW]
    J --> K[Done]
```

## Flow: Switch to a New VPS

```mermaid
flowchart LR
    A[Run restore.sh] --> B[Provision VPS]
    B --> C[setup.sh (all modules)]
    C --> D[restore PocketBase from B2]
    D --> E[Update DNS → new IP]
    E --> F[Old VPS: stop + snapshot]
    F --> G[Done]
```

DNS propagation is the only downtime. The restore completes before you switch
the DNS record, so all clients connect seamlessly once DNS resolves.

---

## Quick Start

### Fresh Setup

```bash
# 1. Copy the entire modular-vps directory to your VPS
scp -r modular-vps root@your-vps:/root/

# 2. SSH in and run the orchestrator
ssh root@your-vps
DOMAIN=networkingguides.duckdns.org ./modular-vps/setup.sh
```

Or in one command:

```bash
scp -r modular-vps root@your-vps:/root/ && \
ssh root@your-vps "DOMAIN=networkingguides.duckdns.org /root/modular-vps/setup.sh"
```

### Setup With Backup Restore (VPS Switch)

```bash
scp -r modular-vps root@your-vps:/root/
ssh root@your-vps "DOMAIN=networkingguides.duckdns.org \
  B2_APPLICATION_KEY_ID=your_key \
  B2_APPLICATION_KEY=your_key \
  B2_BUCKET=my-vpn-backup-bucket \
  /root/modular-vps/restore.sh"
```

Omitting `BACKUP_PATH` auto-selects the latest backup in the bucket.

### Run a Single Module

```bash
scp modular-vps/modules/04-tc.sh root@your-vps:/root/modular-vps/modules/
ssh root@your-vps "DOMAIN=networkingguides.duckdns.org /root/modular-vps/modules/04-tc.sh"
```

Useful for re-applying traffic shaping after a network interface change,
re-running the Brutal module after a kernel update, etc.

---

## Module Contract

Each module:
1. Is **idempotent** — running it twice produces the same result
2. Exits with code 0 on success, non-zero on failure
3. Logs its actions with `[module-name]` prefix to stdout/stderr
4. Uses `DOMAIN` environment variable (validated by 00-env.sh)
5. Does not depend on other modules having run (except 00-env)

The orchestrator (`setup.sh`) runs modules in order and stops on first failure
unless `FORCE=1` is set.

---

## Real-World Testing

Tested on a Voyager VPS (Ubuntu 22.04.5 LTS, kernel 5.15.0-161-generic).

### Critical Fixes Discovered During Testing

| Issue | Finding | Fix |
|-------|---------|-----|
| **Dead repo URL** | `apernet/tcp-brutal-ng` returns 404 | Updated to `apernet/tcp-brutal` |
| **Missing LD_PRELOAD source** | New repo has no `brutal-wrap.c` | Bundled custom wrapper in module |
| **Wrong setsockopt intercept** | Old wrapper intercepted `setsockopt(TCP_CONGESTION)` but ssserver never calls it | New wrapper intercepts `accept()` instead |
| **Caddy without ratelimit** | APT repo Caddy lacks `http.handlers.rate_limit` | Download pre-built from caddyserver.com API |
| **Wrong Caddyfile syntax** | `rate_limit_zones` not a global option; `alps` not valid; multiple keys per zone | Fixed syntax per plugin docs |
| **Missing unzip** | PocketBase download failed without `unzip` | Added apt dependency install |
| **Git non-interactive** | `git clone` prompts for credentials in non-interactive SSH | Added `GIT_TERMINAL_PROMPT=0` |

### Verified Services

| Service | Status | Port |
|---------|:------:|:----:|
| Caddy | ✅ active | 80/443 → 127.0.0.1:8090 |
| PocketBase | ✅ active | 127.0.0.1:8090 |
| shadowsocks-eco | ✅ active | 0.0.0.0:8443 |
| shadowsocks-stealth | ✅ active (Brutal LD_PRELOAD) | 0.0.0.0:8444 |
| shadowsocks-strike | ✅ active | 0.0.0.0:8445 |
| TCP Brutal | ✅ loaded | kernel module |
| UFW | ✅ active | all rules in place |

---

## Backups & Restore

### Backup Chain

```
PocketBase SQLite DB
  → hourly cron: pb_data/data.db → compressed + B2 upload
  → 7-day retention (B2 lifecycle rules)
  → Integrity check on upload (SHA256 manifest)
```

### Restore Chain

```
restore.sh:
  1. Run setup.sh (provisions full VPS)
  2. Download latest backup from B2
  3. Verify SHA256 checksum
  4. Stop PocketBase
  5. Replace data.db
  6. Start PocketBase
  7. Verify collections are intact
```

### Switching VPS Providers

1. Create new VPS at Hetzner/DigitalOcean/Vultr
2. Point DNS to new IP temporarily (TTL 60s)
3. Run `restore.sh` on new VPS
4. Verify services are healthy
5. Update permanent DNS record
6. Snapshot old VPS, then destroy

Total downtime: ~2-5 minutes (DNS propagation).
