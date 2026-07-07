# Gaming-Optimized Protocol Stack: Speed + Censorship Bypass

> Research compiled June 2026 — incorporating the 2026 Anti-Censorship Protocol Benchmarks

---

## The Short Answer

For competitive gaming, **Hysteria2** is your primary protocol. Full stop. It has the lowest latency of any censorship-resistant protocol tested (110–150ms vs VLESS-REALITY's 160–210ms), uses QUIC (UDP-native, no TCP-over-TCP penalty for game packets), and its "Brutal" congestion control aggressively takes bandwidth when you need it.

But it has one weakness: UDP/QUIC is more detectable during crackdowns. So you run a **fallback stack** — Hysteria2 for daily gaming, VLESS-REALITY for when UDP gets choked.

With your resources (domain + Cloudflare + VPS), you can build something that a free VPN couldn't dream of.

---

## 2026 Benchmark Results (Feb 2026)

| Protocol | Latency | Stealth (1-10) | Transport | Best For |
|---|---|---|---|---|
| **Hysteria2** | **110–150 ms** | 7/10 | QUIC (UDP) | 🎮 Gaming, real-time apps |
| **TUIC v5** | 140–180 ms | 5/10 | QUIC (UDP) | Fallback gaming |
| **VLESS-REALITY** | 160–210 ms | **9/10** | TCP (TLS) | 🛡️ Stealth, streaming, browsing |
| Shadowsocks 2022 | 150–200 ms | 4/10 | TCP | Last resort |
| Cloak+SS | 140–190 ms | 6/10 | TCP+TLS | Niche |

**Key takeaway:** Hysteria2 is 30–40% lower latency than VLESS-REALITY. For competitive gaming that's the difference between a headshot and a death screen.

---

## Your Toolkit: What You Have & How to Use It

| Resource | How it plays into gaming |
|---|---|
| **Cloudflare + Domain** | DNS management for fast IP rotation. CDN-proxied website on the same server (helps REALITY fallback blend in). **Optional:** Spectrum (paid) for UDP proxying. |
| **VPS** | Everything in one box. Terminates all VPN protocols, NATs directly to the internet, runs nginx for REALITY fallback, hosts the management panel and activation API. A single 1 vCPU box handles 50-100 concurrent clients. |

---

## The Gaming-Optimized Architecture

### Setup Overview

```
┌──────────────┐     Cloudflare      ┌──────────────────────────────────────────────┐
│   Gamer      │ ─────────────────── │               VPS (Single Box)               │
│   Client     │  (DNS / CDN)        │  ┌─ Hysteria2 :443/udp (Salamander, Brutal)  │
│              │                     │  ├─ Xray :443/tcp (VLESS-REALITY, uTLS)      │
└──────────────┘                     │  ├─ TUIC V5 :8443/udp (cubic, no UDP relay)  │
                                     │  ├─ AmneziaWG :9443/udp (high obfuscation)   │
                                     │  ├─ Nginx :8080 (probe → microsoft.com)      │
                                     │  └─ iptables MASQUERADE ──→ INTERNET         │
                                     │                                              │
                                     │  All protocols terminate on the VPS.         │
                                     │  All traffic NATs to internet from VPS IP.   │
                                     └──────────────────────────────────────────────┘
```

The VPS does everything — terminates all four protocol backends, absorbs active probes via nginx, and NATs all client traffic directly to the internet. No separate enterprise server, no WireGuard tunnel, no home-upload bottleneck. A single 1 vCPU VPS handles 50-100 concurrent gamers.

---

## Protocol #1: Hysteria2 — Your Gaming Main

### Why It Wins for Gaming

| Factor | How Hysteria2 handles it |
|---|---|
| **Latency** | 110–150ms in benchmarks. QUIC's 0-RTT handshake means you're connected before you can blink. |
| **Packet loss** | The custom "Brutal" congestion control ignores individual packet loss — it just keeps sending. On a 5–10% lossy link (mobile, congested networks), it's 3–5× faster than any TCP-based protocol. |
| **UDP-native** | Games send UDP. Hysteria2 sends QUIC (over UDP). No clumsy TCP-encapsulating-UDP overhead that adds latency spikes. |
| **Connection migration** | Your IP changes mid-game (WiFi→cellular, NAT re-binding)? Hysteria2 survives it. OpenVPN and most TCP tunnels drop. |
| **DPI evasion** | Salamander obfuscation transforms QUIC Initial packets into random bytes. HTTP/3 masquerade makes it look like normal QUIC traffic. |
| **Port flexibility** | Can hop ports on the fly. Run on 443 (blends with HTTPS), 53 (DNS masquerade), or any random high port. |

### The Catch (Solve It with Your Resources)

> "Hysteria2 is the fastest of the four when it works. It is also the one most likely to be blocked by accident on a network that does not actively want to block VPNs but happens to be hostile to UDP."
> — VectraVPN operator, 2026

**The issue:** Some networks block UDP entirely. Some mobile carriers choke QUIC. During crackdowns, DPI systems flag anomalous QUIC flows.

**Your solution:** You run a multi-protocol stack on a single VPS — Hysteria2 as primary and VLESS-REALITY as automatic fallback. Not limited to one protocol.

### Optimal Hysteria2 Config for Gaming

```json
{
  "server": {
    "listen": ":443",
    "auth": {
      "type": "password",
      "password": "YOUR_COMPLEX_AUTH_SECRET"
    },
    "tls": {
      "enabled": true,
      "cert": "/etc/letsencrypt/live/yourdomain.com/fullchain.pem",
      "key": "/etc/letsencrypt/live/yourdomain.com/privkey.pem"
    },
    "obfuscation": {
      "type": "salamander",
      "password": "OBFUSCATION_SECRET"
    },
    "masquerade": {
      "type": "proxy",
      "proxy": {
        "url": "https://www.microsoft.com",
        "rewriteHost": true
      }
    },
    "quic": {
      "initStreamReceiveWindow": 8388608,
      "maxStreamReceiveWindow": 8388608,
      "initConnReceiveWindow": 20971520,
      "maxConnReceiveWindow": 20971520,
      "maxIdleTimeout": "30s",
      "disablePathMTUDiscovery": false
    }
  }
}
```

**Key settings for gaming:**
- **`salamander` obfuscation** — defeats DPI that looks for QUIC patterns
- **`masquerade` proxy** — if probed, forwards to real Microsoft (same trick as REALITY for active probing)
- **Large receive windows** — prevents the game from stalling on congestion
- **Port 443** — blends with HTTPS traffic; DPI can't easily block 443 without breaking the web
- **Path MTU discovery on** — avoids fragmentation-induced latency spikes

---

## Protocol #2: VLESS-REALITY + XTLS Vision — The Stealth Fallback

When UDP gets choked (some mobile carriers, enterprise firewalls, crackdown situations), your gamers fall back to this.

| Factor | How it handles gaming |
|---|---|
| **Latency** | 160–210ms (higher than Hysteria2, but consistent) |
| **Stealth** | 9/10 — best in class. Active probing passes because REALITY forwards to real Microsoft/Cloudflare. |
| **XTLS Vision** | Eliminates the double-encryption entropy pattern. Single-layer TLS look. |

This is your backup for when UDP is blocked. It's not as fast as Hysteria2 for gaming, but it's uncensorable — and it works where nothing else does.

### Key Config for Gaming Fallback

```json
{
  "inbounds": [{
    "port": 443,
    "protocol": "vless",
    "settings": {
      "clients": [{
        "id": "YOUR-UUID",
        "flow": "xtls-rprx-vision"
      }],
      "decryption": "none",
      "fallbacks": [{
        "dest": 8080
      }]
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "dest": "www.microsoft.com:443",
        "serverNames": ["www.microsoft.com"],
        "privateKey": "YOUR-PRIVATE-KEY",
        "shortIds": ["YOUR-SHORT-ID"]
      }
    }
  }]
}
```

The nginx fallback on port 8080 serves a real-looking website. Active probes from DPI get Microsoft.com. Only authenticated REALITY clients get the proxy.

---

## Protocol #3: TUIC v5 — The Other QUIC Option

TUIC v5 sits between Hysteria2 and VLESS-REALITY in both latency and stealth:

| Metric | Value |
|---|---|
| Latency | 140–180ms |
| Stealth | 5/10 (lowest) |
| 0-RTT handshake | Yes |

**When to use:** If Hysteria2 gets blocked but you still want QUIC performance. TUIC is simpler, lighter, and has a cleaner codebase. But it's more vulnerable to SNI-based QUIC filtering (academic papers in 2025 showed the GFW can extract SNI from TUIC's initial QUIC packets).

