# Locus V5 — The Definitive Edition

> **V5 is the consolidated, production-hardened version of Locus.**  
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
├── client/                # Desktop client source code (Go + Wails + Vue 3)
│   ├── main.go            # Wails app entry point (embedds frontend, binds App)
│   ├── app.go             # App struct — wraps internal/ packages for the UI
│   ├── wails.json         # Wails project configuration
│   ├── internal/          # 8 backend packages
│   │   ├── activation/    # Luhn-mod-N validation, device fingerprinting, hub activation
│   │   ├── heartbeat/     # Periodic hub check, 5min→2h backoff, 7-day grace
│   │   ├── manager/       # sing-box lifecycle, config generation, watchdog, self-heal
│   │   ├── pinned/        # Hub TLS SPKI pinning
│   │   ├── storage/       # Thread-safe JSON persistence with backup rotation
│   │   ├── tray/          # OPT-IN system tray (LOCUS_TRAY=1; no-op on macOS)
│   │   ├── tunnel/        # TUN interface and kill switch (Linux/Windows/macOS)
│   │   └── updater/       # Two-phase crash-safe updates + version comparison
│   ├── frontend/          # Vue 3 + Vite + TypeScript UI (embedded into the binary)
│   ├── engines/           # Sing-box engine binaries placeholder
│   ├── rsrc_windows_*.syso# requireAdministrator manifest (.syso)
│   ├── go.mod
│   └── Makefile           # Build system
│
├── console/               # Admin console SPA (Vue 3 + Vite) → served at /admin/
│   ├── src/views/         # Dashboard, Codes, Releases, Tiers, Login
│   └── vite.config.ts     # `base: /admin/` is LOAD-BEARING — do not change
│
├── docs/
│   ├── history/          # Curated pre-V5 research archive (business model, N4L threat analysis)
│   ├── BACKEND-API.md     # Complete API reference for all internal/ packages
│   ├── WAILS-MIGRATION.md # Migration plan from Fyne → Wails (build & rollback)
│   ├── ARCHITECTURE.md    # How to architect a compatible client
│   ├── CLIENT-GUIDE.md    # How to build the client app (build commands, platform notes)
│   ├── DEPLOY.md          # Server deployment guide (from blank VPS to live)
│   ├── IMPLEMENT.md       # Step-by-step phased implementation plan (historical)
│   ├── OPS.md             # Operations manual (day-to-day management)
│   ├── API.md             # All API contracts (activation, heartbeat, admin)
│   ├── POCKETBASE-SETUP.md    # Collections, hooks, seeding, admin setup
│   ├── SECRETS-MANAGEMENT.md  # Age-encrypted secrets workflow
│   ├── CI-CD.md           # GitHub Actions pipeline, build matrix, releases
│   ├── ENGINE-SWAP-ANALYSIS.md # sing-box→mihomo + the Linux TUN elevation gap
│   ├── UI-AESTHETICS.md   # Visual design spec (colors, layout, icons)
│   ├── GAMING-UDP.md      # sing-box server + UDP-over-TCP (UoT) for gaming
│   └── FIXES.md           # Issues discovered & fixes applied (dated log)
│
├── server/                # Server deployment code (canonical — VPS modules + hooks)
│   ├── modules/           # 8 idempotent setup modules (00-env → 08-firewall)
│   ├── pb_hooks/          # PocketBase JS hooks (activation, heartbeat, code-lookup,
│   │                      #   unbind, admin console, hiddify)
│   ├── templates/         # Config templates (Caddyfile, ssserver JSONs, systemd,
│   │                      #   locus-upload.service)
│   ├── scripts/           # seed, smoke-test, publish-release, release_upload,
│   │                      #   deploy-console, enable-uot
│   ├── secrets.env.age    # Age-encrypted credentials (key NOT in repo)
│   ├── setup.sh           # One-command VPS orchestrator
│   └── restore.sh         # Full disaster recovery from B2 backup
│
├── VERSION                # THE client version — one line, single source of truth
├── README.md              # V5 overview
└── CONTEXT.md             # Agent/developer context — read this first
│
└── scripts/               # Utility scripts (repo root)
    ├── generate_codes.sh  # Luhn-mod-N activation code generator
    ├── print_codes.sh     # Printable PDF code card sheets
    └── publish-update.sh  # Prepare + publish a release payload
