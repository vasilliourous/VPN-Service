# Niwa Science Fair — Revised Project Plan

> **Project Title:** The Swiss Knife of Digital Security
> *"A self-service, multi-protocol VPN gateway with per-user isolation, adaptive firewall evasion, and privacy-filtered browsing — all orchestrated from your phone or browser."*
>
> **Resources:** Powerful local VPS(s) + Enterprise Proxmox server at home
> **Status:** Revised blueprint — merges core K3s architecture with multi-protocol auto-probing, client apps, and judge-ready presentation framing.

---

## Table of Contents

1. [Project Vision & Judge Narrative](#1-project-vision--judge-narrative)
2. [System Architecture Overview](#2-system-architecture-overview)
3. [Infrastructure Layer: Proxmox + K3s](#3-infrastructure-layer-proxmox--k3s)
4. [Multi-Protocol Adaptive Engine (The Core Innovation)](#4-multi-protocol-adaptive-engine-the-core-innovation)
5. [Per-User Isolation & Session Binding](#5-per-user-isolation--session-binding)
6. ["Open Source Community DNS Security" (AdGuard Home)](#6-open-source-community-dns-security-adguard-home)
7. [Client Applications](#7-client-applications)
8. [Web Dashboard](#8-web-dashboard)
9. [Observability & Monitoring](#9-observability--monitoring)
10. [Science Fair Demonstration Plan](#10-science-fair-demonstration-plan)
11. [Implementation Phases](#11-implementation-phases)
12. [Critical Design Decisions & Trade-offs](#12-critical-design-decisions--trade-offs)

---

## 1. Project Vision & Judge Narrative

### The Pitch

> *"Most people connect to the internet through a straw — one protocol, one server, no protection. If that protocol is blocked, you're cut off. If that server is compromised, your data is exposed. If that connection is unsecured, your browsing is sold.
>
> This project builds a **Swiss Knife of Digital Security** — a system that automatically switches between multiple secure protocols, gives every user their own isolated tunnel, filters threats at the DNS level using open-source community intelligence, and lets you manage it all from a beautiful dashboard on your phone or PC.
>
> It's designed for **digital sovereignty** — the idea that your internet connection should be resilient, private, and under your control."*

### Key Framing For Judges

| Technical Detail | Judge-Friendly Presentation |
|---|---|
| AdGuard Home | "Open-source community DNS threat intelligence — a distributed blocklist maintained by security researchers that catches malware, trackers, and phishing domains before they reach your device." |
| Auto-probing engine | "Adaptive protocol switching — like a radio that automatically finds the clearest frequency when there's interference." |
| K3s / Kubernetes | "Lightweight container orchestration — think of it as a smart hotel that assigns each guest their own private room with individual key." |
| Per-user isolation | "Every user gets their own sandboxed tunnel — one user's problem never affects another." |
| WireGuard + Hysteria 2 + VLESS-REALITY | "Multiple encrypted transport layers — if one is detected or blocked, the system seamlessly switches to another without the user noticing." |

---

## 2. System Architecture Overview

```
                          ┌─────────────────────────────────┐
                          │     CLOUD / INTERNET            │
                          └──────────┬──────────────────────┘
                                     │
                          ┌──────────▼──────────────────────┐
                          │    EDGE ROUTER (hardware)        │
                          │    Port forwards: 443, 51820     │
                          │    80, 8443 (control)            │
                          └──────────┬──────────────────────┘
                                     │
                          ┌──────────▼──────────────────────┐
                          │    OPNsense (Proxmox VM)         │
                          │    • Firewall + NAT              │
                          │    • Suricata IDS (LAN side)     │
                          │    • CrowdSec (brute-force prot) │
                          │    • Routes to MetalLB IP pool   │
                          └──────────┬──────────────────────┘
                                     │
              ┌──────────────────────┼──────────────────────┐
              │                      │                      │
     ┌────────▼────────┐  ┌─────────▼────────┐  ┌─────────▼────────┐
     │  K3s Master     │  │  K3s Worker 1    │  │  K3s Worker N    │
     │  (Proxmox VM)   │  │  (Proxmox VM)    │  │  (Proxmox VM)    │
     │                 │  │                  │  │                  │
     │  • API Server   │  │  ┌────────────┐  │  │  ┌────────────┐  │
     │  • Controller   │  │  │User Pod A  │  │  │  │User Pod Z  │  │
     │  • CRD Operator │  │  │(WireGuard) │  │  │  │(Hysteria2) │  │
     │  • MetalLB      │  │  └────────────┘  │  │  └────────────┘  │
     │  • Traefik      │  │  ┌────────────┐  │  │  ┌────────────┐  │
     └─────────────────┘  │  │User Pod B  │  │  │  │User Pod Y  │  │
                          │  │(VLESS-     │  │  │  │(WireGuard) │  │
                          │  │ REALITY)   │  │  │  └────────────┘  │
                          │  └────────────┘  │  └─────────────────┘
                          └──────────────────┘
```

### Traffic Flow (Adaptive Example)

```
1. User connects via Android app
2. App checks connectivity to each protocol endpoint
3. Measures latency to WireGuard (port 51820), Hysteria 2 (port 443 UDP),
   VLESS-REALITY (port 443 TCP)
4. Selects best available protocol → establishes tunnel
5. All traffic routes through tunnel → AdGuard Home DNS filtering
6. If firewall blocks current protocol → auto-switch to next-best
7. Dashboard shows live protocol status
```

---

## 3. Infrastructure Layer: Proxmox + K3s

### 3.1 Physical Resources

| Resource | Role | Specs |
|---|---|---|
| **Enterprise Server (Proxmox)** | Hypervisor for all VMs | Multi-core Xeon, 64-128GB RAM, NVMe storage |
| **VPS (optional)** | External edge / backup exit node | Hetzner or similar, 2+ vCPU |

### 3.2 Proxmox VM Layout

| VM | Role | Specs | Count |
|---|---|---|---|
| **OPNsense** | Internal firewall, NAT, Suricata, CrowdSec | 2 vCPU, 4GB RAM | 1 |
| **K3s Master** | Kubernetes control plane + operator | 2 vCPU, 4GB RAM | 1 |
| **K3s Worker** | Hosts user VPN pods | 4 vCPU, 8GB RAM (per worker) | 2-3 |
| **Storage/NFS** | Persistent volumes, config maps, user data | 2 vCPU, 4GB RAM, large disk | 1 (or use Proxmox storage directly) |

### 3.3 K3s Configuration

```bash
# Worker node with dedicated data plane interface
curl -sfL https://get.k3s.io | \
  K3S_URL=https://<master-ip>:6443 \
  K3S_TOKEN=<token> \
  K3S_NODE_NAME=worker-1 \
  INSTALL_K3S_EXEC="--flannel-backend=wireguard-native \
    --node-ip=10.0.0.X \
    --kubelet-arg=max-pods=100" \
  sh -

# MetalLB (Layer2 mode)
helm repo add metallb https://metallb.github.io/metallb
helm install metallb metallb/metallb --namespace metallb-system --create-namespace

# IP pool config:
# apiVersion: metallb.io/v1beta1
# kind: IPAddressPool
# metadata:
#   name: vpn-pool
#   namespace: metallb-system
# spec:
#   addresses:
#   - 10.0.0.100-10.0.0.200
```

### 3.4 Node-Level Tuning

```bash
# /etc/sysctl.d/99-vpn-performance.conf on all workers
net.core.rmem_max = 134217728     # 128 MB
net.core.wmem_max = 134217728     # 128 MB
net.core.rmem_default = 16777216
net.core.wmem_default = 16777216
net.ipv4.udp_mem = 134217728 134217728 268435456
net.ipv4.conf.all.forwarding = 1
net.ipv4.conf.all.send_redirects = 0
```

---

## 4. Multi-Protocol Adaptive Engine (The Core Innovation)

This is the heart of the project and what makes it a legitimate science fair contender — a dynamic protocol selection system that probes, measures, and adapts in real-time.

### 4.1 Supported Protocols

| Protocol | Transport | Best For | Stealth | Latency |
|---|---|---|---|---|
| **WireGuard** | UDP (51820) | Daily browsing, low overhead | 5/10 | ~50-80ms |
| **Hysteria 2** | UDP/QUIC (443) | Gaming, lossy networks, strict DPI | 7/10 | ~110-150ms |
| **VLESS-REALITY + XTLS Vision** | TCP (443) | Ultra-restrictive firewalls, UDP-blocked networks | 9/10 | ~160-210ms |

### 4.2 Protocol Selection Algorithm (Auto-Probe Engine)

```
                   ┌─────────────────────────────────┐
                   │  USER CONNECTS                    │
                   │  (Android / Web Dashboard)        │
                   └────────────┬──────────────────────┘
                                │
                   ┌────────────▼──────────────────────┐
                   │  PHASE 1: CONNECTIVITY PROBE       │
                   │                                   │
                   │  For each protocol:                │
                   │  ┌─────────────────────────────┐   │
                   │  │ 1. TCP/UDP port reachable?  │   │
                   │  │ 2. Handshake success?       │   │
                   │  │ 3. Measure RTT              │   │
                   │  │ 4. Measure packet loss %    │   │
                   │  │ 5. Score [0-100]            │   │
                   │  └─────────────────────────────┘   │
                   └────────────┬──────────────────────┘
                                │
                   ┌────────────▼──────────────────────┐
                   │  PHASE 2: SELECTION                │
                   │                                   │
                   │  Weighted decision:                │
                   │  - Available + score > 50          │
                   │  - Lowest latency wins             │
                   │  - Fallback if all scores < 50     │
                   └────────────┬──────────────────────┘
                                │
                   ┌────────────▼──────────────────────┐
                   │  PHASE 3: TUNNEL ESTABLISHMENT     │
                   │                                   │
                   │  - Spin up per-user pod (if not    │
                   │    already running on K3s)         │
                   │  - Bind to user-specific port      │
                   │  - Route DNS through AdGuard       │
                   └────────────┬──────────────────────┘
                                │
                   ┌────────────▼──────────────────────┐
                   │  PHASE 4: CONTINUOUS MONITORING    │
                   │                                   │
                   │  Every 30 seconds:                 │
                   │  - Check current tunnel health     │
                   │  - Measure latency jitter          │
                   │  - Detect packet loss spikes       │
                   │  - If degraded (score < 30):       │
                   │    → Attempt protocol switch        │
                   └────────────┬──────────────────────┘
                                │
                   ┌────────────▼──────────────────────┐
                   │  PHASE 5: GRACEFUL SWITCH          │
                   │                                   │
                   │  - Set connection state: "SWITCH"  │
                   │  - Establish new tunnel in parallel│
                   │  - Route traffic to new tunnel     │
                   │  - Tear down old tunnel            │
                   │  - Log switch event                │
                   └────────────────────────────────────┘
```

### 4.3 Scoring Formula (Judge-Friendly Explanation)

```
Protocol Score = (Latency_Weight × RTT_Score)
              + (Availability_Weight × 100 if reachable else 0)
              + (Loss_Weight × (1 - packet_loss_rate))

Where:
  - Ideal RTT < 50ms → score 100
  - RTT > 500ms → score 0
  - Packet loss > 10% → penalty
```

### 4.4 Server-Side Protocol Configuration

**WireGuard (Gateway Operator-managed):**
```yaml
# Per-user WireGuard config (generated by operator)
apiVersion: vpn.niwa.dev/v1
kind: UserVPN
metadata:
  name: user-abc123
spec:
  protocol: wireguard
  port: 51820
  allowedIPs: ["0.0.0.0/0"]
  dns: ["10.43.0.10"]  # Points to AdGuard
  mtu: 1420
  persistentKeepalive: 25
```

**Hysteria 2 (Server-side):**
```json
{
  "listen": ":443",
  "protocol": "hysteria2",
  "obfs": {
    "type": "salamander",
    "password": "DYNAMIC_PER_USER"
  },
  "masquerade": {
    "type": "proxy",
    "proxy": {
      "url": "https://www.wikipedia.org",
      "rewriteHost": true
    }
  },
  "quic": {
    "initStreamReceiveWindow": 8388608,
    "maxStreamReceiveWindow": 8388608,
    "initConnReceiveWindow": 20971520,
    "maxConnReceiveWindow": 20971520,
    "maxIdleTimeout": "30s"
  }
}
```

**VLESS-REALITY + XTLS Vision (Server-side):**
```json
{
  "inbounds": [{
    "port": 443,
    "protocol": "vless",
    "settings": {
      "clients": [{"id": "USER_UUID", "flow": "xtls-rprx-vision"}],
      "decryption": "none",
      "fallbacks": [{"dest": 8080}]
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "dest": "www.wikipedia.org:443",
        "serverNames": ["www.wikipedia.org"],
        "privateKey": "SERVER_PRIVKEY",
        "shortIds": ["SHORT_ID"]
      }
    }
  }],
  "outbounds": [{
    "protocol": "freedom",
    "settings": {}
  }]
}
```

### 4.5 CRD Operator Extension (From Legacy Plan)

Extend the original WireGuard operator concept to handle multi-protocol:

```yaml
apiVersion: vpn.niwa.dev/v1
kind: UserVPN
metadata:
  name: user-abc123
spec:
  protocols:
    - wireguard:
        enabled: true
        port: 51820
        publicKey: "..."
        presharedKey: "..."
    - hysteria2:
        enabled: true
        port: 443
        obfuscation: salamander
        password: "SESSION_BOUND_KEY"
    - vlessReality:
        enabled: true
        port: 443
        uuid: "USER_UUID"
        flow: xtls-rprx-vision
  sessionBinding:
    type: PSK  # Pre-shared key per session
    rotation: 24h
  dnsFiltering:
    enabled: true
    adguardInstance: "user-abc123"
  status: active
```

The operator:
1. Creates/tears down per-user pods with the appropriate protocol stack
2. Manages MetalLB IP assignments
3. Updates DNS (AdGuard Home) when a user connects
4. Reports link quality metrics to the monitoring stack

---

## 5. Per-User Isolation & Session Binding

### 5.1 Architecture (Legacy Concept, Enhanced)

Each authenticated user gets:
- **Dedicated Kubernetes pod** with their protocol stack
- **Unique MetalLB IP** (10.0.0.100+)
- **Per-session PSK** (rotated every 24h or on disconnect)
- **Dedicated AdGuard Home client** (DNS queries logged per-user)

### 5.2 Session-Bound PSK

```
User authenticates → API generates session token →
  → Token includes: {user_id, expiry, PSK material}
  → PSK derived from: SHA256(session_token + server_secret)
  → PSK embedded in WireGuard config (or Hysteria 2 password)
  → Server stores: {user_id, pubkey, PSK_hash, expiry}
  → On disconnect: PSK invalidated immediately

Even with a full WireGuard config export, another device
cannot connect — the PSK changes every session.
```

### 5.3 Authentication Flow

```
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│  Client  │     │  Traefik │     │   API    │     │  VPN     │
│  App     │     │  (K3s)   │     │  Server  │     │  Pod     │
└────┬─────┘     └────┬─────┘     └────┬─────┘     └────┬─────┘
     │                 │                 │                 │
     │  POST /login    │                 │                 │
     │  (user, pass)   │                 │                 │
     ├────────────────►│────────────────►│                 │
     │                 │                 │                 │
     │                 │     Verify      │                 │
     │                 │  credentials    │                 │
     │                 │◄────────────────│                 │
     │                 │                 │                 │
     │  200 OK         │                 │                 │
     │  + session_token│                 │                 │
     │◄────────────────│◄────────────────│                 │
     │                 │                 │                 │
     │  POST /connect   │                 │                 │
     │  (session_token,│                 │                 │
     │   desired_proto)│                 │                 │
     ├────────────────►│────────────────►│────────────────►│
     │                 │                 │  Create VPN pod │
     │                 │                 │  Generate PSK   │
     │                 │                 │  Assign MetalLB │
     │                 │                 │  IP             │
     │                 │                 │◄────────────────│
     │  200 OK         │                 │                 │
     │  {config, PSK,  │                 │                 │
     │   metalb_ip,    │                 │                 │
     │   dns_servers}  │                 │                 │
     │◄────────────────│◄────────────────│                 │
     │                 │                 │                 │
     │  Establish      │                 │                 │
     │  tunnel using   │                 │                 │
     │  negotiated     │                 │                 │
     │  protocol       │                 │                 │
     ├────────────────────────────────────────────────────►│
     │                 │                 │                 │
     │  WireGuard/Hy2/ │                 │                 │
     │  REALITY tunnel │                 │                 │
     │◄────────────────────────────────────────────────────│
```

---

## 6. "Open Source Community DNS Security" (AdGuard Home)

### 6.1 Judge-Friendly Framing

> *"Before any data reaches your device, every DNS query is checked against a community-maintained threat intelligence database. This blocklist — compiled by security researchers worldwide — catches malware command-and-control servers, phishing domains, trackers, and advertising networks.
>
> Unlike commercial antivirus that keeps its threat lists secret, this uses **open-source community blocklists** (StevenBlack, OISD, phishing.database) that are transparent, auditable, and free."*

### 6.2 Technical Implementation

- **AdGuard Home** runs as a K3s deployment with persistent storage
- **Per-user DNS filtering** — each user gets their own AdGuard client entry for per-query logging
- **Blocklists configured**:
  - StevenBlack Unified (ads + trackers + malware)
  - OISD Full (threat intelligence)
  - Phishing Army (phishing protection)
- **Dashboard**: Each user can see blocked query stats (total blocked, top blocked domains, hourly activity) through the Web Dashboard

### 6.3 DNS Flow

```
Client → VPN Tunnel → AdGuard Home (per-user instance) →
  → Check blocklists → Allowed → Upstream DNS (Cloudflare / Quad9)
                      → Blocked → Return 0.0.0.0 + log event
```

### 6.4 K3s Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: adguard-home
spec:
  replicas: 2
  selector:
    matchLabels:
      app: adguard-home
  template:
    metadata:
      labels:
        app: adguard-home
    spec:
      containers:
      - name: adguard-home
        image: adguard/adguardhome:latest
        ports:
        - containerPort: 53
          name: dns
          protocol: UDP
        - containerPort: 80
          name: http
        volumeMounts:
        - name: adguard-config
          mountPath: /opt/adguardhome/conf
        - name: adguard-data
          mountPath: /opt/adguardhome/work
```

---

## 7. Client Applications

### 7.1 Android App (Branded "Niwa Secure")

**Purpose**: Primary client for mobile users (target audience for a science fair demo).

**Technology Stack**:
- **Language**: Kotlin (Jetpack Compose for UI)
- **VPN Integration**: Android `VpnService` API (TUN-based)
- **Protocol Libraries**: WireGuard Kotlin library, Go bindings for Hysteria 2 / Xray via gomobile
- **API Communication**: Retrofit + OkHttp (HTTPS to K3s Traefik endpoint)

**Features**:

| Feature | Description |
|---|---|
| **One-tap Connect** | Big button — app auto-probes and connects to best protocol |
| **Protocol Badge** | Shows current protocol (WG / Hy2 / VLESS) with color indicator |
| **Latency Display** | Live tunnel RTT in ms (green/yellow/red) |
| **DNS Shield** | Shows blocked threats count today |
| **Protocol Switcher** | Manual override (Auto / WireGuard / Hysteria 2 / VLESS-REALITY) |
| **Session Timer** | Duration of current VPN session |
| **Data Counter** | Per-session data usage |
| **Server Picker** | If multiple exit nodes deployed |
| **Dark Mode** | Science fair aesthetic — sleek dark theme with accent colors |

**WireGuard Integration (Native):**
```kotlin
// Uses Android's VpnService.Builder to create TUN
// and WireGuard's Go library via JNI
val builder = VpnService.Builder()
builder.setMtu(1420)
builder.addAddress("10.0.2.X", 32)
builder.addRoute("0.0.0.0", 0)
builder.addDnsServer("10.43.0.10")  // AdGuard Home
builder.setSession("Niwa Secure")
val tunInterface = builder.establish()
```

**Hysteria 2 / VLESS-REALITY Integration:**
```kotlin
// For non-WireGuard protocols, use gomobile bindings
// The Go engine binary exposes:
interface VpnEngine {
    fun start(configJson: String, tunFd: Int): Boolean
    fun stop()
    fun getStats(): ConnectionStats
    fun getLatency(): Int
}
```

**App Screens (Wireframe):**
```
┌──────────────────────────┐
│   ☰ Niwa Secure          │
│                          │
│      ┌─────────┐         │
│      │  🔒     │         │
│      │ CONNECT │         │
│      └─────────┘         │
│                          │
│   Protocol: Hysteria 2   │
│   Latency:  127ms 🟢    │
│   Threats blocked: 47   │
│   Session: 00:23:15     │
│   Data: 342 MB          │
│                          │
│  ─── Auto ──────────────│
│  ○ WireGuard            │
│  ● Hysteria 2           │
│  ○ VLESS-REALITY        │
│  ○ Manual switch        │
└──────────────────────────┘
```

### 7.2 C# / .NET Desktop App (Windows Alternative)

If a native Windows app is desired for demo purposes (runs alongside the Web Dashboard):

- **Technology**: .NET 8 with WPF or Avalonia UI
- **TUN**: WireGuard NT driver or Wintun
- **Same feature set** as the Android app
- **Systray integration** with quick-connect

---

## 8. Web Dashboard

### 8.1 Purpose

A Progressive Web App (PWA) accessible from any browser on PC — used by users to manage their VPN, or by parents to monitor family accounts.

### 8.2 Technology Stack

| Component | Technology |
|---|---|
| **Frontend** | React / Next.js (PWA) |
| **UI Framework** | Tailwind CSS + shadcn/ui |
| **Real-time** | WebSocket (for live connection status) |
| **State** | React Query + Zustand |
| **Charts** | Chart.js / Recharts (for statistics) |

### 8.3 Dashboard Pages

**Login / Registration:**
- Email + password
- QR code linking (scan from Android app to pair)
- Demo mode (for science fair — "Try without signing up")

**Main Dashboard:**
```
┌──────────────────────────────────────────────────────────┐
│  Niwa Secure Dashboard                      👤 John      │
├──────────┬───────────┬───────────┬───────────────────────┤
│ 🔒 Status │ Protocol  │ Latency   │ 🛡️ DNS Threats Today  │
│ ● Online  │ Hysteria2 │ 127ms 🟢  │ 47 blocked           │
├──────────┴───────────┴───────────┴───────────────────────┤
│                                                          │
│ ┌─────┐ ┌─────┐ ┌──────┐ ┌──────┐ ┌──────────┐        │
│ │📱   │ │💻   │ │🔄    │ │📊   │ │⚙️       │        │
│ │Dev. │ │Apps │ │Proto.│ │Stats │ │Settings  │        │
│ └─────┘ └─────┘ └──────┘ └──────┘ └──────────┘        │
│                                                          │
│ Recent Activity:                                         │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ 14:23  Protocol switched: WireGuard → Hysteria 2    │ │
│ │ 14:22  DNS blocked: doubleclick.net (tracker)       │ │
│ │ 14:20  Connected (session #47)                      │ │
│ └─────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

**Statistics Page:**
- Bandwidth usage over time (hourly/daily/weekly)
- DNS block rate % (how many queries blocked vs allowed)
- Protocol reliability (uptime % per protocol)
- Session history log

**Settings Page:**
- Protocol priority order (drag to re-rank: WG, Hy2, VLESS)
- DNS blocklist categories (ads, malware, phishing, social media)
- Kill switch toggle (block all traffic if VPN drops)
- Session timeout
- Dark/Light theme

**Admin Panel (for demo):**
- View all active users and their current protocol
- Per-user bandwidth charts
- System health
- QR code generator for Android app pairing

---

## 9. Observability & Monitoring

### 9.1 Prometheus + Grafana

Deploy the standard K3s monitoring stack, extended with VPN-specific metrics.

**Key Metrics:**

| Metric | Source | Why |
|---|---|---|
| `vpn_active_users` | Operator | Live user count |
| `vpn_protocol_distribution` | API | % of users on each protocol |
| `vpn_protocol_switch_count` | API | How often auto-probe switches |
| `vpn_latency_p50_p95` | Client heartbeat | Per-protocol latency |
| `adguard_blocked_total` | AdGuard API | DNS threats blocked |
| `adguard_queries_total` | AdGuard API | Total DNS queries |
| `node_udp_buffer_drops` | Node exporter | UDP performance issues |
| `node_bandwidth_total` | Node exporter | Aggregate throughput |

### 9.2 Demo Dashboard (Grafana)

Pre-configure a science-fair-ready Grafana dashboard showing:
- Live connection map (users → pods → protocols)
- Protocol distribution pie chart
- DNS threat blocking counter (with fun "threats foiled" animation)
- System resource usage
- Latency timelines

---

## 10. Science Fair Demonstration Plan

### 10.1 The Demo Setup

| Station | What It Shows |
|---|---|
| **Laptop 1** | Grafana dashboard — live metrics, protocol switching, DNS blocks |
| **Laptop 2** | Web Dashboard — user perspective, connect/disconnect, stats |
| **Android Tablet** | Niwa Secure Android app — one-tap connect, protocol badge |
| **Network Monitor** | A second phone showing a packet capture — "before" (raw HTTP with trackers) vs "after" (encrypted tunnel, DNS filtered) |

### 10.2 The Demo Script (45-60 seconds)

> *"This is the Swiss Knife of Digital Security — a VPN system that's smarter than any off-the-shelf solution.*
>
> *When I tap connect [tap Android app], the app runs a real-time network probe. It checks three different encrypted protocols — measures which one has the best connection right now, and automatically picks it. [Shows protocol badge switching from probing to Hysteria 2]*
>
> *Once connected, every DNS query runs through our open-source threat intelligence filter. [Show dashboard blocked counter climbing] See those numbers climbing? Every one of those is a tracker, ad, or malware domain that never reached the device.*
>
> *If the network conditions change — say a firewall starts blocking UDP — the system detects the degradation and seamlessly switches to a different protocol. [Simulate: switch to VLESS-REALITY] No interruption, no user intervention.*
>
> *Every user gets their own isolated tunnel in our Kubernetes cluster — one user's traffic is cryptographically separated from another's. And it's all manageable from this web dashboard or the Android app.*
>
> *The result: a connection that's private, resilient, and adaptive — true digital sovereignty."*

### 10.3 Demo Triggers (To Show Off)

| Trigger | What Happens | Visual Effect |
|---|---|---|
| Block UDP on firewall | Auto-switch from WireGuard/Hy2 → VLESS-REALITY (TCP) | Protocol badge changes, log entry appears |
| Add new user | New pod spins up in K3s, MetalLB assigns IP | Grafana shows new active user |
| Visit tracking-heavy site | DNS queries spike, many blocked | Counter ticks up rapidly |
| High latency simulation | Protocol switches to fastest alternative | Latency display updates |

### 10.4 Physical Poster / Display

Suggested poster sections:
1. **The Problem** — Internet tracking, censorship, fragile connections
2. **Our Solution** — Multi-protocol adaptive VPN with per-user isolation
3. **How It Works** — Simplified version of the architecture diagram
4. **Live Demo** — QR code to launch dashboard
5. **Results** — Benchmarks, blocking statistics, reliability metrics

---

## 11. Implementation Phases

### Phase 1: Foundation (Weeks 1-2)
- [ ] Set up Proxmox VMs (OPNsense, K3s master, workers)
- [ ] Configure OPNsense routing and NAT to MetalLB pool
- [ ] Install K3s cluster with MetalLB and Traefik
- [ ] Apply node-level sysctl tuning (UDP buffers, forwarding)
- [ ] Deploy AdGuard Home on K3s with community blocklists
- [ ] Deploy Prometheus + Grafana for base monitoring

### Phase 2: Core VPN Operator (Weeks 3-4)
- [ ] Build WireGuard CRD + operator (per-user pod lifecycle)
- [ ] Implement session-bound PSK authentication
- [ ] Implement MetalLB IP assignment per user
- [ ] Deploy test WireGuard pod and verify connectivity
- [ ] Build authentication API (user registration, login, tokens)
- [ ] Connect AdGuard Home per-user DNS filtering

### Phase 3: Multi-Protocol Engine (Weeks 5-6)
- [ ] Add Hysteria 2 server-side to K3s (deployment + config)
- [ ] Add VLESS-REALITY server-side (Xray-core deployment)
- [ ] Build auto-probe connectivity checker (protocol scoring)
- [ ] Implement graceful protocol switching logic
- [ ] Extend CRD operator for multi-protocol user pods
- [ ] Create protocol benchmark suite for demo

### Phase 4: Android App (Weeks 7-8)
- [ ] Build Kotlin app with Jetpack Compose UI
- [ ] Implement VpnService TUN interface
- [ ] WireGuard native integration (JNI/Go bindings)
- [ ] Hysteria 2 integration via gomobile
- [ ] VLESS-REALITY integration via Xray gomobile
- [ ] Auto-probe client-side connectivity check
- [ ] Protocol selection UI and display

### Phase 5: Web Dashboard (Weeks 9-10)
- [ ] Build Next.js application with Tailwind
- [ ] User authentication and session management
- [ ] Live connection status via WebSocket
- [ ] Statistics and charts (bandwidth, DNS blocks, protocol reliability)
- [ ] User settings (protocol priority, kill switch, theme)
- [ ] Admin panel for demo
- [ ] PWA configuration (installable, offline-capable)

### Phase 6: Demo Polish (Weeks 11-12)
- [ ] Grafana science fair dashboard
- [ ] Demo triggers and scripts
- [ ] Physical poster design
- [ ] Benchmark data collection
- [ ] Fallback / error handling hardening
- [ ] Presentation rehearsal and video recording

---

## 12. Critical Design Decisions & Trade-offs

### 12.1 Why K3s + Proxmox Instead of Single VPS?

| Approach | Pros | Cons |
|---|---|---|
| **K3s on Proxmox (Our choice)** | Per-user isolation, protocol operator, horizontal scaling, science-fair wow factor | Higher upfront setup complexity, requires enterprise server |
| **Single VPS + Docker** | Simpler, faster to deploy | No per-user isolation, harder to demo, less impressive to judges |
| **Single VPS + usque/Warp** | Fastest to ship | No science-fair depth, no custom engineering to show |

**Verdict**: For a science fair project, the K3s approach demonstrates more engineering depth, system design thinking, and has a better visual story.

### 12.2 Why Not Just WireGuard?

WireGuard is excellent but limited to UDP on a fixed port. For a *"Swiss Knife"* project, the multi-protocol adaptive engine is the core innovation — it's what separates this from a basic VPN setup. The auto-probe algorithm, graceful switching, and real-time protocol selection are genuine systems engineering problems worthy of a science fair.

### 12.3 Session-Bound PSK vs. Simple Key Pair

Session binding prevents config sharing — even if someone extracts the full WireGuard config from the app, it only works for that session. This demonstrates security engineering thinking beyond the baseline.

### 12.4 AdGuard Home Framing

Avoid mentioning "AdGuard" by brand name to judges. Frame it as:
> *"Open-source community DNS threat intelligence — a transparent, auditable blocklist maintained by security researchers that blocks malware, phishing, and invasive trackers at the network level."*

This is technically accurate and more impressive than "it's an ad blocker."

### 12.5 Observability Is a Feature, Not an Ops Tool

For the science fair, the monitoring stack is a **demonstration tool**, not just operational infrastructure. The live Grafana dashboard showing protocol switching, DNS blocks, and user activity is as much a part of the presentation as the Android app.

### 12.6 Android First, Desktop via Web

Android app is the primary client because:
- It demonstrates VpnService integration (real Android engineering)
- Mobile is where most users experience tracking and censorship
- The Web Dashboard covers desktop use cases without building a native app per OS

---

## Appendix A: Key Open Source Components

| Component | Role | License |
|---|---|---|
| **K3s** | Lightweight Kubernetes | Apache 2.0 |
| **WireGuard** | VPN protocol | GPL 2.0 |
| **Hysteria 2** | QUIC-based protocol (modified) | MIT |
| **Xray-core** | VLESS-REALITY + XTLS Vision | MPL 2.0 |
| **AdGuard Home** | DNS filtering | GPL 3.0 |
| **MetalLB** | Load balancer IP management | Apache 2.0 |
| **OPNsense** | Firewall / IDS | BSD 2-Clause |
| **Suricata** | IDS/IPS | GPL 2.0 |
| **CrowdSec** | Crowd-sourced IPS | MIT |
| **Prometheus + Grafana** | Monitoring | Apache 2.0 |
| **Traefik** | Ingress controller | MIT |

## Appendix B: Comparison to Legacy Plan

| Aspect | Legacy Plan | Revised Plan |
|---|---|---|
| Protocols | WireGuard only | WireGuard + Hysteria 2 + VLESS-REALITY with auto-probe |
| Client apps | Brief notes | Full Android app (Kotlin) + Web Dashboard (React/Next.js) |
| Auto-probing | Not present | Core innovation — 5-phase scoring algorithm |
| AdGuard framing | Technical only | Judge-friendly "Community DNS Threat Intelligence" |
| Science fair demo | Not addressed | Full demo plan, script, triggers, poster outline |
| Implementation phases | Deployment checklist | 6-phase, 12-week build plan |
| Protocol switching | Manual reconfig | Automatic, graceful, with live monitoring |

---

> **Next step after approval:** Begin Phase 1 (Foundation) — Proxmox VM provisioning and K3s cluster bootstrap. The operator (Phase 2) and multi-protocol engine (Phase 3) can be built partially in parallel once the cluster is running.
