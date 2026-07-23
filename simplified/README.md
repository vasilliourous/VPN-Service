# MyVPN — Simplified for Launch

**Target:** 10–20 users at one school. Not production-scale. Not 50,000 users.

## Philosophy

Stop designing for 50,000 users when you have 20. Everything in this plan asks:

> *"Does this matter for 20 high schoolers who just want to play games during lunch?"*

If the answer is no, it's gone. But some features that were cut are re-added when
they directly reduce human maintenance (auto-updater, background mode).

## Delivery Model

```
You generate 100 codes → print on card sheets
  → staple to laminated instruction card
  → stack to middlemen
    → middleman hands card to student ($ cash)
      → student goes home, downloads app from link on card
        → enters code → connected
```

No installers. No app stores. No emails. Physical cards with a download link.

## The Three Tiers

| Tier | Codename | Engine | Speed | Tuning | For |
|------|----------|--------|-------|--------|-----|
| **Eco** | `eco` | Hysteria 2 | 5 Mbps cap | Standard CC, small recv_window | Budget browsing, iMessage, low-res YouTube |
| **Stealth** | `stealth` | Hysteria 2 | 48 Mbps cap | Standard CC, large recv_window | Streaming, social media, general use |
| **Strike** | `strike` | Hysteria 2 | ~80% of wifi speed | Brutal CC, no artificial cap | Gaming (Minecraft, Fortnite, Valorant) |

## What's Re-Added vs V2

| Feature | Status | Why |
|---------|--------|-----|
| **Auto-updater** | ✅ Re-added (80 lines, simplified) | You said you won't write code. Agent pushes updates via `update.json`. |
| **Systray background** | ✅ Added (new) | App lives in system tray, not a window. Connection stays alive when closed. |
| **Printable codes** | ✅ Added (new) | Script outputs codes in card-printable format. |
| Heartbeat + grace | ❌ Cut | Not needed for 20 users |
| EWMA bandwidth monitor | ❌ Cut | Single probe is enough |
| usque/Warp fallback | ❌ Cut | Hysteria 2 only |
| Process disguise | ❌ Cut | Overkill for school |
| Cert pinning | ❌ Cut | Standard HTTPS trust |
| Privileged helper | ❌ Cut | Engine `--tun` flag |

## Directory Layout (when built)

```
myvpn-simplified/
├── README.md              ← this file
├── BUSINESS.md            ← business model, tiers, pricing, middlemen
├── ARCHITECTURE.md        ← technical architecture & data flow
├── IMPLEMENT.md           ← step-by-step implementation for a developer
├── OPS.md                 ← agent operations manual (API calls, SSH commands)
├── DEPLOY.md              ← server setup & operations
├── UI-AESTHETICS.md       ← visual design spec (do after backend)
│
├── cmd/myvpn/main.go      ← entry point
│
├── internal/
│   ├── activation/        ← code validation + fingerprint + config retrieval
│   ├── probe/             ← bandwidth probe (Strike tier)
│   ├── manager/           ← engine lifecycle (start/stop Hysteria 2)
│   ├── updater/           ← background auto-update checker
│   ├── tunnel/            ← TUN setup/teardown (engine --tun + fallbacks)
│   ├── storage/           ← persistent state (JSON file)
│   └── gui/               ← Fyne systray app + control panel
│
├── server/
│   ├── Caddyfile          ← reverse proxy + static files
│   └── pb_hooks/          ← PocketBase JS hooks (activation, suspension, codes)
│
└── scripts/
    ├── setup_vps.sh       ← one-command VPS bootstrap
    ├── generate_codes.sh  ← batch-create activation codes
    └── print_codes.sh     ← output codes in printable card format
```

## How It Works (60-Second Version)

```
Middleman gets stack of pre-printed cards from you
        ↓
Student pays cash → gets card with code + website URL
        ↓
Goes home → downloads app from website
        ↓
Opens app → enters code → app collects hardware fingerprint
        ↓
Fingerprint + code POSTed to server
        ↓
Server validates → stores fingerprint (first time) → returns config
        ↓
App minimizes to system tray (green dot when connected)
        ↓
App auto-updates in background every 6 hours
        ↓
Student plays games / streams / browses
```

That's it. App runs in background, updates itself, student forgets it exists.

## Key Numbers

| Metric | Value |
|--------|-------|
| Target users | 10–20 per school |
| VPS | 1 × Ubuntu 22.04, 2GB RAM ($6–12/month, unmetered) |
| Hysteria 2 server | Single instance, three auth pools |
| Storage | PocketBase SQLite |
| Code complexity | ~1,260 lines Go (vs 2,376 in V1) |
| Build time | One weekend |
| Pricing | Eco $2 · Stealth $4 · Strike $8/mo |
| Website | Static HTML on same VPS, zero maintenance |
