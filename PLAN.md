# VPN Desktop App — Comprehensive Build Plan (v3)

## Target

Cross-platform VPN app (Windows + macOS) for **school/college students** bypassing WiFi restrictions.
Supports Hysteria 2 (gaming primary) and usque/Warp (fallback, unbottlenecked for Gaming plans).
**Closed-source, activation-code gated, permanently bound to one hardware-fingerprinted device.** Centrally managed with suspension and SHA256-verified updates. No Ed25519.

> **Full business context:** See `BUSINESS-OVERVIEW.md` — this document covers the *technical* implementation.
> **Step-by-step implementation:** See `ACTION-PLAN.md`.

---

## 0. Business Context (Quick Summary)

| Aspect             | Detail                                                                           |
| ------------------ | -------------------------------------------------------------------------------- |
| **Market**         | School/college students who want to game & bypass WiFi blocks                    |
| **Distribution**   | Middlemen hand out physical activation codes for cash. They have no app access.  |
| **Competition**    | Free VPNs (X-VPN, Psiphon, Lantern) and xVPN                                     |
| **Differentiator** | Hysteria 2 over QUIC — low-latency gaming on congested WiFi; TUN mode; kill switch |
| **Pricing**        | $2–13/mo across 3 tiers (Warp Lite → Gaming Mid → Gaming Max)                    |
| **Key constraint** | **No real protocol names in the UI** — use custom brand names                    |
| **Key constraint** | **Client-side bottlenecks** enforced by desktop app — users can't override       |
| **Key constraint** | **Permanent one-code-one-device** via hardware fingerprint; suspension, not deactivation |

---

## 1. Architecture Overview (v3)

```
┌────────────────────────────────────────────────────────────┐
│                   CLIENT APP (Go binary)                     │
│  ~15-25 MB, single .exe / Mach-O                            │
│                                                              │
│  ┌──────────┐  ┌───────────┐  ┌─────────────────────────┐  │
│  │  GUI     │  │  Fyne     │  │ Auto-Updater            │  │
│  │  Window  │  │  (minim.) │  │ (SHA256-verified,       │  │
│  └────┬─────┘  └───────────┘  │  platform-scoped)       │  │
│       │                       └─────────────────────────┘  │
│  ┌────┴─────────────────────────────────────────────────┐  │
│  │                   CORE CONTROLLER                     │  │
│  │  ┌──────────┐  ┌──────────────┐  ┌────────────────┐  │  │
│  │  │Process   │  │  Activation  │  │ Heartbeat      │  │  │
│  │  │ Manager  │  │  (fingerprint│  │ + Cert Pinning │  │  │
│  │  │(sandbox, │  │   bound,     │  │ + Token Rotate │  │  │
│  │  │ renamed) │  │   per-code   │  │ + Grace Period │  │  │
│  │  │          │  │   limit)     │  │ + Telemetry    │  │  │
│  │  └──────────┘  └──────────────┘  └────────────────┘  │  │
│  │                                                       │  │
│  │  ┌──────────────────────────────────────────────────┐ │  │
│  │  │           TUNNEL MANAGER                          │ │  │
│  │  │  ┌─────────┐  ┌───────────┐  ┌────────────────┐ │ │  │
│  │  │  │  TUN    │  │  Split    │  │  Kill Switch   │ │ │  │
│  │  │  │  Device │  │  Tunnel   │  │  (no leak)     │ │ │  │
│  │  │  └─────────┘  └───────────┘  └────────────────┘ │ │  │
│  │  │  ┌──────────────┐  ┌──────────────────────────┐ │ │  │
│  │  │  │  DNS Guard   │  │  Health Check (15s       │ │ │  │
│  │  │  │  (no leaks)  │  │  test through tunnel)    │ │ │  │
│  │  │  └──────────────┘  └──────────────────────────┘ │ │  │
│  │  └──────────────────────────────────────────────────┘ │  │
│  └───────────────────────────────────────────────────────┘  │
│                              │                                │
│  ┌──────────────────────────┴─────────────────────────────┐  │
│  │           PROTOCOL ENGINES (runtime-downloaded)         │  │
│  │  ┌──────────┐  ┌────────┐  ┌────────────────────────┐ │  │
│  │  │ Hysteria │  │ usque  │  │ Xray / TUIC (Phase 2)  │ │  │
│  │  │(speedmode│  │(litemode│  │ namemode.exe etc.      │ │  │
│  │  │.exe)     │  │.exe)    │  │  renamed binaries)     │ │  │
│  │  └──────────┘  └────────┘  └────────────────────────┘ │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │  Config Store (encrypted) + Engine priority list  │  │  │
│  │  │  Log Store (rotating, in app data dir)            │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  └─────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼  HTTPS (TLS 1.3, cert-pinned)
┌────────────────────────────────────────────────────────────────┐
│                   ADMIN HUB (your VPS)                         │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────────────┐   │
│  │  Act.    │  │  Device  │  │  Config Push / Updates    │   │
│  │  Codes   │  │  Registry│  │  (platform-scoped,        │   │
│  │  (per-   │  │  (HMAC-  │  │   signed)                 │   │
│  │  code    │  │  bound)  │  │                           │   │
│  │  device  │  │          │  │  Engine binaries served   │   │
│  │  limit)  │  │          │  │  here (runtime download)  │   │
│  │          │  │          │  │  Token rotation endpoint  │   │
│  │          │  │          │  │  Deactivation endpoint    │   │
│  └──────────┘  └──────────┘  └───────────────────────────┘   │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Infrastructure Layer                                  │  │
│  │  - Caddy (reverse proxy + rate limiting)               │  │
│  │  - cron (SQLite backups → offsite bucket)              │  │
│  │  - SQLite WAL mode + busy_timeout                      │  │
│  │  - Uptime monitoring (external service)                │  │
│  │  - Activation brute-force protection (hook)            │  │
│  └────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────┘
```

