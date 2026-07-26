# MyVPN V5 — The Definitive Edition

> **V5 is the consolidated, production-hardened version of MyVPN.**  
> It folds everything learned from V1–V4 and real-world VPS testing into one
> directory: hardened client code, clean server modules, comprehensive documentation,
> fixed issues, and full agent/developer context.

---

## What's Inside

```
v5/
├── README.md              # This file — V5 overview
├── CONTEXT.md             # Agent/developer context — network analysis, protocol tests, reasoning
│
├── client/                # Hardened client source code (Go + Fyne)
│   ├── cmd/myvpn/        # Entry point with signal handling & panic recovery
│   ├── internal/
│   │   ├── activation/    # Luhn-mod-N validation, device fingerprinting, server activation
│   │   ├── gui/           # Fyne desktop app with system tray support
│   │   ├── heartbeat/     # Periodic health check with exponential backoff & jitter
│   │   ├── helper/        # Privileged TUN helper service (systemd/launchd/Windows Service)
│   │   ├── manager/       # Sing-box process lifecycle with health monitoring
│   │   ├── storage/       # Thread-safe JSON persistence with backup rotation
│   │   ├── tunnel/        # TUN interface and kill switch (Linux/macOS/Windows)
│   │   └── updater/       # Two-phase crash-safe update system
│   ├── engines/           # Sing-box engine binaries placeholder
│   ├── go.mod
│   └── Makefile           # Cross-platform build system
│
├── docs/
│   ├── ARCHITECTURE.md    # How to architect a compatible client
│   ├── CLIENT-GUIDE.md    # How to build the client app (build commands, platform notes)
│   ├── DEPLOY.md          # Server deployment guide (from blank VPS to live)
│   ├── IMPLEMENT.md       # Step-by-step phased implementation plan
│   ├── OPS.md             # Operations manual (day-to-day management)
│   ├── API.md             # All API contracts (activation, heartbeat, admin)
│   ├── UI-AESTHETICS.md   # Visual design spec (colors, layout, icons)
│   └── FIXES.md           # Issues discovered & fixes applied
│
├── server/                # Server deployment code (from modular-vps, hardened)
│   ├── modules/           # 8 idempotent setup modules (00-env → 08-firewall)
│   ├── pb_hooks/          # PocketBase JS hooks (activation, heartbeat, admin)
│   ├── templates/         # Config templates (Caddyfile, ssserver JSONs, systemd)
│   ├── setup.sh           # One-command VPS orchestrator
│   └── restore.sh         # Full disaster recovery from B2 backup
│
└── scripts/               # Utility scripts
    ├── generate_codes.sh  # Luhn-mod-N activation code generator
    └── print_codes.sh     # Printable PDF code card sheets
```

---

## Relationship to Earlier Versions

