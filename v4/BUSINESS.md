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

## Revenue & Cash Flow

### School Term Billing

NZ school terms run ~10 weeks each (4 terms per year). Instead of monthly billing,
offer **Term Passes** at a slight discount:

| Tier     | Monthly (×3) | Term Pass (10 weeks) | You Save |
|----------|:------------:|:--------------------:|:--------:|
| Eco      | $6           | $5                   | $1       |
| Stealth  | $12          | $10                  | $2       |
| Strike   | $24          | $18                  | $6       |

Benefits:
- **Upfront cash** — collect for the whole term at once, improves cash flow
- **Fewer collections** — middlemen collect 4× per year instead of 12×
- **Holiday-proof** — summer break (Dec–Jan, ~6 weeks) doesn't kill revenue. Term 1 starts fresh in Feb.
- **Less churn** — students stay connected for the full term instead of month-to-month decisions

### Cash Flow Reality

| Metric | Value |
|--------|-------|
| VPS cost | ~$8/month |
| Breakeven (Eco-only) | 8 users |
| Breakeven (mixed) | 2–4 users |
| Realistic first-month users | 10–30 |
| Monthly gross (10 users, mixed) | ~$30–60 |
| Monthly net after middleman cut | ~$20–45 |
| Annual net (10 users) | ~$200–500 |
| Annual net (50 users) | ~$1,000–2,500 |

This is pocket-money scale at a single school. It does not need to be more. The
product works, the costs are covered, and the upside is a few thousand dollars a
year — not a startup.

### The Real Ceiling

Pricing is constrained by what high school students can pay. Most will pick Eco ($2)
or Stealth ($4). Strike ($8) is a tougher sell — only serious gamers. A realistic
mix at a single school:

| Tier     | % of Users | Monthly per User | Monthly Gross (50 users) |
|----------|:----------:|:----------------:|:------------------------:|
| Eco      | 40%        | $2               | $40                      |
| Stealth  | 40%        | $4               | $80                      |
| Strike   | 20%        | $8               | $80                      |
| **Total** |           |                  | **$200**                 |

After middleman cuts (~25%): ~$150/month net. Enough to cover the VPS, buy a
coffee, and have a tangible side project. Growth beyond one school requires
more middlemen, more support, and eventually another VPS — margins shrink as
you scale, but the product doesn't need to scale to be successful.

## Middleman Operations

Same model as V2/V3:
1. You generate code batches in PocketBase admin
2. Print codes on cards with download link
3. Middlemen buy batches (cash/Bank transfer)
4. Middlemen sell to students — take 20-30% commission
5. Middlemen have NO app access or admin panel

**New in V4:** The `middleman` field in the `codes` collection lets you track which middleman sold which code. Simple reporting: `?filter=(middleman='Alice')`.

## Growth Channels

### Referral Cards

Every code card includes a referral line:
> *"Give this card to a friend — they get 50% off month 1, you get a free week."*

Middlemen hand these out for free. It's the only scalable acquisition channel
in a cash-based school model:
- No ad spend
- No social media marketing
- Works entirely through existing social networks
- Each new user becomes a distributor

Implementation: The `codes` collection has a `referred_by` field. On activation,
if the new code's `referred_by` matches an active code, both parties get a
credit tracked in PocketBase (easy to audit).

### Tier Bundling (Strike + Free Eco)

A Strike user at $8/month gets a free Eco code they can give to a friend:
- Gets more students onto the platform ("everyone uses it" social proof)
- The free Eco user sees what they're missing → potential Stealth/Strike upgrade
- Zero marginal cost — Eco uses the same VPS and the bandwidth cap (5 Mbps tc)
  ensures no single free user can saturate the link
- Creates network effects inside the school
- The free code is `referred_by: <strike_user_code>` for tracking

### Middleman as a Channel

Since middlemen are friends (not anonymous resellers), the trust model is
stronger than typical middleman operations:
- No escrow needed — friends pay when they sell
- Direct feedback loop — friends hear what students actually complain about
- Easy to recruit — if a friend is already selling Eco, offering them
  Stealth/Strike is an upsell conversation, not a cold pitch
- Low fraud risk — friends don't disappear with cash

## Competitive Advantage

