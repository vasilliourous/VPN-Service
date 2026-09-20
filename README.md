# Locus — Secure School VPN

A commercial VPN service for students at N4L-managed NZ schools (Macleans College).
Bypasses N4L's Palo Alto firewall using Shadowsocks TCP (no TLS fingerprinting, no UDP blocks).

> **The authoritative version of everything below is `v5/`** — hardened client
> (`v5/client/`, Go + Wails + Vue 3), server deployment (`v5/server/`), and docs
> (`v5/docs/`, `v5/CONTEXT.md`). Older directories (`v4/`) are historical
> reference only. Directories removed in the 2026-08 cleanup
> (`v3/`, `simplified/`, `v5/legacy/`, `modular-vps/`, root `CONTEXT.md`,
> `v5/scripts/`, and the un-curated remainder of `originals/`) are recoverable
> from git history. The still-relevant originals (business model + N4L threat
> research) are preserved in `v5/docs/history/`.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────┐
│              STUDENT'S LAPTOP                     │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │        Locus Desktop App (Go + Wails)      │   │
│  │         (Vue 3 UI embedded in binary)       │   │
│  │                                             │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  │   │
│  │  │Activation│  │ Heartbeat │  │ Updater   │  │   │
│  │  │ Client   │  │ 5min→2h   │  │ 2-phase   │  │   │
│  │  └────┬─────┘  └────┬─────┘  └─────┬────┘  │   │
│  │       │              │              │        │   │
│  │  ┌────┴──────────────┴──────────────┴────┐   │   │
│  │  │         Manager (sing-box)            │   │   │
│  │  │   Generates config, spawns process    │   │   │
│  │  └─────────────────┬────────────────────┘   │   │
│  └────────────────────┼────────────────────────┘   │
│                       │                            │
│              ┌────────┴────────┐                   │
│              │  sing-box TUN   │  (no SOCKS5 —     │
│              │  device (locus0)│   TUN routes all  │
│              │                 │   device traffic) │
│              └─────────────────┘                   │
└───────────────────────┼────────────────────────────┘
                        │ Shadowsocks TCP
                        ▼
