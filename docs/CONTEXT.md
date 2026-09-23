# Locus — Project Context (V5 Reference)

> **Purpose:** Everything a future agent needs to understand this project without
> searching across the entire repository. Read this first before touching any code
> or documentation.
>
> **This covers:** project history, V5 philosophy, directory structure, what was
> tested and what broke, known issues, agent guidance.
>
> **Not covered here:** build steps (see `docs/`), API contracts (see `docs/API.md`),
> server deployment (see `docs/DEPLOY.md`).

> **Naming rule (read before "tidying up" a `myvpn` string).** The product is
> **Locus**. Two classes of legacy `myvpn` name survive on purpose:
>
> 1. **Live server paths and unit names** — `/usr/local/bin/myvpn-backup.sh`,
>    `myvpn-tc-apply.sh`, `/etc/myvpn/tc`, `/var/log/myvpn-*.log`,
>    `SKIP_CONSOLE`-adjacent unit descriptions. These exist on the deployed host
>    right now. Renaming them in the repo desynchronises the definition from the
>    running system, so a fresh `setup.sh` would install a file the existing
>    units do not call. **Rename only as part of a redeploy, never as a
>    drive-by.**
> 2. **Client backup directory** `.myvpn-backups/` — on every activated
>    machine's disk. Renaming it orphans existing rollback copies.
>
> Everything else — user-visible strings, hook headers, log banners, document
> text, the Windows executable's product name — is **Locus** and should be fixed
> on sight. The one that actually reached a user was the shadowsocks link tag in
> `hiddify.pb.js` ("Eco - MyVPN"), fixed 2026-09-19 (FIXES.md 34).
>
> **Rebrand (2026-08):** This product was renamed **MyVPN → Locus** and re-themed
> from dark-purple to **dark green**. All client code, identifiers, the module path,
> binary/output names, the `locus0` TUN interface, app/log/code-prefix tokens, the
> GUI, and this documentation now use **Locus / locus**. The **deployed server
> keeps legacy `myvpn-*` artifact/path names** (`/etc/myvpn`, `myvpn-*.sh`,
> `myvpn-*.log`, `networkingguides.duckdns.org/…/myvpn-*`) because it has not been
> redeployed — treat those literal server paths as still-current until a fresh
> `setup.sh`/`restore.sh` run renames them. Note the modules themselves still
> *install* under `/etc/myvpn` and `/usr/local/bin/myvpn-*`, so a re-run does not
> rename anything either — the legacy names are not going away on their own.

> **⚠️ Live data (2026-09-19):** the hub carries **11 real `strike` activation
> codes for middleman "Wanzhen"** (one already bound to a device). They are in
> daily use. **Never bulk-delete `codes` or `code_events` rows** — earlier in the
> project's life test-data cleanup was safe; it is not any more. To revoke access
> use **Suspend** (reversible); to move a student to a new laptop use **Unbind**.

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
| Strike | $8/mo | 8445 | BBR | 200 Mbps tc | TCP+UDP (raw) | Gaming |