---

## 2. Protocol Layer & Branding

### 2.1 Protocol-to-Name Mapping

| Real Protocol   | UI Name      | Binary Name    | Tier(s)                        | Phase | Notes                                                      |
| --------------- | ------------ | -------------- | ------------------------------ | ----- | ---------------------------------------------------------- |
| Hysteria 2      | Speed Mode   | `speedmode`    | Gaming Mid, Gaming Max         | 1     | QUIC/UDP, low-latency gaming                              |
| usque (Warp)    | Lite Mode    | `litemode`     | Warp Lite (primary), Gaming (fallback) | 1     | **Warp Lite** = client-side throttled (app-enforced); **Gaming fallback** = unbottlenecked (full speed) |
| VLESS-REALITY   | Stealth Mode | `stealthmode`  | Gaming Max                     | 2     | TCP, best DPI evasion                                      |
| TUIC v5         | Turbo Mode   | `turbomode`    | Turbo                          | 2     | QUIC-based, low overhead                                   |

### 2.2 Protocol Fallback Chain (Heartbeat-Driven)

The server sends a prioritized list of protocol configs in every heartbeat response. The client tries them in order, with a 15-second connect timeout per engine:

```json
{
  "status": "active",
  "plan": "gaming_max",
  "protocols": [
    {"id": "hysteria2", "display_name": "Speed Mode",  "binary_name": "speedmode", "weight": 1},
    {"id": "warp",      "display_name": "Lite Mode",   "binary_name": "litemode",  "weight": 2}
  ],
  "commands": []
}
```

> ⚠️ **Bottleneck is plan-driven, enforced client-side.** usque connects directly to Cloudflare (no VPS in the data path), so throttling is applied by the desktop app. The heartbeat response delivers the config, and the app enforces the limits locally:
> - **`plan: "warp_lite"`** — app throttles usque bandwidth, disables UDP gaming
> - **`plan: "gaming_mid"` / `plan: "gaming_max"`** — usque (as fallback) runs **unbottlenecked** (full speed, UDP allowed)
>
> The server embeds the bandwidth/QoS limits in the `config_json` field of each `ProtocolConfig`. The app reads its own `plan_tier` from local storage and applies the corresponding limits. A closed-source binary prevents users from overriding them.

### 2.3 Obfuscation Rules

- **No real protocol names anywhere in the UI.** Not in menus, tooltips, notifications, or error messages.
- **Binary files are renamed on download** — saved as `speedmode.exe`, `litemode.exe`, etc., not the upstream names.
- **Process names are disguised** — on launch, `argv[0]` is set to an innocuous name (`nethelper.exe` on Windows, `com.apple.spotlight` on macOS). On Windows the binary itself is renamed; on Unix `exec` with a custom `argv[0]`.
- **Config files on disk** use random 8-character hex names (e.g. `3f8a2c1b.json`).
- **Log files** reference protocols by numeric code (01 = Hysteria 2, 02 = usque, 03 = VLESS-REALITY, 04 = TUIC v5).
- **Known limitation:** DPI can still fingerprint Hysteria 2's QUIC handshake. The real obfuscation is "Hysteria 2 is less commonly blocked than OpenVPN/WireGuard." Brand renames buy you plausible deniability with casual inspection (task manager, file explorer), not against determined network analysis.

---

## 3. API Contract (Client ↔ Admin Hub)

### 3.1 Activation

```
POST /api/collections/activations/records
  Body:   { "code": "MYVPN-XXXX-XXXX-XXXX-C" }
  Headers: X-Device-Fingerprint: <sha256-of-hardware>
  Success: {
    "token": "jwt...",
    "plan": "gaming_max",
    "message": "Device permanently bound"
  }
  Failure (already bound): { "code": 409, "message": "Code already bound to another device" }
  Failure (suspended):     { "code": 403, "message": "Account suspended. Contact your middleman." }
  Failure (rate):          { "code": 429, "message": "Too many attempts" }
```

**Device binding flow:**
1. On first launch, the client collects **hardware identifiers** (MAC + disk serial + mobo UUID), hashes them with SHA256, and stores the fingerprint locally.
2. The fingerprint is sent in the `X-Device-Fingerprint` header during activation. The server permanently binds the code to this fingerprint.
3. If the app is deleted and reinstalled on the same device, the same fingerprint is re-computed → same code works again (unless suspended).
4. There is no deactivation. Admin suspends a device; the binding is never destroyed. One code = one device.

**Pragmatic tradeoff:** Activate over a trusted network. The fingerprint is hashed before transmission, but a school MITM could intercept the code itself. Since codes are single-use per device, the marginal risk is low.

**Activation code format:**
```
Format:  MYVPN-XXXX-XXXX-XXXX-C  (the trailing -C is the Luhn check digit)
Parts:   4 groups of 4 uppercase alphanumeric characters (A-Z, 2-9, excluding I,O,0,1)
Checksum: Last character is a Luhn-mod-N check digit
Expiry:   Optional expires field in PocketBase (default 90 days)
Example:  MYVPN-A3X9-K7M2-Q5P1-C
```

### 3.2 Deactivation

**Not supported.** Codes are permanently bound to one device. To block a device, the admin sets `suspended = true` on the activation code record in PocketBase. The binding is preserved — unsuspending the code allows the same device to reconnect.

### 3.3 Heartbeat (with expanded telemetry + token rotation)

