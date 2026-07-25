# MyVPN V5 — The Definitive Edition

> **V5 is the consolidated, production-hardened version of MyVPN.**  
> It folds everything learned from V1–V4 and real-world VPS testing into one
> directory: clean server modules, comprehensive documentation, fixed issues,
> and a developer guide for the client app.

---

## What's Inside

```
v5/
├── README.md              # This file — V5 overview
├── CONTEXT.md             # Agent/developer context — network analysis, protocol tests, reasoning
├── docs/
│   ├── ARCHITECTURE.md    # How to architect a compatible client (practical, implementation-focused)
│   ├── CLIENT-GUIDE.md    # How to build the client app (build commands, platform notes)
│   ├── DEPLOY.md          # Server deployment guide (from blank VPS to live)
│   ├── IMPLEMENT.md       # Step-by-step phased implementation plan (this → then → that)
│   ├── OPS.md             # Operations manual (day-to-day management)
│   ├── API.md             # All API contracts (activation, heartbeat, admin)
│   ├── UI-AESTHETICS.md   # Visual design spec (colors, layout, icons)
│   └── FIXES.md           # Issues discovered & fixes applied
├── server/                # Server deployment code (from modular-vps, hardened)
│   ├── modules/           # 8 idempotent setup modules (00-env → 08-firewall)
│   ├── pb_hooks/          # PocketBase JS hooks (activation, heartbeat, admin)
│   ├── templates/         # Config templates (Caddyfile, ssserver JSONs, systemd)
│   ├── setup.sh           # One-command VPS orchestrator
│   └── restore.sh         # Full disaster recovery from B2 backup
└── scripts/               # Utility scripts
    ├── generate_codes.sh  # Luhn-mod-N activation code generator
    └── print_codes.sh     # Printable PDF code card sheets
```

---

## Relationship to Earlier Versions

| Version | Location | Status | Notes |
|---------|----------|:------:|-------|
| **V1** | *(historical)* | ❌ Lost | Two-phase update safety invented here |
| **V2** | `originals/` | 📚 Reference | Business plans, threat models, competitive intel |
| **V3** | `v3/` | 📚 Reference | sslocal + tun2socks architecture (tun2socks abandonware) |
| **V4** | `v4/` | ✅ Current | Go + Fyne client source code (3,947 lines, 19 files) — **client developed separately** |
| **V5** | `v5/` | 🏆 **Definitive** | Consolidated docs + hardened server code + context for agents |
| **modular-vps/** | `modular-vps/` | ✅ Tested | VPS modules tested on Voyager VPS (source for v5/server/) |
| **simplified/** | `simplified/` | 📚 Reference | Hysteria 2 / QUIC-based alternative (blocked by N4L UDP block) |

The **client source code** lives in `v4/` and is developed separately.  
V5 documents the protocol contracts and build pipeline so anyone can create
a compatible client.

---

## What Makes V5 Different

1. **Hardened server code** — All 8 modules tested on a real VPS, bugs fixed
2. **Comprehensive documentation** — Architecture, deploy, ops, API — everything in one place
3. **Known issues documented** — Every code quality issue and architectural concern is written down
4. **Client developer guide** — Standalone document for building a compatible client
5. **Clean separation** — Server code, client docs, and reference material are clearly separated

---

## Quick Start

```bash
# 1. Deploy server (requires a blank Ubuntu 22.04 VPS + domain)
DOMAIN=api.yourdomain.com ./v5/server/setup.sh

# 2. Create PocketBase admin account at https://api.yourdomain.com/_/
#    Then create collections: codes, tier_configs, activation_attempts, update_config

# 3. Generate activation codes
./v5/scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 50

# 4. Build client (see v5/docs/CLIENT-GUIDE.md)
cd v4 && make build
```
