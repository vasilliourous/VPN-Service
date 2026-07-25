# MyVPN — Secure School VPN

A commercial VPN service for students at N4L-managed NZ schools (Macleans College).
Bypasses N4L's Palo Alto firewall using Shadowsocks TCP (no TLS fingerprinting, no UDP blocks).

---

## Architecture Overview

```
┌─────────────────────────────────────────────────┐
│              STUDENT'S LAPTOP                     │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │         MyVPN Desktop App (Go+Fyne)        │   │
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
│              SOCKS5 :1080                           │
│                       │                            │
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
│       │                         │  • Unbin│       │
│       │                         └─────────┘       │
│       │                                            │
│  ┌────┴────┐  ┌──────────┐  ┌──────────┐          │
│  │ Eco     │  │ Stealth  │  │ Strike   │          │
│  │ :8443   │  │ :8444    │  │ :8445    │          │
│  │ BBR     │  │ Brutal   │  │ BBR+UDP  │          │
│  │ 5M tc   │  │ 48M rate │  │ 200M tc  │          │
│  └─────────┘  └──────────┘  └──────────┘          │
│                                                   │
│  Backups → Backblaze B2 (hourly, 7-day retention)  │
└─────────────────────────────────────────────────┘
```

---

## The Three Tiers

| Tier | Price | Port | Server CC | Bandwidth | UDP | Experience |
|------|-------|:----:|:---------:|:---------:|:---:|------------|
| **Eco** | $2/mo | 8443 | BBR (system) | 5 Mbps (tc capped) | ❌ | Text loads, video buffers. Exists to sell Stealth. |
| **Stealth** | $4/mo | 8444 | **Brutal** (kernel module) | 48 Mbps (target rate) | ❌ | Fast streaming. Jitter makes gaming unplayable. |
| **Strike** | $8/mo | 8445 | BBR (system) | 200 Mbps (tc capped) | ✅ | Gaming (33-44ms latency). 4K streaming. |

---

## Project Layout

```
myvpn/
├── modular-vps/              ← SERVER: VPS setup scripts (deployable unit)
│   ├── setup.sh              ← Orchestrator — runs all 8 modules
│   ├── restore.sh            ← Full restore from B2 backup
│   ├── modules/              ← 8 idempotent setup modules
│   │   ├── 00-env.sh         ── Environment validation
│   │   ├── 01-bbr.sh         ── BBR congestion control + kernel tuning
│   │   ├── 02-shadowsocks.sh ── 3× ssserver instances (eco/stealth/strike)
│   │   ├── 03-brutal.sh      ── TCP Brutal kernel module + LD_PRELOAD wrapper
│   │   ├── 04-tc.sh          ── Traffic shaping (tc HTB qdisc)
│   │   ├── 05-caddy.sh       ── Caddy reverse proxy + rate limiting
│   │   ├── 06-pocketbase.sh  ── PocketBase admin backend + JS hooks
│   │   ├── 07-backups.sh     ── Hourly B2 backup service
│   │   └── 08-firewall.sh    ── UFW firewall rules
│   ├── pb_hooks/             ← PocketBase JavaScript hooks
│   │   ├── activation.pb.js  ── Code validation + device binding
│   │   ├── heartbeat.pb.js   ── Suspension check + staged rollout
│   │   └── admin_unbind.pb.js ── Admin code recovery endpoint
│   └── templates/            ← Config templates
│       ├── Caddyfile
│       ├── eco.json / stealth.json / strike.json
│       └── pocketbase.service
│
├── v4/                       ← CLIENT: Go desktop application
│   ├── cmd/myvpn/main.go     ← Entry point
│   ├── internal/
│   │   ├── activation/       ── Luhn-mod-N validation + device fingerprinting
│   │   ├── storage/          ── JSON persistence (activation state)
│   │   ├── manager/          ── sing-box config generation + lifecycle
│   │   ├── heartbeat/        ── Periodic hub check (5min→2h backoff)
│   │   ├── updater/          ── Two-phase update + auto-rollback
│   │   ├── tunnel/           ── Fallback TUN + kill switch + DNS
│   │   ├── gui/              ── Fyne v2 desktop GUI
│   │   └── helper/           ── Privileged TUN helper service
│   ├── Makefile              ── Cross-platform build system
│   └── go.mod                ── Go module definition
│
├── scripts/                  ← Operational tooling
│   ├── generate_codes.sh     ── Generate Luhn-mod-N activation codes
│   └── print_codes.sh        ── Printable PDF code cards
│
└── .github/workflows/        ← CI/CD
    └── build.yml             ── Build + release for all platforms
```

---

## How It Works

### Network Protocol

The VPN uses **Shadowsocks TCP** — a simple, fast tunnel protocol that encrypts traffic with AES-256-GCM. Unlike TLS-based proxies (Trojan, Xray VLESS), Shadowsocks has no TLS handshake or certificate exchange, so it bypasses N4L's JA3 fingerprinting.

All traffic goes through a single TCP connection per tier. The client runs **sing-box** which provides a local SOCKS5 proxy on `127.0.0.1:1080`. Applications connect to this proxy, and sing-box tunnels everything through Shadowsocks to the server.

### Tiers & Congestion Control

Each tier uses a different congestion control algorithm:

