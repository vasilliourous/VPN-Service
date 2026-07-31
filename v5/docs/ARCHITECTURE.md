# MyVPN Architecture Guide

> **⚠️ PARTIALLY SUPERSEDED.** This document describes the client architecture.
> The GUI layer moved from Fyne to **Wails + Vue 3** — see
> [`WAILS-MIGRATION.md`](WAILS-MIGRATION.md). The backend components (activation,
> heartbeat, manager, storage, updater) and the TUN-based design described here
> remain accurate; see [`BACKEND-API.md`](BACKEND-API.md) for their exact API.

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
│  │              myvpn (Go + Fyne)                   │   │
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
│  │  rate    │  │ JS hooks │  │  BBR/Brutal/BBR       │  │
│  │  limit   │  │          │  │  5/48/200 Mbps        │  │
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
> The only elevated privilege needed is TUN device creation — handled by
> `myvpn-helper` (`pkexec`/`sudo`/UAC). Once the TUN interface is up,
> sing-box runs as a regular user process.
>
> See [§2.4 Manager](#24-manager-internalmanager) for the exact sing-box
> config that makes this work.

---

## 2. Components & Responsibilities

### 2.1 Main Entry Point (`cmd/myvpn/main.go`)

Thin launcher. Responsibilities:
1. Parse flags (`--hub`, `--revert`, `--version`)
2. Run update recovery check (auto-revert if previous update crashed)
3. Initialize storage
4. Launch GUI

No business logic. No networking. Just orchestration.

### 2.2 Storage (`internal/storage/`)

Persistent state management. Single JSON file, thread-safe, atomic writes.

**Data stored:**
```json
{
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
  "tier": "eco",
  "device_fingerprint": "<sha256>",
  "server_config": { "server": "...", "server_port": 8443, "password": "...", "method": "aes-256-gcm" },
  "udp_relay": false,
  "activated": true,
  "version": "1.0.0",
  "update_pending": false,
  "last_heartbeat_ok": 1700000000,
  "heartbeat_failures": 0
}
```

**File location:** `~/.config/myvpn/storage.json` (Linux), `~/Library/Application Support/MyVPN/` (macOS), `%APPDATA%\MyVPN\` (Windows).

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
- Platform-specific collection (Linux: sysfs, macOS: IOKit, Windows: wmic)
- Fallback chain: full hardware → MAC+hostname → random UUID (persisted)
- Fingerprint is STABLE once generated (cached in memory, persisted in storage)

**Code format:** `MYVPN-XXXX-XXXX-XXXX-C` (18 chars, base-32 charset excluding I/O/0/1, Luhn-mod-N checksum)

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
  "dns": { "final": "1.1.1.1", "servers": { "default": { "address": "1.1.1.1", "detour": "proxy" } } },
  "inbounds": [{ "type": "tun", "tag": "tun-in", "interface_name": "myvpn0",
                 "address": "10.0.0.2/30", "mtu": 1500, "auto_route": true, "strict_route": true }],
  "outbounds": [{ "type": "shadowsocks", "tag": "proxy",
                  "server": "networkingguides.duckdns.org", "server_port": 8443,
                  "method": "aes-256-gcm", "password": "..." }],
  "route": { "rules": [{"rule": "geoip:private", "outbound_tag": "direct"}],
             "auto_detect_interface": true, "final": "proxy" }
}
```

**Process lifecycle:**
- `Start()` → write config file → spawn sing-box → verify it's running
- `Stop()` → send SIGTERM → wait 5s → SIGKILL if still running
- Health check → verify process is alive + TUN interface exists

**No privileged helper needed** (BYOD = admin rights). sing-box creates TUN directly.

### 2.5 Heartbeat (`internal/heartbeat/`)

Periodic communication with hub. Runs in a background goroutine.

**Timing:**
- Starting interval: 5 minutes
- On failure: double interval (10min → 20min → ... → 2h max)
- On success: reset to 5 minutes

**Request:**
```
GET /api/heartbeat?code=MYVPN-...&fp=<sha256>
```

**Response fields used:**
- `status`: "ok" or error
- `tier`: tier name (for display)
- `server_config`: refreshed server config (change IPs, passwords without client update)
- `update_available`, `update_url`, `update_sha256`: staged rollout signal

**Grace period:** Client tracks `last_heartbeat_ok`. If heartbeat fails for 7 days,
show "tap to retry" — don't auto-loop forever.

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

### 2.7 GUI (`internal/gui/`)

Fyne v2 desktop application. Two screens:
1. **Activation screen** — code input, tier info, activate button
2. **Main screen** — connection status, connect/disconnect, timer, speed indicator

**No system tray for MVP** — user opens app to connect, can minimise but closing
disconnects. System tray can be added later.

**Black + purple theme.** No technical protocol names visible. Just "Connected" / "Disconnected"
with tier badge (Eco/Stealth/Strike).

### 2.8 sing-box (Engine)

Downloaded separately (bundled with installer or downloaded at runtime).
Platform-specific binary.

**Responsibilities:**
- Create TUN device (10.0.0.2/30)
- Route all IPv4 traffic through TUN (except RFC1918 local)
- Shadowsocks TCP outbound to VPS
- DNS through tunnel (1.1.1.1)
- Handle reconnection internally (sing-box has built-in retry)

**No separate SOCKS5 or tun2socks layer** — sing-box does everything in one process.

---

## 3. Startup Sequence

```
main.go:
  1. Parse flags
  2. updater.CheckOnStartup() — auto-revert if crash detected
  3. storage.New() — load or create config

GUI:
  4. Check storage.IsActivated()
     └─ No → Show activation screen
     └─ Yes → Show main screen
  
  5. On connect button:
     a. Manager generates sing-box config
     b. Manager.Start() — spawn sing-box
     c. Begin health checks (every 15s through TUN)
     d. Heartbeat.Start() — begin 60s loop

  6. On disconnect button:
     a. Heartbeat.Stop()
     b. Manager.Stop() — SIGTERM → SIGKILL
     c. Clean up TUN interface
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

---

## 5. Data Flow

```
Activation:
  Code + Fingerprint ──POST──► Hub validates ──► Returns tier + server config
                                     │
                               Saves to storage (persistent)
                                     │
                               Heartbeat begins (60s loop)

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
| Fyne GUI | ✅ | ✅ | ✅ |
| Hardware fingerprint | ✅ sysfs | ✅ IOKit | ✅ wmic |
| sing-box TUN | ✅ direct | ✅ direct | ✅ direct |
| Binary swap | ✅ rename | ✅ rename | ✅ .old trick |
| sing-box binary | linux-amd64 | darwin-amd64 / arm64 | windows-amd64 |

**No privileged helper service needed** — BYOD environment means the user has
admin rights. sing-box creates TUN interfaces directly.

---

## 7. Key Implementation Rules

| Rule | Why |
|------|-----|
| **Only 2 binaries** (myvpn + sing-box) | Less breakage surface area. No helper, no tun2socks, no sslocal. |
| **No TLS in the tunnel** | JA3 fingerprinting is the #1 detection method. Shadowsocks AEAD has no TLS fingerprint. |
| **No UDP in tunnel** | N4L drops all UDP. TCP only (except Strike's UDP-over-TCP wrapper). |
| **Server-enforced caps** | Client can't bypass its tier cap. tc and Brutal rate targets are on the VPS. |
| **Permanent device binding** | One code = one device forever. No deactivation. Admin can suspend (not destroy) binding. |
| **Crash-safe updates** | Two-phase sentinel with auto-revert. No update signing keys needed. |
| **Grace period is client-enforced** | Client tracks heartbeat timestamps. 7 days of silence → "tap to retry". |
| **Storage is plain JSON** | No app-level encryption. OS disk encryption is assumed. |

---

## 8. Related Documents

| Document | Purpose |
|----------|---------|
| `CLIENT-GUIDE.md` | Build commands, package structure, platform notes, testing checklist |
| `API.md` | Full server API contracts (activation, heartbeat, admin) |
| `DEPLOY.md` | Server deployment from blank VPS |
| `OPS.md` | Day-to-day operations |
| `CONTEXT.md` | Background reasoning, network analysis, protocol testing results |
| `FIXES.md` | Code quality issues and their fixes |
