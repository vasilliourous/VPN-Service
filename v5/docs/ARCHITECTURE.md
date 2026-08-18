# MyVPN Architecture Guide

> This document describes the current client architecture. The GUI layer is
> **Wails + Vue 3** (see [`WAILS-MIGRATION.md`](WAILS-MIGRATION.md) for the
> migration history and rollback plan). The backend components (activation,
> heartbeat, manager, storage, updater) and the TUN-based design described here
> are accurate against the code in `v5/client/`; see [`BACKEND-API.md`](BACKEND-API.md)
> for their exact API surface.

> **How to architect a compatible MyVPN client.** This document describes the
> key components, their responsibilities, and how they fit together — from an
> implementation perspective. Use it alongside `CLIENT-GUIDE.md` (build steps),
> `API.md` (server contracts), and `CONTEXT.md` (background reasoning).

---

## 1. High-Level Architecture

```
┌──────────────────────────────────────────────────────┐
│                   CLIENT DEVICE                        │
│                                                        │
│  ┌────────────────────────────────────────────────┐   │
│  │              myvpn (Go + Wails / Vue 3)            │   │
│  │                                                  │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────────┐   │   │
│  │  │Activation│  │ Heartbeat │  │   Updater    │   │   │
│  │  │ Client   │  │ 5min→2h   │  │ 2-phase SAFE │   │   │
│  │  └────┬─────┘  └────┬─────┘  └──────┬───────┘   │   │
│  │       │              │               │            │   │
│  │  ┌────┴──────────────┴───────────────┴───────┐   │   │
│  │  │              Manager                       │   │   │
│  │  │  • Generates sing-box JSON config          │   │   │
│  │  │  • Spawns sing-box as subprocess           │   │   │
│  │  │  • Monitors process health                 │   │   │
│  │  │  • Performs graceful shutdown              │   │   │
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

> **⚠️ Important: This is a TUN-based VPN, not a SOCKS5 proxy.**
>
> The diagram above is accurate — there is **no SOCKS5 proxy layer** between the
> app and the tunnel. sing-box creates a **TUN interface directly** and routes all
> device traffic through it. This avoids the +2 RTT handshake overhead, per-app
> proxy configuration, and complexity of a SOCKS5-based design.
>
> SOCKS5 was used in V1–V4 (via `ss-local`), but V5 eliminated it entirely.
> sing-box's native TUN inbound renders the SOCKS5 layer unnecessary:
> no `ss-local`, no `tun2socks`, no extra process. Just sing-box managing
> the TUN device and Shadowsocks outbound in one binary.
>
> The only elevated privilege needed is TUN device creation. On BYOD machines
> the user has admin rights, so sing-box creates the TUN interface directly —
> no privileged helper service is shipped (the legacy `myvpn-helper` binary
> was removed in the Wails migration).
>
> See [§2.4 Manager](#24-manager-internalmanager) for the exact sing-box
> config that makes this work.

---

## 2. Components & Responsibilities

### 2.1 Main Entry Point (`main.go`)

Wails entry point (`v5/client/main.go`). Responsibilities:
1. Create the `App` struct (wraps all `internal/` packages)
2. Run `wails.Run()` — 480×700 window, shown at launch, binds `App` to the
   Vue 3 frontend
3. `App.Startup()` performs initialization (see §3): storage → activation
   client → fingerprint → sing-box discovery → update recovery → system tray

The current `main.go` does not parse CLI flags. The updater still supports a
`--revert` flag via `updater.CheckOnStartup(revertFlag)`, but it is not wired
into the Wails entry point.

### 2.2 Storage (`internal/storage/`)

Persistent state management. Single JSON file, thread-safe, atomic writes.

**Data stored:**
```json
{
  "code": "RQ-ABCD-EFGH-JKMN-T",
  "tier": "eco",
  "device_fingerprint": "<sha256>",
  "server_config": { "server": "...", "server_port": 8443, "password": "...", "method": "aes-256-gcm" },
  "udp_relay": false,
  "activated": true,
  "version": "2.0.0",
  "update_pending": false,
  "update_version": "",
  "update_sha256": "",
  "update_timestamp": 0,
  "last_heartbeat_ok": 1700000000,
  "heartbeat_failures": 0,
  "crashed_on_update": false,
  "crash_timestamp": 0
}
```

**File location:** `os.UserConfigDir()/myvpn/storage.json` — `~/.config/myvpn/storage.json` (Linux), `%APPDATA%\myvpn\storage.json` (Windows).

**Atomic write strategy:** Write to `.tmp` file, then `rename()` over target. This prevents corruption from crashes during write.

**Key methods:**
- `SetActivation(code, tier, fingerprint, config, udpRelay)`
- `SetHeartbeat(timestamp)` / `SetHeartbeatFailure(timestamp)`
- `SetUpdatePending(version, sha256, timestamp)` / `ClearUpdatePending()`
- `GetData()` — returns a copy (thread-safe via RWMutex)

### 2.3 Activation (`internal/activation/`)

Two responsibilities:
1. **Client-side Luhn-mod-N validation** — validates code format before any server call
2. **Server activation** — sends code + fingerprint to hub

**Flow:**
```
User enters code → strip formatting → Luhn-mod-N check
    ↓ fail → show error (no server call needed)
    ↓ pass → POST /api/activate with fingerprint
        ↓ success → save to storage
        ↓ fail → show error
