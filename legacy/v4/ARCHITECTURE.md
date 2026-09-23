# V4 Architecture — Sing-Box Unified Client

> **V4 replaces sslocal + tun2socks with sing-box, adds enterprise-grade update safety, and restores critical reliability features stripped in V3.**
> 
> For the full evolution from V1→V4, see the root [`README.md`](../README.md).

---

## Why V4 Exists

V3 had three categories of problems:

| Category | V3 Problem | V4 Fix |
|----------|-----------|--------|
| **Client engines** | sslocal + tun2socks = two processes. tun2socks is abandonware (last update 2020). SOCKS5 bridge adds latency. | **Sing-box** — one binary, built-in TUN, actively maintained, no SOCKS5 hop. |
| **Update safety** | Replace binary + restart. If new binary crashes, **customer loses VPN entirely.** No auto-rollback. | **Two-phase sentinel** — `.update-pending` → `.update-confirmed` handshake. Auto-revert on crash. Manual `--revert` flag. |
| **Business resilience** | No offsite backups. No heartbeat grace period. No rate limiting on activation. No Luhn checksum on codes. No code recovery on device failure. | Hourly B2 backups. 7-day grace period. Rate limiting + Luhn. Admin code unbind endpoint. |

---

## System Overview

```
┌─────────────────────────────────────────────────┐
│              STUDENT'S LAPTOP                     │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │         MyVPN Desktop App (Go+Fyne)        │   │
│  │                                             │   │
│  │  ┌──────┐   ┌──────────────────────────┐   │   │
│  │  │ GUI  │◄─►│    Manager (process.go)   │   │   │
│  │  │(Fyne)│   │  ┌─────────────────────┐  │   │   │
│  │  └──────┘   │  │   sing-box          │  │   │   │
│  │             │  │  • TUN mode          │  │   │   │
│  │  ┌──────┐   │  │  • Shadowsocks       │  │   │   │
│  │  │Activ-│   │  │  • Built-in DNS      │  │   │   │
│  │  │ation │   │  │  • Auto-reconnect    │  │   │   │
│  │  └──────┘   │  └────────┬────────────┘  │   │   │
│  │             │           │                │   │   │
│  │  ┌──────┐   │  ┌─────────────────────┐  │   │   │
│  │  │Up-   │   │  │   Two-Phase Update  │  │   │   │
│  │  │dater │   │  │   (.prev + sentinel)│  │   │   │
│  │  └──────┘   │  └─────────────────────┘  │   │   │
│  │             │                            │   │   │
│  │  ┌──────┐   │  ┌─────────────────────┐  │   │   │
│  │  │Heart-│   │  │   Grace Period      │  │   │   │
│  │  │beat  │   │  │   (7-day counter)   │  │   │   │
│  │  └──────┘   │  └─────────────────────┘  │   │   │
│  └─────────────┴────────────────────────────┘   │
│             │                                    │
│   ┌─────────┴─────────┐                          │
│   │  :8443  │  :8444  │  :8445                   │
│   │  Eco    │ Stealth │  Strike                   │
│   │  BBR    │ Brutal  │  BBR+UDP                  │
│   │  tc 5M  │ 48M tgt │  200M tc                  │
│   │  TCP    │ TCP     │  TCP+UDP                  │
│   └─────────┴─────────┴──────────┘                │
│              TCP encrypted (AES-256-GCM)           │
└──────────────────────┬────────────────────────────┘
                       │
                       ▼
               ┌──────────────┐
               │    N4L       │
               │  Palo Alto   │
               └──────┬───────┘
                       │
                       ▼
┌─────────────────────────────────────────────────┐
│              VPS (Ubuntu 22.04)                   │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │  Caddy (reverse proxy + rate limiting)     │   │
│  └──────────────────┬────────────────────────┘   │
│                     │                             │
│  ┌──────────────────┴────────────────────────┐   │
│  │  PocketBase (admin API + SQLite)           │   │
│  │  ┌──────────────────────────────────────┐  │   │
│  │  │  Collections:                        │  │   │
│  │  │  • codes (code, tier, used,          │  │   │
│  │  │    suspended, fingerprint,            │  │   │
│  │  │    middleman, notes)                  │  │   │
│  │  │  • tier_configs (tier, config,       │  │   │
│  │  │    speed_limit_bps, udp_relay,        │  │   │
│  │  │    active)                            │  │   │
│  │  │  • activation_attempts (ip,           │  │   │
│  │  │    created) — rate limit tracking     │  │   │
│  │  └──────────────────────────────────────┘  │   │
│  │  JS Hooks: activation, suspension, rate    │   │
│  └─────────────────────────────────────────────┘   │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │  ssserver (Eco)      :8443  BBR + tc 5M   │   │
│  │  ssserver (Stealth)  :8444  Brutal 48M    │   │
│  │  ssserver (Strike)   :8445  BBR 200M tc   │   │
│  └───────────────────────────────────────────┘   │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │  Backups (hourly → Backblaze B2)           │   │
│  │  Uptime monitor (PingPong/UptimeRobot)     │   │
│  └───────────────────────────────────────────┘   │
└─────────────────────────────────────────────────┘
```

