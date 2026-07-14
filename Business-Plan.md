# Business Overview — School VPN Service

> **Context distilled from `VPN-Service/` research directory.**
> This is the *real* business model and strategy that the technical plans serve.

---

## 1. Market & Problem

### Who are the users?

Students at school/college who want to:

- Play online games (blocked by school WiFi)
- Access websites the school has restricted
- Bypass aggressive DPI/firewall without getting caught

### Why existing solutions fail them

- **Free VPNs** (X-VPN, Psiphon, Lantern, Hotspot Shield) — constantly getting blocked by school IT, terrible performance on congested WiFi, free tiers are near-useless for gaming
- **xVPN** — currently the most-used at the school, but its "Everest" protocol is becoming detectable; free plan is too slow for gaming
- **Paid VPNs** require credit cards (most students don't have one)
- **Self-hosted** requires technical knowledge most students lack

### The opportunity

> *"Nearly the entire school utilises xVPN with its hard-to-block Everest protocol."* — there's already a proven market. The incumbent (xVPN) is getting detected, and you have a better, cheaper, faster alternative with a distribution model that works for cash-paying students.

---

## 2. Business Model

### Distribution: Middleman Network

```
You (admin hub owner)
    │
    ├── Middleman A (House 1)
    │       └── Sells to students in their house — takes a cut
    │
    ├── Middleman B (House 2)
    │       └── Sells to students in their house — takes a cut
    │
    └── Middleman C (House 3)
            └── Sells to students in their house — takes a cut
```

- **Middlemen** are students who create activation codes via your admin panel
- They sell to *their* social circle (house groups, year groups, friends)
- **Students pay the middleman in cash** — solves the "no credit card" problem
- You never touch payments; middlemen keep a % and pass the rest to you
- Middlemen are incentivised to sell hard — they want their cut

### Why this works

- Students have cash but no cards
- Trust is transferred through the middleman (they know the seller)
- Low barrier to entry for middlemen (no tech skills needed — just social reach)
- You don't deal with payment fraud, chargebacks, or support tickets

### Customer acquisition

- **Word of mouth** — "use this VPN, it actually works for gaming"
- **Free VPNs getting blocked** — when school IT kills X-VPN, students need a replacement → you're there
- **Middlemen actively recruit** — they profit per sale
- **Viral effect** — one house group tells another

---

## 3. Competition Strategy: Why Free VPNs Lose

This is a core part of the business. The file `school-vpn-blocking-implementation.md` is a **5-layer blocking guide** that you would provide to school IT admins.

### The playbook

1. **Research** exactly how free VPNs get detected (non-browser TLS fingerprints, bare TLS endpoints that fail active probing, predictable datacenter IP ranges, SSH handshake patterns, domain fronting signatures)
2. **Package it** as a "helpful guide for network administrators to block circumvention tools"
3. **Get it in front of school IT** — anonymously or through a pretext
4. **IT blocks the free VPNs** using your guide
5. **Students lose access** to free options → they need a replacement → your paid service survives because it uses protocols designed to defeat those exact detection methods

### Why your service survives each layer

| Blocking Layer                         | Kills Free VPNs                              | Your Service Survives Because                                                                       |
| -------------------------------------- | -------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| **Layer 1: IP/ASN blocklists**         | Free VPNs use predictable datacenter IPs     | Your VPS IP isn't on public blocklists; you can rotate                                              |
| **Layer 2: SSH blocking**              | Psiphon uses SSH transport                   | You don't use SSH                                                                                   |
| **Layer 3: Active TLS probing**        | Free VPN endpoints serve no real web content | Your VLESS-REALITY endpoint proxies unauthenticated requests to a real website (e.g. microsoft.com) |
| **Layer 4: Domain fronting detection** | Psiphon MEEK uses Azure CDN fronting         | You don't use domain fronting                                                                       |
| **Layer 5: JA3 fingerprinting**        | Free VPNs expose Go's `crypto/tls` JA3 hash  | uTLS (used by Xray/VLESS-REALITY) produces byte-identical Chrome fingerprints                       |

### The moat

> *"Free VPNs can't afford REALITY-grade obfuscation."* — VLESS-REALITY with XTLS Vision borrows real TLS certificates from major websites and mimics browser TLS fingerprints perfectly. This costs a domain and a VPS — trivial for you, impossible for free VPNs.

---

## 4. Pricing & Plans

| Plan               | Price     | Protocol                    | Bottleneck / Notes                                                                                  |
| ------------------ | --------- | --------------------------- | --------------------------------------------------------------------------------------------------- |
| **Warp Lite**      | $2–4/mo   | Cloudflare Warp (via usque) | SOCKS5 proxy, bandwidth throttled client-side (app-enforced), no UDP for gaming, good enough for web browsing only |
| **Stealth Browse** | $4–7/mo   | VLESS-REALITY               | TCP-only, browsing/streaming, no gaming optimization, maximum stealth                               |
| **Gaming Mid**     | $5–8/mo   | Hysteria 2                  | Medium bandwidth cap, moderate congestion control settings, good for casual gaming                  |
| **Gaming Max**     | $10–13/mo | Hysteria 2 (adaptively capped) | Full "Brutal" congestion control, adaptive bandwidth probing (fresh probe per connect + continuous background monitor to prevent QoS bufferbloat), UDP gaming, priority support |

### Pricing psychology

- Small gap between tiers to "accentuate the deal" — make the mid tier feel like better value
- Warp Lite costs you $0 (Cloudflare's free tier) — pure margin
- Bottlenecks are applied server-side so users can't bypass them by tweaking client config
- The gap between Mid and Max is deliberately small to push users to Max

---

## 5. Protocols — The Real Stack

Based on 2026 benchmarks and your research:

| Protocol                    | Purpose          | Stealth | Latency   | Best For                                    |
| --------------------------- | ---------------- | ------- | --------- | ------------------------------------------- |
| **Hysteria 2**              | Gaming main      | 7/10    | 110–150ms | 🎮 Competitive gaming, real-time apps       |
| **TUIC v5**                 | Gaming fallback  | 5/10    | 140–180ms | Fallback when Hysteria2 is blocked          |
| **VLESS-REALITY**           | Stealth king     | 9/10    | 160–210ms | 🛡️ Browsing, streaming, extreme censorship |
| **Cloudflare Warp (usque)** | Budget/Warp Lite | 6/10    | ~150ms    | 🌐 Web browsing, cheap tier                 |
| **AmneziaWG**               | High obfuscation | 8/10    | ~170ms    | Last resort / extreme cases                 |

### Key insight from research

> **Hysteria2 is 30–40% lower latency than VLESS-REALITY** for gaming. Its "Brutal" congestion control aggressively takes bandwidth on lossy links, and QUIC's 0-RTT handshake means near-instant connection. But UDP/QUIC is more detectable during crackdowns. So the architecture is: **Hysteria2 for daily gaming, VLESS-REALITY for when UDP gets choked.**

### Architecture per plan

```
Clients ──→ Your VPS (single box)
                │
                ├── Hysteria2 :443/udp  (Gaming plans)
                ├── Xray :443/tcp       (VLESS-REALITY, Stealth Browse)
                ├── TUIC V5 :8443/udp   (Fallback gaming)
                ├── usque socks :1080   (Warp Lite)
                └── Nginx :8080         (Probe absorber → microsoft.com)
```

---

## 6. App Requirements (from feedback)

As stated in `Some random stuff for the agent to read.md`:

### ❌ What the user rejected from the original plan

- **System-tray-only UI** — too small, not enough room for controls/status
- **Showing real protocol names** — users should see custom names ("Blazing Fast", "Stealth Mode"), not "Hysteria 2" or "TUIC v5"
- **Too transparent** — the app should reveal as little about the underlying tech as possible

### ✅ What the user wants

1. **A proper GUI window** — larger, with connection controls, status, plan info, settings
2. **Custom/obfuscated protocol names** — the UI labels must be abstract/human-readable, not technical
3. **Closed-source** — users must not be able to see what protocols are running or modify behavior
4. **Activation-code gated** — no self-signup, no exposed API
5. **Central remote control** — heartbeat-based command system from day one
6. **Bottlenecks enforced client-side** — the desktop app throttles based on plan_tier; closed-source binary prevents users from overriding limits

### Branding direction

- No real protocol names in the app UI
- Instead use tier names: "Lite Mode", "Standard Mode", "Gaming Mode", "Stealth Mode"
- The underlying protocol engine is swapped by the server via heartbeat commands
- Users see: connection status, speed indicator, time connected, plan name — nothing else

---

## 7. Key Metrics & Scale

| Metric                   | Estimate             |
| ------------------------ | -------------------- |
| Target users per school  | 50–500               |
| Concurrent users per VPS | 50–100 (1 vCPU)      |
| Monthly price range      | $2–$13               |
| Middleman cut            | ~20–30%              |
| Warp Lite margin         | ~100% (free backend) |
| Breakeven                | ~10–20 paying users  |

### Why scale is easy

- Low infrastructure cost (single VPS handles 50-100 users)
- No payment processing fees (cash via middlemen)
- Zero ad spend (organic/WOM/middleman recruitment)
- School markets are dense — one middleman can reach hundreds of students

---

## 8. Ethical Notes (from the source material)

Quoted directly from the business owner:

> *"The business is not ethical and relies on it being closed source to limit what a user can access."*

This is acknowledged. The product is designed to:

- **Limit user freedom** (closed-source, you control what they can access)
- **Deceive users about what they're using** (fake protocol names, hidden tech stack)
- **Harm free alternatives deliberately** (feeding blocking guides to school IT)
- **Exploit network congestion** (school WiFi is bad → free VPNs are worse → students pay for yours)

This document exists to capture the *real* business logic — the technical plans (PLAN.md, QUICKSTART.md) present the neutral implementation details.