┌─────────────────────────────────────────────────┐
│              VPS (Ubuntu 22.04)                   │
│                                                   │
│  Caddy (TLS + rate_limit) ←──→ PocketBase        │
│       │                              │            │
│       │                         ┌────┴────┐       │
│       │                         │ JS Hooks│       │
│       │                         │  • Act. │       │
│       │                         │  • HB   │       │
│       │                         └─────────┘       │
│  ┌────┴────┐  ┌──────────┐  ┌──────────┐          │
│  │ Eco     │  │ Stealth  │  │ Strike   │          │
│  │ :8443   │  │ :8444    │  │ :8445    │          │
│  │ BBR     │  │ BBR      │  │ BBR+UDP  │          │
│  │ 5M tc   │  │ 100M tc  │  │ 200M tc  │          │
│  └─────────┘  └──────────┘  └──────────┘          │
│                                                   │
│  Backups → Backblaze B2 (hourly, 7-day retention)  │
└─────────────────────────────────────────────────┘
```

Only two binaries ship on the client: `locus` (desktop app, Wails + Vue 3) and
`sing-box` (tunnel engine). There is no SOCKS5 proxy layer, no separate TUN
helper service — sing-box creates the TUN interface directly (BYOD machines
give users admin rights).

---

## The Three Tiers

| Tier | Price | Port | Server CC | Bandwidth | UDP | Experience |
|------|-------|:----:|:---------:|:---------:|:---:|------------|
| **Eco** | $2/mo | 8443 | BBR (system) | 5 Mbps (tc capped) | ❌ | Text loads, video buffers. Exists to sell Stealth. |
| **Stealth** | $4/mo | 8444 | BBR (system) | 100 Mbps (tc capped) | ❌ | Fast streaming. BBR keeps bufferbloat low. |
| **Strike** | $8/mo | 8445 | BBR (system) | 200 Mbps (tc capped) | ✅ raw (UoT planned 8446) | Gaming. 4K streaming. |

> All tiers are **BBR + tc**: no kernel modules to maintain, caps enforced with
> HTB classes plus `fq_codel` leaf qdiscs to keep latency flat under load.
> Strike carries **raw** UDP today; the UDP-over-TCP endpoint (port 8446) is
> built but not enabled on the current host — see `v5/docs/GAMING-UDP.md`.

Activation codes are **`RQ-XXXX-XXXX-XXXX-C`** (15 chars, Luhn-mod-N checksum,
charset `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`). The `MYVPN-` form was retired in the
2026-08-17 migration and no live code uses it.

---

## Project Layout

```
VPN-Service/
├── v5/                       ← DEFINITIVE VERSION (start here)
│   ├── client/               ← CLIENT: Go 1.22 + Wails v2 + Vue 3 desktop app
│   │   ├── main.go           ← Wails entry point (binds App, embeds frontend/dist)
│   │   ├── app.go            ← App struct — wraps internal/ for the Vue UI
│   │   ├── internal/         ← 8 packages (activation, heartbeat, manager, pinned,
│   │   │                        storage, tray, tunnel, updater)
│   │   ├── frontend/         ← Vue 3 + Vite + TypeScript UI (embedded into binary)
│   │   ├── engines/          ← sing-box binary placeholder
│   │   └── Makefile          ← dev / build / build-all targets
│   ├── server/               ← SERVER: VPS setup modules + PocketBase hooks
│   │   ├── scripts/          ← seed, smoke-test, publish-release, deploy-console
│   │   └── pb_hooks/         ← activation, heartbeat, code-lookup, unbind, console
│   ├── console/              ← ADMIN: Vue 3 SPA served at /admin/
│   ├── docs/                 ← Architecture, deploy, ops, API, fixes
│   │   └── history/          ← Curated pre-V5 research (business model, N4L threat analysis)
│   ├── VERSION               ← THE client version (single source of truth)
│   ├── README.md             ← V5 overview
│   └── CONTEXT.md            ← Full agent/developer context
├── v4/                       ← PREVIOUS client (Go + Fyne, reference only)
├── scripts/                  ← Operational tooling
│   ├── generate_codes.sh     ── Generate Luhn-mod-N activation codes
│   ├── print_codes.sh        ── Printable PDF code cards
│   └── publish-update.sh     ── Prepare + publish a release payload
└── .github/workflows/        ← CI/CD
    └── build.yml             ── Build + release for Linux + macOS + Windows (Wails)
```

---

## How It Works

### Network Protocol

The VPN uses **Shadowsocks TCP** — a simple, fast tunnel protocol that encrypts traffic with AES-256-GCM. Unlike TLS-based proxies (Trojan, Xray VLESS), Shadowsocks has no TLS handshake or certificate exchange, so it bypasses N4L's JA3 fingerprinting.

All traffic goes through a single TCP connection per tier. The client runs **sing-box**, which creates a **TUN interface** (`locus0`, `10.0.0.1/30`) and routes all device traffic through it. sing-box encrypts everything with Shadowsocks and sends it to the VPS. No SOCKS5 proxy layer, no per-app configuration.

### Tiers & Congestion Control

All three tiers use **BBR** (Bottleneck Bandwidth and Round-trip propagation time), Linux's default CC — fair, stable, and bufferbloat-friendly. (An earlier design used the `tcp-brutal` kernel module for Stealth, but its aggressive rate-filling caused bufferbloat and jitter on the school network, so it was removed — see `v5/docs/FIXES.md`.) Bandwidth is capped purely with `tc` HTB classes on the VPS:

- **Eco**: 5 Mbps (class 1:10, port 8443)
- **Stealth**: 100 Mbps (class 1:20, port 8444)
- **Strike**: 200 Mbps (class 1:30, port 8445)

### Activation Flow

1. User enters code in the app: `RQ-ABCD-EFGH-JKMN-T`
2. **Client-side Luhn-mod-N validation** catches typos instantly
3. App generates a **device fingerprint** (SHA256 of MAC address + disk serial + motherboard UUID)
4. POST to `/api/activate` with `{ code, fingerprint }`
5. Server validates Luhn checksum, looks up code, binds fingerprint
6. Returns tier config with server address, port, password, method
7. App stores config locally and starts the tunnel

### Heartbeat & Grace Period

The app sends a heartbeat every 5 minutes (POST `/api/heartbeat` with
`{ code, fingerprint }`). If the hub is unreachable:
- Interval doubles: 5min → 10min → 20min → ... → 2h max
- VPN keeps working (config is stored locally, no token dependency)
- **7-day grace period** before requiring re-activation

On server response:
- `200 OK`: Normal operation. Resets interval to 5min.
- `403 suspended`: Server-side suspension handling.
- `update_available` field: Triggers staged rollout update.

### Staged Rollouts

Updates are delivered gradually:
1. Server sets `rollout_percent` (0-100) in PocketBase `update_config`
2. Heartbeat response includes `update_available` only if the fingerprint passes the server-side hash gate
3. If eligible, the client downloads the new binary, verifies SHA256, and applies the two-phase update

### Two-Phase Update Safety

```
Normal:     .update-pending → .update-confirmed → done
Crash:      .update-pending → (no .update-confirmed) → auto-revert
Manual:     --revert flag → restore from .locus-backups/
```

The `.update-pending` sentinel is written before swapping the binary. On first successful launch of the new version, `.update-confirmed` is written. If the new version crashes before writing confirmation, the next start detects the orphaned `.update-pending` and auto-reverts.

### Device Fingerprinting

The fingerprint is a deterministic SHA256 hash of hardware identifiers:

1. **Strong**: MAC address + disk serial + motherboard UUID
2. **Medium**: MAC address + motherboard UUID
3. **Weak**: MAC address + hostname + machine_id
4. **Fallback**: Random UUID v4 (persistent for install lifetime)

This binds an activation code to a specific device. If the device is lost or broken, an admin can suspend the code (binding is preserved and can be un-suspended).

---

## Build Order

### Step 1: Deploy Server

```bash
# Copy the server tree AND the age key to the VPS
scp -r v5/server age-key.txt root@your-vps:/root/server/

