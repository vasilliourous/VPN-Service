# MyVPN V3 — Shadowsocks Architecture

**Target:** 10–20 users at one school. Network: N4L (Palo Alto firewall, UDP blocked, TLS fingerprinted).

## What Changed From V2

| Area | V2 (simplified) | V3 (current) |
|------|-----------------|--------------|
| **Engine** | Hysteria 2 (QUIC/UDP) → blocked by N4L | Shadowsocks-rust (TCP) → only protocol that passes N4L |
| **TUN** | Hysteria 2 built-in `--tun` | `tun2socks` (separate binary, forwards to SOCKS5) |
| **Bandwidth probe** | 500KB download to set Brutal target | ❌ Removed — TCP doesn't need it |
| **Client binaries** | `hysteria2` (one binary) | `sslocal` + `tun2socks` (two binaries) |
| **Server instances** | Single server + Brutal port | **Three** instances — one per tier (8443/8444/8445) |
| **Bandwidth caps** | Non-functional `speed` field in config | Server-side `tc` (Eco) + Brutal target rate (Stealth) |
| **TCP Brutal** | N/A | Server-side kernel module for Stealth tier, target rate 48 Mbps |
| **UDP relay** | N/A (Hysteria 2 is UDP-native) | SOCKS5 UDP ASSOCIATE → 33-44ms gaming (Strike only) |
| **BBR** | N/A | System default CC; Strike inherits, Eco overridden to CUBIC |
| **VPS setup** | Pre-configured | From scratch — blank Ubuntu 22.04 |

## Why Hysteria 2 Was Replaced

N4L (the school network provider) blocks all non-DNS UDP and fingerprints non-browser TLS:

| Mechanism | What it kills | Shadowsocks bypass |
|-----------|--------------|-------------------|
| UDP block | QUIC protocols (Hysteria 2, TUIC V5) | TCP only (no UDP needed) |
| JA3 fingerprinting | TLS proxies (Trojan, Xray) | No TLS — traffic is random bytes |
| SNI/IP mismatch detection | TLS proxies with disguise | No TLS handshake at all |
| App-ID classification | Pattern-based tunnel detection | Opaque encrypted stream, unclassifiable |

## The Three Tiers

| Tier | Price | Port | Server CC | Client CC | Bandwidth Limit | UDP Relay | Experience |
|------|-------|:----:|:---------:|:---------:|:---------------:|:---------:|------------|
| **Eco** | $2 | 8443 | CUBIC (forced via LD_PRELOAD) | CUBIC (OS default) | 5 Mbps (server tc) | ❌ | Text loads slow, video buffers |
| **Stealth** | $4 | 8444 | **Brutal** (forced via LD_PRELOAD) | CUBIC (OS default) | 48 Mbps (Brutal target) | ❌ | Fast streaming, aggressive speed — poor for gaming |
| **Strike** | $8 | 8445 | **BBR** (system default) | CUBIC (OS default) | Unlimited | ✅ | Gaming (33-44ms), 4K streaming |

## Architecture

```
App (Go + Fyne)
├── Activation → PocketBase returns {config, speed_limit_bps, udp_relay}
├── sslocal -c config.json -b 127.0.0.1:1080
│   └── tier port: Eco→:8443, Stealth→:8444, Strike→:8445
├── tun2socks --tun tun0 --proxy socks5://127.0.0.1:1080
│   └── --socks5-udp only for Strike
├── System routing → all traffic through TUN
└── Background auto-updater checks update.json every 6h

Server VPS:
├── ssserver (Eco)   :8443  CUBIC+tc  5 Mbps  tcp_only
├── ssserver (Stealth):8444  Brutal    48 Mbps tcp_only
├── ssserver (Strike) :8445  BBR       Unlimited tcp_and_udp
├── Caddy → PocketBase (codes + tier_configs)
└── tc qdisc: port 8443 shaped to 5 mbit
```

## Directory

```
v3/
├── README.md         ← this file
├── ARCHITECTURE.md   ← full system design
├── BUSINESS.md       ← business model, tiers, pricing
├── IMPLEMENT.md      ← step-by-step build guide
├── DEPLOY.md         ← server setup from scratch
├── OPS.md            ← agent operations manual
└── UI-AESTHETICS.md  ← visual design (do after backend)
```
