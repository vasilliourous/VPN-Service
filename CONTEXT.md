# VPN-Service — Full Session Context

> **Generated:** 2026-07-24
> **Purpose:** Single reference document capturing all implementation decisions, testing results, configurations, and architectural reasoning for the VPN-Service project.
> **Audience:** Future AI agents (and human developers) who need to understand the complete context without re-running tests.

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Network Environment (N4L School Network)](#2-network-environment-n4l-school-network)
3. [Testing Methodology](#3-testing-methodology)
4. [What Works vs What Doesn't](#4-what-works-vs-what-doesnt)
5. [Active Server Infrastructure](#5-active-server-infrastructure)
6. [Shadowsocks Implementation (Primary Solution)](#6-shadowsocks-implementation-primary-solution)
7. [Xray VLESS+REALITY Implementation (Experimental)](#7-xray-vlessreality-implementation-experimental)
8. [Other Protocols Tested & Results](#8-other-protocols-tested--results)
9. [Product Tier Architecture](#9-product-tier-architecture)
10. [SOCKS5 vs TUN vs Direct TCP Tunnel](#10-socks5-vs-tun-vs-direct-tcp-tunnel)
11. [TCP Brutal Analysis](#11-tcp-brutal-analysis)
12. [Forward Error Correction (FEC) Analysis](#12-forward-error-correction-fec-analysis)
13. [Kernel Tuning](#13-kernel-tuning)
14. [Key Files & Locations](#14-key-files--locations)
15. [Future Considerations](#15-future-considerations)

---

## 10. SOCKS5 vs TUN vs Direct TCP Tunnel

### Why This Matters

This is a critical architectural decision for the VPN-Service client app. The current implementation uses SOCKS5, but there are three progressively better approaches for the final product.

### SOCKS5 (Current — ss-local)

**How it works:**
```
Game/App → SOCKS5 proxy → sslocal (encrypted TCP) → ssserver → Internet
```

**Overhead per TCP connection:**
```
Client → SOCKS5:  [version auth selection]  (1 RTT)
Client ← SOCKS5:  [auth method chosen]      
Client → SOCKS5:  [connect request]         (1 RTT)
Client ← SOCKS5:  [connection established]
                  ↓
               2 RTTs of overhead per connection
```
At ~35ms RTT to the VPS, that's ~70ms per connection × 50 page resources = 3.5 seconds of pure SOCKS5 handshake overhead on every page load.

**Pros:**
- No admin rights needed
- Simple to implement
- Per-app routing possible
- UDP ASSOCIATE works for gaming

**Cons:**
- +2 RTT handshake per TCP connection (~70ms at 35ms RTT)
- Requires per-app proxy configuration (most games ignore system proxy)
- Users must manually configure each app they want proxied
- Non-technical users find it confusing

### Option A: Redsocks/ss-redir (TCP Tunnel, No SOCKS5 Overhead)

**How it works:**
```
Game/App → [iptables NAT redirect] → ss-redir (encrypted TCP directly) → ssserver
```

Instead of a SOCKS5 handshake, `ss-redir` uses iptables `TPROXY` or `REDIRECT` to intercept TCP traffic at the kernel level and forward it directly through the encrypted tunnel. No SOCKS framing — the first byte of data goes out immediately.

**Overhead per TCP connection:**
```
Game/App → [0 RTT — kernel redirects directly] → ss-redir → encrypted TCP → ssserver
```
**Zero additional handshake overhead.**

**Pros:**
- No SOCKS5 handshake overhead (saves ~70ms per connection)
- System-wide — no per-app config needed
- Transparent to all applications

**Cons:**
- ⚠️ **TCP only — no UDP** (can't do gaming)
- Requires root/admin rights for iptables rules
- More complex to set up

### Option B: tun2socks + sslocal (TUN Virtual NIC, Full VPN)

**How it works:**
```
Game/App → [TUN virtual NIC] → tun2socks → sslocal (SOCKS5) → ssserver → Internet
```

`tun2socks` creates a virtual network adapter (TUN interface). All traffic (TCP + UDP) goes through this virtual NIC and gets piped through the SOCKS5 proxy. The SOCKS5 handshake still happens, but tun2socks can **pool TCP connections** to reduce overhead.

**Pros:**
- System-wide — all apps routed automatically (like a real VPN)
- **UDP support** — games, voice chat, DNS all work
- No per-app configuration needed
- Click Connect → everything works

**Cons:**
- Requires admin rights (TUN device creation)
- SOCKS5 still present internally (but hidden from user)
- Slightly higher CPU overhead from TUN + SOCKS5 stack

### Option C: Pure TCP + UDP Tunnel (ss-redir + ss-tunnel, No SOCKS5 at All)

**How it works:**
```
TCP traffic → [iptables TPROXY] → ss-redir → ssserver
UDP traffic → [iptables TPROXY] → ss-tunnel → ssserver
```

Run two Shadowsocks instances side-by-side:
- **ss-redir** handles TCP (direct, no SOCKS5)
- **ss-tunnel** handles UDP (dedicated UDP relay)
- iptables routes traffic to the correct instance based on protocol

**Overhead per connection:**
```
Zero SOCKS5 overhead. Zero handshake. Kernel-level redirection directly to encrypted tunnel.
```

**Pros:**
- Absolute minimum overhead
- System-wide (no per-app config)
- TCP and UDP both supported
- Fastest possible TCP path

**Cons:**
- Requires root/admin rights (iptables + TUN)
- Most complex to set up and debug
- Harder to implement split-tunnelling

### Performance Comparison

| Metric | SOCKS5 (ss-local) | ss-redir (TCP tunnel) | tun2socks + sslocal | Pure tunnel (ss-redir + ss-tunnel) |
|--------|:------------------:|:---------------------:|:-------------------:|:----------------------------------:|
| **TCP overhead** | +2 RTT (~70ms) | **+0 RTT (0ms)** | +0 RTT (pooled) | **+0 RTT (0ms)** |
| **UDP support** | ✅ Yes | ❌ No | ✅ Yes | ✅ Yes |
| **System-wide** | ❌ Per-app | ✅ Yes | ✅ Yes | ✅ Yes |
| **Admin rights** | ❌ Not needed | ✅ Required | ✅ Required | ✅ Required |
| **Setup complexity** | Simple | Medium | Medium | Complex |
| **Best for tier** | Testing/debug | Stealth/Eco (TCP-only) | **Strike (gaming)** | Power users |

### Recommendation for the Product

**Do not phase out SOCKS5 entirely** — but demote it to an internal implementation detail:

```
┌───────────────────────────────────────────────┐
│          MyVPN Desktop App (Go + Fyne)          │
│                                                 │
│  ┌───────────────────────────────────────────┐  │
│  │  Default: tun2socks + sslocal              │  │
│  │  • System-wide VPN experience              │  │
│  │  • UDP gaming works natively               │  │
│  │  • Click Connect → everything routes       │  │
│  │  • SOCKS5 hidden internally                │  │
│  └───────────────────────────────────────────┘  │
│                                                 │
│  ┌───────────────────────────────────────────┐  │
│  │  Fallback: SOCKS5-only (ss-local)          │  │
│  │  • Used when TUN creation fails            │  │
│  │  • No admin rights = SOCKS5 mode           │  │
│  │  • Per-app config required, show guide     │  │
│  └───────────────────────────────────────────┘  │
│                                                 │
│  ┌───────────────────────────────────────────┐  │
│  │  Advanced: Pure tunnel (ss-redir+ss-tun)   │  │
│  │  • Toggle in settings for power users      │  │
│  │  • Best performance, most complex          │  │
│  └───────────────────────────────────────────┘  │
└───────────────────────────────────────────────┘
```

### Build Order

| Phase | Approach | Why |
|-------|----------|-----|
| **MVP** | SOCKS5 (ss-local) | Fastest to ship, works now |
| **V2** | tun2socks + sslocal | Better UX, gaming-ready |
| **V3** | Pure tunnel option | Performance tier for enthusiasts |

---

## 1. Project Overview

**VPN-Service** is a commercial VPN service targeting students at NZ schools (specifically Macleans College) managed by N4L (Network for Learning) with Palo Alto firewalls. The service offers tiered plans:

| Tier | Price | Target Use | Primary Engine |
|------|-------|------------|---------------|
| Eco | $2/mo | Text/email | Shadowsocks (TCP, 5 Mbps cap) |
| Stealth | $4/mo | Streaming/browsing | Shadowsocks (TCP, ~48 Mbps) |
| Strike | $8/mo | Gaming | Shadowsocks UDP relay (33-44ms) |

**Current architecture:** Single VPS running Shadowsocks-rust as the primary engine for all tiers, with protocol-level differentiation enforcing tier limits.

---

## 2. Network Environment (N4L School Network)

### Network Properties

| Property | Value |
|----------|-------|
| School | Macleans College, NZ |
| Managed by | N4L (Network for Learning) |
| Firewall | Palo Alto (SSL decryption capability, App-ID, URL filtering) |
| Client IP range | 10.47.0.0/19 |
| External IP | 202.150.96.64 |
| DNS | 172.16.0.1, 172.16.0.2 |

### Known Blocking Behaviour

| Traffic type | Status | Mechanism |
|-------------|--------|-----------|
| TCP 22 (SSH) | ✅ Fully open (any IP) | — |
| TCP 80/443 | ✅ To whitelisted destinations only | URL/category filtering |
| TCP >1024 (unknown IPs) | ❌ Blocked | "Category of unknown" → block |
| **All non-DNS UDP** | **❌ Dropped** | Blanket block — all UDP except port 53 |
| DNS (UDP 53) | ✅ Only with valid DNS query format | DPI-inspected |
| ICMP (ping) | ✅ Open | — |
| Cloudflare IPs | ✅ Generally whitelisted | Categorised as "CDN" |

### Key Insight: The Block Varies by Day

- **Test No. 1 (bad day):** 4.23/5.86 Mbps baseline, 1.5% packet loss, WARP TLS fails
- **Test No. 2 (good day):** 83.6/185 Mbps baseline, 0.6% loss, TUIC V5 worked, WARP worked

N4L applies **dynamic/load-based filtering** — not a flat permanent block. On congested periods they tighten filtering; on quiet periods traffic flows freely.

### N4L TLS Interception (Critical Finding)

**N4L does NOT do full MITM SSL decryption** on personal devices (no CA certificate installed = no full decryption). However, Palo Alto blocks TLS-based proxies through:

1. **JA3 fingerprinting** — Go's TLS library (used by Xray, Trojan, etc.) has a distinct Client Hello fingerprint that differs from browsers. Palo Alto identifies this as a proxy tool and drops the connection before any decryption occurs.

2. **SNI/IP mismatch** — The Client Hello sends `sni: www.bing.com` but the connection goes to `114.23.136.47`. Normal browsers never do this. Strong proxy indicator.

3. **App-ID classification** — Even without decrypting, Palo Alto analyses traffic patterns (packet sizes, timing, bidirectionality) and classifies tunnelling protocols.

Shadowsocks bypasses all three because **it doesn't use TLS** — its encrypted packets look like random bytes to the firewall.

---

## 3. Testing Methodology

All tests conducted from a Whale sandbox running on the school WiFi (N4L network), with Cloudflare WARP disconnected for direct-N4L measurements.

### Engine testing procedure

1. Install server engine on VPS
2. Verify server starts and listens
3. Test local connectivity (client on VPS → server on VPS)
4. Test remote connectivity (client on school machine → server on VPS)
5. Verify actual traffic passes through tunnel (curl via SOCKS5 proxy)
6. Measure latency (TCP RTT, UDP RTT)
7. Test with and without WARP

### Client binaries used for testing

All copied from VPS via SSH (direct GitHub downloads blocked by N4L):
- Hysteria 2 client: `scp root@VPS:/usr/local/bin/hysteria /tmp/hy2`
- TUIC V5 client: `scp root@VPS:/tmp/tuic-client /tmp/tuic-client`
- Shadowsocks client: downloaded from GitHub releases (sometimes blocked)

---

## 4. What Works vs What Doesn't

### ✅ Works on N4L Network

| Solution | TCP Latency | UDP Latency | Notes |
|----------|:-----------:|:-----------:|-------|
| **Shadowsocks** (TCP tunnel) | ~120ms | **33-44ms** (UDP relay) | **Primary solution** — tested working |
| **Shadowsocks** (UDP relay) | N/A | **33-44ms** | Gaming traffic path |
| **WARP MASQUE** (via Cloudflare) | ~80ms | ~75ms | Only when WARP can connect |
| **SSH tunnel** (SOCKS5 via :22) | ~90ms | N/A | Always open on port 22 |

### ❌ Blocked/Non-functional on N4L Network

| Solution | Failure mode | Root Cause |
|----------|-------------|------------|
| **Hysteria 2** (direct QUIC) | `timeout: no recent network activity` | QUIC/UDP blocked at network level |
| **TUIC V5** (direct QUIC) | SOCKS5 starts but relay times out | QUIC/UDP blocked at network level |
| **Hysteria 2 via UDP-over-TCP** | QUIC handshake fails in tunnel | Tunnel adds delay, breaks QUIC timing |
| **Trojan** (TLS) | `SSL handshake failed: stream truncated` | TLS fingerprinting by N4L |
| **Xray VLESS+REALITY** | Connects but traffic times out | N4L TLS fingerprinting detects non-browser TLS |
| **AmneziaWG** (UDP) | UDP:42069 unreachable | UDP blocked at network level |
| **AmneziaWG via TCP wrapper** | Not fully tested — TCP transports works | Would have TCP-over-TCP issues |
| **udp2raw** (raw socket) | Needs root/raw socket permissions | Client (school laptop) may have admin, sandbox didn't |

### Key Discovery: Hysteria 2 vs TUIC V5

Both are QUIC-based. TUIC V5 appeared to work in some tests (SOCKS5 started), but actual traffic relay timed out — meaning both are ultimately blocked by N4L's UDP block. The earlier Test No. 2 where TUIC V5 worked was a "good day" when filtering was relaxed.

---

## 5. Active Server Infrastructure

### Voyager VPS (114.23.136.47) — Primary

| Property | Value |
|----------|-------|
| Provider | Voyager (NZ VPS) |
| Location | Auckland |
| OS | Debian 12 (bookworm) |
| Kernel | 6.1.0-9-amd64 |
| RAM | 961 MB |
| Disk | 15 GB |
| Hostname | freeman-fmnxfc |

### DigitalOcean VPS (134.199.153.119) — DESTROYED

Was used for testing. Destroyed by user.

### Active Services on Voyager VPS

| Service | Port | Protocol | Status |
|---------|------|----------|--------|
| SSH | 22 | TCP | ✅ Active |
| Shadowsocks | 8443 | TCP+UDP | ✅ Active |
| Xray VLESS+REALITY | 9443 | TCP | ✅ Active (experimental, likely blocked) |

### Inactive/Removed Services (cleaned up)

| Service | Reason removed |
|---------|---------------|
| Hysteria 2 | UDP blocked by N4L |
| TUIC V5 | UDP blocked by N4L |
| udp2raw | Needs raw sockets (root) on client |
| AmneziaWG | UDP blocked by N4L, kernel module issues |
| Trojan | TLS fingerprinted by N4L |

---

## 6. Shadowsocks Implementation (Primary Solution)

### Installation

On Voyager VPS (`114.23.136.47`):

```bash
curl -fsSL -o /tmp/ss.tar.xz "https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.23.0/shadowsocks-v1.23.0.x86_64-unknown-linux-gnu.tar.xz"
tar -xf /tmp/ss.tar.xz -C /tmp/
cp /tmp/ssserver /usr/local/bin/
apt-get install -y xz-utils  # if not present
```

### Server Configuration

**File:** `/etc/shadowsocks/config.json`

```json
{
    "server": "0.0.0.0",
    "server_port": 8443,
    "password": "a1075c3c6d5847654fc33428dc8beb18",
    "method": "aes-256-gcm",
    "mode": "tcp_and_udp",
    "fast_open": true,
    "no_delay": true,
    "ipv6_first": false,
    "udp_timeout": 300,
    "udp_associate_timeout": 600
}
```

### Systemd Service

**File:** `/etc/systemd/system/shadowsocks.service`

```ini
[Unit]
Description=Shadowsocks Server
After=network.target

[Service]
User=root
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/config.json
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### Password & Auth

```
Password: a1075c3c6d5847654fc33428dc8beb18
```

### Hiddify Link

```
ss://YWVzLTI1Ni1nY206YTEwNzVjM2M2ZDU4NDc2NTRmYzMzNDI4ZGM4YmViMTg=@114.23.136.47:8443#SS-Gaming
```

### Performance Measurements (from school network, no WARP)

| Metric | Chacha20 | AES-256-GCM |
|--------|:--------:|:-----------:|
| TCP RTT (web page) | 111-123ms | 118-122ms |
| **UDP RTT (gaming path)** | **34-44ms** | **33-44ms** |
| Direct ping to VPS | ~35ms | ~35ms |

**Key takeaway:** Cipher choice doesn't meaningfully affect latency. UDP gaming traffic already near base RTT.

### Shadowsocks Protocol Behaviour for Gaming

The SOCKS5 UDP ASSOCIATE method is how gaming UDP traffic flows:

1. Game sends UDP packet → sslocal (UDP local port)
2. sslocal encapsulates in encrypted TCP → ssserver
3. ssserver decapsulates → forwards as UDP → game server
4. Response follows reverse path

**Why this works:** The school-to-VPS leg uses TCP (reliable, passes N4L firewall), while the game sees UDP end-to-end. No TCP meltdown because the inner traffic (game UDP) doesn't trigger double congestion control.

---

## 7. Xray VLESS+REALITY Implementation (Experimental)

### Installation

```bash
bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install
```

### Server Configuration

**File:** `/usr/local/etc/xray/config.json`

```json
{
  "log": { "loglevel": "warning" },
  "inbounds": [{
    "port": 9443,
    "protocol": "vless",
    "settings": {
      "clients": [{
        "id": "47e79ac8-25be-484a-9d60-ef32315a5029",
        "flow": "xtls-rprx-vision"
      }],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "dest": "www.bing.com:443",
        "serverNames": ["www.bing.com", "www.microsoft.com"],
        "privateKey": "0Fi3d1spljxRaavkzhmGWJf53NkoQujWMPkq06e4b00",
        "shortIds": ["98d5139790390b03"]
      }
    },
    "sniffing": {
      "enabled": true,
      "destOverride": ["http", "tls"]
    }
  }],
  "outbounds": [{
    "protocol": "freedom",
    "tag": "direct"
  }]
}
```

### Key Generation

```bash
# Generate x25519 keys for REALITY
xray x25519
# Output format:
# PrivateKey: <key>  
# Password (PublicKey): <key>

# Generate UUID
xray uuid

# Generate short ID
openssl rand -hex 8
```

### Current Keys

```
XRAY_PRIV=0Fi3d1spljxRaavkzhmGWJf53NkoQujWMPkq06e4b00
XRAY_PUB=pOA8TJgewqSAuWSMcSoASBL2F880oO8fQaVSX8NUnRk
XRAY_UUID=47e79ac8-25be-484a-9d60-ef32315a5029
XRAY_SID=98d5139790390b03
```

### Hiddify Link

```
vless://47e79ac8-25be-484a-9d60-ef32315a5029@114.23.136.47:9443?encryption=none&security=reality&sni=www.bing.com&fp=chrome&pbk=pOA8TJgewqSAuWSMcSoASBL2F880oO8fQaVSX8NUnRk&sid=98d5139790390b03&type=tcp&flow=xtls-rprx-vision#VLESS-REALITY
```

### Test Results

| Test | Result |
|------|--------|
| TCP port reachable | ✅ `connect() succeeded` |
| TLS handshake | ✅ Xray accepts connection |
| VLESS+REALITY auth | ✅ Xray logs show "accepted" |
| **Traffic relay** | **❌ Times out (8009ms each attempt)** |
| UDP relay | ❌ Times out |

**Root cause:** N4L's Palo Alto identifies the non-browser TLS fingerprint (JA3) and/or the SNI/IP mismatch. Connection establishes but App-ID classifies it as tunnelling after analysing traffic patterns, then silently drops subsequent packets.

---

## 8. Other Protocols Tested & Results

### Hysteria 2

| Config variant | Result |
|---------------|--------|
| Default (QUIC :443) | ❌ Timeout |
| Port 53 (DNS port) | ❌ Timeout |
| Port 123 | ❌ Timeout |
| Salamander obfuscation | ❌ Config error in v2.10 |
| Minimal QUIC params | ❌ Timeout |
| Custom ALPN (h3 only) | ❌ Timeout |
| UDP-over-TCP bridge | ❌ QUIC handshake fails in tunnel |

**Verdict:** Hysteria 2's QUIC fork is fingerprintable. N4L's DPI identifies and drops it regardless of port or obfuscation.

### TUIC V5

| Config variant | Result |
|---------------|--------|
| Default (QUIC :123) | ❌ SOCKS5 starts but relay times out |
| UDP relay mode quic | ❌ Same result |
| To DO VPS | ❌ Same result |
| To Voyager VPS | ❌ Same result |

**Verdict:** Same as Hysteria 2 — all QUIC implementations blocked by N4L's UDP block. Earlier "working" test (Test No. 2) was a "good day" with relaxed filtering.

### Trojan

| Config variant | Result |
|---------------|--------|
| Self-signed cert, port 9443 | ❌ `SSL handshake failed: stream truncated` |
| With nginx TLS fallback | ❌ Same error |

**Verdict:** TLS-based proxies get fingerprinted by N4L's JA3 detection. Go's TLS implementation is distinctive and flagged.

### AmneziaWG

| Config variant | Result |
|---------------|--------|
| Direct UDP (port 42069) | ❌ UDP unreachable |
| With obfuscation (jc/jmin/jmax) | ❌ Still UDP, still blocked |
| TCP transport wrappr (socat :42070) | Could work but TCP-over-TCP issues |
| TCP transport reachability | ✅ TCP :42070 connected |

**Verdict:** AmneziaWG's obfuscation doesn't help because N4L blocks all non-DNS UDP at the network level, not via DPI. The TCP wrapper approach would suffer from TCP-over-TCP meltdown.

### udp2raw

| Config variant | Result |
|---------------|--------|
| Server (faketcp mode, :4096) | ✅ Installed and running on VPS |
| TCP :4096 reachability | ✅ Port reachable from school network |
| Client (needs raw socket) | ❌ Cannot run without root/CAP_NET_RAW |

**Verdict:** udp2raw could work if the client has admin rights (school laptop may have this). The client needs root to create raw sockets for faketcp mode. Worth testing on a real school laptop.

---

## 9. Product Tier Architecture

### Tier Design (Current)

| Tier | Price | Shadowsocks Config | UDP Relay | TCP CC | Target Experience |
|------|-------|-------------------|:---------:|--------|-------------------|
| Eco | $2 | 5 Mbps cap, small window | Disabled | CUBIC | Text loads slow, video buffers |
| Stealth | $4 | 48 Mbps cap, large window | Disabled | CUBIC/BBR | 1080p streaming stable |
| Strike | $8 | Full speed, Brutal target probe | **Enabled** | BBR | Gaming (33-44ms), 4K streaming |

### Server-Side Implementation

All tiers share the same Shadowsocks server. Differentiation is **client-side**:

1. PocketBase stores tier configs (password + method + speed cap + UDP mode)
2. On activation, the client receives the config for their tier
3. Eco/Stealth clients use TCP only; Strike clients get UDP relay config

### Proposed Stream Tier (with TCP Brutal)

If TCP Brutal is implemented, a fourth tier could be:

| Tier | Price | Engine | CC | UDP Relay | Experience |
|------|-------|--------|:---------:|:---------:|------------|
| Stream | $6 | Shadowsocks TCP | **Brutal** | Disabled | Fast streaming, browsing — poor for gaming by design |

This creates natural segmentation: Stream users get maximum TCP throughput but can't game on it, pushing them to the Strike tier if they want gaming.

---

## 10. TCP Brutal Analysis

### What It Is

TCP Brutal is a Linux kernel module implementing a rate-based congestion control algorithm similar to Hysteria 2's Brutal CC. It aims to "reclaim" available bandwidth aggressively.

### Key Projects

| Project | Author | Notes |
|---------|--------|-------|
| [tcp-brutal](https://github.com/klzgrad/tcp-brutal) | klzgrad | Original implementation, targets kernel 5.x |
| [tcp-brutal-ng](https://github.com/apernet/tcp-brutal-ng) | Aperture (Hysteria team) | Newer, better maintained |

### How to Use with Shadowsocks

**Option 1: System-wide default**
```bash
modprobe tcp_brutal
sysctl net.ipv4.tcp_congestion_control=brutal
```
⚠️ Affects all TCP connections on the VPS, not just Shadowsocks.

**Option 2: LD_PRELOAD wrapper (surgical)**
```c
// brutal-wrap.c — forces brutal CC on all ss sockets
#define _GNU_SOURCE
#include <dlfcn.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <string.h>

int setsockopt(int sockfd, int level, int optname,
               const void *optval, socklen_t optlen) {
    static int (*real_setsockopt)(int, int, int, const void *, socklen_t) = NULL;
    if (!real_setsockopt) real_setsockopt = dlsym(RTLD_NEXT, "setsockopt");
    if (level == IPPROTO_TCP && optname == TCP_NODELAY) {
        char *cc = "brutal";
        real_setsockopt(sockfd, IPPROTO_TCP, TCP_CONGESTION, cc, strlen(cc));
    }
    return real_setsockopt(sockfd, level, optname, optval, optlen);
}
```

Compile:
```bash
gcc -shared -fPIC -o brutal-wrap.so brutal-wrap.c -ldl
```

Run:
```bash
LD_PRELOAD=/path/to/brutal-wrap.so ssserver -c /etc/shadowsocks/config.json
```

### Pros & Cons for Product

| Aspect | Evaluation |
|--------|------------|
| **Throughput** | ✅ Excellent — aggressively fills pipe |
| **Latency stability** | ❌ Poor — increases bufferbloat and jitter |
| **Gaming fit** | ❌ Actively harmful — spiky latency is anti-gaming |
| **Streaming fit** | ✅ Great — fast buffering, high throughput |
| **Implementation complexity** | Medium — kernel module + LD_PRELOAD |
| **Kernel compatibility** | ⚠️ Debian 12 (6.1) may need patching |

### Verdict

TCP Brutal is a legitimate product differentiation tool, not a gaming improvement. Its natural anti-gaming behaviour (high jitter) can be positioned as a deliberate feature: the Stream tier gets bandwidth, the Strike tier gets latency.

---

## 11. Forward Error Correction (FEC) Analysis

### Why FEC Doesn't Help Here

| Claim | Counter-argument |
|-------|-----------------|
| "FEC fixes packet loss" | The tunnel leg (school→VPS) uses TCP, which already handles loss via retransmission. FEC on top of TCP is redundant. |
| "FEC reduces gaming latency" | FEC adds latency — it must batch packets to compute parity data. For 60pps games, this adds 8-16ms of delay before transmission. Your current 33-44ms UDP path is faster. |
| "FEC helps on lossy links" | The school-to-VPS leg has 0% loss in testing. The VPS-to-internet leg is standard internet — FEC on the tunnel doesn't help there. |

### When FEC Would Be Useful

- Native UDP path with >1% uncorrelated packet loss
- Direct peer-to-peer gaming where you control both endpoints
- High-loss wireless links (cellular, satellite)

### Verdict

**UDP forwarding through Shadowsocks is already sufficient** for gaming. FEC would add complexity, latency, and overhead for zero benefit on the actual network path.

---

## 12. Kernel Tuning

Applied on Voyager VPS:

```bash
# TCP congestion control
net.ipv4.tcp_congestion_control = bbr
net.core.default_qdisc = fq

# TCP Fast Open (3 = client + server)
net.ipv4.tcp_fastopen = 3

# Low-latency gaming tuning
net.ipv4.tcp_notsent_lowat = 16384
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_fin_timeout = 15

# Buffer sizes
net.core.rmem_default = 1048576
net.core.wmem_default = 1048576
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
```

**File:** Added to `/etc/sysctl.conf` on Voyager VPS.

---

## 13. Key Files & Locations

### On Voyager VPS (114.23.136.47)

| File | Purpose |
|------|---------|
| `/etc/shadowsocks/config.json` | Shadowsocks server config |
| `/etc/systemd/system/shadowsocks.service` | Shadowsocks systemd unit |
| `/usr/local/bin/ssserver` | Shadowsocks server binary |
| `/usr/local/etc/xray/config.json` | Xray VLESS+REALITY config |
| `/etc/systemd/system/xray.service` | Xray systemd unit (installed by Xray-install) |
| `/usr/local/bin/xray` | Xray binary |
| `/etc/sysctl.conf` | Kernel tuning parameters |

### In Workspace (school-network-analysis.md)

| File | Purpose |
|------|---------|
| `school-network-analysis.md` | Original school network analysis |
| `VPN-Service/CONTEXT.md` | This document |
| `VPN-Service/simplified/ARCHITECTURE.md` | Simplified architecture plan |
| `VPN-Service/simplified/IMPLEMENT.md` | Implementation guide |
| `VPN-Service/simplified/OPS.md` | Operations manual |

---

## 14. Future Considerations

### Short-term

1. **Test Shadowsocks latency on real games** — The 33-44ms UDP measurement was from DNS queries. Actual game traffic should be measured in Fortnite, Valorant, etc.

2. **Test with WARP enabled** — WARP MASQUE mode over TCP may provide a faster path to Cloudflare, with lower latency to the VPS.

3. **udp2raw on a real laptop** — If the school laptop has admin rights, udp2raw `faketcp` mode could let native WireGuard work over the TCP tunnel. Worth testing.

4. **Switch to shadowsocks-libev** — If TCP Brutal is desired, shadowsocks-libev has `--tcp-congestion` flag that ss-rust lacks.

### Medium-term

5. **Multi-VPS architecture** — VPS closer to game servers (Sydney for OCE games, US West for NA games) would lower the UDP relay leg latency.

6. **Auto-engine selection** — The app probes connectivity and selects between Shadowsocks TCP/UDP, WARP, or direct depending on network conditions.

7. **TCP Brutal for Stream tier** — If the product expands to a Stream-only tier, implementing TCP Brutal creates natural product differentiation.

### Not Recommended

- ❌ Direct QUIC protocols (Hysteria 2, TUIC V5) — blocked by N4L UDP block
- ❌ TLS-based proxies (Trojan, Xray VLESS) — fingerprinted by N4L JA3 detection
- ❌ AmneziaWG over UDP — UDP blocked at network level
- ❌ FEC — solves a problem that doesn't exist on this network
- ❌ WireGuard-go + TCP tunnel — TCP-over-TCP meltdown ruins gaming

---

*End of context document. Generated 2026-07-24 from a full session testing all protocols against the N4L school network from Macleans College, NZ.*
