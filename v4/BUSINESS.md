# Business Model & Operations (V4)

Same tier structure as V3 with updated technical differentiators.

## The Three Tiers

| Tier | Price | Port | Protocol | Server CC | Bandwidth | UDP | Customer Experience |
|------|-------|:----:|----------|:---------:|:---------:|:---:|---------------------|
| **Eco** | $2 | 8443 | Shadowsocks TCP | BBR | 5 Mbps (tc capped) | ❌ | Works, but slow. Buffers on video. Text/IM fine. |
| **Stealth** | $4 | 8444 | Shadowsocks TCP | **Brutal** | 48 Mbps (Brutal target) | ❌ | Fast streaming. Brutal CC floods the pipe — jitter makes gaming unplayable. |
| **Strike** | $8 | 8445 | Shadowsocks TCP+UDP | BBR | 200 Mbps (tc capped) | ✅ | Low latency gaming (33-44ms). 4K streaming. Soft cap prevents single-user saturation. |

### Why the Tiers Work Together

- **Eco exists to sell Stealth.** At $2 vs $4, Stealth feels like a no-brainer. Eco is deliberately slow (5 Mbps tc cap) — usable but frustrating.
- **Stealth's Brutal CC creates natural tier separation.** The aggressive bandwidth filling gives excellent streaming speeds, but the jitter makes gaming frustrating. A Stealth user who wants to game MUST upgrade to Strike.
- **Strike's UDP + low-latency path is the real product.** Gaming on school WiFi that blocks everything else. A 200 Mbps soft cap (server tc) keeps things fair — way above any game's needs (Valorant ~5 Mbps, Fortnite ~20 Mbps), but prevents one user from saturating the link.

### What the Customer Actually Sees

The app interface shows only:
```
Eco:     grey ○ "Eco · Basic"
Stealth: purple ◉ "Stealth · Stream"
Strike:  gold ⚡ "Strike · Gaming"
```

No protocol names (Shadowsocks, Brutal, BBR). No port numbers. No technical details.
The customer sees plan names and connection status — nothing else.

## Pricing

| | Eco | Stealth | Strike |
|---|-----|---------|--------|
| Monthly | $2 | $4 | $8 |
| Middleman cut (20-30%) | $0.40-0.60 | $0.80-1.20 | $1.60-2.40 |
| Your net | ~$1.50 | ~$3 | ~$6 |
| Breakeven | ~8 users | ~4 users | ~2 users |

VPS costs ~$6-12/month. Even 2 Strike users cover the server.

## Middleman Operations

Same model as V2/V3:
1. You generate code batches in PocketBase admin
2. Print codes on cards with download link
3. Middlemen buy batches (cash/Bank transfer)
4. Middlemen sell to students — take 20-30% commission
5. Middlemen have NO app access or admin panel

**New in V4:** The `middleman` field in the `codes` collection lets you track which middleman sold which code. Simple reporting: `?filter=(middleman='Alice')`.

## Competitive Advantage

| Free VPNs | MyVPN V4 |
|-----------|----------|
| Use TLS → JA3 fingerprinted | Opaque encrypted stream → unclassifiable |
| Use UDP (WireGuard, QUIC) → blocked | TCP only (passes N4L) |
| IPs on public blocklists | Fresh VPS IP, unknown to blocklists |
| Slow on congested WiFi (TCP CUBIC) | Brutal CC on Stealth fights for bandwidth |
| Oversold servers | 20 users on a dedicated VPS |
| Crash on bad update | Two-phase rollback — auto-reverts |

## Pitch Scripts for Middlemen

- **Eco:** "Snack money. Actually loads YouTube at lunch."
- **Stealth:** "Eco works but buffers. This one is FAST. For streaming, it's the one."
- **Strike:** "You want to play Minecraft/Fortnite/Valorant during school? This. Nothing else works on the school WiFi."