```
POST /api/heartbeat
  Headers: Authorization: Bearer <token>
           X-Device-Fingerprint: <sha256-of-hardware>
  Body: {
    "device_fingerprint": "<sha256-of-hardware>",
    "app_version": "1.0.0",
    "os_platform": "windows_amd64",
    "protocol_id": "hysteria2",
    "connected": true,
    "health_status": "healthy",        // "healthy" | "degraded" | "dead"
    "engine_exit_code": 0,             // non-zero if engine crashed since last heartbeat
    "last_error": "",                  // most recent error message
    "uptime_seconds": 3600,
    "bytes_up": 1234,
    "bytes_down": 5678
  }
  Success: {
    "status": "active",
    "plan": "gaming_max",
    "token": "new-jwt...",            // ROTATED — client MUST replace stored token
    "protocols": [{"id":"hysteria2","display_name":"Speed Mode","binary_name":"speedmode","config":{...}, "weight":1}],
    "commands": []
  }
```

**Token rotation:** Every successful heartbeat returns a new short-lived token (24h expiry). The client replaces its stored token immediately.

**Grace vs. token interaction:** The 7-day grace period **overrides** token expiry. When the client enters grace mode (heartbeat failures > 3), it continues using its last valid token until the grace period expires, regardless of the token's original 24h lifespan. Token expiry is only enforced during normal operation. Once grace expires, the client must re-activate. Documented to prevent conflicting implementations.

### 3.4 Remote Commands (from heartbeat response)

| Command         | Payload                                                                               | Effect                                               |
| --------------- | ------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| `update`        | `{ "url":"...","sha256":"...","version":"1.0.1","min_version":"1.0.0","platform":"windows_amd64","signature":"b64...","key_id":"key1" }` | Ed25519-verified auto-update (platform-scoped) |
| `disable`       | `{}`                                                                                  | Stop engine, show "Account disabled"                 |
| `reconfigure`   | `{}`                                                                                  | Re-read config from storage and restart              |
| `set_plan`      | `{ "plan": "turbo", "protocols": [...] }`                                             | Update plan + protocol priority list                 |
| `message`       | `{ "title": "...", "body": "..." }`                                                   | Show notification to user                            |
| `ping`          | `{}`                                                                                  | Immediate heartbeat confirmation (debugging)         |

---

## 4. Security Architecture

### 4.1 Update Verification (SHA256, No Signing)

**No Ed25519 signing.** Updates are verified by SHA256 hash only. The admin hub publishes the SHA256 alongside the binary URL. The client downloads, hashes, and compares.

**Reasons:**
- No key generation, no offline storage, no key rotation, no risk of losing the signing key
- Certificate pinning (§4.3) protects the hub connection itself from MITM
- Platform check (`windows_amd64` vs `darwin_arm64`) prevents cross-platform serving

**Update payload format:**
```json
{
  "platform": "windows_amd64",
  "version": "1.0.1",
  "sha256": "abcdef1234...",
  "url": "https://api.yourdomain.com/files/updates/myvpn.exe"
}
```

### 4.2 Activation Device Binding (Permanent One-Code-One-Device)

- **Hardware fingerprint:** Client collects MAC address + disk serial + motherboard UUID, hashes them with SHA256. This fingerprint survives app deletion — same device always produces the same fingerprint.
- **Permanent binding:** On first activation, the code is permanently bound to the device fingerprint. The code can never be unbound or reused on a different device.
- **Re-activation after reinstall:** Same device enters same code → server matches fingerprint → allows re-activation. Suspended devices are blocked with a message but keep their binding.
- **Suspension, not deactivation:** Admin can set `suspended = true` on an activation code to block connections. The binding is never destroyed. When unsuspended, the same code works on the same device again.
- **No multi-device codes:** One code = one device. Middlemen sell one code per student.
- **Token generation:** Server generates an HMAC-SHA256 signed token (or proper JWT) so tokens cannot be forged client-side. The PocketBase hook uses a server-side `TOKEN_SECRET` env var.
- **Pragmatic recommendation:** Activation should happen over a trusted network (home WiFi, phone hotspot) rather than school WiFi where MITM is present. The hardware fingerprint is hashed before transmission.

### 4.3 Certificate Pinning

- SHA-256 hash of the VPS TLS certificate's `SubjectPublicKeyInfo` embedded in client.
- Verified on every HTTPS connection via `crypto/tls.VerifyPeerCertificate`.
- Defeats school IT SSL inspection proxies.

### 4.4 Heartbeat Grace Period

- After 3 consecutive heartbeat failures, client enters **grace mode** (7 days).
- VPN continues to work, GUI shows "X days remaining" in status area.
- Each successful heartbeat resets the timer.
- When grace expires, VPN disconnects with "Cannot verify subscription" notification.

### 4.5 Tunnel Health Check + Auto-Reconnect Loop

**Health check (15s interval):**
- Every 15 seconds while "Connected": send a TCP probe to `1.1.1.1:853` (DNS-over-TLS) through the tunnel.
- 5-second timeout. If it fails, try HTTP HEAD to `https://httpstat.us/200` as fallback.
- Include `health_status` in every heartbeat payload.

**Auto-reconnect loop (critical — prevents 3AM disconnects):**
- 3 consecutive failed health checks → mark **"Degraded"** → kill current engine → try next engine in protocol priority list.
- If fallback engine connects → mark **"Connected (fallback)"**, send telemetry about the switch.
- If all engines fail → mark **"Disconnected"** → engage kill switch → wait **30 seconds** → retry the primary engine.
- Retry loop: attempt primary engine up to **5 times**, with 30s delay between each attempt.
- Log every attempt (`engine-<id>.log`) with timestamp, engine, result.
- After 5 failed retries → give up, show **"Disconnected — tap to retry"** notification, stop auto-retry loop until user manually clicks Connect.

### 4.6 TUN Mode + DNS Guard + Privilege Elevation

