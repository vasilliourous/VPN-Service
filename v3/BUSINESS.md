# Business Model & Operations (V3)

Same as V2 with updates for Shadowsocks architecture and three-tier port separation.

## The Three Tiers

| Tier | Price | Port | Protocol | Speed | Server CC | UDP | Experience |
|------|-------|:----:|----------|-------|:---------:|:---:|------------|
| **Eco** | $2 | 8443 | Shadowsocks TCP | 5 Mbps cap (server tc) | CUBIC | ❌ | Text loads slow, video buffers. It works, just not great. |
| **Stealth** | $4 | 8444 | Shadowsocks TCP | 48 Mbps cap (Brutal target) | **Brutal** | ❌ | Fast streaming, aggressive throughput. Jitter makes gaming unplayable. |
| **Strike** | $8 | 8445 | Shadowsocks TCP+UDP | Unlimited | BBR | ✅ | Gaming (33-44ms), 4K streaming. |

### Why Brutal is in Stealth

TCP Brutal aggressively fills the pipe. It's great for throughput, terrible for latency stability. This creates natural differentiation:

| Tier | What you get | What you don't get |
|------|-------------|-------------------|
| Eco | A connection | Speed |
| Stealth | Maximum speed | Stable latency (bad for gaming) |
| Strike | Low latency gaming | Nothing (it's the top tier) |

A user who buys Stealth for streaming is happy. A user who buys Stealth hoping to game will be frustrated (high jitter) — and that's the feature. It pushes them to Strike.

### Why BBR is in Strike (Not Stealth)

BBR maintains low latency under congestion — essential for real-time gaming. Combined with UDP relay, Strike gives the best possible gaming experience on congested school WiFi. CUBIC (Eco) and Brutal (Stealth) both introduce jitter under load, making them unsuitable for gaming regardless of bandwidth.

### No "Stream" Tier Needed

The V2 plan considered a separate Stream tier with Brutal. It's cleaner to just put Brutal in Stealth. Four tiers confuse middlemen. Three tiers with clear differentiation sell themselves.

## Competitive Advantage

Shadowsocks bypasses N4L's Palo Alto firewall in ways that free VPNs cannot:

| Free VPNs | MyVPN |
|-----------|-------|
| Use TLS → JA3 fingerprinted | Uses opaque encrypted stream → unclassifiable |
| Use UDP (WireGuard, QUIC) → blocked | Uses TCP → passes through |
| IPs on public blocklists | Fresh VPS IP, unknown to blocklists |
| Slow on congested WiFi (TCP CUBIC) | Brutal CC on Stealth fights for bandwidth |
| Oversold servers | 20 users on a dedicated VPS |

## Pricing

| | Eco | Stealth | Strike |
|---|-----|---------|--------|
| Monthly | $2 | $4 | $8 |
| Middleman cut (20-30%) | $0.40-0.60 | $0.80-1.20 | $1.60-2.40 |
| Your net | ~$1.50 | ~$3 | ~$6 |
| Breakeven | ~8 users | ~4 users | ~2 users |

VPS costs ~$6-12/month. Even 2 Strike users cover the server.

## Middlemen

Same model as V2. Middlemen buy code batches, sell to students for cash, take 20-30%. They don't need to understand the tiers — they just say "basic, fast, or gaming."

Pitch scripts:
- **Eco:** "Snack money. Actually loads YouTube."
- **Stealth:** "Eco works but buffers. This one is FAST."
- **Strike:** "You want to play Minecraft during lunch? This."
