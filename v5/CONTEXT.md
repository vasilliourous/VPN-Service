# MyVPN — Project Context (V5 Reference)

> **Purpose:** Everything a future agent needs to understand this project without
> searching across the entire repository. Read this first before touching any code
> or documentation.
>
> **This covers:** project history, V5 philosophy, directory structure, what was
> tested and what broke, known issues, agent guidance.
>
> **Not covered here:** build steps (see `docs/`), API contracts (see `docs/API.md`),
> server deployment (see `docs/DEPLOY.md`).

---

## 1. What This Project Is

A commercial VPN service for students at N4L-managed NZ schools (Macleans College).
It bypasses Palo Alto firewalls using **Shadowsocks TCP** — the only protocol that
consistently passes through N4L's detection (no TLS fingerprint, no UDP dependency).

The service has three tiers:

| Tier | Price | Port | CC | Cap | Transport | Use case |
|------|-------|:----:|:---:|:---:|:---------:|----------|
| Eco | $2/mo | 8443 | BBR | 5 Mbps tc | TCP only | Text, browsing |
| Stealth | $4/mo | 8444 | BBR | 100 Mbps tc | TCP only | Streaming |
| Strike | $8/mo | 8445 | BBR | 200 Mbps tc | TCP+UDP | Gaming |

**Distribution model:** Middlemen hand out physical activation code cards for cash.
Students pay cash (no credit card needed). Middlemen take ~20-30% commission.
The business and sales docs are kept privately — not in this repo.

---

## 2. Repository Structure

```
VPN-Service/
├── v5/                 ← DEFINITIVE VERSION (hardened client + server + docs)
│   ├── client/         ← Hardened Go + Wails/Vue 3 client code (v2.0.0)
│   ├── server/         ← VPS deployment modules + PocketBase hooks
│   ├── docs/           ← Architecture, deploy, ops, API, fixes
│   │   └── history/    ← Curated pre-V5 research archive (business model, N4L threat analysis)
│   ├── README.md       ← V5 overview
│   └── CONTEXT.md      ← THIS FILE
├── v4/                 ← PREVIOUS client source (reference only, superseded by v5/client/)
├── scripts/            ← Code generator + PDF card printer (canonical location)
└── README.md, .github/...
```

### What goes where

| Directory | Purpose | For whom |
|-----------|---------|----------|
| `v5/` | Definitive version — start here | **Everyone** |
| `v5/client/` | Hardened Go + Wails/Vue 3 client source code | **Client developers** |
| `v5/server/` | VPS deployment modules (bash) + PocketBase hooks | Server deployers |
| `v5/docs/` | Architecture, client guide, deploy, ops, API, fixes | Client developers, operators |
| `v5/docs/history/` | Curated pre-V5 research archive (business model, N4L threat analysis) | Reference only — read CONTEXT/ARCHITECTURE first |
| `v4/` | Predecessor client code | Reference only — use v5/client/ |
| `scripts/` (root) | Luhn-mod-N code generator + PDF card printer | Middleman managers |

> **2026-08 culling (round 2):** `modular-vps/`, root `CONTEXT.md`, and
> `v5/scripts/` were deleted as stale duplicates — `v5/server/` is the one and
> only server deployment directory (it absorbed `hiddify.pb.js`), root
> `scripts/` is the one and only code-generator location, and `v5/CONTEXT.md`
> is the one and only context document. All deleted files remain recoverable
> from git history.
>
> **2026-08-14 follow-up:** macOS support was RE-ENABLED (unsigned local build
> — see `v5/docs/CLIENT-GUIDE.md` for the Gatekeeper workaround). The darwin
> code paths (`darwinTUN`, `pfctl` kill-switch, `networksetup` DNS, `ioreg`
> fingerprint), `darwin_link.go`, macOS CI targets, updater URLs, and Makefile
> targets were restored. macOS installs are unsigned: the user must
> right-click → Open (or `xattr -cr`) on first launch.
>
> **2026-08 history archive:** the still-relevant `originals/` docs (business
> model + N4L attacker/defender research) were restored to `v5/docs/history/`
> after review; the rest of `originals/` (superseded action plans) stays in
> git history only.

---

