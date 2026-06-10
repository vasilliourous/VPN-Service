# VPN Monetised Service — Implementation Guide (VPS-Only)

> **Production-grade runbook for deploying a 3-tier paid VPN service on a single VPS.**

---

## Architecture: Single Box, Everything On It

```
Student Device (school WiFi)
    │
    │  TLS/QUIC on ports 443/8443/9443
    ▼
VPS ($5-10/mo, 1-2 vCPU, Ubuntu 24.04)
    ├─ Hysteria2 :443/udp     → Premium (gaming, Brutal CC, Salamander)
    ├─ VLESS-REALITY :443/tcp → Premium fallback (uTLS Chrome)
    ├─ TUIC V5 :8443/udp      → Lite (web only, cubic CC, no UDP)
    ├─ AmneziaWG :9443/udp    → Standard (high obfuscation, 30Mbps cap)
    ├─ Nginx :8080            → Probe absorption (REALITY fallback dest)
    ├─ Marzban Panel (Docker) → User management
    ├─ Activation API         → Code generation & subscriptions
    └─ iptables MASQUERADE    → NAT to internet (VPS IP)
```

No home server in the traffic path. No WireGuard tunnels. No dual NAT. The VPS terminates protocols and NATs directly to the internet.

## Quick Navigation

| Document | What It Covers |
|----------|---------------|
| **[00-ARCHITECTURE-OVERVIEW.md](00-ARCHITECTURE-OVERVIEW.md)** | Network topology, tier→protocol mapping, design decisions |
| **[01-MASTER-IMPLEMENTATION.md](01-MASTER-IMPLEMENTATION.md)** | Complete build guide — Sections 1-13: setup, protocols, nginx, NAT, tiers, Marzban, client app, activation API, security, monitoring, backups, runbooks |
| **[03-HIGH-AVAILABILITY.md](03-HIGH-AVAILABILITY.md)** | Low-cost HA design — second VPS, Cloudflare failover, emergency swap |
| **[04-SCALE-OUT-ORCHESTRATION.md](04-SCALE-OUT-ORCHESTRATION.md)** | Horizontal scale-out — placement service, load agent, collector, auto-migration |
| **[10-ADMIN-FEEDBACK.md](10-ADMIN-FEEDBACK.md)** | Fake report to give the school IT admin — blocks free VPNs, not your stack |

### Configs (`configs/`)

| File | Service |
|------|---------|
| `hysteria2-premium.yaml` | Hysteria2 Premium tier config |
| `xray-vless-reality.json` | Xray VLESS-REALITY fallback config |
| `tuic-lite.json` | TUIC V5 Lite tier config |
| `amneziawg-standard.conf` | AmneziaWG Standard tier config |
| `nginx-fallback.conf` | Nginx probe absorption |
| `nginx-api.conf` | Nginx reverse proxy for activation API |
| `marzban-docker-compose.yml` | Marzban panel Docker Compose |
| `marzban.env` | Marzban environment variables |
| `activation-api.env` | Activation API environment variables |
| `workers.conf.example` | Worker pool hostname list (scale-out — orchestrator only) |

### Scripts (`scripts/`)

| Script | Purpose |
|--------|---------|
| `vps-nat-setup.sh` | iptables MASQUERADE — outbound traffic gets VPS IP |
| `vpn-health-check.sh` | Service monitoring, run via cron every 5 min |
| `vpn-backup.sh` | Nightly backup of all configs + DB |
| `load-agent.py` | System metrics reporter on :9099 (scale-out — every VPS) |
| `collector.py` | Polls workers, scores them, writes best candidate (scale-out — orchestrator only) |

## Tier Summary

| Tier | Price | Protocol | Gaming | Key Differentiator |
|------|-------|----------|--------|-------------------|
| Lite | $5 | TUIC V5 | ❌ No | cubic CC, **no UDP relay** — web browsing only |
| Standard | $9 | AmneziaWG | ⚠️ OK-ish | High obfuscation overhead, MTU 1280, 30 Mbps cap |
| Premium | $10 | Hysteria2 + VLESS fallback | ✅ Best | Brutal CC, Salamander obfs, unlimited speed |

## Execution Order

```
PHASE 1 — Infrastructure
  1. Section 1  — Fill in variables
  2. Section 2  — VPS base setup + hardening
  3. Section 4  — Nginx fallback
  4. Section 5  — NAT + egress

PHASE 2 — Protocols
  5. Section 3  — Install Hysteria2, Xray, TUIC, AmneziaWG
  6. Section 6  — Per-tier configurations

PHASE 3 — Management
  7. Section 7  — Marzban panel (Docker)
  8. Section 9  — Activation API
  9. Section 10 — Security hardening

PHASE 4 — Clients
  10. Section 8  — Build/deploy client app
  11. Distribute APK + activation codes

PHASE 5 — Operations
  12. Section 11 — Monitoring + health checks
  13. Section 12 — Backups
  14. Section 13 — Runbooks
```

## Pre-Flight Checklist

- [ ] VPS provisioned with Ubuntu 24.04, SSH access
- [ ] Domain name pointed to Cloudflare (orange cloud/proxied)
- [ ] All `REPLACE_ME` values in configs replaced
- [ ] REALITY keys generated (`xray x25519`)

## Critical Security Notes

1. **Rotate VPS IPs proactively** — if the school blocks your VPS IP, follow the emergency swap runbook (Section 12.3).
2. **Don't use the same subdomain for everything** — separate `panel.`, `api.` to make domain-level blocking harder.
3. **Monitor protocol detection research** — Hysteria2's Salamander obfuscation and VLESS-REALITY may eventually be fingerprintable.
4. **Keep the activation API admin key secret** — it controls code generation and user data.

---

*Questions? Issues? Read the full guide first — it's designed to be exhaustive.*