---

## Key Component Changes from V3

### Sing-Box Replaces sslocal + tun2socks

| Factor | V3 (sslocal + tun2socks) | V4 (sing-box) |
|--------|--------------------------|---------------|
| Binary count | 2 | **1** |
| Memory | ~10-15MB each | ~20-30MB total |
| TUN | tun2socks (abandonware 2020) | Built-in (actively maintained) |
| SOCKS5 hop | Yes (latency + complexity) | **No** (direct TUN→Shadowsocks) |
| DNS handling | Separate package needed | Built-in fake-dns |
| Auto-reconnect | Manual Go implementation | **Built-in** |
| Config format | Two configs (sslocal + tun2socks) | **Single JSON** |
| Cross-platform | ✅ | ✅ |
| Last upstream update | tun2socks: 2020 | **Ongoing** (2024-2025 releases) |

### Two-Phase Update Auto-Rollback

The V1 sentinel system (which V3 stripped) is restored:

```
applySwap:
  save current binary as .prev
  write .update-pending sentinel ← Phase 1
  write new binary
  restart

First launch after update:
  .update-pending EXISTS + .prev EXISTS
  → rename .update-pending → .update-confirmed ← Phase 2
  → keep .prev as safety net
  → continue normally

Second clean launch:
  .update-confirmed EXISTS
  → delete .update-confirmed + .prev
  → clean state

Crash on first launch:
  .update-pending still exists (never renamed to .confirmed)
  → AUTO-REVERT: restore .prev, delete sentinels
  → previous version starts, customer never notices

Manual revert:
  myvpn --revert
  → restore .prev, rename current to .bad for debugging
```

**Staged rollouts (gradual update delivery):**
Pushing an update to all customers at once risks mass outage if a bug slips through.
`update.json` supports a `rollout_percent` field (0-100):

```json
{
  "version": "1.1.0",
  "rollout_percent": 5,
  "windows": { ... },
  "macos_intel": { ... },
  "macos_arm": { ... }
}
```

Each client generates a deterministic random number from its device fingerprint.
If `rand(0,100) >= rollout_percent`, the client skips the update and tries again
on the next heartbeat check. This lets you:

- Start at 5% → monitor heartbeats for crash signals (drop in reports from new version)
- Ramp to 25% → confirm telemetry looks normal
- Ramp to 100% → full rollout
- Set to 0% to halt a bad rollout — no new clients take it, existing ones auto-revert

This is the difference between "update breaks your app forever" and "update breaks, app fixes itself."

### Heartbeat with 7-Day Grace Period + Exponential Backoff

Since Shadowsocks doesn't use JWT tokens (the password IS the auth), the heartbeat model is simpler than V1 but still provides resilience:

```
Heartbeat loop:
  GET /api/heartbeat?code=XXXX-XXXX-XXXX&fp=DEVICE_FINGERPRINT_HASH
  → Server responds: { status: "ok" [, update_available: "1.1.0"] }

  Base interval:   5 minutes
  On failure:      double interval, cap at 2 hours
  On success:      reset to 5 minutes

If hub unreachable:
  → Enter GRACE mode
  → VPN keeps working (config is stored locally, no token dependency)
  → Show yellow "can't reach server" indicator in GUI
  → Count up to 7 days
  → After 7 days: show "Re-activation required — server unreachable"
  → VPN still works until user quits app

Suspension:
  Server responds 403 { status: "suspended" }
  → Immediately disconnect VPN
  → Show "Account suspended — contact your middleman"
  → Refuse to connect until suspension lifted

Staged rollout via heartbeat:
  The fp parameter enables server-side canary gating. The server hashes the
  fingerprint and only signals update_available to devices whose bucket
  falls within rollout_percent. The client independently verifies eligibility
  using the same hash, providing double-gating for safety.

Crash detection:
  The updater writes .update-timestamp when an update is applied, and the
  heartbeat loop writes .last-heartbeat on each successful check. If on
  next startup the app finds an update was applied recently but no heartbeat
  was recorded, it assumes the update crashed at runtime and auto-reverts.
```