Run it on port 443 with TLS. Accept that it'll be the first protocol to fall during crackdowns.

---

## Resource Strategy: Cloudflare + VPS + Enterprise Server

### How the Pieces Fit Together

```
                    Cloudflare DNS (proxied)
                           │
              ┌────────────┴────────────┐
              │                         │
        yourdomain.com            gaming.yourdomain.com
              │                         │
         CDN proxy (TCP)          Spectrum* (UDP)
              │                         │
         ┌────┴────┐              ┌─────┴─────┐
         │  VPS    │              │ Enterprise  │
         │ (nginx) │              │   Server    │
         │  TLS    │              │  Hysteria2  │
         │  front  │              │  REALITY    │
         │         │              │  TUIC       │
         └─────────┘              └─────────────┘
```

### 1. Cloudflare DNS Management

```
yourdomain.com        → CDN proxied (orange cloud) → VPS (TCP 443)
gaming.yourdomain.com → CDN proxied → VPS (Hysteria2 front)
api.yourdomain.com    → CDN proxied → VPS (REALITY signaling)
```

- **Proxied DNS (orange cloud)** hides your VPS IP from censors.
- **No Spectrum needed for TCP** — Cloudflare CDN proxying covers TCP 443 for free.
- **Spectrum needed for UDP** — Only if you want Cloudflare to proxy your Hysteria2 QUIC traffic. Without Spectrum, Hysteria2 gamers connect directly to the VPS IP (you can still hide it via DNS-only records + IP rotation).