## 3. V5 — What Makes It Different

V5 exists because earlier versions (V1–V4) were spread
across the repo with overlapping docs, untested assumptions, and no central reference.
V5 consolidates everything into one place: **both client and server code**, hardened
from real-world testing, with comprehensive documentation.

### Principles

1. **Only 2 binaries on the client** — myvpn (GUI + manager) + sing-box (engine).
   No TUN helper service, no tun2socks, no sslocal, **no SOCKS5 proxy layer**.
   BYOD means every user has admin rights, so sing-box creates TUN directly.

   > **Why no SOCKS5?** Earlier versions (V1–V4) used `ss-local` which exposes a
   > SOCKS5 proxy, adding +2 RTT handshake overhead per connection and requiring
   > per-app proxy configuration. V5 eliminated this entirely. sing-box's native
   > TUN inbound creates a virtual network interface and routes all device traffic
   > through it — no SOCKS5, no extra processes, no handshake overhead. The only
   > privilege needed is TUN creation, which sing-box handles directly on BYOD
   > machines. See `docs/ARCHITECTURE.md` for the exact sing-box config.
2. **Server-enforced caps** — tc HTB qdisc on the VPS limits bandwidth per tier.
   The client can't bypass its cap because the throttle happens post-decryption.
3. **Permanent device binding** — one activation code = one device forever.
   SHA256 of MAC + disk serial + motherboard UUID. Admin can suspend, not deactivate.
4. **Crash-safe updates** — two-phase sentinel handshake. No signing keys needed.
5. **No TLS in the tunnel** — Shadowsocks AEAD is indistinguishable from random data.

### Client Hardening (v5/client/ vs v4/)

The v5/client/ codebase is a hardened evolution of the v4 source:

- **Context propagation** for all network operations
- **Panic recovery** with stack traces at global and goroutine level
- **Signal handling** (SIGINT/SIGTERM → graceful shutdown → force kill after 5s)
- **Input validation** on all user-facing entry points
- **Atomic file writes** with `.tmp` + `rename()` strategy and 3-deep backup rotation
- **Process health monitoring** with auto-restart (max 3 restarts in 5 minutes)
- **Download validation** with min/max size + SHA256 checksum
- **Thread safety** via `sync.Mutex`/`sync.RWMutex` on all shared state
- **Heartbeat jitter** (±10%) to prevent thundering herd
- **Error wrapping** throughout with `fmt.Errorf("...: %w", err)`
- **Callback timeout guard** (5s max for heartbeat callbacks)

---

## 4. VPS Testing Results

All modules tested on a Voyager VPS (Ubuntu 22.04, kernel 5.15.0-161-generic):

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

### 8 Issues Found & Fixed

All documented in `v5/docs/FIXES.md`:

| # | Severity | Issue | Fix |
|:-:|:--------:|-------|-----|
| S1 | 🔴 | Caddy systemd service missing | Auto-create service with cap_net_bind_service |
| S2 | 🔴 | TLS cert provisioning failed | XDG_CONFIG_HOME + /var/lib/caddy |
| S3 | 🔴 | PocketBase NAMESPACE error | Removed systemd security hardening |
| S4 | 🟡 | Admin token extraction failed | Python-based seeding script |
| S5 | 🟡 | Bash heredocs mangled JSON | Moved to seed-pb.py |
| S6 | 🔴 | `!$double` bash bug | Replaced with if/else |
| S7 | 🔴 | PB 0.22 JS hook API incompatibility | All hooks rewritten |
| S8 | 🟡 | Smoke test IP detection off by one | Fixed awk pattern |

---

## 5. Client Code Structure (v5/client/)

> **⚠️ Updated for the Wails migration (2026).** The GUI moved from Fyne to
> **Wails v2 + Vue 3** (`frontend/`). The old Fyne GUI, helper binary, and old
> entry point were removed in the Wails migration (the `v5/legacy/` reference
> copy was deleted in the 2026-08 cleanup; pre-migration client in `v4/`,
> git history for the removed files). The `internal/` backend packages are
> unchanged. See `docs/WAILS-MIGRATION.md` and `docs/BACKEND-API.md`.