# Run setup (~10-15 min). Secrets decrypt automatically from secrets.env.age.
ssh root@your-vps "/root/server/setup.sh"
```

This provisions: BBR, 3× Shadowsocks, tc shaping (Eco 5 / Stealth 100 / Strike 200
Mbps, all with fq_codel), the Strike UDP-over-TCP endpoint on :8446, Caddy + TLS
(serving `/admin/` and `/updates/`), PocketBase (+ collections/admin/hooks/tier
configs), the admin console, the release uploader, B2 backups, UFW + fail2ban.

**No follow-up steps.** Verify with `smoke-test.sh` (expect 23 passed / 0 failed).
Note `setup.sh` deploys **from the copy on the VPS**, not from your working tree —
keep `/root/server/` in sync with the repo.

Optional extras:

```bash
# Mint a first batch of codes as part of the deploy (once only)
FIRST_BATCH=50 FIRST_BATCH_MIDDLEMAN=Sarah ssh root@your-vps "/root/server/setup.sh"

# Opt outs
ENABLE_UOT=0      # skip the sing-box UDP-over-TCP endpoint (+ its firewall rule)
SKIP_CONSOLE=1    # skip the admin console (not recommended)
```

The admin console must already be built into a bundle for the deploy to succeed
(`scripts/deploy-console.sh` builds and uploads it); a missing bundle now fails
the deploy loudly rather than leaving `/admin/` 404ing.

### Step 2: Verify PocketBase

`06-pocketbase.sh` and `seed-pb.py` now create the admin, the collections, the
schema and the tier configs automatically — a failure here is **fatal**, not a
warning. To verify or customise by hand, see `v5/docs/POCKETBASE-SETUP.md`
(collections: `codes`, `tier_configs`, `activation_attempts`, `update_config`,
`code_events`).

### Step 3: Deploy the Admin Console

```bash
v5/server/scripts/deploy-console.sh   # builds, uploads, verifies /admin/
```

Day-to-day operations (issuing codes, suspend/unbind, tiers, releases) then
happen at `https://<domain>/admin/` — no SSH required.

### Step 4: Generate & Print Codes

```bash
# Console: Codes & Clients -> Generate. Or from the CLI:
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_PB_ADMIN_JWT eco 50
./scripts/print_codes.sh eco-codes.txt eco-cards.pdf
```

The generator needs the **PocketBase admin JWT** (PB 0.22 rejects
`ADMIN_API_TOKEN` for record access).

### Step 5: Build Client App

```bash
cd v5/client
make build          # current platform only
make build-all      # Linux + Windows + macOS
```