**Activation codes are `RQ-XXXX-XXXX-XXXX-C`** (15 chars: `RQ` prefix + 3×4
random charset chars + 1 Luhn check char; charset
`ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, no `I/O/0/1`). The pre-2026-08-17
`MYVPN-XXXX-XXXX-XXXX-C` form is **dead** — codes were migrated, and no live code
uses the old prefix. The Luhn-mod-N checksum covers the whole body **including**
the `RQ` prefix, so any code generator, hook or validator must agree on that or
generated codes will fail on the device. (This detail is easy to get wrong from a
code read — the client validates the full 15-char string, while the server-side
checksum helper is fed the full body too.)

**Distribution model:** Middlemen hand out physical activation code cards for cash.
Students pay cash (no credit card needed). Middlemen take ~20-30% commission.
The business and sales docs are kept privately — not in this repo.

---

## 2. Repository Structure

```
VPN-Service/
├── client/              ← THE SHIPPING CLIENT. Tauri 2 fork of Clash Verge Rev,
│                        tunnelling via mihomo. Has its own docs in client/docs/.
├── server/              ← LIVE hub. VPS deployment modules + PocketBase hooks
│   ├── console/         ← Admin console SPA (Vue 3 + Vite), served at /admin/
│   ├── modules/         ← Numbered deploy modules (00-env … 08-firewall)
│   ├── pb_hooks/        ← PocketBase JS hooks (activation, heartbeat, release, …)
│   └── scripts/         ← bump-version, publish-release, hooks-sync, seed-*, smoke-*
├── legacy/
│   ├── wails-client/    ← RETIRED Go + Wails + sing-box client (was v5/client).
│   │                    Kept for its contract tests. See its ARCHIVED.md.
│   └── v4/              ← Predecessor Go + Fyne client. Reference only.
├── docs/                ← Architecture, deploy, ops, API, fixes, CONTEXT (this file)
│   └── history/         ← Curated research archive + dated session records
├── scripts/             ← Code generator + PDF card printer (canonical location)
├── VERSION              ← The ARCHIVED client's version. NOT the fork's — see below.
└── README.md, bump.sh, .github/workflows/build.yml
```

> **Restructured 2026-09-23.** Everything under `v5/` moved: `v5/server/` →
> `server/`, `v5/console/` → `server/console/`, `v5/client/` →
> `legacy/wails-client/`, `v5/docs/` + `extra-details/` → `docs/`. Moved with
> `git mv`, so history is intact. Any path in this document still reading `v5/`
> other than a historical note is stale — fix it.
>
> **Version authority.** `VERSION` and `server/scripts/bump-version.sh` still
> drive the **archived** Wails client (`legacy/wails-client/main.go`,
> `buildinfo.go`, `wails.json`, `frontend/package.json`) and its committed
> Windows `.syso` resources. That is deliberate: it keeps the archived tree
> self-consistent. It is **not** the shipping client's version — the fork
> versions itself in `client/package.json` and `client/src-tauri/Cargo.toml`.
> Reconciling them is an open decision, not an oversight.

### What goes where

| Directory | Purpose | For whom |
|-----------|---------|----------|
| `client/` | **THE SHIPPING CLIENT** — Tauri 2 fork of Clash Verge Rev, mihomo engine | **Everyone — start here** |
| `legacy/wails-client/` | RETIRED Go + Wails + sing-box client (see its `ARCHIVED.md`) | Contract/spec reference only |
| `server/console/` | Admin console SPA — day-to-day hub operations in a browser | Operators |
| `server/` | VPS deployment modules (bash) + PocketBase hooks | Server deployers |
| `docs/` | Architecture, client guide, deploy, ops, API, fixes | Client developers, operators |
| `docs/history/` | Curated pre-V5 research archive (business model, N4L threat analysis) | Reference only — read CONTEXT/ARCHITECTURE first |
| `legacy/v4/` | Predecessor Go + Fyne client code | Reference only |
| `scripts/` (root) | Luhn-mod-N code generator + PDF card printer | Middleman managers |

> **2026-08 culling (round 2):** `modular-vps/`, root `CONTEXT.md`, and
> `v5/scripts/` were deleted as stale duplicates — `server/` is the one and
> only server deployment directory (it absorbed `hiddify.pb.js`), root
> `scripts/` is the one and only code-generator location, and `docs/CONTEXT.md`
> is the one and only context document. (Their paths were later flattened again
> in the 2026-09-23 restructure above.) All deleted files remain recoverable
> from git history.
>
> **2026-08-14 follow-up:** macOS support was RE-ENABLED (unsigned local build
> — see `docs/CLIENT-GUIDE.md` for the Gatekeeper workaround). The darwin
> code paths (`darwinTUN`, `pfctl` kill-switch, `networksetup` DNS, `ioreg`
> fingerprint), `darwin_link.go`, macOS CI targets, updater URLs, and Makefile
> targets were restored. macOS installs are unsigned: the user must
> right-click → Open (or `xattr -cr`) on first launch.
>
> **2026-08 history archive:** the still-relevant `originals/` docs (business
> model + N4L attacker/defender research) were restored to `docs/history/`
> after review; the rest of `originals/` (superseded action plans) stays in
> git history only.

---

## 3. V5 — What Makes It Different

V5 exists because earlier versions (V1–V4) were spread
across the repo with overlapping docs, untested assumptions, and no central reference.
V5 consolidates everything into one place: **both client and server code**, hardened
from real-world testing, with comprehensive documentation.

### Principles

1. **Only 2 binaries on the client** — locus (GUI + manager) + sing-box (engine).
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
   The updater also refuses anything that is **not strictly newer** than the
   running build, so a stale `update_config` row cannot downgrade the fleet.
5. **No TLS in the tunnel** — Shadowsocks AEAD is indistinguishable from random data.
6. **The tunnel is self-healing** — a 10s watchdog probes real traffic and escalates
   (restart → kill foreign engines + drop the stale `locus0` TUN → start fresh),
   then hands control back to the student rather than churning forever.
7. **Hub TLS is pinned by SPKI** — activation, heartbeat and update downloads
   reject a leaf key that is not on the allow-list. Pinning the *key* (not the
   cert) survives Let's Encrypt renewals. Empty pin list = fail-open with a
   warning, so a factory build still works until an operator sets
   `LOCUS_HUB_PINS`.

### Client Hardening (legacy/wails-client/ vs v4/)

The legacy/wails-client/ codebase is a hardened evolution of the v4 source:

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
- **Tunnel watchdog** — independent 10s probe loop that verifies traffic actually
  flows, with an escalation ladder and a bounded retry count (see §3)
- **Downgrade-proof updater** — numeric version comparison (`1.10.0 > 1.9.0`;
  build metadata ignored), so only strictly-newer builds are ever applied
- **Hub TLS pinning** — SPKI allow-list on every hub connection (`internal/pinned`)
- **Stale-host self-heal** — on start and on connect, orphaned `sing-box`
  processes are killed and a leftover `locus0` TUN is removed, rather than
  telling a student to go use Task Manager
- **Code normalisation** — stored/seeded codes are rewritten to the canonical
  `RQ-XXXX-XXXX-XXXX-C` form at startup, so legacy installs keep heartbeating

---

## 4. VPS Testing Results

All modules were originally proven on a Voyager VPS (Ubuntu 22.04,
kernel 5.15.0-161-generic). They were then **re-proven on a completely blank
box** on 2026-09-19 — which found seven further bugs that only bite on a fresh
host (see `docs/FIXES.md` → "Blank-VPS deploy"). That blank-box run is the more
meaningful result, because a deploy script that only works on an
already-provisioned host is not a deploy script.

**Current live host:** `170.64.196.179` (DigitalOcean, Ubuntu 22.04.5, 1 vCPU /
454 MB RAM + 1 GB swap, Sydney) serving `networkingguides.duckdns.org`.
Earlier hosts `114.23.136.59` and `134.199.155.166` are retired — the latter is
offline. Run `free -m` on any change; 454 MB is tight and the memory guard in
`00-env.sh` is deliberately tuned for it.

| Module | Status | Notes |
|--------|:------:|-------|
| 00-env | ✅ | OS, arch, root, disk, memory all validated (**memory guard fixed** — it rejected 512MB droplets) |
| 01-bbr | ✅ | BBR active, TCP tuning params set |
| 02-shadowsocks | ✅ | 3 instances installed and enabled |
| 04-tc | ✅ | Eco 5Mbit, Stealth 100Mbit, Strike 200Mbit classes active (+ fq_codel leaf qdiscs) |
| 05-caddy | ✅ | Caddy with ratelimit plugin; now also `/admin/` SPA + `/updates/` file server. Console bundle is a **required** deploy input |
| 06-pocketbase | ✅ | 0.22.21 installed; bootstrap failure is now **fatal**, not a warning |
| 07-backups | ✅ | Timer enabled; verification now hashes the **downloaded** artifact |
| 08-firewall | ✅ | UFW active, all ports open, SSH protected by **fail2ban** |

End state of the blank-box run: all 8 modules exit 0, full `setup.sh` re-runs are
idempotent, and `smoke-test.sh` reports **23 passed / 0 failed / 0 warnings**.

**Zero-touch deploy (since the 2026-09 config pass):** a fresh `setup.sh` needs
no follow-up. It deploys the admin console and the release fetch service (both now
**required** — a missing bundle fails the deploy instead of warning), installs
the Strike UDP-over-TCP endpoint on 8446 and opens it in the firewall, seeds
tier configs with `uot_port` advertised, and installs everything enabled at
boot. Optional extras: `FIRST_BATCH=<n>` (+ `FIRST_BATCH_MIDDLEMAN`,
`FIRST_BATCH_EXPIRES`) mints a first batch of codes once, recorded in
`/root/.first_batch_done` so a re-run cannot create duplicate inventory. Opt
outs: `ENABLE_UOT=0`, `SKIP_CONSOLE=1`, `SKIP_DNS_CHECK=1`.

### 9 Issues Found & Fixed (first VPS round)

All documented in `docs/FIXES.md`:

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
| S9 | 🟡 | Secrets decryption via process substitution failed silently | Temp file instead of `source <(cmd)` |

### Blank-VPS deploy — 7 further bugs (2026-09-19)

A fresh host is a different test from a re-run, and it found bugs that had been
invisible for months. Full write-ups in `docs/FIXES.md`; the headlines:

| # | Issue | Consequence |
|:-:|-------|-------------|
| 1 | `00-env.sh` memory guard compared against 512 **MiB** | A "512MB" droplet reports 454MB — impossible to pass on the hardware it targeted |
| 2 | `seed-pb.py` used `/api/collections/_superusers/` (404 on PB 0.22) | **No admin, no collections, no schema** — while printing "✓ complete" |
| 3 | `06-pocketbase.sh` treated that as a warning | A hub serving 500s looked deployed; now fatal + health-poll |
| 4 | `08-firewall.sh` used `ufw limit 22/tcp` (6 conns/30s) | Locked out deployment automation; replaced with fail2ban |
| 5 | `smoke-test.sh`: `tc … | grep -q` SIGPIPEs `tc` | Under `pipefail` → exit 141, false tc failures |
| 6 | `update_config` duplicated a row every deploy | Multiple active rows; now reconciled |
| 7 | `findFirstRecordByData` **throws** on a miss | Unknown codes returned HTTP 500 instead of 404 in all four hooks |

The single most dangerous finding, though, was not a deploy bug at all:
**`$app.dao().findRecordsByFilter()` silently returns an empty array** on this
PocketBase build (0.22.21) for every collection and filter, with no error. It had
quietly disabled the **client update gate**, the **activation rate limit** and the
**code-lookup rate limit** — the latter two turning `/api/activate` and
`/api/code-lookup` into unbounded code-enumeration oracles. Use
`findFirstRecordByFilter` / `findRecordsByExpr` / `findFirstRecordByData` instead.
See `docs/FIXES.md` → "Client update system" for the full list.

⚠️ **Do not** re-add `ufw limit 22/tcp` and **do not** inject a custom limiter
chain into `/etc/ufw/before.rules` — the latter locked SSH out completely (port
22 timed out while 80/443 served) and needed provider-console recovery.

---

## 5. Client Code Structure (legacy/wails-client/)

> **⚠️ Updated for the Wails migration (2026).** The GUI moved from Fyne to
> **Wails v2 + Vue 3** (`frontend/`). The old Fyne GUI, helper binary, and old
> entry point were removed in the Wails migration (the `v5/legacy/` reference
> copy was deleted in the 2026-08 cleanup; pre-migration client in `v4/`,
> git history for the removed files). The `internal/` backend packages are
> unchanged. See `docs/WAILS-MIGRATION.md` and `docs/BACKEND-API.md`.

```
legacy/wails-client/
├── main.go                      # Wails app entry (embedds frontend/dist, binds App)
├── app.go                       # App struct — wraps internal/ for the Vue UI
├── wails.json                   # Wails project configuration
├── internal/
│   ├── storage/storage.go      # Persistent JSON state (thread-safe, atomic writes, backups)
│   ├── activation/
│   │   ├── activation.go       # Activation client with retry + context + ValidateCodeFormat
│   │   ├── fingerprint_linux.go   # Self-contained fingerprint (shared logic + Linux collector)
│   │   ├── fingerprint_windows.go # Self-contained fingerprint (shared logic + Windows collector)
│   │   ├── fingerprint_darwin.go  # Self-contained fingerprint (shared logic + macOS collector)
│   │   └── luhn.go             # Luhn-mod-N checksum validation
│   ├── heartbeat/heartbeat.go  # Periodic server health check with jitter
│   ├── manager/
│   │   ├── process.go          # sing-box lifecycle + config generation
│   │   ├── watchdog.go         # 10s tunnel probes + recovery ladder (restart → reset → degraded)
│   │   ├── selfheal_{unix,windows}.go  # kill foreign engines, drop stale locus0 TUN
│   │   └── process_{unix,windows}.go   # process-group detach / Windows specifics
│   ├── pinned/pinned.go        # Hub TLS SPKI pinning (fail-closed once configured)
│   ├── tray/tray.go            # OPT-IN system tray (LOCUS_TRAY=1); no-op on darwin
│   ├── tunnel/tunnel.go        # TUN interface + kill switch (per-platform)
│   └── updater/
│       ├── updater.go          # Two-phase update with crash safety
│       ├── recover.go          # Crash detection and auto-revert
│       ├── version.go          # Numeric version compare — refuses downgrades
│       ├── update_linux.go     # Linux binary swap + fork
│       ├── update_windows.go   # Windows binary swap + fork
│       └── update_darwin.go    # macOS binary swap + fork
├── frontend/                    # Vue 3 + TypeScript + Vite UI
│   └── src/                    # App.vue, components/, stores/, lib/bridge.ts
├── engines/README.md           # Engine binary placeholder
├── rsrc_windows_*.syso         # requireAdministrator manifest (.syso, go:generate)
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
| Total lines (Go) | ~7,700 across 38 files (main + 8 internal packages) |
| Client version | `the root VERSION file` (single source; injected by Makefile/CI) |
| Engine | sing-box 1.12.1 (client bundle + optional server UoT both pin 1.12.1) |
| Min Go version | 1.22 |
| Platforms | Linux, macOS (Intel+ARM, unsigned), Windows |
| Dependencies | Wails v2.12.0 + Vue 3 (Fyne removed) |

---

## 6. Architecture Overview

```
┌──────────────────────────────────────────────────────┐
│                   CLIENT DEVICE                        │
│                                                        │
│  ┌────────────────────────────────────────────────┐   │
│  │              locus (Go + Wails / Vue 3)            │   │
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
| GUI toolkit | Wails v2 | 2.12.0 |
| Frontend | Vue 3 + Vite + TypeScript | ^3.4.0 |
| Tunnel engine | sing-box | 1.12.1 |
| Server OS | Ubuntu | 22.04 |
| Proxy protocol | Shadowsocks (ssserver-rust) | v1.23.0 |
| Reverse proxy | Caddy (custom rate_limit) | Latest |
| Database | PocketBase | 0.22.21 |
| TCP CC (all tiers) | BBR | Kernel built-in |
| Backups | Backblaze B2 | — |
| Traffic shaping | tc (HTB qdisc) | — |
| Firewall | UFW + fail2ban | — |

---

## 8. Agent Guidance

### Where to start

1. **Read `docs/README-legacy-v5.md`** for the high-level overview and quick start.
2. **Read `docs/ARCHITECTURE.md`** for the complete system design.
3. **Refer to `legacy/wails-client/`** for the client source code.
4. **Refer to `server/`** for server deployment.
5. **Use `docs/`** for specific guides (deploy, ops, API, implement).

### Rules of thumb

1. **Start here.** V5 is the definitive version. Don't read v4/
   unless you need historical context.
2. **The client code is in `legacy/wails-client/`.** The old `v4/` directory exists for
   reference only — never build from it.
3. **The server modules are idempotent.** You can re-run any module safely.
   Each module checks if its work is already done before proceeding.
4. **The JS hooks have been rewritten for PocketBase 0.22+.** If activation
   returns a generic 400 error after a fresh deploy, check `journalctl -u pocketbase`
   for hook load errors.
5. **All tiers use BBR — no kernel modules to maintain.** Bandwidth caps are tc-based
   (Eco 5 / Stealth 100 / Strike 200 Mbps); after a kernel update, re-apply with
   `systemctl restart tc-eco-cap tc-stealth-cap tc-strike-cap`.
6. **The server domain is `networkingguides.duckdns.org`** pointing to `170.64.196.179`
   (DigitalOcean, Sydney; verified 2026-09-19). DNS is managed by the hosting
   provider — no DuckDNS updater runs on the box. Older notes cite `.59`/`.166`;
   those hosts are retired.
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
10. **Use the admin console for day-to-day work.** `https://…/admin/` replaces SSH +
    Python + sqlite for issuing codes, suspending/unbinding, editing tiers and
    publishing releases. The SSH recipes in `docs/OPS.md` remain the fallback path
    (and are what the console itself calls).
11. **Do not bulk-delete `codes` or `code_events`.** Real customer data lives
    there — see the live-data warning at the top of this file.

### Key credentials (live VPS)

```
PocketBase admin:   admin@networkingguides.duckdns.org
PocketBase UI:      https://networkingguides.duckdns.org/_/
Admin API token:    <see /root/.admin_api_token on the VPS>
B2 bucket:          vpsvpnbackup
```

> Earlier revisions of this file embedded the literal admin token. It has been
> redacted, but it remains **in git history** — if that history is ever pushed
> somewhere less trusted, rotate the token (`ADMIN_API_TOKEN` in `/etc/environment`
> on the VPS, then update `/root/.admin_api_token` and the console's bookmark).
> Rotating it invalidates any `/admin/?token=…` bookmark.

All credentials are stored on the VPS at `/root/` — see `docs/POCKETBASE-SETUP.md` for
the full list of credential files and locations. Secrets never go in the repo in
plaintext; they live age-encrypted in `server/secrets.env.age`
(see `docs/SECRETS-MANAGEMENT.md`).

### Common pitfalls

- **Don't use `v4/` as the source of truth** — it's
  historical reference only. V5 is the authoritative version.
- **The client code now lives in `legacy/wails-client/`** — not v4/. Always build from legacy/wails-client/.
- **The JS hooks have been rewritten for PocketBase 0.22+** — if activation still returns a generic
  400 error after a fresh deploy, check `journalctl -u pocketbase` for hook load errors.
- **A new hook file needs a PocketBase restart.** Editing an existing `*.pb.js`
  hot-reloads; *adding* one does not. This is the usual reason a newly deployed
  endpoint 404s.
- **Never trust `findRecordsByFilter` in a hook.** It returns zero rows with no
  error on PB 0.22.21. See the note in §4.
- **Helper functions must be declared INSIDE a `routerAdd` handler.** File-scope
  declarations are not visible to the callback ("helperFn is not defined") — this
  silently 500'd an entire hook for its whole life.
- **DNS is managed by the hosting provider** — the VPS hostname `networkingguides.duckdns.org`
  resolves to the VPS IP automatically. No DuckDNS updates needed.
- **The live hub is production, and there is no sandbox.** Stage changes on the
  VPS copy under `/root/server/`, never experiment on the live hooks.