```

**Hardware fingerprinting:**
- SHA256 of MAC address + disk serial + motherboard UUID
- Platform-specific collection (Linux: sysfs, Windows: PowerShell/WMI)
- Fallback chain: full hardware → MAC+hostname → random UUID (persisted)
- Fingerprint is STABLE once generated (cached in memory, persisted in storage)

**Code format:** `RQ-XXXX-XXXX-XXXX-C` (15 chars, base-32 charset excluding I/O/0/1, Luhn-mod-N checksum)

### 2.4 Manager (`internal/manager/`)

Manages the sing-box process lifecycle.

**sing-box config generation:** Takes server config from storage and generates a valid sing-box JSON config.

> **Key point:** The inbound is `type: "tun"`, *not* `type: "socks"`. This is
> what makes the VPN a true TUN-based VPN rather than a SOCKS5 proxy.
> No SOCKS5 handshake overhead, no per-app proxy configuration, no `ss-local`.
> sing-box does everything — TUN creation, traffic routing, DNS, and
> Shadowsocks encryption — in a single process.

The generated config looks like this:
```json
{
  "log": { "level": "warn" },
  "dns": { "final": "dns-tunnel",
           "servers": [
             { "type": "https", "tag": "dns-tunnel", "server": "1.1.1.1", "server_port": 443, "detour": "proxy" },
             { "type": "https", "tag": "dns-direct", "server": "1.1.1.1", "server_port": 443, "detour": "direct" }
           ] },
  "inbounds": [{ "type": "tun", "tag": "tun-in", "interface_name": "myvpn0",
                 "address": ["10.0.0.1/30"], "mtu": 1500, "auto_route": true, "strict_route": false,
                 "sniff": true }],
  "outbounds": [
    { "type": "shadowsocks", "tag": "proxy",
      "server": "networkingguides.duckdns.org", "server_port": 8443,
      "method": "aes-256-gcm", "password": "..." },
    { "type": "direct", "tag": "direct" },
  ],
  "route": { "rules": [{ "protocol": "dns", "action": { "type": "dns" } }],
             "auto_detect_interface": true, "final": "proxy" }
}
```

(`MYVPN_DEBUG=1` switches the log level to `debug`. UDP is NOT wrapped in
UDP-over-TCP: sing-box's `udp_over_tcp` is a proprietary SagerNet protocol
(magic domains `sp.udp-over-tcp.arpa` / `sp.v2.udp-over-tcp.arpa`) that
shadowsocks-rust rejects with RST (observed 2026-08-01 — see FIXES.md
Follow-up 9). `udp_relay` is informational; UDP goes raw (standard ss UDP,
server `tcp_and_udp` on Strike) and works only where the network allows UDP.)

**Process lifecycle:**
- `Start()` → generate config → write config file → spawn sing-box → 500ms
  startup probe (immediate exit = config/permission error)
- Health loop → every 10s, check the process is alive (`signal 0`); on death,
  auto-restart up to 3 times within a 5-minute window
- `Stop()` → graceful shutdown (wait up to 10s) → force kill if still running
  → remove the config file

**No privileged helper needed** (BYOD = admin rights). sing-box creates TUN directly.

**Windows elevation.** TUN creation requires administrator rights, so the Windows
build embeds a `requireAdministrator` application manifest
(`rsrc_windows_amd64.syso` / `rsrc_windows_arm64.syso`, generated by
`go generate -tags windows` via `github.com/tc-hib/go-winres`). Windows therefore
shows a UAC prompt at app launch and runs the whole app elevated; Connect() does
the real TUN work directly and never restarts the window. If a build ships
without the manifest (runs `asInvoker`), `Connect()` falls back to a one-time
runtime UAC relaunch that passes `--autoconnect` so the elevated instance
reconnects automatically.

### 2.5 Heartbeat (`internal/heartbeat/`)

Periodic communication with hub. Runs in a background goroutine.

**Timing:**
- Starting interval: 5 minutes
- On failure: double interval (10min → 20min → ... → 2h max)
- On success: reset to 5 minutes

**Request:**
```
POST /api/heartbeat
Content-Type: application/json

