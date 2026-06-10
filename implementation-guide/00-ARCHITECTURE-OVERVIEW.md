# Architecture Overview — VPS-Only Edition

## Network Topology

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          SCHOOL NETWORK                                   │
│  Student Phone/PC                                                        │
│  └─ VPN Client App (Hysteria2/VLESS/TUIC/AmneziaWG) ─────────────────────┘
│       │                                                                   │
│       │  TLS/QUIC on port 443/8443/9443 (looks like normal HTTPS)        │
│       ▼                                                                   │
└──────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                          CLOUD VPS (Single Box)                           │
│  OS: Ubuntu 24.04 LTS                                                     │
│  Purpose: Protocol termination, traffic egress, probe absorption,         │
│           management panel, activation API — everything                   │
│                                                                            │
│  ┌───────────────────────────────────────────────────────────────────┐   │
│  │  PORT 443 (shared — QUIC/UDP + TCP/TLS)                            │   │
│  │  ├─ Hysteria2 (QUIC/UDP)     → Premium gaming, Brutal CC           │   │
│  │  ├─ VLESS-REALITY (TCP/TLS)  → Premium fallback, uTLS Chrome       │   │
│  │  └─ Nginx fallback (TCP)     → Probe absorption (via REALITY dest) │   │
│  ├───────────────────────────────────────────────────────────────────┤   │
│  │  PORT 8443 (QUIC/UDP)                                              │   │
│  │  └─ TUIC V5                  → Lite tier, cubic CC, no UDP relay   │   │
│  ├───────────────────────────────────────────────────────────────────┤   │
│  │  PORT 9443 (UDP)                                                   │   │
│  │  └─ AmneziaWG                → Standard tier, high obfuscation      │   │
│  ├───────────────────────────────────────────────────────────────────┤   │
│  │  PORT 8444 (HTTPS)                                                 │   │
│  │  └─ Marzban Panel (Docker)   → User management, traffic stats      │   │
│  ├───────────────────────────────────────────────────────────────────┤   │
│  │  PORT 443 (HTTPS, api. subdomain)                                  │   │
│  │  └─ Activation API           → Code generation, device activation  │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                                                            │
│  iptables MASQUERADE → Internet egress (VPS public IP)                    │
└──────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
                              INTERNET
```

## Tier → Protocol Mapping

| Tier | Price | Protocol | Port | Transport | Gaming | Key Differentiator |
|------|-------|----------|------|-----------|--------|-------------------|
| Lite | $5 | TUIC V5 | 8443/udp | QUIC | ❌ No | cubic CC, **UDP relay disabled** — web only |
| Standard | $9 | AmneziaWG | 9443/udp | UDP+obfs | ⚠️ OK-ish | High obfuscation overhead, MTU 1280, 30 Mbps cap |
| Premium | $10 | Hysteria2 | 443/udp | QUIC | ✅ Best | Brutal CC, Salamander obfs, unlimited speed |
| Premium Fallback | — | VLESS-REALITY | 443/tcp | TCP+TLS | ⚠️ OK | Auto-activated when UDP is blocked |

## Key Design Decisions

1. **Single box does everything.** No home server in the traffic path. No WireGuard tunnel. No dual NAT. All protocol termination and internet egress happens on the VPS. This eliminates home-upload bottlenecks, reduces latency, and removes an entire category of failure modes.

2. **Single port 443 for the main protocols.** Hysteria2 (QUIC/UDP) and VLESS-REALITY (TCP/TLS) share port 443 — QUIC and TCP are different IP protocols so they don't conflict. This looks like normal HTTPS traffic to basic DPI.

3. **REALITY as probe defense.** VLESS-REALITY's "REALITY" feature makes the server respond to unauthorized probes exactly like `www.microsoft.com`. Active probing (Layer 3 in the school's blocking playbook) gets fooled. Xray forwards unauthenticated connections to Nginx, which serves a generic "Cloud Infrastructure Services" page.

4. **No Cloudflare Spectrum needed.** The VPS terminates protocols directly. Cloudflare is only used for DNS (orange cloud/proxied) to hide the VPS origin IP. Spectrum would add latency and cost.

5. **Protocol diversity = censorship resilience.** If the school blocks UDP/QUIC, VLESS-REALITY over TCP keeps clients connected. If they fingerprint one protocol, the others still work. Each tier gets a different protocol with different network signatures.

6. **VPS IP is the egress IP.** No home IP involvement means no home upload bottleneck, no home ISP dependency, and simpler troubleshooting. If the VPS IP gets blocked (unlikely — the school blocks known free-VPN ASNs, not random cloud IPs), swap to a new VPS and update DNS. See the HA guide for a two-VPS setup that makes this automatic.

## Port Map Summary

| Port | Protocol | Service | Tier |
|------|----------|---------|------|
| 443/udp | QUIC | Hysteria2 (Salamander obfs, Brutal CC) | Premium |
| 443/tcp | TCP+TLS+REALITY | Xray VLESS-REALITY (uTLS Chrome) | Premium fallback |
| 8443/udp | QUIC | TUIC V5 (cubic CC, no UDP relay) | Lite |
| 9443/udp | UDP | AmneziaWG (high obfuscation) | Standard |
| 8080/tcp | HTTP | Nginx (internal — REALITY fallback dest) | — |
| 8444/tcp | HTTPS | Marzban management panel | — |
| 443/tcp | HTTPS | Activation API (api. subdomain, Nginx reverse proxy) | — |
| 9099/tcp | HTTP | Load agent (UFW-restricted to orchestrator IP only) | — |

## Scale-Out Path

When you outgrow a single VPS, the architecture extends to a **placement-service model** rather than a load balancer. An orchestrator VPS runs the activation API and a collector that polls all workers every 60 seconds for CPU/memory/connections/load. New clients are assigned to the least-loaded worker at activation time, then connect directly to that worker — the orchestrator never sees their traffic.

Full specification: **[04-SCALE-OUT-ORCHESTRATION.md](04-SCALE-OUT-ORCHESTRATION.md)**

```
                 ┌─ ORCHESTRATOR (API + collector + worker)
                 │
Student → API ───┼─ WORKER VPS A (Hysteria2, VLESS, TUIC, AmneziaWG, NAT)
  (activate)     │
                 └─ WORKER VPS B (Hysteria2, VLESS, TUIC, AmneziaWG, NAT)
                        ▲
                        │
              CLIENT TRAFFIC (direct, after activation)
```

## DNS & Cloudflare

- Domain pointed to Cloudflare with all records proxied (orange cloud)
- SSL/TLS: Full (strict)
- A records for `@`, `panel`, `api` all pointing to VPS IP
- Cloudflare hides the VPS origin IP from casual inspection