Or push a `v*` tag (e.g. `v2.0.0`) — GitHub Actions builds **4 portable
bundles**, the **Windows installer + macOS disk images**, and the raw
per-platform executables + `manifest.json` + checksums that the auto-updater
consumes. (`locus` + `sing-box` per bundle.)

### Step 6: Install & Test

**Windows:** run `locus-setup-<version>.exe` (installs to `%ProgramFiles%\Locus`).
**macOS:** open the `.dmg`, drag Locus into Applications, then right-click → Open
the first time (the build is unsigned).
**Linux / portable:** extract the platform zip and run `locus` — no installer,
no admin rights.

Windows runs elevated, since TUN creation requires it. Test activation,
connection, heartbeat, and update.

> **Install somewhere the app owns — not Downloads.** A portable copy run out of
> Downloads cannot reliably update itself: Windows will not let the updater
> rename a file that Defender, SmartScreen or the search indexer has open, and a
> freshly written `.exe` in Downloads is opened by one of them within
> milliseconds. The installer exists to remove that failure mode. A portable copy
> is still supported and stages its download privately inside its own directory,
> so extracting to a normal folder (not Downloads) works too. The Diagnostics
> screen reports which mode the running copy is in.

---

## Operations

### Quick Health Check

```bash
ssh root@your-vps "
  systemctl is-active caddy pocketbase shadowsocks-eco shadowsocks-stealth shadowsocks-strike
  tc class show dev eth0 | grep -E '1:10|1:20|1:30'
  sysctl net.ipv4.tcp_congestion_control
  tc -s class show dev eth0 | head -10
"
```

### Backup & Restore

```bash
# Manual backup
/usr/local/bin/locus-backup.sh

# Full VPS restore
DOMAIN=networkingguides.duckdns.org \
  B2_APPLICATION_KEY_ID=xxx \
  B2_APPLICATION_KEY=xxx \
  B2_BUCKET=my-vpn-backup-bucket \
  /root/v5/server/restore.sh
```

### Monitoring

Set up uptime monitoring on `https://networkingguides.duckdns.org/api/health` (UptimeRobot, PingPing, etc.).
This endpoint returns `{"message":"API is healthy.","code":200}` when PocketBase is running.

Day-to-day operations live at `https://networkingguides.duckdns.org/admin/`
(admin token required) — dashboard, codes, releases and tiers. See
`v5/docs/OPS.md`.

---

## Real-World Testing

The server modules were first proven on a Voyager VPS, then **re-proven on a
completely blank box** (2026-09-19) — which found seven further bugs that only
appear on a fresh host. A re-run is not a deploy test.

| Module | Status | Notes |
|--------|:------:|-------|
| 00-env | ✅ | OS, arch, root, disk, memory all validated (memory guard fixed for 512MB droplets) |
| 01-bbr | ✅ | BBR active, TCP tuning params set |
| 02-shadowsocks | ✅ | 3 instances installed and enabled |
| 04-tc | ✅ | Eco 5Mbit, Stealth 100Mbit, Strike 200Mbit classes active (+ fq_codel) |
| 05-caddy | ✅ | Caddy with ratelimit plugin; also serves `/admin/` and `/updates/` |
| 06-pocketbase | ✅ | 0.22.21 installed; bootstrap failure is now fatal, not a warning |
| 07-backups | ✅ | Timer enabled; verification hashes the **downloaded** artifact, restore proven |
| 08-firewall | ✅ | UFW active, all ports open, SSH protected by **fail2ban** |

All 3 Shadowsocks services active (8443/8444/8445). All tiers on BBR with tc caps (5/100/200 Mbps).
Caddy + PocketBase serving the API, the admin console, and release binaries.

**Live host:** `170.64.196.179` (DigitalOcean, Ubuntu 22.04.5, 1 vCPU / 454 MB +
1 GB swap, Sydney) → `networkingguides.duckdns.org`. Earlier hosts are retired.
End state of the blank-box run: all 8 modules exit 0, `setup.sh` re-runs are
idempotent, and `smoke-test.sh` reports **23 passed / 0 failed / 0 warnings**.

> ⚠️ **Live customer data.** The hub holds real activation codes in daily use.
> Never bulk-delete `codes` or `code_events` — suspend or unbind instead.

See `v5/README.md` and `v5/CONTEXT.md` for the full current documentation.