### 2. VPS as Edge Node (The "Sacrifice" Layer)

The VPS is what censors see. It serves:
- A legitimate-looking website (WordPress, landing page, or static site)
- REALITY fallback (nginx proxies probes to Microsoft.com)
- Connection forwarding to the enterprise server

**Why this matters for gaming:** The VPS absorbs all the probing and reputation damage. Your enterprise server stays off the radar. If the VPS IP gets blocked, you spin up a new VPS and update DNS — your enterprise server's config doesn't change.

### 3. Enterprise Server as the Engine

The enterprise server runs:
- **Hysteria2** on gaming ports — handles the QUIC encryption + Brutal congestion control at high packet rates (game traffic is small packets, high frequency — CPU matters)
- **VLESS-REALITY** as TCP fallback
- **TUIC v5** as secondary QUIC option

Hysteria2 is CPU-intensive (QUIC encryption in userspace). Your enterprise server's high per-core performance is exactly what's needed.

---

## Client-Side: Auto-Fallback for Gamers

The real magic is client-side auto-switching. Use a client that supports multiple protocols with fallback thresholds:

```
Priority 1: Hysteria2 (UDP, port 443)
    ├── Healthy? → Route game traffic here
    └── Unhealthy? (latency > 200ms or connection fails)
        └── Fallback to Priority 2

Priority 2: VLESS-REALITY + XTLS Vision (TCP, port 443)
    ├── Healthy? → Route game traffic here (slightly higher latency, works anywhere)
    └── Unhealthy?
        └── Fallback to Priority 3

Priority 3: TUIC v5 (UDP, port 443)
    └── Highest latency, but gives another QUIC option if Hysteria2 is blocked
```

**Client apps that support multi-protocol auto-fallback:**
- **Sing-box** (best — supports all three with built-in fallback)
- **Nekoray / Nekobox** (strong multi-protocol)
- **v2rayNG / v2rayN** (with manual switching)
- **Clash Meta / Mihomo** (rule-based protocol selection)

---

## Performance Comparison: Gaming-Specific

Testing from the 2026 benchmarks, scaled to what matters for competitive gaming:

| Scenario | Hysteria2 | VLESS-REALITY | TUIC v5 |
|---|---|---|---|
| **Raw latency (ms)** | 110–150 ✅ | 160–210 | 140–180 |
| **Jitter (variance)** | Low ✅ | Low | Medium |
| **Lossy network (5%)** | 3–5× TCP speed ✅ | Collapses | 2–3× TCP speed |
| **UDP game traffic** | Native ✅ | Wrapped (TCP) | Native ✅ |
| **Connection migration** | Yes ✅ | No | Yes ✅ |
| **Stealth / DPI bypass** | 7/10 | **9/10** ✅ | 5/10 |
| **Survives UDP blocks** | ❌ | **✅ Yes (TCP)** | ❌ |
| **CPU overhead** | Higher (QUIC) | Low | Moderate |

**The winning combo:**
- **Default route:** Hysteria2 (greener ping, UDP-native for games)
- **Auto-fallback:** VLESS-REALITY (when UDP is blocked, still works at 160–210ms)
- **TUIC v5** is skippable — the gap between it and Hysteria2 isn't big enough to justify the complexity unless Hysteria2 specifically gets blocked in your region.

---

## What to Avoid for Gaming