**Replace HTTP proxy with TUN virtual network interface.** For MVP, use a **full tunnel** (all traffic through VPN except RFC 1918 local addresses). Split tunnel (only blocked destinations through VPN) is Phase 2.

| Feature | MVP Implementation | Phase 2 Improvement |
|---------|-------------------|---------------------|
| **Tunnel scope** | Full tunnel: default route through TUN, exclude RFC 1918 (10.x, 172.16.x, 192.168.x) | Split tunnel: only specific game/blocked traffic through VPN |
| **TUN device** | Hysteria 2 supports `tun` natively. usque uses SOCKS5 → routed through the TUN adapter | Add a TUN wrapper for all engines |
| **DNS leak protection** | Redirect UDP :53 through tunnel. CLI: `netsh interface ipv4 set dns` / `networksetup -setdnsservers` | Hardcoded DoH upstream fallback |
| **Kill switch** | Delete the default TUN route on health failure. Blocks most traffic (not bulletproof, but effective against casual leaks). | WFP callout driver (Windows) / pf anchor (macOS) for true leak blocking |

**Privilege elevation strategy (CRITICAL):**

TUN device creation requires admin/root privileges. The client app runs as the user. There must be an elevation strategy:

| Platform | MVP Approach | Tradeoff |
|----------|-------------|----------|
| **Windows** | Install a **privileged helper Windows service** (runs as SYSTEM) that creates/manages the TUN interface. The client app communicates with the service via a **named pipe** (`\\.\pipe\MyVPNHelper`). The service is installed once (requires admin on install) and auto-starts on boot. | Requires admin password on install. After that, users never see a UAC prompt. ~2 days of extra work. |
| **Windows (lazy MVP)** | Ship a batch file that the user runs once as admin to install the TUN adapter. On every connect, prompt for admin via a scheduled task that runs the engine elevated. | **UX debt:** User sees UAC prompt on every connect. Document this clearly — it's viable for technical users but kills adoption. |
| **macOS** | Use a `launchd` privileged daemon that creates the TUN interface. Installed via a PKG or a one-time `sudo` command. | Same model as Windows helper service. Without a paid Apple Developer account, you cannot ship a setuid helper easily. |

**Recommended:** Build the Windows helper service from Day 1. It's not that complex (~200 lines in Go with `golang.org/x/sys/windows/svc`) and it's the difference between "works" and "requires IT support."

### 4.7 Process Sandboxing + Binary Renaming

| Layer | Technique |
|-------|-----------|
| **Binary renamed on download** | Saved as `speedmode.exe`, `litemode.exe`, etc. Never as `hysteria.exe` or `usque.exe`. |
| **Process name disguised** | `argv[0]` set to `nethelper.exe` (Windows) or `com.apple.spotlight` (macOS). On Windows the renamed .exe handles this automatically. |
| **Windows job object** | `KILL_ON_JOB_CLOSE`, `DIE_ON_UNHANDLED_EXCEPTION`, active process limit = 1, no desktop access. |
| **macOS home-dir isolation** | Engine runs in a disposable temp home directory with no access to user files. `Pdeathsig=SIGKILL` if parent dies. |
| **Linux** | `unshare(CLONE_NEWNET)` + `prctl(PR_SET_NO_NEW_PRIVS)` (for server-side components). |

### 4.8 Activation Code Format

Codes are printed on paper and handed out by middlemen. Format:

```
MYVPN-A3X9-K7M2-Q5P1-C
```

- Prefix `MYVPN-` (branded, distinguishes from other codes)
- 4 groups of 4 characters: uppercase A-Z + digits 2-9 (excludes I, O, 0, 1 for readability)
- Last character is a **Luhn-mod-N check digit** — detects single-character typos
- Case-insensitive on input
- `expires_at` date in PocketBase (default 90 days from creation, configurable per code)

**Generation:** Create via PocketBase admin UI or a simple script. The Luhn-mod-N check digit is computed server-side and stored with the code.

### 4.9 Admin Hub Hardening

| Layer | Protection |
|-------|-----------|
| **Caddy rate limiting** | 1 req/s per IP on heartbeat, 3 req/m per IP on activation |
| **Activation brute-force** | PocketBase hook: track failed attempts per IP, block after 5 in 10 min |
| **SQLite WAL mode** | `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;` set in a startup hook — prevents write contention under concurrent heartbeat + activation + admin queries |
| **Off-VPS backups** | Hourly `.backup` → transferred to Backblaze B2 / S3-compatible storage (not just same-VPS directory). Daily `.dump.gz` → offsite. |
| **Uptime monitoring** | Free external service (UptimeRobot/BetterUptime) checking `/_/health` every 5 min |
| **Minimal attack surface** | Only ports 80/443 open via ufw; PocketBase listens on 127.0.0.1 only |

---

## 5. App Architecture

### 5.1 Directory Structure (v2)