This is more resilient than V1's JWT-based system — the VPN tunnel doesn't depend on the hub at all. The hub is only needed for initial activation and suspension checks. The exponential backoff prevents connection storms when the server comes back after an outage, and the crash detection catches runtime failures that the startup-only sentinel would miss.

### Bandwidth Enforcement (Unchanged from Fixed V3)

| Tier | Port | Server CC | Bandwidth Limit | UDP | Enforcement Method |
|------|:----:|:---------:|:---------------:|:---:|-------------------|
| Eco | 8443 | BBR (system default) | 5 Mbps shared | ❌ | Server `tc` HTB qdisc |
| Stealth | 8444 | Brutal (LD_PRELOAD) | 48 Mbps target | ❌ | Brutal sysctl target rate |
| Strike | 8445 | BBR (system default) | 200 Mbps shared | ✅ | Server `tc` HTB qdisc |

System default CC = BBR. Only Brutal wrapper needed (no cubic-wrap).

> **Important:** Brutal CC runs on the **server** (Linux VPS), not on the client.
> The `tcp-brutal-ng` kernel module changes how the server's TCP stack sends data
> on the Stealth port (8444). The client (Windows/macOS/Linux) uses its default
> TCP stack and simply receives data faster. The student's operating system is
> irrelevant — this is purely a server-side optimization.

### Activation Codes with Luhm-mod-N Checksum

```
Format:  MYVPN-XXXX-XXXX-XXXX-C
Example: MYVPN-A7X3-K9M2-Q5P1-C
         │              └─ Luhn-mod-N check digit
         └─ 16 data characters (A-Z, 2-9, no I/O/0/1)
```

The checksum is validated:
1. **Client-side** — catches typos before the HTTP round-trip. Instant feedback.
2. **Server-side** — in the PocketBase hook, before DB lookup. Rejects invalid checksums without touching the database.

### Rate Limiting on Activation

Two layers:
1. **Caddy** — `rate_limit` module: 100 req/10s per IP globally
2. **PocketBase hook** — `activation_attempts` collection tracks failed attempts per IP. Block after 5 failed attempts within 10 minutes.

### Backups (Offsite to Backblaze B2)

Restored from V1, which V3 stripped entirely:

| Interval | What | Where |
|:--------:|------|-------|
| Hourly | SQLite `.backup` file | Backblaze B2 bucket |
| Daily | Compressed SQL dump | Backblaze B2 bucket |
| Immediate (before admin actions) | Manual trigger via script | VPS + B2 |

### Code Recovery on Device Failure

New admin API endpoint (PocketBase hook):

```
POST /api/admin/unbind-code
Body: { "code": "XXXX-XXXX-XXXX", "admin_token": "..." }
→ Clears the fingerprint from the code
→ Customer can re-activate on new device
→ Original device loses access (code was already marked used)
```

This gives you a way to handle the common support case: "I broke my laptop, I bought a new one, can I still use my VPN?"

### Diagnostic Export

When a customer has connection problems, guessing wastes everyone's time.
The app includes a "Help → Export Diagnostics" menu item that writes a plain-text
file to the desktop containing:

- App version and build timestamp
- OS and architecture
- Activated tier
- Current connection state (IDLE/CONNECTED/DEGRADED/DISCONNECTED/GRACE)
- Last 20 heartbeat HTTP status codes (no payload, no passwords — just "200" or "403" or "timeout")
- Last 5 error messages (the Go error strings, stripped of any user paths)
- Sentinel file status: `.update-pending` exists? `.update-confirmed` exists? `.prev` exists?
- sing-box engine running? (true/false)

No personal data. No IPs. No browsing history. No passwords. The file is safe
to paste into a Discord DM or email. The customer sends it to their middleman,
the middleman forwards it to you, and you diagnose the problem in 30 seconds
instead of 5 messages back and forth.

---

## Client-Side Config (PocketBase tier_configs)

The `config` object is now a sing-box outbound configuration, not sslocal.

```json
{
  "tier": "eco",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
    "server_port": 8443,
    "password": "eco-password",
    "method": "aes-256-gcm",
    "plugin": "",
    "plugin_opts": ""
  },
  "speed_limit_bps": 0,
  "udp_relay": false,
  "active": true
}
```

```json
{
  "tier": "stealth",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
    "server_port": 8444,
    "password": "stealth-password",
    "method": "aes-256-gcm",
    "plugin": "",
    "plugin_opts": ""
  },
  "speed_limit_bps": 0,
  "udp_relay": false,
  "active": true
}
```