| Protocol | Why it's bad for gaming |
|---|---|
| **WebSocket + TLS (V2Ray WS)** | TCP-over-TCP penalty = 30–50% throughput loss. Latency spikes on any packet loss. |
| **Standard Shadowsocks** | TCP-based. Active probing kills it. No congestion control tuned for real-time traffic. |
| **OpenVPN** | 1194/UDP is instantly recognizable. Heavy overhead. |
| **Standard WireGuard** | 51820/UDP is fingerprintable. No obfuscation layer. Gets DPI'd and blocked fast. |
| **Tor** | High latency (seconds). Not even close to gaming-capable. |
| **Trojan (plain)** | TLS fingerprint is detectable. No active probe defense unless behind a real site. |

---

---

## Adaptive Speed Probing — Handling QoS Bandwidth Fluctuations

> **Critical for Hysteria 2 on school WiFi:** Hysteria 2's "Brutal" congestion control is aggressive by design — it grabs as much bandwidth as it can. On a QoS-shaped network where bandwidth fluctuates second-to-second, an uncapped Brutal connection will:
> 1. Fill the buffer when bandwidth drops → **bufferbloat** → game latency spikes from 100ms → 800ms
> 2. Saturate the WiFi AP → **packet loss** → rubberbanding in games
> 3. Trigger **school IT bandwidth anomaly alerts** → protocol gets blocked faster

### The Three-Layer Adaptive Solution

The client implements a three-layer adaptive bandwidth system built specifically for Hysteria 2's Brutal CC:

```
┌──────────────────────────────────────────────────────────────┐
│  LAYER 1: Connect Probe (runs BEFORE TUN creation)           │
│  ──────── Probe traffic uses the regular network, ────────   │
│  ──────── NOT the tunnel (which doesn't exist yet)  ──────── │
│                                                              │
│  ┌──────────────────────────────────┐                        │
│  │ 1. Baseline RTT to VPS (HEAD  )  │                        │
│  │ 2. Quick 1MB download, time it   │                        │
│  │ 3. If ≥1.5s → staged ramp test:  │                        │
│  │    500KB @ 500KB/s → 1MB/s →     │                        │
│  │    2MB/s → 5MB/s (Gaming Max)    │                        │
│  │ 4. Pick highest cap with:        │                        │
│  │    • <1% packet loss AND         │                        │
│  │    • RTT < 1.5× baseline RTT     │                        │
│  │ 5. Clamp to tier limits:         │                        │
│  │    • Floor:  3 Mbps (min)        │                        │
│  │    • Max:   50 Mbps (Mid)        │                        │
│  │              100 Mbps (Max)      │                        │
│  │ 6. If probe fails → use fallback │                        │
│  │    (40/20/8 Mbps per tier)       │                        │
│  └──────────┬───────────────────────┘                        │
│             │                                                │
│             ▼                                                │
│  ┌──────────────────────────────────────────────────┐        │
│  │  ╔══ CREATE TUN DEVICE + INSTALL ROUTES ═════╗  │        │
│  │  ║  Now the tunnel is ready for engine traffic║  │        │
│  │  ╚════════════════════════════════════════════╝  │        │
│  │  Apply clamped cap to Hysteria 2 config          │        │
│  │  via --speed (bps) and launch engine             │        │
│  └──────────────────┬───────────────────────────────┘        │
│                     │                                        │
│                     ▼                                        │
│  LAYER 2: Background Monitor (every 15s while connected)     │
│                                                              │
│  ┌──────────────────────────────────┐                        │
│  │ Download 100KB chunk, measure    │                        │
│  │ throughput + RTT                 │                        │
│  │                                  │                        │
│  │ EWMA smoothing (α=0.3) to avoid  │                        │
│  │ reacting to transient spikes     │                        │
│  │                                  │                        │
│  │ N consecutive low samples →      │                        │
│  │ REDUCE cap by 30% (fast sink)    │                        │
│  │                                  │                        │
│  │ N consecutive high samples →     │                        │
│  │ INCREASE cap by 20% (slow rise)  │                        │
│  │                                  │                        │
│  │ RTT spike > 2× baseline →        │                        │
│  │ CUT cap by 50% (bufferbloat!)    │                        │
│  └──────────┬───────────────────────┘                        │
│             │                                                │
│             ▼                                                │
│  LAYER 3: Runtime Cap Injection                              │
│                                                              │
│  ┌──────────────────────────────────┐                        │
│  │ Monitor calls onCapChange() →    │                        │
│  │ writes new speed to Hysteria 2   │                        │
│  │ config JSON → SIGHUP reload      │                        │
│  │ UDP control socket update        │                        │
│  └──────────────────────────────────┘                        │
└──────────────────────────────────────────────────────────────┘
```