```
v5/client/
├── main.go                      # Wails app entry (embedds frontend/dist, binds App)
├── app.go                       # App struct — wraps internal/ for the Vue UI
├── wails.json                   # Wails project configuration
├── internal/
│   ├── storage/storage.go      # Persistent JSON state (thread-safe, atomic writes, backups)
│   ├── activation/
│   │   ├── activation.go       # Activation client with retry + context + ValidateCodeFormat
│   │   ├── fingerprint_linux.go   # Self-contained fingerprint (shared logic + Linux collector)
│   │   ├── fingerprint_windows.go # Self-contained fingerprint (shared logic + Windows collector)
│   │   └── luhn.go             # Luhn-mod-N checksum validation
│   ├── heartbeat/heartbeat.go  # Periodic server health check with jitter
│   ├── manager/process.go      # Sing-box lifecycle + config generation
│   ├── updater/
│   │   ├── updater.go          # Two-phase update with crash safety
│   │   ├── recover.go          # Crash detection and auto-revert
│   │   ├── update_linux.go     # Linux binary swap + fork
│   │   └── update_windows.go   # Windows binary swap + fork
│   └── tunnel/tunnel.go        # TUN interface + kill switch (per-platform)
├── frontend/                    # Vue 3 + TypeScript + Vite UI
│   └── src/                    # App.vue, components/, stores/, lib/bridge.ts
├── engines/README.md           # Engine binary placeholder
├── go.mod
└── Makefile                    # Wails build targets
```

Legacy (NOT in the Go module — removed in the 2026-08 cleanup):

```
(removed — old Fyne GUI, TUN helper binary, and entry point. Pre-migration
client in `v4/`; removed files recoverable from git history)
```

### Key Metrics

| Metric | Value |
|--------|-------|
| Total lines (Go) | ~4,200 across 19 files |
| Client version | 2.0.0 |
| Engine | sing-box 1.10.0 |
| Min Go version | 1.22 |
| Platforms | Linux, macOS (Intel+ARM, unsigned), Windows |
| Dependencies | Wails v2 + Vue 3 (Fyne removed) |

---

## 6. Architecture Overview

```
┌──────────────────────────────────────────────────────┐
│                   CLIENT DEVICE                        │
│                                                        │
│  ┌────────────────────────────────────────────────┐   │
│  │              myvpn (Go + Wails / Vue 3)            │   │
│  │                                                  │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────────┐   │   │
│  │  │Activation│  │ Heartbeat │  │   Updater    │   │   │
│  │  │ + retry  │  │ + jitter  │  │ 2-phase SAFE │   │   │
│  │  └────┬─────┘  └────┬─────┘  └──────┬───────┘   │   │
│  │       │              │               │            │   │
│  │  ┌────┴──────────────┴───────────────┴───────┐   │   │
│  │  │              Manager                       │   │   │
│  │  │  • Generates sing-box JSON config          │   │   │
│  │  │  • Spawns sing-box as subprocess           │   │   │
│  │  │  • Health monitoring + auto-restart        │   │   │
│  │  │  • Graceful shutdown with timeout          │   │   │
│  │  └───────────────────┬───────────────────────┘   │   │
│  └──────────────────────┼───────────────────────────┘   │
│                         │                                 │
│              ┌──────────┴──────────┐                      │
│              │  sing-box (engine)   │                      │
│              │  Creates TUN device  │                      │
│              │  Routes all traffic  │                      │
│              │  through Shadowsocks │                      │
│              └──────────────────────┘                      │
└──────────────────────────┼───────────────────────────────┘
                           │ Shadowsocks TCP (AES-256-GCM)
                           │ :8443 / :8444 / :8445
                           ▼
┌──────────────────────────────────────────────────────┐
│                   VPS (Ubuntu 22.04)                    │
│                                                        │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐  │
│  │  Caddy   │  │PocketBase│  │  ssserver × 3        │  │
│  │  TLS +   │  │ SQLite   │  │  Eco/Stealth/Strike   │  │
│  │  rate    │  │ JS hooks │  │  BBR/BBR/BBR          │  │
│  │  limit   │  │          │  │  5/100/200 Mbps       │  │
│  └──────────┘  └──────────┘  └──────────────────────┘  │
└──────────────────────────────────────────────────────┘
```