```json
{
  "tier": "strike",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
    "server_port": 8445,
    "password": "strike-password",
    "method": "aes-256-gcm",
    "plugin": "",
    "plugin_opts": ""
  },
  "speed_limit_bps": 200000000,
  "udp_relay": true,
  "active": true
}
```

The Go manager constructs a full sing-box JSON config by wrapping this outbound config with TUN inbound, route rules, and DNS settings. The `speed_limit_bps` field documents the server-side cap enforced via tc for Eco (5 Mbps) and Strike (200 Mbps). Stealth's limit is enforced by the Brutal target rate.

---

## Port Map

| Service | Port | Purpose |
|---------|:----:|---------|
| Eco ssserver | 8443 | Shadowsocks, BBR + tc 5 Mbps |
| Stealth ssserver | 8444 | Shadowsocks, Brutal at 48 Mbps target |
| Strike ssserver | 8445 | Shadowsocks, BBR, 200 Mbps shared (tc), UDP |
| Caddy | 443 | TLS for PocketBase + update files |
| Caddy | 80 | ACME HTTP-01 challenge |
| PocketBase | 8090 (localhost) | Admin API + DB |
| SSH | 22 | Admin access |

---

## Anti-Detection & Obfuscation

### Current State: Shadowsocks Without Obfuscation

The current V4 plan uses bare Shadowsocks (AEAD cipher, no obfuscation plugin). This works today because Palo Alto's Shadowsocks App-ID signature relies on specific byte patterns that AEAD encryption disrupts. However, firewall signature databases are updated regularly.

### Risk Assessment

| Scenario | Likelihood | Impact | Mitigation |
|----------|-----------|--------|------------|
| N4L updates Palo Alto App-ID for Shadowsocks | Low-Medium | High — entire service blocked | Plugin-based obfuscation |
| N4L adds DPI on port 8443-8445 specifically | Low | Medium | Change ports |
| N4L blocks all non-443 non-web traffic | Very Low | High | Port hiding (run on 443) |

### Obfuscation Plugin (Optional, Add When Needed)

If N4L starts detecting Shadowsocks, add **v2ray-plugin** (WebSocket + TLS disguise) to the Shadowsocks config:

```bash
# Server-side: download v2ray-plugin
wget https://github.com/shadowsocks/v2ray-plugin/releases/download/v1.3.2/v2ray-plugin-linux-amd64-v1.3.2.tar.gz
tar -xzf v2ray-plugin-*.tar.gz
cp v2ray-plugin_linux_amd64 /usr/local/bin/v2ray-plugin

# Update Shadowsocks config to use the plugin
# /etc/shadowsocks/eco.json:
{
  "server": "0.0.0.0",
  "server_port": 8443,
  "password": "...",
  "method": "aes-256-gcm",
  "mode": "tcp_only",
  "plugin": "v2ray-plugin",
  "plugin_opts": "server;tls;host=www.cloudflare.com;path=/ws"
}
```

**Client-side (sing-box):** Replace the Shadowsocks outbound with a Shadowsocks + WebSocket outbound in the sing-box config. The sing-box `shadowsocks` outbound type supports a `plugin` field, or you can use a `shadow-tls` or `shadowsocks + websocket` chain.

**Trade-offs:** Obfuscation adds ~10% overhead, increases handshake latency slightly, and makes traffic look like WebSocket-over-TLS (which N4L cannot block without breaking legitimate web apps). Do not enable unless detection becomes an issue.

### Port Agility

If port-based blocking occurs:
- Change all three ports in the Shadowsocks configs and tier_configs
- Update firewall rules
- Clients get new config on next heartbeat (config refresh)
- No app update needed

### Future-Proofing

The sing-box architecture supports protocol chaining. If Shadowsocks is ever fully blocked, the Manager can be updated to generate configs using a different outbound type (e.g., VLESS, Trojan, or Hysteria2 over TCP) without changing any other code. The tier_config in PocketBase controls the entire outbound configuration.

---

## TUN Privilege Model

Creating a TUN device requires root (Linux), a privileged system extension (macOS),
or a kernel driver (Windows). Sing-box needs these privileges. The app runs at user
tier — it cannot create TUN interfaces directly.

### Architecture

A small **privileged helper service** is installed once (during first-time setup)
and runs persistently in the background. The user app communicates with it over
local IPC (a named pipe on Windows, a Unix socket on macOS/Linux).