```
myvpn/
├── main.go                        # Entry point — thin launcher, hands off to GUI
├── go.mod
├── go.sum
├── Makefile                       # Cross-compile targets
├── BUILD_KEY_GEN.sh               # (REMOVED — no signing keys; use scripts/publish_update.sh)
├── internal/
│   ├── activation/
│   │   ├── activate.go            # Code validation, hardware fingerprint, token storage
│   │   └── deactivate.go          # Deactivation endpoint call
│   ├── branding/
│   │   └── names.go               # Protocol name obfuscation mapping
│   ├── tunnel/
│   │   ├── tun.go                 # TUN device interface (platform-agnostic)
│   │   ├── tun_windows.go         # wintun setup/teardown
│   │   ├── tun_darwin.go          # utun setup/teardown (macOS)
│   │   ├── split.go               # Route table management (split tunnel)
│   │   ├── killswitch.go          # Firewall rules on disconnect/crash
│   │   └── dns.go                 # DNS leak protection (redirect :53 / DoH fallback)
│   ├── manager/
│   │   ├── process.go             # Launch/stop engines (platform-agnostic)
│   │   ├── process_windows.go     # Job object sandboxing + hide window
│   │   ├── process_darwin.go      # macOS sandboxing (home dir isolation)
│   │   └── config.go              # Generate engine config from server payload
│   ├── activation/
│   │   ├── activate.go            # Code validation + activation + fingerprint collection
│   │   ├── fingerprint_windows.go # Hardware fingerprint (win: getmac + wmic + disk serial)
│   │   └── fingerprint_darwin.go  # Hardware fingerprint (mac: ifconfig + system_profiler + ioreg)
│   ├── heartbeat/
│   │   ├── heartbeat.go           # Periodic API + cert pinning + grace period + suspension handling
│   │   └── telemetry.go           # Engine log capture, crash marker, error report
│   ├── health/
│   │   └── health.go              # Tunnel health check (15s probe, 3-failure threshold)
│   ├── throttle/
│   │   └── throttle.go            # Client-side bandwidth limiter — throttles usque for warp_lite plan; gaming plans pass through unbottlenecked
│   ├── updater/
│   │   ├── update.go              # Download + SHA256 verify (no signing) + apply
│   │   ├── update_windows.go      # Spawn helper to replace running .exe
│   │   └── update_unix.go         # Rename-in-place & relaunch
│   ├── enginedl/
│   │   └── download.go            # Download engine binaries from admin hub at runtime
│   └── storage/
│       └── storage.go             # Token, hardware fingerprint, config, engine cache, logs
├── ui/                            # GUI package (Phase 2)
│   ├── main_window.go             # Fyne/Wails main window setup
│   ├── dashboard.go               # Connection status, health, stats, plan
│   ├── activation_dialog.go       # Activation code input
│   └── resources/                 # Icons, images, fonts
├── scripts/
│   ├── build.sh                   # Cross-compile all platforms
│   ├── download_binaries.sh       # Fetch latest engine releases (for manual preload)
│   ├── publish_update.sh          # Compute SHA256 and output update payload JSON
│   └── setup_server.sh            # Deploy everything to VPS
└── assets/
    └── icon.ico                   # App icon (also .png for macOS)
```

### 5.2 MVP Lifecycle (v3)

```
App Launch
    │
    ▼
Setup autostart (scheduled task / LaunchAgent) BEFORE activation prompt
    │
    ▼
Check if activated
    │
    ├── No → Show activation prompt
    │         ├── Collect hardware fingerprint (MAC + disk serial + mobo UUID SHA256)
    │         ├── POST to server → server permanently binds code to fingerprint
    │         │   ├── Fresh code → bind and return token
    │         │   ├── Same device re-activating → return token (unless suspended)
    │         │   └── Different device → reject (code already bound)
    │         └── Store token + fingerprint locally
    │
    ▼
Launch GUI (with branded plan name + grace days if applicable)
    │
    ▼
Download engine binaries from admin hub (if not cached or version mismatch)
    ├── HTTP GET from admin hub, verified by SHA256
    ├── Saved as renamed binary (e.g. speedmode.exe)
    └── Cached in app data directory
    │
    ▼
Start heartbeat loop (every 60s):
    ├── POST heartbeat (cert-pinned TLS, device fingerprint header)
    │   ├── Success → Reset grace timer
    │   │             ├── Store new rotated token
    │   │             ├── Check for suspension → if "suspended", stop engine, show message
    │   │             ├── Process remote commands (update, disable, etc.)
    │   │             └── Update protocol priority list from response
    │   │
    │   └── Failure → Increment consecutive failure counter
    │                 ├── < 3 failures → retry next interval
    │                 ├── ≥ 3 failures → enter grace mode (7-day countdown)
    │                 └── Grace expired → disconnect, kill switch, show notification
    │
    ▼
User clicks "Connect":
    ├── Create TUN device (wintun / utun)
    ├── Install split-tunnel routes (LAN exclusions, default through TUN)
    ├── Install DNS guard (redirect :53 through tunnel)
    ├── Load highest-priority protocol from list
    ├── Try engine #1 (e.g. speedmode.exe) → launch as disguised process
    │   ├── Success → Start health check loop (15s probes)
    │   │             ├── Healthy → keep running
    │   │             ├── Degraded (3 fails) → try engine #2
    │   │             └── All engines fail → Disconnected + kill switch
    │   └── Fail (15s timeout) → try engine #2
    │
    ▼
User clicks "Disconnect":
    ├── Stop engine
    ├── Remove TUN device
    ├── Remove split-tunnel routes
    ├── Disable kill switch (restore normal connectivity)
    └── Update GUI status
```

### 5.2 Connection State Machine

The app tracks six explicit states. The GUI reflects the current state:

```
                    ┌──────────────────────────────────────┐
                    │              IDLE                     │
                    │  (app launched, no activation yet)    │
                    └────────────┬─────────────────────────┘
                                 │ activation successful
                                 ▼
              ┌──────────────────────────────────────┐
              │           ACTIVE (idle)                │
              │  GUI: "Disconnected"               │
              │  Heartbeat running, waiting for user   │
              └────────────┬──────────────────────────┘
                           │ user clicks Connect
                           ▼
              ┌──────────────────────────────────────┐
              │         CONNECTING                     │
              │  Download engine, create TUN, start    │
              │  engine, install routes                │
              └────────────┬──────────────────────────┘
                           │ engine started + health OK
                           ▼
     ┌─────────────────────────────────────────────────────────┐
     │                  CONNECTED_PRIMARY                       │
     │  GUI: "Connected — Gaming Mode"                      │
     │  Health checks every 15s, heartbeat every 60s           │
     └──┬─────────────────────────────────────────────────┬────┘
        │ 3 health fails                                   │ 3 health fails
        ▼                                                  ▼
 ┌─────────────────────┐                    ┌──────────────────────────┐
 │  CONNECTED_FALLBACK  │                   │      DEGRADED             │
 │  GUI: "Connected (Fallback)"         │                    │  GUI: "Degraded —    │
 │  (Lite Mode —        │                    │  reconnecting..."        │
 │  fallback)"          │                    │  Auto-retry loop starts  │
 └────────┬────────────┘                    └───────────┬──────────────┘
          │ health recovers                              │ all engines failed
          ▼                                              ▼
 CONNECTED_PRIMARY                            ┌────────────────────────┐
  (auto-promote)                              │     DISCONNECTED       │
                                              │ Kill switch engaged    │
                                              │ "Disconnected — tap to │
                                              │  retry"                │
                                              └────────┬───────────────┘
                                                       │ user taps retry
                                                       ▼
                                                    CONNECTING
```

**Additional states (orthogonal — can overlap with above):**

| State | Trigger | GUI |
|-------|---------|---------|
| **GRACE** | 3+ heartbeat failures | "X days remaining" tooltip added |
| **GRACE_EXPIRED** | Grace period over without recovery | "Cannot verify subscription" — VPN disconnects, must re-activate |
| **UPDATING** | Update command received | "Updating..." notification |

When in CONNECTED_PRIMARY or CONNECTED_FALLBACK, the auto-reconnect loop runs:
- 3 failed health checks → DEGRADED → kill engine → try next protocol → if fallback works → CONNECTED_FALLBACK
- All engines fail → DISCONNECTED → wait 30s → retry primary → up to 5 attempts → then give up until manual retry

### 5.3 MVP Lifecycle (v3)

```
App Launch
    │
    ▼  [State: IDLE]
Collect hardware fingerprint (MAC + disk serial + mobo UUID SHA256, stored locally)
    │
    ▼
Setup autostart (scheduled task / LaunchAgent) BEFORE activation
    │
    ▼
Check if activated
    │
    ├── No → Show activation prompt  [State: ACTIVATING]
    │         ├── POST to server (fingerprint in header)
    │         ├── Server permanently binds code to fingerprint
    │         └── Store token + fingerprint locally
    │
    ▼  [State: ACTIVE]
Launch GUI (branded plan name + grace days if in grace mode)
    │
    ▼
Check for engine binaries in cache → download if missing
    ├── Primary engine (Hysteria 2) bundled in installer as bootstrap
    ├── Additional engines downloaded from admin hub at runtime
    ├── Saved as renamed binary (speedmode.exe, litemode.exe)
    └── Verified by SHA256 before caching
    │
    ▼
Start heartbeat loop (every 60s):
     ├── POST heartbeat (cert-pinned TLS, device fingerprint in header)
    │   ├── Success → Reset grace timer
    │   │             ├── Store new rotated token (grace overrides expiry)
    │   │             ├── Process remote commands (update, disable, etc.)
    │   │             └── Update protocol priority list
    │   │
    │   └── Failure → Increment consecutive failure counter
    │                 ├── < 3 → retry next interval
    │                 ├── ≥ 3 → enter GRACE state (7-day countdown, token frozen)
    │                 └── Grace expired → GRACE_EXPIRED → disconnect + kill switch
    │
    ▼  [State: ACTIVE — waiting for user]
User clicks "Connect":
    │
    ▼  [State: CONNECTING]
    ├── Ensure engine binary exists (download if not cached)
    ├── Create TUN device via privileged helper service (Windows: SYSTEM service,
    │   macOS: launchd daemon) — no UAC prompt on subsequent connects
    ├── Install full-tunnel route: default gateway through TUN,
    │   RFC 1918 excluded (10.x, 172.16.x, 192.168.x stay direct)
    ├── Install DNS guard: redirect UDP :53 through tunnel
    ├── Try engine #1 (highest priority) → launch as disguised process
    │   ├── Start health check loop (15s probes to 1.1.1.1:853)
    │   │   ├── [State: CONNECTED_PRIMARY] healthy → keep running
    │   │   ├── [State: CONNECTED_FALLBACK] primary failed, fallback ok
    │   │   ├── [State: DEGRADED] 3 health fails → auto-reconnect loop
    │   │   │   ├── Kill engine → try next protocol in list
    │   │   │   ├── If all fail → wait 30s → retry primary (×5 max)
    │   │   │   └── After 5 retries → [State: DISCONNECTED] → kill switch
    │   │   └── [State: DISCONNECTED] all engines dead → "tap to retry"
    │   │
    │   └── Fail to start (15s timeout) → try engine #2
    │
    ▼
User clicks "Disconnect":
    ├── Stop engine
    ├── Remove TUN device + routes
    ├── Disable kill switch → restore normal connectivity
    └── [State: ACTIVE]
```

### 5.4 Telemetry & Privacy

**What's collected (sent in every heartbeat):**

| Field | Example | Note |
|-------|---------|------|
| `device_fingerprint` | `sha256...` | Hardware-derived device identifier — survives app reinstall |
| `app_version` | `1.0.0` | For update targeting |
| `os_platform` | `windows_amd64` | For update targeting + support |
| `protocol_id` | `hysteria2` | Which engine is active |
| `connected` | `true` | Connection state |
| `health_status` | `healthy` | Tunnel health |
| `uptime_seconds` | `3600` | Session duration |
| `last_error` | `engine crashed: signal 11` | Last error (empty if none) |

**What's NOT collected:** browsing history, destination IPs, DNS queries, website URLs, or any user content.

