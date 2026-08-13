# MyVPN — Secure School VPN

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
│  │        MyVPN Desktop App (Go + Wails)      │   │
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
│              │  device (myvpn0)│   TUN routes all  │
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

Only two binaries ship on the client: `myvpn` (desktop app, Wails + Vue 3) and
`sing-box` (tunnel engine). There is no SOCKS5 proxy layer, no separate TUN
helper service — sing-box creates the TUN interface directly (BYOD machines
give users admin rights).

---

## The Three Tiers

| Tier | Price | Port | Server CC | Bandwidth | UDP | Experience |
|------|-------|:----:|:---------:|:---------:|:---:|------------|
| **Eco** | $2/mo | 8443 | BBR (system) | 5 Mbps (tc capped) | ❌ | Text loads, video buffers. Exists to sell Stealth. |
| **Stealth** | $4/mo | 8444 | BBR (system) | 100 Mbps (tc capped) | ❌ | Fast streaming. BBR keeps bufferbloat low. |
| **Strike** | $8/mo | 8445 | BBR (system) | 200 Mbps (tc capped) | ✅ | Gaming (33-44ms latency). 4K streaming. |

---

## Project Layout

```
VPN-Service/
├── v5/                       ← DEFINITIVE VERSION (start here)
│   ├── client/               ← CLIENT: Go 1.22 + Wails v2 + Vue 3 desktop app (v2.0.0)
│   │   ├── main.go           ← Wails entry point (binds App, embeds frontend/dist)
│   │   ├── app.go            ← App struct — wraps internal/ for the Vue UI
│   │   ├── internal/         ← 6 independent packages (activation, heartbeat,
│   │   │                        manager, storage, tunnel, updater)
│   │   ├── frontend/         ← Vue 3 + Vite + TypeScript UI (embedded into binary)
│   │   ├── engines/          ← sing-box binary placeholder
│   │   └── Makefile          ← dev / build / build-all targets
│   ├── server/               ← SERVER: VPS setup modules + PocketBase hooks
│   ├── docs/                 ← Architecture, deploy, ops, API, fixes
│   │   └── history/          ← Curated pre-V5 research (business model, N4L threat analysis)
│   ├── README.md             ← V5 overview
│   └── CONTEXT.md            ← Full agent/developer context
├── v4/                       ← PREVIOUS client (Go + Fyne, reference only)
├── scripts/                  ← Operational tooling
│   ├── generate_codes.sh     ── Generate Luhn-mod-N activation codes
│   └── print_codes.sh        ── Printable PDF code cards
└── .github/workflows/        ← CI/CD
    └── build.yml             ── Build + release for Linux + Windows (Wails)
```

---

## How It Works

### Network Protocol

The VPN uses **Shadowsocks TCP** — a simple, fast tunnel protocol that encrypts traffic with AES-256-GCM. Unlike TLS-based proxies (Trojan, Xray VLESS), Shadowsocks has no TLS handshake or certificate exchange, so it bypasses N4L's JA3 fingerprinting.

All traffic goes through a single TCP connection per tier. The client runs **sing-box**, which creates a **TUN interface** (`myvpn0`, `10.0.0.1/30`) and routes all device traffic through it. sing-box encrypts everything with Shadowsocks and sends it to the VPS. No SOCKS5 proxy layer, no per-app configuration.

### Tiers & Congestion Control

All three tiers use **BBR** (Bottleneck Bandwidth and Round-trip propagation time), Linux's default CC — fair, stable, and bufferbloat-friendly. (An earlier design used the `tcp-brutal` kernel module for Stealth, but its aggressive rate-filling caused bufferbloat and jitter on the school network, so it was removed — see `v5/docs/FIXES.md`.) Bandwidth is capped purely with `tc` HTB classes on the VPS:

- **Eco**: 5 Mbps (class 1:10, port 8443)
- **Stealth**: 100 Mbps (class 1:20, port 8444)
- **Strike**: 200 Mbps (class 1:30, port 8445)

### Activation Flow

1. User enters code in the app: `MYVPN-A7X3-K9M2-Q5P1-C`
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
Manual:     --revert flag → restore from .myvpn-backups/
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
# Copy to VPS
scp -r v5/server root@your-vps:/root/

# Run setup (takes 10-15 minutes)
ssh root@your-vps
DOMAIN=networkingguides.duckdns.org ./v5/server/setup.sh
```

This provisions: BBR, 3× Shadowsocks, tc shaping (Eco 5 / Stealth 100 / Strike 200 Mbps), Caddy + TLS, PocketBase, B2 backups, UFW firewall. See `v5/docs/DEPLOY.md`.

### Step 2: Configure PocketBase

1. Visit `https://networkingguides.duckdns.org/_/` — create admin account
2. Create collections: `codes`, `tier_configs`, `activation_attempts`
3. Create `update_config` collection for staged rollouts
4. Set `admin_api_token` in PocketBase app settings
5. Upload JS hooks from `v5/server/pb_hooks/`
6. Seed tier configs with passwords from `/root/.tier_passwords`

### Step 3: Generate & Print Codes

```bash
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_TOKEN eco 50
./scripts/print_codes.sh eco-codes.txt eco-cards.pdf
```

### Step 4: Build Client App

```bash
cd v5/client
make build          # current platform only
make build-all      # Linux + Windows
```

Or push a `v*` tag (e.g. `v2.0.0`) — GitHub Actions builds, bundles, and releases
both platform zips automatically (`myvpn` + `sing-box` per platform).

### Step 5: Install & Test

1. Extract the platform zip
2. Launch `myvpn` (Windows: `myvpn.exe`)
3. Test activation, connection, heartbeat, update

No admin install step is needed — the app runs as a normal user and sing-box
creates the TUN interface with the user's own admin rights (BYOD).

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
/usr/local/bin/myvpn-backup.sh

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

---

## Real-World Testing

The server modules were tested on a Voyager VPS (Ubuntu 22.04, kernel 5.15.0-161-generic):

| Module | Status | Notes |
|--------|:------:|-------|
| 00-env | ✅ | OS, arch, root, disk, memory all validated |
| 01-bbr | ✅ | BBR active, TCP tuning params set |
| 02-shadowsocks | ✅ | 3 instances installed and enabled |
| 04-tc | ✅ | Eco 5Mbit, Stealth 100Mbit, Strike 200Mbit classes active |
| 05-caddy | ✅ | Custom build with ratelimit plugin, Caddyfile validated |
| 06-pocketbase | ✅ | 0.22.21 installed, health check passing |
| 07-backups | ✅ | Script installed, timer enabled (B2 credentials needed) |
| 08-firewall | ✅ | UFW active, all ports open, SSH rate-limited |

All 3 Shadowsocks services active (8443/8444/8445). All tiers on BBR with tc caps (5/100/200 Mbps).
Caddy + PocketBase serving API at `https://domain/_/`.

See `v5/README.md` and `v5/CONTEXT.md` for the full current documentation.