```
Install (admin once):
  macOS:  installer copies helper → /Library/PrivilegedHelperTools/
          installs launchd plist → /Library/LaunchDaemons/
  Windows: installer copies helper → %ProgramFiles%/MyVPN/
           registers as Windows service (sc create)
  Linux:  installer copies helper → /opt/myvpn-helper/
           writes systemd unit → /etc/systemd/system/

Runtime IPC flow:
  App (user)                    Helper (root/System)
  ┌────────────┐               ┌──────────────────┐
  │ Write       │── IPC socket ──→ Read config       │
  │ sing-box    │               │ Write temp file    │
  │ config      │               │ Spawn sing-box     │
  │             │←── status  ────│ Monitor process    │
  │ Read status │               │ Report PID + health│
  └────────────┘               └──────────────────┘
```

### Security

- **Caller verification:** The helper verifies the caller's identity before
  accepting commands. On macOS, it checks `SO_PEERCRED` on the Unix socket
  (only the same UID as the installer connects). On Windows, it checks the
  named pipe's caller SID against the original installer SID.
- **Single config at a time:** The helper only accepts one active sing-box config.
  Submitting a new config stops the old one.
- **No network access:** The helper never makes network connections. It only
  spawns sing-box (which handles its own networking) and reports its status.
- **Cleanup on uninstall:** The uninstaller stops the helper, removes the
  plist/service registration, and deletes the helper binary.

### Platform Details

| Platform | IPC Method | Helper Location | Service Type |
|----------|-----------|-----------------|-------------|
| macOS    | Unix socket (`/var/run/myvpn-helper.sock`) | `/Library/PrivilegedHelperTools/com.myvpn.helper` | launchd LaunchDaemon |
| Windows  | Named pipe (`\\.\pipe\MyVPNHelper`) | `%ProgramFiles%\MyVPN\myvpn-helper.exe` | Windows Service |
| Linux    | Unix socket (`/run/myvpn-helper.sock`) | `/opt/myvpn-helper/myvpn-helper` | systemd service |

### Bundle

The helper binary is bundled alongside the main app and sing-box:

```
myvpn-windows.zip/
├── myvpn.exe
├── sing-box-windows-amd64.exe
└── myvpn-helper.exe          ← NEW

myvpn-macos-intel.zip/
├── myvpn-intel
├── sing-box-darwin-amd64
└── myvpn-helper-darwin       ← NEW
```

The installer runs the helper binary once with `--install` (requires admin),
which registers it with the system. After that, the helper starts automatically
on boot and the user app connects to it at runtime.

---

## Scaling & Geographic Placement

### Current Limitations

The Strike tier promises 33-44ms gaming latency. This is accurate when:
- The VPS is in the same region as the game servers (e.g., Sydney VPS + OCE game servers)
- The school's N4L connection adds minimal latency to the VPS

It is NOT accurate when:
- Students play on US or EU game servers (Sydney → US West = ~150ms added)
- The VPS is in a different AWS/Azure region than the games

### Medium-Term Solution

Deploy a second VPS in a different region (e.g., US West for NA game servers).
Give Strike users the closest VPS based on a simple latency check at connect time.
This requires:
1. A second ssserver instance on the new VPS, configured identically
2. A heartbeat endpoint that returns the closest VPS URL per tier
3. The client measuring latency to both VPSs at connect time

Not needed for MVP at one school playing on OCE servers. Worth noting for
future growth.

---

## What Gets Cut From V3 vs Added

| Feature | V3 | V4 | Reason |
|---------|:--:|:--:|--------|
| sslocal | ✅ | ❌ | Replaced by sing-box |
| tun2socks | ✅ | ❌ | Replaced by sing-box TUN |
| sing-box | ❌ | ✅ | One binary, active maintenance |
| SOCKS5 bridge | ✅ | ❌ | Removed — sing-box bypasses it |
| Two server instances | ✅ | ❌ | Now three (one per tier) |
| LD_PRELOAD wrappers | 2 | **1** (brutal only) | BBR is system default |
| Two-phase update | ❌ | ✅ | Restored from V1 |
| Heartbeat + grace | ❌ | ✅ | Restored from V1 |
| Offsite backups | ❌ | ✅ | Restored from V1 |
| Luhn checksum on codes | ❌ | ✅ | Restored from V1 |
| Rate limiting on activation | ❌ | ✅ | Restored from V1 |
| Code recovery (admin unbind) | ❌ | ✅ | New |
| Auto-reconnect | ❌ | ✅ | Built into sing-box |
| Force-update command | ❌ | ✅ | Heartbeat response signals update |
| `speed` field in config | ✅ | ❌ | Non-functional, replaced by server tc |
| Bandwidth probe | ❌ | ❌ | TCP doesn't need it |