**Privacy controls:**
- Opt-out toggle in Settings panel: "Send anonymous usage data" (default: on for MVP)
- When off: heartbeat still runs (required for activation/remote commands) but telemetry fields (uptime, health, errors) are zeroed
- Store the preference in `storage.AppData`
- Phase 2: make telemetry opt-in instead of opt-out

### 5.5 Log Rotation

Engine logs (`~/.myvpn/logs/engine-<id>.log`) rotate automatically:

```go
const (
    MaxLogSize = 10 * 1024 * 1024  // 10 MB
    MaxLogFiles = 3                 // keep 3 rotated files
)

func rotateLog(path string) {
    // Check size, rotate if > 10MB
    // engine.log → engine.log.1 → engine.log.2 → engine.log.3 → delete
}
```

Called in `storage.Init()` as a background goroutine that checks every 5 minutes.
Prevents unbounded disk usage on busy clients.

**Engine log capture:**
- Each engine's stdout/stderr is piped to a rotating log file in `~/.myvpn/logs/engine-<protocol-id>.log`.
- Max 3 log files, 5MB each. Oldest rotated out.
- Last 5 lines of the log are kept in memory for error reporting.

**Heartbeat telemetry fields:**
```go
type HeartbeatRequest struct {
    DeviceID    string `json:"device_id"`
    AppVersion  string `json:"app_version"`
    OSPlatform  string `json:"os_platform"`   // "windows_amd64"
    ProtocolID  string `json:"protocol_id"`
    Connected   bool   `json:"connected"`
    HealthStatus string `json:"health_status"` // "healthy" | "degraded" | "dead"
    EngineExitCode int  `json:"engine_exit_code"`
    LastError   string `json:"last_error"`
    UptimeSec   int    `json:"uptime_seconds"`
    BytesUp     int64  `json:"bytes_up"`
    BytesDown   int64  `json:"bytes_down"`
}
```

**Crash marker:**
- On graceful shutdown, delete a marker file.
- On startup, check if marker file exists — if yes, the previous run crashed.
- Report `last_error = "previous_run_crashed"` in the first heartbeat.

---

## 6. Risk Assessment (v2)

