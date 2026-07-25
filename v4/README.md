# MyVPN V4 — Sing-Box + Enterprise Reliability

> **Build this.** V4 is the current production-ready architecture. It replaces V3's
> sslocal+tun2socks with a single maintained binary (sing-box), restores the
> two-phase update rollback safety from V1, and adds business-critical features
> that V3 stripped.

---

## What Changed From V3

| Area | V3 Problem | V4 Fix |
|------|-----------|--------|
| **Client engine** | sslocal + tun2socks (2 binaries, tun2socks abandonware) | **sing-box** — 1 binary, actively maintained, built-in TUN |
| **SOCKS5 bridge** | Extra network hop between sslocal → tun2socks | **Eliminated** — sing-box handles TUN→Shadowsocks internally |
| **Update safety** | Replace + restart. Crash = dead app. | **Two-phase sentinel** — auto-rollback on crash, manual `--revert` |
| **Heartbeat** | None | 5-min heartbeat with **7-day grace period** |
| **Backups** | None | **Hourly offsite** to Backblaze B2 |
| **Activation codes** | No checksum, client types blind | **Luhn-mod-N** — catches typos before server round-trip |
| **Rate limiting** | None on activation | **Caddy + PocketBase hook** — 5 attempts per 10 min per IP |
| **Code recovery** | Device breaks = customer loses VPN | **Admin unbind endpoint** — clear fingerprint, re-activate on new device |
| **Auto-reconnect** | App must re-implement | **Built into sing-box** |
| **Force update** | Wait 6 hours for check | **Heartbeat delivers update notification** — app checks immediately |
| **LD_PRELOAD wrappers** | 2 (cubic-wrap + brutal-wrap) | **1** (brutal-wrap only) — BBR is system default |
| **Server instances** | 2 (shared Eco+Strike port) | **3 separate ports** — Eco:8443, Stealth:8444, Strike:8445 |

---

## The Three Tiers

| Tier | Price | Port | Server CC | Bandwidth Limit | UDP | Experience |
|------|-------|:----:|:---------:|:---------------:|:---:|------------|
| **Eco** | $2 | 8443 | BBR | 5 Mbps (server tc) | ❌ | Text loads slow, video buffers |
| **Stealth** | $4 | 8444 | **Brutal** (LD_PRELOAD) | 48 Mbps (Brutal target) | ❌ | Fast streaming, jitter makes gaming bad |
| **Strike** | $8 | 8445 | BBR | 200 Mbps (server tc) | ✅ | Gaming (33-44ms), 4K streaming |

---

## Architecture

```
App (Go + Fyne)                  VPS (Ubuntu 22.04)
┌───────────────────┐           ┌────────────────────────┐
│ Activation         │────POST──→│ PocketBase             │
│  • Luhn checksum   │  /api/   │  • codes + tier_configs│
│  • Fingerprint     │ activate │  • activation_attempts  │
└────────┬──────────┘           │  • JS hooks            │
         │                      │  • Hourly B2 backups   │
┌────────┴──────────┐           │  • Uptime monitoring   │
│ Heartbeat (5 min)  │──GET────→│  /api/heartbeat        │
│  • Grace counter   │ /heartbeat│  (suspension + update) │
│  • Update check    │           │                        │
└────────┬──────────┘           └────────┬───────────────┘
         │                               │
┌────────┴──────────┐           ┌────────┴───────────────┐
│ Manager            │           │ ssserver instances      │
│  • Builds sing-box │──TCP──→   │  Eco    :8443 (BBR+tc) │
│    config from     │           │  Stealth:8444 (Brutal)  │
│    tier_configs    │           │  Strike :8445 (BBR+UDP) │
│  • Launches/       │           └────────────────────────┘
│    monitors        │
│    sing-box        │
└────────┬──────────┘
         │
┌────────┴──────────┐
│ Updater            │
│  • Two-phase       │──HTTP──→ /var/www/html/update.json
│    sentinel        │
│  • Auto-rollback   │
│  • --revert flag   │
└───────────────────┘
```

---

## Key Numbers

| Metric | V3 | V4 |
|--------|:--:|:--:|
| Client binaries bundled | 3 (app + sslocal + tun2socks) | **2** (app + sing-box) |
| Server services | 2 shadowsocks + Caddy + PB + wrappers | 3 shadowsocks + Caddy + PB + 1 wrapper |
| Backup safety | ❌ None | ✅ Hourly offsite (B2) |
| Update crash safety | ❌ None | ✅ Two-phase auto-rollback |
| Code recovery | ❌ None | ✅ Admin unbind endpoint |
| Checksum validation | ❌ None | ✅ Luhn-mod-N (client + server) |
| Rate limiting | ❌ None | ✅ Caddy + PB hook |
| Heartbeat resilience | ❌ None | ✅ 7-day grace period |
| VPS cost | ~$8/mo | ~$8/mo (same) |
| Build time | 1 weekend | 1-2 weekends |

---

## Directory

```
v4/
├── README.md         ← this file
├── ARCHITECTURE.md   ← full system design
├── BUSINESS.md       ← business model, tiers, pricing
├── IMPLEMENT.md      ← step-by-step build guide
├── DEPLOY.md         ← server setup from scratch
├── OPS.md            ← agent operations manual
└── UI-AESTHETICS.md  ← visual design (link: ../v3/UI-AESTHETICS.md)
```
