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
│   │  tc 5M  │ 48M tgt │  unlimited                │
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
│  │  ssserver (Strike)   :8445  BBR unlimited │   │
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

This is the difference between "update breaks your app forever" and "update breaks, app fixes itself."

### Heartbeat with 7-Day Grace Period

Since Shadowsocks doesn't use JWT tokens (the password IS the auth), the heartbeat model is simpler than V1 but still provides resilience:

```
Heartbeat every 5 minutes:
  GET /api/health?code=XXXX-XXXX-XXXX
  → Server responds: { status: "ok" } or { status: "suspended" }

If hub unreachable:
  → Enter GRACE mode
  → VPN keeps working (config is stored locally, no token dependency)
  → Show yellow "can't reach server" indicator in GUI
  → Count up to 7 days
  → After 7 days: show "Re-activation required — server unreachable"
  → VPN still works until user quits app

Suspension:
  Server responds { status: "suspended" }
  → Immediately disconnect VPN
  → Show "Account suspended — contact your middleman"
  → Refuse to connect until suspension lifted
```

This is more resilient than V1's JWT-based system — the VPN tunnel doesn't depend on the hub at all. The hub is only needed for initial activation and suspension checks.

### Bandwidth Enforcement (Unchanged from Fixed V3)

| Tier | Port | Server CC | Bandwidth Limit | UDP | Enforcement Method |
|------|:----:|:---------:|:---------------:|:---:|-------------------|
| Eco | 8443 | BBR (system default) | 5 Mbps shared | ❌ | Server `tc` HTB qdisc |
| Stealth | 8444 | Brutal (LD_PRELOAD) | 48 Mbps target | ❌ | Brutal sysctl target rate |
| Strike | 8445 | BBR (system default) | Unlimited | ✅ | None needed |

System default CC = BBR. Only Brutal wrapper needed (no cubic-wrap).

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

---

## Client-Side Config (PocketBase tier_configs)

The `config` object is now a sing-box outbound configuration, not sslocal.

```json
{
  "tier": "eco",
  "config": {
    "server": "api.yourdomain.com",
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
    "server": "api.yourdomain.com",
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
    "server": "api.yourdomain.com",
    "server_port": 8445,
    "password": "strike-password",
    "method": "aes-256-gcm",
    "plugin": "",
    "plugin_opts": ""
  },
  "speed_limit_bps": 0,
  "udp_relay": true,
  "active": true
}
```

The Go manager constructs a full sing-box JSON config by wrapping this outbound config with TUN inbound, route rules, and DNS settings. The `speed_limit_bps` field is informational (server-side enforcement handles caps).

---

## Port Map

| Service | Port | Purpose |
|---------|:----:|---------|
| Eco ssserver | 8443 | Shadowsocks, BBR + tc 5 Mbps |
| Stealth ssserver | 8444 | Shadowsocks, Brutal at 48 Mbps target |
| Strike ssserver | 8445 | Shadowsocks, BBR, unlimited, UDP |
| Caddy | 443 | TLS for PocketBase + update files |
| Caddy | 80 | ACME HTTP-01 challenge |
| PocketBase | 8090 (localhost) | Admin API + DB |
| SSH | 22 | Admin access |

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