| Free VPNs | MyVPN V4 |
|-----------|----------|
| Use TLS → JA3 fingerprinted | Opaque encrypted stream → unclassifiable |
| Use UDP (WireGuard, QUIC) → blocked | TCP only (passes N4L) |
| IPs on public blocklists | Fresh VPS IP, unknown to blocklists |
| Slow on congested WiFi (TCP CUBIC) | Brutal CC on Stealth fights for bandwidth |
| Oversold servers | 20 users on a dedicated VPS |
| Crash on bad update | Two-phase rollback — auto-reverts |

## Risk Factors & Mitigations

### 1. Protocol Blocking (Highest Impact)

| Risk | Likelihood | Impact | Mitigation |
|------|:----------:|:------:|------------|
| N4L fingerprints Shadowsocks TCP | Low now, rising over time | Critical — product stops working | v2ray-plugin fallback port (WebSocket disguise). Heartbeat-delivered protocol switching so clients adapt without updates. |

Shadowsocks over TCP works today because N4L's Palo Alto filters target TLS
(JA3 fingerprinting) and UDP protocols. Shadowsocks is neither. But no
obfuscation is permanent — N4L can update their filters.

**Business continuity plan:** The heartbeat response already returns a `config`
object. Extend it to return `protocol` (e.g., `"shadowsocks"`, `"v2ray"`,
`"trojan"`). When N4L blocks the current protocol:
1. Deploy a new protocol on a new port
2. Update the `tier_configs` record in PocketBase
3. All active clients pick up the new config on their next heartbeat (within 5 min)
4. Old protocol gets deprecated with a 7-day grace period

No app update needed. No friction for users. The product survives the filter
change.

### 2. Brutal CC Module Failure

| Risk | Likelihood | Impact | Mitigation |
|------|:----------:|:------:|------------|
| DKMS fails after kernel update | Medium | Medium — Stealth silently downgrades to BBR | Server-side heartbeat check verifies Brutal module is loaded. Logged and alerted. If down for >24h, notify middlemen. |

### 3. School Administration Action

| Risk | Likelihood | Impact | Mitigation |
|------|:----------:|:------:|------------|
| School IT blocks VPS IP | Low-Medium | High — all users lose access | Have a backup VPS ready on a different provider with a different IP range. Failover means updating DNS and tier_configs. |
| School confiscates code cards | Low | Low — generate new codes | Codes are just strings in PocketBase. Re-print. |
| School threatens legal action | Very Low | High — project ends | Shut down quietly. No user data stored beyond what's needed for activation. No logs of browsing history. Same legal standing as any VPN service. |

### 4. Revenue Concentration

If 80% of revenue comes from 2–3 middlemen (friends), losing one friend
(e.g., they graduate, move schools, get caught) cuts revenue by 30–40%.
Mitigation: always have 3+ middlemen, and keep the referral pipeline running
so new middlemen replace ones who leave.

## Pitch Scripts for Middlemen

- **Eco:** "Snack money. Actually loads YouTube at lunch."
- **Stealth:** "Eco works but buffers. This one is FAST. For streaming, it's the one."
- **Strike:** "You want to play Minecraft/Fortnite/Valorant during school? This. Nothing else works on the school WiFi."

---

## Why This Business Model Works

1. **Friends as middlemen.** The trust gap that kills most cash-based distribution
   doesn't exist here. You know who you're dealing with. Collections are reliable.
   Feedback is direct. Problems get communicated fast.

2. **Zero fixed costs beyond the VPS.** No payment processor fees. No advertising.
   No office. No salaries. Every dollar of revenue above ~$8/month is profit.

3. **The product has a real moat for its market.** Free VPNs don't work on N4L
   (UDP blocked, TLS fingerprinted). School WiFi is slow. Students who want to
   game or stream during school have no other working option. You're not competing
   on price — you're competing on *actually working*.

4. **Low exit cost.** If it doesn't work, you stop. The VPS bill ends. No
   inventory. No debt. No legal entanglements.

5. **It's a side project, not a startup.** This distinction matters. A side
   project that makes $200/month and covers its costs is a success. It doesn't
   need to grow 10×. It doesn't need investors. It just needs to work for the
   people using it.