### Why This Matters for Gaming

| Scenario | Without Adaptive Probe | With Adaptive Probe |
|---|---|---|
| **QoS kicks in during peak class time** | Brutal CC fills buffer → 800ms latency | Monitor detects RTT spike → cuts cap → stable 120ms |
| **Bandwidth frees up after lunch** | Stuck at old low cap → underutilized link | Monitor sees headroom → increases cap → full speed |
| **Quick connect from dorm** | 30-min cache from congested hallway → slow start | Fresh probe every connect → accurate cap immediately |
| **School IT rate-limits your MAC** | Keeps trying at high speed → gets blocked | Drops to floor (3 Mbps) → avoids detection |
| **Probe fails / VPS speedtest down** | Connection fails or uses stale data | Falls back to safe tier default (40/20/8 Mbps) → monitor refines from there |
| **VPS uplink is the bottleneck** | Probe says 150 Mbps → engine pushes VPS too hard | Hard cap at tier ceiling (100/50/8 Mbps) protects VPS uplink |
| **Bandwidth drops below 500 KB/s** | Engine starves or behaves erratically | Floor of 3 Mbps (375 KB/s) prevents total starvation |

### Tier-Specific Behavior

| Plan | Probe Depth | Cap Floor | Cap Ceiling | Dynamic Monitor | Notes |
|---|---|---|---|---|---|
| **Gaming Max** ($10-13/mo) | Full: quick test + staged ramp up to 100 Mbps | **3 Mbps** (never starve) | **100 Mbps** (VPS uplink limit) | Yes — adjusts up/down in real-time | Uncapped if fast, else dynamically limited & clamped. Probe failure → fallback to 40 Mbps |
| **Gaming Mid** ($5-8/mo) | Quick test + capped ramp to 50 Mbps | **3 Mbps** (never starve) | **50 Mbps** (tier limit) | Yes — but ceiling at 50 Mbps | Hard ceiling even if network could go faster. Probe failure → fallback to 20 Mbps |
| **Warp Lite** ($2-4/mo) | Skipped entirely | — | **8 Mbps** (hardcoded) | No — uses hardcoded 1 MB/s cap | usque doesn't need dynamic adjustment |

### Key Metrics to Monitor

The heartbeat telemetry includes:
- `probe_result_kbps` — bandwidth cap from the initial probe
- `monitor_avg_kbps` — EWMA average during session
- `monitor_adjustments` — number of cap changes during session (indicator of QoS volatility)
- `bufferbloat_events` — how many RTT spikes triggered cap cuts

This data helps you tune the EWMA α, reduce/increase thresholds, and identify schools with aggressive QoS.

---

## Summary: Your Gaming Protocol Stack

```
┌─────────────────────────────────────────────────────────────┐
│                    GAMING PROTOCOL STACK                     │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  🥇 Hysteria2 (Primary)                                     │
│     Port: 443 (UDP/QUIC)                                    │
│     Obfuscation: Salamander                                 │
│     Masquerade: www.microsoft.com                           │
│     Congestion: Brutal                                      │
│     Latency: 110–150ms                                      │
│     Auto-fallback: → VLESS-REALITY if >200ms or fails       │
│                                                             │
│  🥈 VLESS-REALITY + XTLS Vision (Fallback)                  │
│     Port: 443 (TCP/TLS)                                     │
│     Security: reality (uTLS Chrome 120 fingerprint)         │
│     Fallback: nginx → www.microsoft.com                     │
│     Latency: 160–210ms                                      │
│     Activates: when UDP is blocked / during crackdowns      │
│                                                             │
│  Infrastructure:                                            │
│     Cloudflare → DNS (proxied), CDN (for fallback site)     │
│     VPS → Protocol termination, NAT egress, nginx fallback  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

Hysteria2 is your main. It's the fastest thing that still beats censorship. VLESS-REALITY is your insurance policy for when networks get hostile. The VPS handles everything — protocol termination, NAT egress, probe absorption, and the management panel. Go-based Hysteria2 and TUIC are incredibly efficient — a single 1 vCPU $5 VPS comfortably handles 50-100 concurrent clients with room to spare.

The free VPNs (Xvpn, Psiphon, Lantern) can't offer any of this — they don't run Salamander-obfuscated QUIC, they don't do REALITY active-probe defense, and they don't have a dedicated single-box architecture. That's your moat.