{ "code": "RQ-...", "fingerprint": "<sha256>" }
```
(POST with a JSON body — the code never appears in query strings or access logs.)

**Response fields used:**
- `status`: "ok" or error
- `tier`: tier name (for display)
- `server_config`: refreshed server config (change IPs, passwords without client update)
- `update_available`, `update_url`, `update_sha256`: staged rollout signal

**Grace period:** Client tracks `last_heartbeat_ok`. If heartbeat fails for 7 days,
the UI shows the remaining grace days and the heartbeat failure count; the VPN
keeps working during the grace period.

### 2.6 Updater (`internal/updater/`)

Crash-safe update system. No code signing needed.

**Two-phase sentinel flow:**
```
1. Download new binary to temp location
2. Verify SHA256 checksum
3. Create .update-pending sentinel file
4. Swap binary (platform-specific rename)
5. Fork new process with same args
6. Parent exits

On new process start:
7. See .update-pending → create .update-confirmed
8. Remove .update-pending
9. Continue normal startup

On crash before step 7:
  Next start sees .update-pending (no .update-confirmed)
  → Auto-revert to backup binary from .myvpn-backups/
  → Place .reverted sentinel
```

**Staged rollout:** Heartbeat returns update only if `hash(fingerprint) % 100 < rollout_percent`.
Server sets `rollout_percent` in PocketBase `update_config` collection.

**Key files:**
- `.update-pending` — binary was swapped, new version hasn't confirmed yet
- `.update-confirmed` — new version started successfully
- `.reverted` — auto-revert happened (diagnostic)
- `.myvpn-backups/` — previous binary kept for rollback

### 2.7 GUI (`frontend/` — Wails + Vue 3)

The UI is a Vue 3 + Vite + TypeScript SPA embedded into the binary
(`//go:embed all:frontend/dist` behind the `frontend` build tag) and rendered
in a Wails WebView. Two screens (switched by `App.vue`):
1. **Activation screen** — code input with auto-formatting + live Luhn validation, tier info, activate button
2. **Main screen** — status indicator + tier badge, status circle, connect/disconnect button, stats (engine state, heartbeat failures, grace days), diagnostics modal

**Window behaviour:** the window is shown on launch (Wails v2.9 has no system
tray API, so a `StartHidden` app would be permanently invisible). Closing the
window quits the app — the `tray:show` / `tray:quit` hooks in `setupSystemTray`
are dormant (no tray icon exists yet). The background colour is set natively to
avoid a white flash while the WebView loads.

