# MyVPN — Simplified for Launch

**Target:** 10–20 users at one school. Not production-scale. Not 50,000 users.

## Philosophy

Stop designing for 50,000 users when you have 20. Everything in this plan asks:

> *"Does this matter for 20 high schoolers who just want to play games during lunch?"*

If the answer is no, it's gone.

## What Got Cut From V2

| Feature | Why Removed |
|---------|-------------|
| **Heartbeat + grace period** | 20 users don't need 7-day grace periods or telemetry. If the VPS is down, fix it and the next connect works. |
| **Auto-updater** | Just put new binaries on Google Drive / Discord and tell them. No sentinel, no SHA256 pipeline, no rollback. |
| **EWMA bandwidth monitor** | Simplified to a single 500KB download probe at connect time. No ongoing adaptation (~30 lines in `internal/probe/`). |
| **usque/Warp fallback** | Hysteria 2 only. If it gets blocked, you push a new config. |
| **Hardware fingerprinting** | Kept as educational infrastructure (~120 lines across 4 files). See ARCHITECTURE.md for details. |
| **Process disguise / sandboxing** | No one at a school is doing process forensics on 20 students. |
| **Cert pinning** | Standard HTTPS trust is fine. If your VPS cert changes the app won't break. |
| **Privileged helper service** | Use Hysteria 2's built-in `--tun` flag. |
| **4 tiers → 3 tiers** | Dropped the artificial "Gaming Mid" friction tier. Added "Eco" as a genuine budget option. |

## The Three Tiers

| Tier | Codename | Engine | Speed | Tuning | For |
|------|----------|--------|-------|--------|-----|
| **Eco** | `eco` | Hysteria 2 | 5 Mbps cap | Standard CC, small recv_window | Budget browsing, iMessage, low-res YouTube |
| **Stealth** | `stealth` | Hysteria 2 | 48 Mbps cap | Standard CC, large recv_window | Streaming, social media, general use |
| **Strike** | `strike` | Hysteria 2 | ~80% of wifi speed | Brutal CC, no artificial cap | Gaming (Minecraft, Fortnite, Valorant) |

## Directory Layout (when built)

```
myvpn-simplified/
├── README.md              ← this file
├── BUSINESS.md            ← business model, tiers, pricing, middlemen
├── ARCHITECTURE.md        ← technical architecture & data flow
├── IMPLEMENT.md           ← step-by-step implementation for a developer
├── DEPLOY.md              ← server setup & operations
│
├── cmd/
│   └── myvpn/
│       └── main.go        ← entry point
│
├── internal/
│   ├── activation/        ← code validation + fingerprint collection + config retrieval
│   ├── probe/             ← single-download bandwidth probe (Strike tier only)
│   ├── manager/           ← engine lifecycle (start/stop Hysteria 2)
│   ├── tunnel/            ← TUN setup/teardown (via engine --tun + helpers)
│   ├── storage/           ← persistent state (JSON file)
│   └── gui/               ← Fyne window + activation prompt + connect flow
│
├── server/
│   ├── Caddyfile          ← reverse proxy config
│   └── pb_hooks/          ← PocketBase JS hooks
│       ├── activation.pb.js
│       └── suspension.pb.js
│
└── scripts/
    ├── setup_vps.sh       ← one-command VPS bootstrap
    └── generate_codes.sh  ← batch-create activation codes
```

## How It Works (60-Second Version)

```
Student gets paper code from middleman ($ cash)
        ↓
Opens app → enters code → app collects hardware fingerprint
        ↓
Fingerprint + code POSTed to server
        ↓
Server validates → stores fingerprint (first time) → returns config
        ↓
App saves config
        ↓
[Strike tier only] Downloads 500KB probe → measures bandwidth → sets Brutal target
        ↓
Starts Hysteria 2 with --tun → tunnel is up
        ↓
Student plays games / streams / browses
        ↓
Disconnects → engine stops → no heartbeat, no tracking
```

That's it. No background monitors, no telemetry, no grace periods, no update polling.
Fingerprinting + probe are the only two extras from the V2 plan, and both are
under 100 lines combined.

## Key Numbers

| Metric | Value |
|--------|-------|
| Target users | 10–20 per school |
| VPS | 1 × Ubuntu 22.04, 2GB RAM ($6–12/month, unmetered bandwidth) |
| Hysteria 2 server | Single instance, three auth pools (eco / stealth / strike) |
| Storage | PocketBase SQLite — good for 20 users |
| Code complexity | ~1,200 lines Go (vs 2,376 in V1, ~1,700 in V2) |
| Build time | One weekend for a competent Go developer |
| Pricing | Eco $2/mo · Stealth $4/mo · Strike $8/mo |
