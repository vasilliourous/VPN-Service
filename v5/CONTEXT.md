# MyVPN — Agent Context

> **Purpose:** Single reference for future AI agents (and developers) to understand
> the full context of this project without searching across the entire repository.
>
> **What this IS:** Network environment analysis, protocol testing results,
> architectural reasoning, and operational context that doesn't belong in
> the codebase docs or architecture guide.
>
> **What this is NOT:** A build guide, deployment manual, or API reference.
> See `docs/` for practical implementation instructions.

---

## 1. Network Environment — N4L School Network

### Properties

| Property | Value |
|----------|-------|
| School | Macleans College, NZ |
| Managed by | N4L (Network for Learning) |
| Firewall | Palo Alto (SSL decryption capability, App-ID, URL filtering) |
| Client IP range | 10.47.0.0/19 |
| External IP | 202.150.96.64 |
| DNS | 172.16.0.1, 172.16.0.2 |
| Device model | BYOD — every student owns their own device (has admin rights) |

### Known Blocking Behaviour

| Traffic type | Status | Mechanism |
|-------------|--------|-----------|
| TCP 22 (SSH) | ✅ Fully open (any IP) | — |
| TCP 80/443 | ✅ To whitelisted destinations only | URL/category filtering |
| TCP >1024 to unknown IPs | ❌ Blocked | "Category of unknown" → block |
| **All non-DNS UDP** | **❌ Dropped** | Blanket block — all UDP except port 53 |
| DNS (UDP 53) | ✅ Only with valid DNS query format | DPI-inspected |
| ICMP (ping) | ✅ Open | — |
| Cloudflare IPs | ✅ Generally whitelisted | Categorised as "CDN" |

### Key Insight: Dynamic Filtering

N4L does NOT apply a flat permanent block. Filtering tightens or loosens based on
network load (likely automated App-ID updates or load-based policy enforcement):

- **Bad day:** 4.23/5.86 Mbps baseline, 1.5% packet loss. WARP TLS fails, QUIC fails.
  Only Shadowsocks TCP works.
- **Good day:** 83.6/185 Mbps baseline, 0.6% loss. TUIC V5 works, WARP works, QUIC works.

This means any protocol that relies on UDP (Hysteria 2, TUIC V5, WireGuard) or
TLS fingerprint hiding (VLESS+REALITY) is **unreliable** — it may work one day
and fail the next. Shadowsocks TCP (AEAD, no TLS, no UDP) is the only protocol
that works consistently because it has no detectable signatures for the Palo Alto
to classify.

---

## 2. Protocol Testing Results

All testing conducted from the N4L school network at Macleans College against a
Voyager VPS (114.23.136.47, Ubuntu 22.04).

### Shadowsocks (TCP — PRIMARY)

| Test | Result |
|------|--------|
| Eco (port 8443, BBR, TCP only) | ✅ Works consistently |
| Stealth (port 8444, Brutal CC, TCP only) | ✅ Works consistently |
| Strike (port 8445, BBR, TCP+UDP) | ✅ TCP works, **UDP relay blocked** |
| **Verdict** | **Only reliable protocol.** AEAD encryption has no TLS fingerprint. Passes as "unknown TCP" which N4L doesn't block on high ports. |

### Hysteria 2 (QUIC — FAILED)

| Config variant | Result |
|---------------|--------|
| Default (QUIC :443) | ❌ Timeout |
| Port 53 (DNS port) | ❌ Timeout |
| Port 123 | ❌ Timeout |
| Salamander obfuscation | ❌ Config error in v2.10 |
| Minimal QUIC params | ❌ Timeout |
| Custom ALPN (h3 only) | ❌ Timeout |
| UDP-over-TCP bridge | ❌ QUIC handshake fails in tunnel |
| **Verdict** | **Blocked.** N4L's DPI identifies QUIC's handshake fingerprint regardless of port or obfuscation. |

### Xray VLESS+REALITY (TLS — FAILED)

| Test | Result |
|------|--------|
| TCP port reachable | ✅ `connect()` succeeded |
| TLS handshake | ✅ Xray accepts connection |
| VLESS+REALITY auth | ✅ Xray logs show "accepted" |
| **Traffic relay** | **❌ Times out (8009ms each attempt)** |
| UDP relay | ❌ Times out |
| **Verdict** | **Connection establishes but App-ID detects tunnelling.** The Palo Alto's JA3 fingerprinting identifies the non-browser TLS fingerprint (uTLS imitation isn't perfect) and/or analyses traffic patterns post-handshake, then silently drops subsequent packets. |

### TUIC V5 (QUIC — UNRELIABLE)

| Test | Result |
|------|--------|
| Default (QUIC :123) | ❌ SOCKS5 starts but relay times out |
| UDP relay extension | ❌ Times out |
| **On a good day** | ✅ Worked (83 Mbps baseline day) |
| **Verdict** | **Works when N4L is lenient, fails when they're strict.** QUIC is fundamentally unreliable on this network. |