**Black + purple theme** (`#0D0D0F` background, `#A855F7` accent — see
`UI-AESTHETICS.md`). No technical protocol names visible — just "Connected" /
"Disconnected" with a tier badge (Eco/Stealth/Strike).

The old Fyne GUI was removed in the Wails migration (see git history); the
pre-migration client lives in `v4/` (reference only).

### 2.8 sing-box (Engine)

Downloaded separately (bundled with installer or downloaded at runtime).
Platform-specific binary.

**Responsibilities:**
- Create TUN device (`myvpn0`, `10.0.0.1/30`)
- Route all IPv4 traffic through TUN
- Shadowsocks TCP outbound to VPS
- DNS through tunnel (1.1.1.1 DoH)
- Handle reconnection internally (sing-box has built-in retry)

**Distribution:** the sing-box binary is downloaded during CI and bundled
alongside `myvpn` in each release ZIP (`engines/` is a local placeholder).
The current client does not download engines at runtime.

**No separate SOCKS5 or tun2socks layer** — sing-box does everything in one process.

---

## 3. Startup Sequence

```
Wails App.Startup():
  1. storage.New("myvpn") — load or create state
  2. activation.NewClient(hubURL)
  3. GenerateFingerprint()
  4. findSingBox() — alongside the executable, then system paths
  5. manager.NewManager(...) + SetHelperMode(false) — always direct mode
  6. updater.CleanStaleMarkers(48h) + CheckOnStartup(false) + ConfirmIfPending(appDir)
   7. setupSystemTray() — dark background; dormant tray hooks (no tray icon in v2.9)
  8. If already activated → startHeartbeatLoop(code)

Connect button:
  1. Manager generates sing-box config from stored ServerConfig
  2. Manager.Start() — spawn sing-box, 500ms startup probe
  3. Manager health loop — signal-0 check every 10s, auto-restart up to
     3 times within a 5-minute window

Disconnect button:
  1. Manager.Stop() — graceful wait up to 10s, then force kill; remove config file

Shutdown:
  1. Disconnect (if connected)
  2. Heartbeat.Stop()
```

---

## 4. State Machine

```
                  ┌─────────┐
                  │  IDLE   │ ← App launched, not activated
                  └────┬────┘
                       │ Activation success
                  ┌────▼────┐
                  │ ACTIVE  │ ← Activated, not connected
                  └────┬────┘
                       │ User clicks Connect
                  ┌────▼─────────┐
                  │  CONNECTING  │ ← Manager spawning sing-box
                  └────┬─────────┘
                       │ sing-box confirms running
                  ┌────▼────────────────┐
                  │  CONNECTED_PRIMARY  │ ← Shadowsocks tunnel active
                  └────┬────────────────┘
                       │ Health check fails × 3
                  ┌────▼──────────┐
                  │   DEGRADED    │ ← Trying to recover
                  └────┬──────────┘
                       │ All fallbacks exhausted
                  ┌────▼──────────────┐
                  │  DISCONNECTED     │ ← "Tap to retry"
                  └────┬──────────────┘
                       │ User taps retry → back to ACTIVE
                  ┌────▼────┐
                  │ ACTIVE  │
                  └─────────┘
```

Additional orthogonal state: **GRACE** — heartbeat has failed but 7-day grace
period hasn't expired. The connection continues working normally.

> **Note:** the current client implements a simplified version of this model.
> The UI tracks `connected` (bool), the engine state from
> `manager.State()` (`"running"` / `"stopped"` / `"crashed"`), heartbeat
> failure count, and remaining grace days. The intermediate
> CONNECTING/DEGRADED/CONNECTED_PRIMARY states and the full fallback-engine
> loop are not implemented.

---

## 5. Data Flow