```

---

## Relationship to Earlier Versions

| Version | Location | Status | Notes |
|---------|----------|:------:|-------|
| **V1** | *(historical)* | ❌ Lost | Two-phase update safety invented here |
| **V2** | *(removed 2026-08)* | 📚 Git history | Business plans, threat models, competitive intel |
| **V3** | *(removed 2026-08)* | 📚 Git history | sslocal + tun2socks architecture (tun2socks abandonware) |
| **V4** | `v4/` | 📚 Reference | Go + Fyne client source code (predecessor to v5/client/) |
| **V5** | `v5/` | 🏆 **Definitive** | Hardened client + hardened server + comprehensive docs |
| **modular-vps/** | *(removed 2026-08)* | 📚 Git history | VPS modules tested on Voyager VPS (source for v5/server/) |
| **simplified/** | *(removed 2026-08)* | 📚 Git history | Hysteria 2 / QUIC-based alternative (blocked by N4L UDP block) |

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

1. **All 8 modules tested on a real VPS** — 9 bugs found and fixed in the first
   round, then **7 more found on a completely blank box** (see FIXES.md). A
   re-run is not a deploy test.
2. **PocketBase 0.22 compatible** — JS hooks rewritten for the PB 0.22.21 API.
   Note the two traps that cost the most time: `findRecordsByFilter` silently
   returns nothing, and file-scope helpers are invisible inside `routerAdd`.
3. **Idempotent modules** — Each module safely re-runnable; full `setup.sh`
   re-runs exit 0 and `smoke-test.sh` reports 23/23.
4. **B2 offsite backups** — Hourly encrypted backups, verified against the
   **downloaded** artifact (not the local source), with a proven restore path.
5. **Admin console** — day-to-day operations from a browser at `/admin/`
   instead of SSH + Python + sqlite.
6. **Comprehensive documentation** — Architecture, deploy, ops, API — everything in one place.

---

## Quick Start

```bash
# 1. Copy the server tree AND the age key to a fresh VPS, then run the orchestrator.
#    Secrets (domain, tokens, tier passwords, B2 keys, PB admin) decrypt from
#    secrets.env.age — see docs/SECRETS-MANAGEMENT.md
scp -r v5/server age-key.txt root@YOUR_VPS:/root/server/
ssh root@YOUR_VPS "/root/server/setup.sh"

# 2. Verify the deployment (expect 23 passed / 0 failed)
ssh root@YOUR_VPS "DOMAIN=networkingguides.duckdns.org /root/server/scripts/smoke-test.sh"

# 3. Sign in to the admin console and issue codes from there
#    https://networkingguides.duckdns.org/admin/   (admin token; see /root/.admin_api_token)

# Or generate codes from the CLI (root scripts/ is canonical)
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_PB_ADMIN_JWT eco 50

# 4. Build client
cd v5/client && make build

# 5. Build for all platforms
cd v5/client && make build-all
```

> The code generator takes the **PocketBase admin JWT**, not the `ADMIN_API_TOKEN`
> — PB 0.22 rejects the latter for record access.


---

## Architecture at a Glance

```
Student Laptop (locus client)
├── Wails + Vue 3 GUI (activation, status, tray)
├── Manager → spawns sing-box → TUN tunnel
└── Heartbeat → server health + staged updates

VPS (Ubuntu 22.04)  —  170.64.196.179
├── ssserver × 3 (Eco:8443, Stealth:8444, Strike:8445)
├── Caddy (TLS + rate limiting + /admin/ + /updates/)
├── PocketBase (activation, heartbeat, code-lookup, admin API)
├── locus-upload (127.0.0.1:8091 — release binaries)
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

**File:** `.github/workflows/build.yml` (repo root — GitHub Actions only runs root workflows)
**Docs:** `v5/docs/CI-CD.md`
**Trigger:** `git tag v2.0.0 && git push origin v2.0.0`

### Build Artifacts

Each release produces **4 platform bundles** (for humans downloading an installer):

| File | Platform |
|------|----------|
| `locus-Linux-amd64.zip` | Linux x86_64 |
| `locus-Windows-amd64.zip` | Windows x86_64 |
| `locus-macOS-amd64.zip` | macOS Intel |
| `locus-macOS-arm64.zip` | macOS Apple Silicon |

Each bundle contains:
- `locus` — Desktop client (Wails + Vue 3, single binary)
- `sing-box` — Tunnel engine (Shadowsocks + TUN)

Plus the **raw executables the auto-updater actually consumes** (not zips):

```
locus-linux-amd64        locus-darwin-amd64
locus-windows-amd64.exe  locus-darwin-arm64
+ <each>.sha256 + manifest.json
```

The updater replaces the app binary in place and cannot unpack a zip, so these
raw artifacts are what get published to the hub via
`v5/server/scripts/publish-release.sh`. The build **fails** if any platform
artifact is missing from `manifest.json`.

### Local Development (no CI)

There is no `make ci` target — the pipeline runs on GitHub Actions only
(see `.github/workflows/build.yml`).

```bash
# Hot-reload development (Vite dev server + Wails)
cd v5/client && make dev

# Local production build
cd v5/client && make build
```

> **Note:** the frontend must be built before Go compiles — `//go:embed`
> requires `frontend/dist`. `wails build` does this automatically per
> `wails.json`; `make dev` also builds the frontend first. For manual
> `go build`, pass the full Wails tag set — `-tags "frontend desktop production"`
> (`desktop,production` are required Wails tags; without them the binary is the
> stub app that shows the "correct build tags" error at runtime). On Linux,
> add `webkit2_41` when building against WebKitGTK 4.1 (Ubuntu 24.04+):
> `-tags "frontend desktop production webkit2_41"`. Without the
> `frontend` tag, `go build .` compiles with the empty `assets_stub.go`
> asset FS (no UI).

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

**Current:** Locus Client v2.0.0 / Server v1.0.0

The **client version (single source of truth)** is the one-line `v5/VERSION`
file — bump that one file; the Makefile and CI (`v*` git tag) read and inject it.
Server/engine versions are pinned at build/deploy time in `v5/server/`
modules (e.g. `02-shadowsocks.sh`: `SING_BOX_VERSION`, `SS_VERSION`;
`05-caddy.sh`: `CADDY_VERSION`; `06-pocketbase.sh`: `PB_VERSION`).