### Other Protocols

| Protocol | Result | Reason |
|----------|--------|--------|
| WireGuard (UDP) | ❌ Blocked | All UDP dropped |
| AmneziaWG (UDP) | ❌ Blocked | All UDP dropped |
| Trojan (TLS) | ❌ Blocked | JA3 fingerprinting |
| WARP (TLS) | ⚠️ Inconsistent | Works on good days, fails on bad days |
| WARP MASQUE (TCP) | 🟡 Untested | Could provide TCP fallback path |
| udp2raw faketcp | 🟡 Untested | Could tunnel UDP over TCP for gaming |

---

## 3. TCP-over-TCP Meltdown Analysis

### The Problem

Shadowsocks creates a TCP tunnel (encrypted TCP stream between client and server).
When a client application opens a TCP connection through the tunnel, that's
**TCP inside TCP** — two layers of congestion control reacting to the same
packet loss.

```
Application TCP connection
    └── Shadowsocks TCP tunnel
         └── Physical TCP (WiFi → Internet)
```

### Why It Matters

If the underlying WiFi link drops a packet:

1. **Inner TCP** (application) sees the loss and backs off its send window
2. **Outer TCP** (Shadowsocks tunnel) also sees the loss and backs off its send window
3. Both backoffs compound, causing a **double congestion collapse**
4. Throughput can drop to near-zero until both TCP stacks recover

### Why It's Acceptable Here

- The N4L school network is **low-loss** (< 1.5% packet loss even on bad days)
- Low-loss WiFi means TCP-over-TCP meltdown rarely triggers
- Brutal CC on the Stealth tier ignores TCP fairness and aggressively recovers
- The alternative (UDP-based protocols) is completely blocked by N4L
- For gaming (Strike tier): the TCP tunnel adds latency (~33-44ms) but is stable

### Mitigations

1. **BBR congestion control** on the outer tunnel (server-side) — BBR is less
   sensitive to packet loss than CUBIC because it measures bandwidth, not loss
2. **Brutal CC** on Stealth tier — ignores loss-based backoff entirely
3. **UDP-over-TCP** (Strike tier) — wraps gaming UDP in TCP, avoiding inner loss
4. **sing-box tun `auto_route`** — handles routing efficiently

### Future Options

- Test `udp2raw faketcp` mode: wraps UDP in fake TCP headers that pass through
  firewalls but aren't real TCP (no backoff). Would enable native WireGuard/UDP
  gaming traffic over the TCP tunnel.

---

## 4. Architectural Reasoning

### Why Shadowsocks TCP Won (over Hysteria 2, Xray, etc.)

| Protocol | Why it lost |
|----------|-------------|
| Hysteria 2 | QUIC is UDP-based. N4L drops all non-DNS UDP. Even UDP-over-TCP failed because the QUIC handshake itself is fingerprintable. |
| Xray VLESS+REALITY | uTLS browser fingerprint imitation isn't perfect. Palo Alto's JA3 detection identifies the non-browser TLS handshake, then App-ID classifies the tunnelling behaviour post-handshake. |
| TUIC V5 | Same QUIC problem as Hysteria 2. Worked on good days, failed on bad days — unreliable. |
| WireGuard / AmneziaWG | UDP blocked entirely. |
| Trojan | TLS-based, same JA3 problem as Xray. |
| Shadowsocks TCP | **No TLS, no UDP.** AEAD encryption is indistinguishable from random TCP payload. No JA3 fingerprint because there's no TLS handshake. Passes through as "unknown TCP" which N4L allows on high ports. |

### Why No TUN Helper Service (BYOD Context)

The original architecture documents describe a privileged helper service for TUN
creation. This is **not needed** because:

- BYOD school → every student has admin rights on their own machine
- sing-box runs with `--tun` flag directly — no IPC to a helper daemon needed
- Eliminating the helper removes a large attack surface (named pipe, Unix socket,
  privilege escalation vectors, crash handling)
- Less binaries = less breakage surface area. The goal is: **one app binary +
  one engine binary (sing-box) per platform.**

### Why No SOCKS5 Fallback Mode

Earlier architecture versions described a SOCKS5 + tun2socks bridge. This is
**abandoned** because:

- sing-box handles TUN natively — no separate tun2socks process needed
- tun2socks is abandonware (last update 2020)
- sing-box has built-in SOCKS5 inbound if needed (but we don't use it)
- BYOD = admin rights = TUN always works

### Why the Minimal Binary Philosophy

Every additional binary is a potential failure point:
- Must be downloaded, verified, and cached
- May fail to start, crash, or hang
- Adds to the update payload
- Increases support burden ("it doesn't work" → which binary failed?)

Current target: **2 binaries per platform** — myvpn (GUI + manager) + sing-box (engine).
The TUN helper is eliminated. The SOCKS5/tun2socks bridge is eliminated.

---

## 5. Product Tier Architecture

### Tier Comparison

| Tier | Price | Port | CC | Cap | Transport | Target Use |
|------|-------|:----:|:---:|:---:|:---------:|------------|
| Eco | $2/mo | 8443 | BBR | 5 Mbps tc | TCP only | Text, email, browsing |
| Stealth | $4/mo | 8444 | **Brutal** | 48 Mbps target | TCP only | Streaming, downloads |
| Strike | $8/mo | 8445 | BBR | 200 Mbps tc | TCP+UDP | Gaming |

### How Bandwidth Caps Work (Server-Side)

- **Eco (5 Mbps):** tc HTB qdisc class 1:10, matches source port 8443
- **Stealth (48 Mbps):** Brutal CC kernel module target rate (no tc cap — Brutal manages itself)
- **Strike (200 Mbps):** tc HTB qdisc class 1:30, matches source port 8445

The caps are **server-enforced**, not client-enforced. The client gets full-speed
Shadowsocks access; the server throttles before the encrypted tunnel egresses.
This means a hacked/broken client can't bypass its tier cap.

### Brutal CC — How It Works

Brutal is a kernel-level congestion control algorithm that ignores traditional
TCP fairness. It's designed to aggressively fill available bandwidth.

1. `tcp-brutal` kernel module registers `brutal` as a CC algorithm
2. An LD_PRELOAD wrapper intercepts `accept()` on the Stealth ssserver socket
3. Each accepted connection's `TCP_CONGESTION` is set to `brutal`
4. Brutal targets a configurable rate (default 48 Mbps for Stealth)
5. It ramps up until it hits the target or saturates the link

**Critical downside:** Brutal is unfair. A Stealth user can drown out Eco, Strike,
and even unrelated services on the same VPS. This is intentional product
differentiation — Stealth costs more because it uses more bandwidth.

---

## 6. Key Decisions That Shaped the Project

| Decision | Choice | Why |
|----------|--------|-----|
| Protocol | Shadowsocks TCP | Only protocol that works on N4L consistently (no TLS, no UDP) |
| Client engine | sing-box | One binary, active maintenance, built-in TUN, replaces sslocal+tun2socks |
| Server CC | BBR default, Brutal for Stealth | BBR for most, Brutal for premium tier |
| Bandwidth caps | tc (server-side) | Client can't bypass; no client bandwidth code needed |
| Activation | Luhn-mod-N + SHA256 fingerprint | Permanent device binding, survives reinstall |
| Updates | Two-phase sentinel | Auto-revert on crash without Ed25519 signing keys |
| Backend | PocketBase (SQLite) | Go binary, no external DB, JS hooks for custom logic |
| TUN model | Direct (no helper service) | BYOD = admin rights = no privileged helper needed |
| Binary count | 2 (myvpn + sing-box) | Minimise breakage surface area |

---

## 7. Open Questions & Future Work

### Short-term

1. **Measure actual game traffic latency** — UDP relay tests were from DNS queries.
   Real game traffic (Fortnite, Valorant) through Strike's UDP-over-TCP needs measuring.

2. **Test WARP MASQUE (TCP mode)** — Cloudflare's WARP over TCP may provide lower
   latency to the VPS than direct Shadowsocks in some conditions.

3. **udp2raw on a real laptop** — If `faketcp` mode works, it could let native
   WireGuard run over the TCP tunnel (better UDP gaming performance).

4. **Switch to shadowsocks-libev** — If TCP Brutal is needed at the engine level,
   `ss-libev` has `--tcp-congestion` flag that `ss-rust` lacks (currently Brutal
   is applied server-side via LD_PRELOAD, not client-side).

### Medium-term

5. **Multi-VPS architecture** — VPS closer to game servers (Sydney for OCE games,
   US West for NA games) lowers the UDP relay leg latency for Strike users.

6. **Auto-engine selection** — Client probes connectivity and selects between
   Shadowsocks TCP/UDP, WARP, or direct depending on network conditions.

7. **TCP Brutal for a streaming tier** — If the product expands to a stream-only
   tier, implementing Brutal CC creates natural product differentiation.

### Not Recommended

| Approach | Why not |
|----------|---------|
| Direct QUIC (Hysteria 2, TUIC V5) | Blocked by N4L UDP block |
| TLS proxies (Trojan, Xray VLESS) | Fingerprinted by JA3 detection |
| AmneziaWG over UDP | UDP blocked at network level |
| FEC | Solves a problem that doesn't exist on low-loss N4L |
| WireGuard-go + TCP tunnel | TCP-over-TCP meltdown ruins gaming |