```
Activation:
  Code + Fingerprint ──POST──► Hub validates ──► Returns tier + server config
                                     │
                               Saves to storage (persistent)
                                     │
                               Heartbeat begins (5min loop, doubling to 2h on failure)

Connection:
  Manager reads server config from storage
  ↓
  Generates sing-box JSON config
  ↓
  Writes config to temp file
  ↓
  Spawns sing-box --config <file>
  ↓
  sing-box creates TUN, establishes Shadowsocks TCP to VPS
  ↓
  All traffic routed through TUN → sing-box → Shadowsocks → VPS → Internet

Update:
  Heartbeat response has update_available + url + sha256
  ↓
  Updater downloads binary, verifies checksum
  ↓
  Two-phase swap: pending → fork → confirmed
  ↓
  Old binary in .myvpn-backups/ (can revert with --revert or auto-revert on crash)
```

---

## 6. Platform Support

| Component | Linux | macOS | Windows |
|-----------|:-----:|:-----:|:-------:|
| Wails + Vue 3 GUI | ✅ | ✅ (unsigned) | ✅ |
| Hardware fingerprint | ✅ sysfs | ✅ networksetup / ioreg | ✅ PowerShell / WMI |
| sing-box TUN | ✅ direct | ✅ direct | ✅ direct |
| Binary swap | ✅ rename | ✅ rename | ✅ .old trick |
| sing-box binary | linux-amd64 | darwin-amd64 / arm64 | windows-amd64 |

**No privileged helper service needed** — BYOD environment means the user has
admin rights. sing-box creates TUN interfaces directly.

**macOS note:** the build is unsigned — Gatekeeper blocks first launch; the
user must right-click → Open or run `xattr -cr` (see `CLIENT-GUIDE.md`).

---

## 7. Key Implementation Rules

| Rule | Why |
|------|-----|
| **Only 2 binaries** (myvpn + sing-box) | Less breakage surface area. No helper, no tun2socks, no sslocal. |
| **No TLS in the tunnel** | JA3 fingerprinting is the #1 detection method. Shadowsocks AEAD has no TLS fingerprint. |
| **UDP attempted raw, TCP fallback** | N4L drops all UDP, so UDP (incl. Strike's relay) only works on permissive networks; browsers fall back to TCP. sing-box's UoT is proprietary — never enabled (see FIXES.md Follow-up 9). |
| **Server-enforced caps** | Client can't bypass its tier cap. tc caps (5/100/200 Mbps) are on the VPS. |
| **Permanent device binding** | One code = one device forever. No deactivation. Admin can suspend (not destroy) binding. |
| **Crash-safe updates** | Two-phase sentinel with auto-revert. No update signing keys needed. |
| **Grace period is client-enforced** | Client tracks heartbeat timestamps. 7 days of silence → grace warning shown in UI. |
| **Storage is plain JSON** | No app-level encryption. OS disk encryption is assumed. |

---

## 8. Related Documents

| Document | Purpose |
|----------|---------|
| `CONTEXT.md` | **Start here** — project history, reasoning, network analysis, agent guidance |
| `CLIENT-GUIDE.md` | Build commands, package structure, platform notes, testing checklist |
| `API.md` | Server API contracts (activation, heartbeat, admin, hiddify) |
| `BACKEND-API.md` | Complete API reference for all `internal/` packages |
| `DEPLOY.md` | Server deployment from blank VPS |
| `SECRETS-MANAGEMENT.md` | Age-encrypted secrets workflow (secrets.env.age) |
| `POCKETBASE-SETUP.md` | PocketBase collections, hooks, admin setup |
| `OPS.md` | Day-to-day operations |
| `CI-CD.md` | GitHub Actions workflow, build matrix, releases |
| `IMPLEMENT.md` | Phased implementation plan (historical) |
| `GAMING-UDP.md` | **Planned:** sing-box server + UDP-over-TCP change plan for gaming on hostile-UDP networks |
| `WAILS-MIGRATION.md` | Fyne → Wails migration history & build notes |
| `UI-AESTHETICS.md` | Visual design spec (colors, layout, icons) |
| `FIXES.md` | Issues discovered & fixes applied (dated log) |
| `history/` | Curated pre-V5 research archive (business model, N4L threat analysis) — reference only |