---

## 7. Key Technologies

| Component | Technology | Version |
|-----------|-----------|:-------:|
| Client language | Go | 1.22+ |
| GUI toolkit | Wails v2 | 2.9.1 |
| Frontend | Vue 3 + Vite + TypeScript | ^3.4.0 |
| Tunnel engine | sing-box | 1.10.0 |
| Server OS | Ubuntu | 22.04 |
| Proxy protocol | Shadowsocks (ssserver-rust) | v1.23.0 |
| Reverse proxy | Caddy (custom rate_limit) | Latest |
| Database | PocketBase | 0.22.21 |
| TCP CC (all tiers) | BBR | Kernel built-in |
| Backups | Backblaze B2 | — |
| Traffic shaping | tc (HTB qdisc) | — |
| Firewall | UFW | — |

---

## 8. Agent Guidance

### Where to start

1. **Read `v5/README.md`** for the high-level overview and quick start.
2. **Read `v5/docs/ARCHITECTURE.md`** for the complete system design.
3. **Refer to `v5/client/`** for the client source code.
4. **Refer to `v5/server/`** for server deployment.
5. **Use `v5/docs/`** for specific guides (deploy, ops, API, implement).

### Rules of thumb

1. **Start here.** V5 is the definitive version. Don't read v4/
   unless you need historical context.
2. **The client code is in `v5/client/`.** The old `v4/` directory exists for
   reference only — never build from it.
3. **The server modules are idempotent.** You can re-run any module safely.
   Each module checks if its work is already done before proceeding.
4. **The JS hooks have been rewritten for PocketBase 0.22+.** If activation
   returns a generic 400 error after a fresh deploy, check `journalctl -u pocketbase`
   for hook load errors.
5. **All tiers use BBR — no kernel modules to maintain.** Bandwidth caps are tc-based
   (Eco 5 / Stealth 100 / Strike 200 Mbps); after a kernel update, re-apply with
   `systemctl restart tc-eco-cap tc-stealth-cap tc-strike-cap`.
6. **The server domain is `networkingguides.duckdns.org`** pointing to `114.23.136.59`
   (verified 2026-08-01; an older note said `.47` — the VPS IP changed).
   This is the VPS hostname — DNS is managed by the hosting provider.
7. **Stopping PocketBase stops the backup timer.** `pocketbase-backup.timer` has
   `Requires=pocketbase.service` — systemd `Requires=` propagates stops but not
   starts, so after any `systemctl stop pocketbase` run
   `systemctl restart pocketbase-backup.timer`. `restore.sh` does this automatically.
8. **Restored DBs may have a stale admin password.** If the DB predates the current
   secrets file, `pocketbase admin update <email> <pass>` (in `/opt/pocketbase`)
   aligns it. `restore.sh` does this automatically when `PB_ADMIN_*` are set.
9. **Use the current b2 CLI syntax.** Plain bucket names fail ("Invalid B2 URI");
   use `b2://bucket/path` URIs (`b2 ls --recursive`, `b2 file download`, `b2 file info`).
   `restore.sh` uses the current syntax.

### Key credentials (live VPS as of 2026-08-01)

```
PocketBase admin:   admin@networkingguides.duckdns.org
PocketBase UI:      https://networkingguides.duckdns.org/_/
Admin API token:    CslWcWOt7jFhmYELTZahvpqKF3uV/RnWChUYTjbVAU4=
B2 bucket:          vpsvpnbackup
```

All credentials are stored on the VPS at `/root/` — see `docs/POCKETBASE-SETUP.md` for
the full list of credential files and locations.

### Common pitfalls

- **Don't use `v4/` as the source of truth** — it's
  historical reference only. V5 is the authoritative version.
- **The client code now lives in `v5/client/`** — not v4/. Always build from v5/client/.
- **The JS hooks have been rewritten for PocketBase 0.22+** — if activation still returns a generic
  400 error after a fresh deploy, check `journalctl -u pocketbase` for hook load errors.
- **DNS is managed by the hosting provider** — the VPS hostname `networkingguides.duckdns.org`
  resolves to the VPS IP automatically. No DuckDNS updates needed.