| Risk                                                     | Severity  | Mitigation                                                                                  |
| -------------------------------------------------------- | --------- | ------------------------------------------------------------------------------------------- |
| Activation code sharing (one code → many devices)        | **Critical** | **Permanent hardware fingerprint binding.** One code = one device forever. Suspension blocks without unbinding. |
| Raw device fingerprint sent over wire (MITM replay)      | **Critical** | **Client-held secret.** Server never sees raw fingerprint. Device proof is HMAC-SHA256("activation"+code, secret). |
| TUN mode requires admin/root on every launch             | **Critical** | **Privileged helper service** (Windows: SYSTEM service via named pipe). macOS: launchd daemon. One-time admin on install. |
| Engine crashes at 3AM → user stays disconnected          | **Critical** | **Auto-reconnect loop:** 3 health fails → try fallback → all fail → wait 30s → retry primary (×5) → give up with "tap to retry". |
| Proxy model leaks traffic (games ignore HTTP proxy)      | **Critical** | **TUN mode** (full tunnel for MVP). Kill switch: delete default TUN route on health failure. |
| No connection health monitoring → false "Connected"      | **Critical** | **15-second health probe** through tunnel. 3 fails = Degraded → fallback → Disconnected. |
| Admin hub VPS compromised                                | **Critical** | **Cert-pinned clients** + SHA256-verified updates. Admin can remotely disable devices via heartbeat. |
| School IT SSL proxy intercepts traffic                   | **Critical** | **Certificate pinning** prevents MITM even with forced CA installation.                    |
| Update serves wrong platform binary                      | **High**    | **Platform field in update payload.** `windows_amd64` update rejected on `darwin_arm64`.   |
| usque stops working (Cloudflare API changes)             | **High**    | Protocol fallback chain — switch to Hysteria 2 or VLESS-REALITY via heartbeat command.      |
| Admin hub goes down / domain taken                        | **High**    | **7-day heartbeat grace period** (grace overrides token expiry). Offsite backups.           |
| Bootstrap chicken-and-egg (can't download engine on blocked WiFi) | **High** | **Bundle Hysteria 2 in installer.** Runtime download only for updates. |
| Grace period and token rotation conflict                  | **Medium**  | **Grace overrides token expiry.** Token rotation only enforced during normal (non-grace) operation. Documented. |
| School IT blocks Hysteria 2 QUIC fingerprints            | **Medium**  | Switch to VLESS-REALITY (TCP) via heartbeat command. Documented as known limitation.        |
| SQLite contention under load                             | **Medium**  | **WAL mode + busy_timeout=5000** in startup hook. Offsite backups. Grace period covers hub downtime. |
| Activation code brute-force                              | **Medium**  | Caddy rate limiting + PocketBase hook tracking failed attempts per IP.                     |
| No log rotation → disk fills up on busy client           | **Medium**  | **Auto-rotate at 10MB**, keep last 3 files. Background goroutine in storage.Init().          |
| Antivirus flags disguised process names                  | **Medium**  | Submit renamed binaries to AV vendors. Avoid UPX.                                          |
| macOS notarization                                       | **Low**     | Targeted devices won't have Gatekeeper enforced in school labs.                            |
| Malicious update pushed to the hub                       | **Low**     | Cert-pinned TLS connection + SHA256 verification. Admin can push hotfix by heartbeat.     |

---

## 7. Admin Hub — Implementation Details

### 7.1 PocketBase

Single Go binary providing:
- SQLite database (WAL mode, busy_timeout=5000)
- Auto-generated REST API with auth
- Admin dashboard at `https://your-server/_/` (middlemen don't use this — only you)
- File storage (for app binaries + engine binaries)
- JavaScript hooks for activation rate limiting + device binding logic

### 7.2 Infrastructure Layer

**Caddy** reverse proxy with rate limiting (see `ACTION-PLAN.md Phase 1.1`).

**SQLite WAL mode** — set via PocketBase hook on startup:

```javascript
// pb_hooks/sqlite_pragmas.pb.js
onBeforeServe(() => {
    const db = $app.dao().db();
    db.exec("PRAGMA journal_mode=WAL;");
    db.exec("PRAGMA busy_timeout=5000;");
    console.log("SQLite pragmas set: WAL + busy_timeout=5000");
});
```

**Off-VPS backups** — hourly SQLite backup shipped to Backblaze B2 (or any S3-compatible):
```bash
# Install b2 CLI, configure credentials
# Then in backup.sh:
sqlite3 /opt/pocketbase/pb_data/data.db ".backup /tmp/pb-backup.db"
b2 upload-file my-bucket /tmp/pb-backup.db "backups/data-$(date +%Y%m%d-%H%M%S).db"
```

### 7.3 Middleman Note

Middlemen **only hand out physical activation codes**. They have no app access, no admin panel access, no dashboard. Codes are generated by you (the admin) via the PocketBase admin interface and given to middlemen on paper. Middlemen sell these paper codes for cash and take their cut.

---

## 8. Competition Strategy: Hysteria 2 as Differentiator

- School WiFi is congested, high packet loss, high latency
- TCP-based VPNs collapse under packet loss
- Hysteria 2 uses QUIC with Forward Error Correction — thrives on lossy networks
- Free VPNs don't/can't offer Hysteria 2 — genuine technical moat
- **No free VPN offers a kill switch + split tunnel + TUN mode** in a single lightweight client
- Marketing (branded): "Speed Mode uses next-gen transport that actually works on school WiFi"

---

## 9. Key Repositories & Resources

| Resource                  | URL                                   | Purpose                              |
| ------------------------- | ------------------------------------- | ------------------------------------ |
| Hysteria 2                | https://github.com/apernet/hysteria   | Gaming-optimized QUIC protocol (MIT) |
| usque (Warp MASQUE)       | https://github.com/Diniboy1123/usque  | Warp protocol engine (fallback)      |
| Xray-core (VLESS-REALITY) | https://github.com/XTLS/Xray-core     | Stealth protocol (REALITY + uTLS)    |
| tuic-go (TUIC v5)         | https://github.com/iyear/tuic-go      | QUIC-based low-overhead (MIT)        |
| wintun                     | https://www.wintun.net                | TUN driver for Windows               |
| Fyne (Go)                 | https://fyne.io                        | Cross-platform minimal GUI            |
| Fyne (Go GUI)             | https://fyne.io                       | Cross-platform GUI toolkit (Phase 2) |
| PocketBase                | https://pocketbase.io                 | Admin hub backend                    |
| Backblaze B2              | https://www.backblaze.com/b2/         | Offsite backup storage               |
| SHA256 (Go stdlib)        | `crypto/sha256`                       | Update verification (no external deps) |

---

## Summary — Revised Phases

| Phase       | Timeline     | Deliverable                                                                                                                                                     |
| ----------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Phase 1** | —            | **Hardened MVP with TUN mode.** VPS: rate limiting, WAL-mode SQLite, offsite backups, uptime monitoring. Hysteria 2 + usque dual-engine. Minimal Fyne GUI with permanent hardware-fingerprint activation (one code = one device), suspension support, cert-pinned heartbeat with token rotation + telemetry + grace period, SHA256-verified platform-scoped updates, TUN mode with kill switch (route deletion) + DNS guard + IPv6 blocking, bandwidth probing + client-side throttling, 15s health check, process sandboxing + binary renaming, runtime download. Windows + macOS. |
| **Phase 2** | —            | Split tunnel, WFP/pf kill switch, add VLESS-REALITY + TUIC v5, systray minimize-to-tray, telemetry analytics dashboard.                                      |
| **Phase 3** | Week 4+      | Gaming protocol optimization, fallback chain tuning, dual-admin-hub failover, key rotation mechanism.                                                          |

Phase 1 is **genuinely shippable to real users** because:
- **Activation survives reinstall** — hardware fingerprint; same device always re-recognized
- **No admin prompts on every launch** — privileged helper service (Windows: SYSTEM via named pipe, macOS: launchd daemon) installed once
- **Engine crashes don't leave you stranded** — auto-reconnect loop retries all engines, then retries primary 5 times with 30s delay before giving up
- **Traffic actually flows** — TUN mode (not HTTP proxy that games ignore), full tunnel with RFC 1918 exclusions
- **You know when it breaks** — 15s health probe, degraded state reported in heartbeat telemetry
- **It survives hub failure** — 7-day grace period (overrides token expiry), offsite backups, uptime monitoring
- **Bootstrap isn't chicken-and-egg** — primary engine (Hysteria 2) bundled in installer; runtime download only for updates
- **Compromised hub can't push malware** — cert-pinned TLS + SHA256-verified updates, platform-scoped
- **School IT can't spy on heartbeat** — cert pinning defeats SSL inspection proxies
- **Task Manager shows `speedmode.exe`** not `hysteria.exe`
- **Logs won't fill your disk** — auto-rotated at 10MB, last 3 kept
- **You know what data is sent** — privacy notice in the app (opt-out toggle), no browsing history or destination IPs collected
- **Connection states are predictable** — explicit 6-state machine (IDLE → ACTIVE → CONNECTING → CONNECTED_PRIMARY → CONNECTED_FALLBACK → DEGRADED → DISCONNECTED) with GRACE orthogonal