| Version | Location | Status | Notes |
|---------|----------|:------:|-------|
| **V1** | *(historical)* | ❌ Lost | Two-phase update safety invented here |
| **V2** | `originals/` | 📚 Reference | Business plans, threat models, competitive intel |
| **V3** | `v3/` | 📚 Reference | sslocal + tun2socks architecture (tun2socks abandonware) |
| **V4** | `v4/` | 📚 Reference | Go + Fyne client source code (predecessor to v5/client/) |
| **V5** | `v5/` | 🏆 **Definitive** | Hardened client + hardened server + comprehensive docs |
| **modular-vps/** | `modular-vps/` | ✅ Tested | VPS modules tested on Voyager VPS (source for v5/server/) |
| **simplified/** | `simplified/` | 📚 Reference | Hysteria 2 / QUIC-based alternative (blocked by N4L UDP block) |

The **client source code** now lives in `v5/client/` — ported from v4 with
significant hardening improvements (see below). The older `v4/` directory
remains as reference but all active development is on `v5/client/`.

---

## What Makes V5 Different

### Client Hardening (v5/client/ vs v4/)

1. **Context propagation** — All network operations accept `context.Context` for cancellation and timeout
2. **Panic recovery** — Global recovery in `main()`, per-goroutine recovery in GUI
3. **Signal handling** — Graceful shutdown on SIGINT/SIGTERM with 5-second force-kill timeout
4. **Input validation** — All user inputs validated before processing (activation codes, configs, paths)
5. **Atomic operations** — Storage file writes use temp-file + rename with backup rotation (keep 3)
6. **Process health monitoring** — Manager tracks sing-box process, auto-restarts up to 3 times in 5 minutes
7. **Download validation** — Update downloads check minimum size + maximum size + SHA256 before swap
8. **Thread safety** — All shared state protected by `sync.Mutex`/`sync.RWMutex` with documented locking
9. **Resource cleanup** — `defer` for file handles, socket cleanup on startup, temp directory cleanup
10. **Jitter in heartbeat** — ±10% interval jitter prevents thundering herd on server restart
11. **Error wrapping** — All errors wrapped with context using `fmt.Errorf("context: %w", err)`
12. **Callback timeout guard** — Heartbeat callbacks have a 5-second timeout to prevent blocking

### Server Hardening

1. **All 8 modules tested on a real VPS** — 8 bugs found and fixed (see FIXES.md)
2. **PocketBase 0.22 compatible** — JS hooks rewritten for PB 0.22.21 API
3. **Idempotent modules** — Each module safely re-runnable
4. **B2 offsite backups** — Hourly encrypted backups with SHA256 integrity verification
5. **Comprehensive documentation** — Architecture, deploy, ops, API — everything in one place

---

## Quick Start

```bash
# 1. Deploy server (requires a blank Ubuntu 22.04 VPS + domain)
DOMAIN=networkingguides.duckdns.org ./v5/server/setup.sh

# 2. Create PocketBase admin account at https://networkingguides.duckdns.org/_/
#    Then create collections: codes, tier_configs, activation_attempts, update_config

# 3. Generate activation codes
./v5/scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_ADMIN_TOKEN eco 50

# 4. Build client
cd v5/client && make build

# 5. Build for all platforms
cd v5/client && make bundle-all
```

---

## Architecture at a Glance

```
Student Laptop (myvpn client)
├── Go + Fyne GUI (activation, status, tray)
├── Manager → spawns sing-box → TUN tunnel
└── Heartbeat → server health + staged updates

VPS (Ubuntu 22.04)
├── ssserver × 3 (Eco:8443, Stealth:8444, Strike:8445)
├── Caddy (TLS + rate limiting)
├── PocketBase (activation, heartbeat, admin)
└── Backblaze B2 backups

Transport: Shadowsocks AES-256-GCM over TCP
```

---

## CI/CD Pipeline

The client is built and released via **GitHub Actions**.

| Trigger | Action |
|---------|--------|
| Push to `main` | Lint + vet + test build |
| Tag `v*` (e.g., `v2.0.0`) | Full build + bundle + GitHub Release |
| Pull request | Lint + vet + test build |

**File:** `v5/.github/workflows/build.yml`
**Docs:** `v5/docs/CI-CD.md`
**Trigger:** `git tag v2.0.0 && git push origin v2.0.0`

### Build Artifacts

Each release produces 4 platform bundles:
- `myvpn-Linux-amd64.zip` — Linux x86_64
- `myvpn-macOS-amd64.zip` — macOS Intel
- `myvpn-macOS-arm64.zip` — macOS Apple Silicon
- `myvpn-Windows-amd64.zip` — Windows x86_64

Each bundle contains:
- `myvpn` — Desktop client (Go + Fyne)
- `sing-box` — Tunnel engine (Shadowsocks + TUN)
- `myvpn-helper` — Privileged TUN helper service

### Local CI Simulation

```bash
cd v5/client && make ci
```

---

## Documentation Index

| Resource | Location |
|----------|----------|
| Architecture | `v5/docs/ARCHITECTURE.md` |
| Client build guide | `v5/docs/CLIENT-GUIDE.md` |
| CI/CD pipeline | `v5/docs/CI-CD.md` |
| Server deployment | `v5/docs/DEPLOY.md` |
| Operations manual | `v5/docs/OPS.md` |
| API reference | `v5/docs/API.md` |
| Implementation plan | `v5/docs/IMPLEMENT.md` |
| UI design spec | `v5/docs/UI-AESTHETICS.md` |
| Known issues & fixes | `v5/docs/FIXES.md` |
| Project context | `v5/CONTEXT.md` |

## Version

**Current:** MyVPN Client v2.0.0 / Server v1.0.0