- **Eco & Strike**: Use **BBR** (Bottleneck Bandwidth and Round-trip propagation time), Linux's default CC. BBR is fair and stable.
- **Stealth**: Uses **Brutal CC**, a kernel module that aggressively fills bandwidth regardless of packet loss. The `tcp-brutal` module registers "brutal" as a TCP congestion control algorithm. An LD_PRELOAD wrapper intercepts `accept()` calls on the Stealth ssserver and sets `TCP_CONGESTION=brutal` + target rate on each client connection.

### Activation Flow

1. User enters code in the app: `MYVPN-A7X3-K9M2-Q5P1-C`
2. **Client-side Luhn-mod-N validation** catches typos instantly
3. App generates a **device fingerprint** (SHA256 of MAC address + disk serial + motherboard UUID)
4. POST to `/api/activate` with `{ code, fingerprint }`
5. Server validates Luhn checksum, looks up code, binds fingerprint
6. Returns tier config with server address, port, password, method
7. App stores config locally and starts the tunnel

### Heartbeat & Grace Period

The app sends a heartbeat every 5 minutes. If the hub is unreachable:
- Interval doubles: 5min → 10min → 20min → ... → 2h max
- VPN keeps working (config is stored locally, no token dependency)
- **7-day grace period** before requiring re-activation

On server response:
- `200 OK`: Normal operation. Resets interval to 5min.
- `403 suspended`: Immediately disconnect. Show suspension message.
- `update_available` field: Triggers staged rollout update.

### Staged Rollouts

Updates are delivered gradually:
1. Server sets `rollout_percent` (0-100) in PocketBase `update_config`
2. Heartbeat response includes `update_available` only if fingerprint passes server-side hash gate
3. Client independently checks: `hash(fingerprint) % 100 < rollout_percent`
4. If eligible, downloads new binary, verifies SHA256, applies two-phase update

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

This binds an activation code to a specific device. If the device is lost or broken, an admin can unbind the code via the `/api/admin/unbind` endpoint.

---

## Build Order

### Step 1: Deploy Server

```bash
# Copy to VPS
scp -r modular-vps root@your-vps:/root/

# Run setup (takes 10-15 minutes)
ssh root@your-vps
DOMAIN=api.yourdomain.com ./modular-vps/setup.sh
```

This provisions: BBR, 3× Shadowsocks, Brutal CC, tc shaping, Caddy + TLS, PocketBase, B2 backups, UFW firewall.

### Step 2: Configure PocketBase

1. Visit `https://api.yourdomain.com/_/` — create admin account
2. Create collections: `codes`, `tier_configs`, `activation_attempts`
3. Create `update_config` collection for staged rollouts
4. Set `admin_api_token` in PocketBase app settings
5. Upload JS hooks from `modular-vps/pb_hooks/`
6. Seed tier configs with passwords from `/root/.tier_passwords`

### Step 3: Generate & Print Codes

```bash
./scripts/generate_codes.sh https://api.yourdomain.com YOUR_TOKEN eco 50
./scripts/print_codes.sh eco-codes.txt eco-cards.pdf
```

### Step 4: Build Client App

```bash
cd v4
make bundle-all           # Builds for all platforms + bundles sing-box
```

Or push a `v*` tag for GitHub Actions CI/CD.

### Step 5: Install & Test

1. Install helper service (admin): `sudo myvpn-helper --install`
2. Launch MyVPN app
3. Test activation, connection, heartbeat, update

---

## Operations

### Quick Health Check

```bash
ssh root@your-vps "
  systemctl is-active caddy pocketbase shadowsocks-eco shadowsocks-stealth shadowsocks-strike
  lsmod | grep tcp_brutal
  sysctl net.ipv4.tcp_congestion_control
  tc -s class show dev eth0 | head -10
"
```

### Backup & Restore

```bash
# Manual backup
/usr/local/bin/myvpn-backup.sh

# Full VPS restore
DOMAIN=api.yourdomain.com \
  B2_APPLICATION_KEY_ID=xxx \
  B2_APPLICATION_KEY=xxx \
  B2_BUCKET=my-vpn-backup-bucket \
  /root/modular-vps/restore.sh
```

### Monitoring

Set up uptime monitoring on `https://api.yourdomain.com/api/health` (UptimeRobot, PingPing, etc.).
This endpoint returns `{"message":"API is healthy.","code":200}` when PocketBase is running.

---

## Real-World Testing

The modular-vps scripts were tested on a Voyager VPS (Ubuntu 22.04, kernel 5.15.0-161-generic):

| Module | Status | Notes |
|--------|:------:|-------|
| 00-env | ✅ | OS, arch, root, disk, memory all validated |
| 01-bbr | ✅ | BBR active, TCP tuning params set |
| 02-shadowsocks | ✅ | 3 instances installed and enabled |
| 03-brutal | ✅ | Cloned from `apernet/tcp-brutal` (repo renamed from `tcp-brutal-ng`), compiled custom LD_PRELOAD wrapper from bundled C source |
| 04-tc | ✅ | Eco 5Mbit, Strike 200Mbit classes active |
| 05-caddy | ✅ | Custom build with ratelimit plugin, Caddyfile validated |
| 06-pocketbase | ✅ | 0.22.21 installed, health check passing |
| 07-backups | ✅ | Script installed, timer enabled (B2 credentials needed) |
| 08-firewall | ✅ | UFW active, all ports open, SSH rate-limited |

All 3 Shadowsocks services active (8443/8444/8445). Brutal CC kernel module loaded.
Caddy + PocketBase serving API at `https://domain/_/`.
