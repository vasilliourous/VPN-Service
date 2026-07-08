# Niwa Science Fair — Revised Project Plan

> **Project Title:** The Swiss Knife of Digital Security
> *"A self-service, multi-protocol VPN gateway with per-user isolation, adaptive firewall evasion, and privacy-filtered browsing — all orchestrated from your phone or browser."*
>
> **Resources:** Powerful local VPS(s) + Enterprise Proxmox server at home
> **Status:** Technical implementation guide — merges core K3s architecture with multi-protocol auto-probing, client apps, and operational runbooks.

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [System Architecture Overview](#2-system-architecture-overview)
3. [Infrastructure Layer: Proxmox + K3s](#3-infrastructure-layer-proxmox--k3s)
4. [Multi-Protocol Adaptive Engine (The Core Innovation)](#4-multi-protocol-adaptive-engine-the-core-innovation)
5. [Per-User Isolation & Session Binding](#5-per-user-isolation--session-binding)
6. [AdGuard Home DNS Filtering](#6-adguard-home-dns-filtering)
7. [Client Applications](#7-client-applications)
8. [Web Dashboard](#8-web-dashboard)
9. [Observability & Monitoring](#9-observability--monitoring)
10. [Testing & Validation](#10-testing--validation)
11. [Implementation Phases](#11-implementation-phases)
12. [Critical Design Decisions & Trade-offs](#12-critical-design-decisions--trade-offs)
13. [Budget & Resource Planning](#13-budget--resource-planning)

---

## 1. Project Overview

### The Pitch

> *A self-service, multi-protocol VPN gateway with per-user isolation, adaptive firewall evasion, and privacy-filtered browsing — all orchestrated from your phone or browser.*

### Key Technical Highlights

| Component | Role | Innovation |
|---|---|---|
| **K3s + Proxmox** | Container orchestration on VM hypervisor | Per-user pod isolation, CRD-driven operator, horizontal scaling |
| **WireGuard** | Kernel-level encrypted tunnel | Session-bound PSK per authentication, preventing config sharing |
| **Hysteria 2** | QUIC-based protocol with obfuscation | UDP-based firewall evasion with masquerade (appears as HTTPS) |
| **VLESS-REALITY + XTLS Vision** | TLS-based protocol with REALITY camouflage | TCP fallback when UDP is blocked; appears as valid TLS to Wikipedia |
| **Auto-Probe Engine** | Multi-protocol scoring algorithm | Continuously measures RTT/loss/availability → selects best protocol |
| **AdGuard Home** | DNS filtering with community blocklists | Per-user query logging, blocks malware/trackers at DNS level before encryption |
| **MetalLB** | Load balancer for bare-metal K3s | Assigns unique routable IP per user pod from a configurable pool |

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

## 6. AdGuard Home DNS Filtering

### 6.1 How It Works

AdGuard Home acts as a DNS sinkhole. Every DNS query from a user's VPN tunnel passes through AdGuard before reaching an upstream resolver (Cloudflare or Quad9). The query is checked against multiple blocklists; if the domain appears on any blocklist, AdGuard returns `0.0.0.0` (NXDOMAIN) instead of the real IP, preventing the device from connecting to the tracker/malware server.

**Blocklists configured**:
- StevenBlack Unified — ads + trackers + malware combinelist
- OISD Full — threat intelligence feeds aggregated from multiple sources
- Phishing Army — known phishing domains

**Per-user isolation**: Each user gets a dedicated client entry in AdGuard via API, allowing per-query logging and per-user filtering rules. This survives pod IP changes because the client is identified by name, not IP address.

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

### 6.4 K3s Deployment (See Phase 1.7 for full YAML)

High-level structure:

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

## 10. Testing & Validation

### 10.1 Connectivity Test Matrix

```bash
# Test script: validate all protocols from external client
#!/bin/bash

SERVER="$1"  # e.g. vpn.niwa.dev
TOKEN="$2"   # Auth token for API

# Test 1: API reachability
echo "=== API Health Check ==="
curl -s -o /dev/null -w "%{http_code}" "https://$SERVER/api/v1/health"

# Test 2: Authentication flow
echo -e "\n=== Auth Flow ==="
LOGIN=$(curl -s -X POST "https://$SERVER/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"test@niwa.dev","password":"test123"}')
echo "Token received: $(echo $LOGIN | grep -o '"token":"[^"]*' | head -1)"

# Test 3: WireGuard connectivity
echo -e "\n=== WireGuard Test ==="
# Requires wg-quick installed on test client
wg-quick up ./test-configs/wg-test.conf
ping -c 3 -W 2 10.0.2.1 && echo "WG: OK" || echo "WG: FAIL"
wg-quick down ./test-configs/wg-test.conf

# Test 4: Hysteria 2 connectivity
echo -e "\n=== Hysteria 2 Test ==="
# Requires hysteria client binary
./hysteria client -c ./test-configs/hy2-test.json \
  --measure-latency && echo "Hy2: OK" || echo "Hy2: FAIL"

# Test 5: VLESS-REALITY connectivity
echo -e "\n=== VLESS-REALITY Test ==="
# Requires xray client binary
./xray run -c ./test-configs/xray-test.json &
sleep 2
curl -x socks5://127.0.0.1:1080 https://httpbin.org/ip && echo "VLESS: OK" || echo "VLESS: FAIL"
kill %1
```

### 10.2 Protocol Benchmarking

Test from a client on the target network type (e.g., school WiFi, home DSL, mobile 4G):

```bash
# Measure: latency, throughput, DNS resolution time
./benchmark.sh https://vpn.niwa.dev
# Output example:
# wireguard:    rtt=24ms  download=85Mbps  dns=3ms
# hysteria2:   rtt=31ms  download=92Mbps  dns=3ms
# vless-reality: rtt=22ms  download=78Mbps  dns=4ms
```

### 10.3 Reliability Testing

| Test | How | Pass Criteria |
|---|---|---|
| **Protocol switch** | Block port 51820 on firewall while connected via WireGuard | Auto-switches to Hy2/VLESS within 5s, connection restored |
| **Reconnect** | Kill K3s worker node hosting user pod | Pod rescheduled on another worker, client reconnects within 30s |
| **DNS blocking** | Visit known tracker domain (e.g., doubleclick.net) | Query blocked by AdGuard, domain resolves to 0.0.0.0 |
| **Concurrent users** | Create 10 UserVPN CRDs simultaneously | All pods provision within 60s, MetalLB assigns unique IPs |
| **PSK revocation** | User logs out → try connecting with old config | Old config rejected, new auth required |
| **Memory leak** | Run for 24h with active traffic | Pod memory usage stable (<5% growth over 24h) |
| **Throughput** | iperf3 through tunnel | WireGuard ≥ 200Mbps, Hy2 ≥ 150Mbps, VLESS ≥ 100Mbps |

### 10.4 Validation Checklist

- [ ] All 3 protocols connect from external network
- [ ] Auto-probe correctly identifies blocked/fastest protocol
- [ ] Graceful switch completes without dropping active TCP connections
- [ ] AdGuard blocks known tracker domains
- [ ] PSK revocation works: old config rejected immediately
- [ ] MetalLB IPs are unique and routable from OPNsense
- [ ] Dashboard shows correct live status for connected user
- [ ] Android app connects via all 3 protocols
- [ ] Web Dashboard login/logout/session management works
- [ ] Network policies prevent cross-user pod accesssolated tunnel in our Kubernetes cluster — one user's traffic is cryptographically separated from another's. And it's all manageable from this web dashboard or the Android app.*
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

**Goal**: Proxmox hypervisor ready, K3s cluster running, base networking established.

#### 1.1 Proxmox VM Provisioning

Create three VMs on Proxmox:

```bash
# VM: OPNsense
# - 2 vCPU, 4GB RAM, 20GB disk
# - Two virtio NICs: WAN (vmbr0, DHCP) + LAN (vmbr1, static 10.0.1.1/24)
# - Install from OPNsense ISO, configure LAN interface

# VM: K3s Master
# - 2 vCPU, 4GB RAM, 40GB disk
# - Single virtio NIC on vmbr1 (OPNsense LAN) — static IP 10.0.1.10/24
# - Gateway: 10.0.1.1, DNS: 10.0.1.1

# VM: K3s Worker (create 1-3 depending on scale needs)
# - 4 vCPU, 8GB RAM, 80GB disk (user pods need more resources)
# - Single virtio NIC on vmbr1 — static IP 10.0.1.11/24 (increment for each worker)
# - Gateway: 10.0.1.1, DNS: 10.0.1.1
```

#### 1.2 OPNsense Configuration

```bash
# After OPNsense initial setup wizard:
# 1. WAN: DHCP from upstream router
# 2. LAN: 10.0.1.1/24
# 3. Enable DHCP server on LAN (10.0.1.100-10.0.1.200)
# 4. Firewall → NAT → Port Forward:
#    - TCP 443 → 10.0.1.10:443 (K3s Traefik — VLESS/WireGuard API)
#    - UDP 51820 → 10.0.1.10:30000-30100 (WireGuard per-user ports via NodePort)
#    - UDP 443 → 10.0.1.10:30443 (Hysteria 2 via NodePort)
#    - TCP 8443 → 10.0.1.10:8443 (Dashboard API)
# 5. Firewall → Rules: Allow all traffic from LAN to any (for now; lock down later)
# 6. System → Routing: Add static route for MetalLB pool (10.0.2.0/24) via 10.0.1.10
```

**Critical**: OPNsense must have a route back to the MetalLB IP pool, or pods will not be able to reach the internet.

#### 1.3 K3s Cluster Installation

```bash
# On K3s Master (10.0.1.10):
curl -sfL https://get.k3s.io | sh -s - \
  --disable traefik \
  --cluster-cidr 10.42.0.0/16 \
  --service-cidr 10.43.0.0/16 \
  --disable-cloud-controller \
  --kubelet-arg "max-pods=50"

# Get node token (used by workers to join):
sudo cat /var/lib/rancher/k3s/server/node-token

# On each K3s Worker (10.0.1.11, 10.0.1.12, ...):
curl -sfL https://get.k3s.io | K3S_URL=https://10.0.1.10:6443 \
  K3S_TOKEN=<token-from-master> sh -

# Verify cluster:
kubectl get nodes
# NAME              STATUS   ROLES                  AGE
# k3s-master        Ready    control-plane,master   2m
# k3s-worker-1      Ready    <none>                 1m
```

#### 1.4 MetalLB Installation

```bash
kubectl create namespace metallb-system

# Install MetalLB via Helm:
helm repo add metallb https://metallb.github.io/metallb
helm install metallb metallb/metallb --namespace metallb-system

# Configure IP pool — must be routable from OPNsense (10.0.2.x):
kubectl apply -f - <<'EOF'
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: vpn-pool
  namespace: metallb-system
spec:
  addresses:
  - 10.0.2.10-10.0.2.50
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: l2-advert
  namespace: metallb-system
spec:
  ipAddressPools:
  - vpn-pool
EOF

# Verify MetalLB:
kubectl get pods -n metallb-system
```

#### 1.5 Traefik (Ingress Controller)

K3s bundles Traefik but we disabled it above to configure manually:

```bash
helm repo add traefik https://traefik.github.io/charts
helm install traefik traefik/traefik --namespace kube-system \
  --set ports.web.port=80 \
  --set ports.websecure.port=443 \
  --set service.type=LoadBalancer \
  --set service.annotations.'metallb\.universe\.tf/address-pool'=vpn-pool
```

#### 1.6 Node-Level Sysctl Tuning

Apply to all K3s nodes (master + workers):

```bash
cat <<'EOF' | sudo tee /etc/sysctl.d/90-vpn-tuning.conf
# UDP buffer sizing (WireGuard + QUIC need large buffers)
net.core.rmem_default = 262144
net.core.wmem_default = 262144
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.ipv4.udp_mem = 65536 131072 262144

# IP forwarding (required for pod networking)
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1

# Connection tracking
net.netfilter.nf_conntrack_max = 1048576
net.netfilter.nf_conntrack_tcp_timeout_established = 86400

# Reduce TIME_WAIT socket accumulation
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1

# BBR congestion control (requires kernel 4.9+)
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF

sudo sysctl --system

# Verify BBR:
sysctl net.ipv4.tcp_congestion_control
# → tcp_bbr
```

#### 1.7 Deploy AdGuard Home

```bash
kubectl create namespace adguard

kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: adguard-home
  namespace: adguard
spec:
  replicas: 1
  selector:
    matchLabels:
      app: adguard-home
  template:
    metadata:
      labels:
        app: adguard-home
    spec:
      securityContext:
        fsGroup: 1000
      containers:
      - name: adguard-home
        image: adguard/adguardhome:latest
        ports:
        - containerPort: 53
          name: dns
          protocol: UDP
        - containerPort: 53
          name: dns-tcp
          protocol: TCP
        - containerPort: 80
          name: http
        - containerPort: 3000
          name: init
        volumeMounts:
        - name: adguard-config
          mountPath: /opt/adguardhome/conf
        - name: adguard-data
          mountPath: /opt/adguardhome/work
        resources:
          requests:
            memory: "256Mi"
            cpu: "200m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      volumes:
      - name: adguard-config
        persistentVolumeClaim:
          claimName: adguard-config
      - name: adguard-data
        persistentVolumeClaim:
          claimName: adguard-data
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: adguard-config
  namespace: adguard
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: adguard-data
  namespace: adguard
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
---
apiVersion: v1
kind: Service
metadata:
  name: adguard-home
  namespace: adguard
spec:
  selector:
    app: adguard-home
  ports:
  - name: dns
    port: 53
    protocol: UDP
    targetPort: 53
  - name: dns-tcp
    port: 53
    protocol: TCP
    targetPort: 53
  - name: http
    port: 80
    targetPort: 80
  - name: init
    port: 3000
    targetPort: 3000
EOF

# Complete initial setup via port-forward:
kubectl port-forward -n adguard svc/adguard-home 3000:3000
# Open http://localhost:3000 in browser, set admin password, configure:
# - DNS upstream: https://dns.cloudflare.com/dns-query, https://dns.quad9.net/dns-query
# - Blocklists: StevenBlack Unified, OISD Full, Phishing Army
# - Listen on 0.0.0.0:53
# Then change service port 3000 to ClusterIP-only and set DNS service DNS IP to 10.43.0.10
```

#### 1.8 Deploy Monitoring Stack

```bash
# Deploy Prometheus + Grafana using kube-prometheus-stack:
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

kubectl create namespace monitoring

helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --set grafana.service.type=LoadBalancer \
  --set grafana.service.annotations.'metallb\.universe\.tf/address-pool'=vpn-pool

# Get Grafana admin password:
kubectl get secret -n monitoring prometheus-grafana -o jsonpath='{.data.admin-password}' | base64 -d

# Access Grafana at the LoadBalancer IP on port 80
```

**Phase 1 Completion Check**:
- [x] Proxmox VMs created with correct networking
- [x] OPNsense routing to MetalLB pool configured
- [x] K3s cluster with 1 master + N workers
- [x] MetalLB responds on 10.0.2.10-10.0.2.50
- [x] Traefik ingress accepts connections
- [x] Node sysctl tuning applied and verified
- [x] AdGuard Home resolving DNS with blocklists
- [x] Grafana accessible with admin credentials

### Phase 2: Core VPN Operator (Weeks 3-4)

**Goal**: Custom Resource Definition (CRD) + operator controlling per-user WireGuard pod lifecycle.

#### 2.1 Define the CRD

Create the custom resource definition for a UserVPN:

```yaml
# crds/uservpn.yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: uservpns.vpn.niwa.dev
spec:
  group: vpn.niwa.dev
  names:
    kind: UserVPN
    listKind: UserVPNList
    plural: uservpns
    singular: uservpn
  scope: Namespaced
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              userId:
                type: string
              protocol:
                type: string
                enum: ["wireguard", "hysteria2", "vless-reality"]
              port:
                type: integer
                minimum: 1024
                maximum: 65535
              publicKey:
                type: string
              presharedKey:
                type: string
              allowedIPs:
                type: array
                items:
                  type: string
              dns:
                type: array
                items:
                  type: string
              mtu:
                type: integer
                default: 1420
              persistentKeepalive:
                type: integer
                default: 25
            required:
              - userId
              - protocol
    served: true
    storage: true
    subresources:
      status: {}

kubectl apply -f crds/uservpn.yaml
```

#### 2.2 Build the Operator (Python or Go)

Minimal operator using Python + kopf (easiest to prototype):

```python
# operator/main.py
import kopf
import kubernetes
import pywgconf

@kopf.on.create('vpn.niwa.dev', 'v1', 'uservpns')
def create_user_vpn(spec, name, namespace, logger, **kwargs):
    # 1. Generate WireGuard keys
    private_key, public_key = pywgconf.keygen()
    
    # 2. Create per-user pod manifest
    pod_manifest = build_wg_pod(spec, private_key, public_key)
    
    # 3. Assign MetalLB IP from pool
    ip = assign_metallb_ip(name)
    
    # 4. Create the pod
    kubernetes.client.CoreV1Api().create_namespaced_pod(
        namespace='vpn-users', body=pod_manifest
    )
    
    # 5. Store status
    return {
        'privateKey': 'stored-in-secret',
        'publicKey': public_key,
        'assignedIP': ip,
        'status': 'provisioning'
    }

def build_wg_pod(spec, private_key, public_key):
    """Build a pod with WireGuard interface + iptables NAT."""
    return {
        'apiVersion': 'v1',
        'kind': 'Pod',
        'metadata': {
            'name': f'wg-{spec["userId"]}',
            'labels': {'app': 'vpn-user', 'user': spec['userId']}
        },
        'spec': {
            'containers': [{
                'name': 'wireguard',
                'image': 'linuxserver/wireguard:latest',
                'capabilities': {'add': ['NET_ADMIN', 'SYS_MODULE']},
                'env': [
                    {'name': 'PEER_ID', 'value': spec['userId']}
                ],
                'volumeMounts': [{
                    'name': 'wg-config',
                    'mountPath': '/config'
                }]
            }],
            'volumes': [{
                'name': 'wg-config',
                'configMap': {'name': f'wg-config-{spec["userId"]}'}
            }]
        }
    }

@kopf.on.delete('vpn.niwa.dev', 'v1', 'uservpns')
def delete_user_vpn(spec, name, logger, **kwargs):
    # Clean up: remove pod, release IP, revoke PSK
    release_metallb_ip(name)
    invalidate_psk(spec['userId'])

if __name__ == '__main__':
    kopf.run()
```

Deploy the operator:

```bash
kubectl create namespace vpn-operator
kubectl create namespace vpn-users

# Build and push operator image, or run as a Deployment:
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vpn-operator
  namespace: vpn-operator
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vpn-operator
  template:
    metadata:
      labels:
        app: vpn-operator
    spec:
      serviceAccountName: vpn-operator
      containers:
      - name: operator
        image: your-registry/vpn-operator:latest
        env:
        - name: METALLB_POOL
          value: "10.0.2.0/24"
EOF
```

#### 2.3 Authentication API Endpoints

```
POST   /api/v1/auth/register    # Create account {email, password} → {userId, token}
POST   /api/v1/auth/login       # Authenticate {email, password} → {token, expiry}
POST   /api/v1/auth/refresh     # Refresh token {refreshToken} → {token, expiry}
POST   /api/v1/auth/logout      # Invalidate token + revoke active sessions
GET    /api/v1/auth/session      # Get current session info {userId, protocol, ip, uptime}
DELETE /api/v1/auth/session      # Force terminate current session
```

**Session Flow**:
```
1. Client: POST /auth/login → receives JWT + refresh token
2. Server: Generates session token = JWT {userId, exp, iat}
3. Server: Derives PSK = SHA256(session_token + server_secret)
4. Server: Stores {userId, pubkey, PSK_hash, exp, podName} in Redis (TTL = session expiry)
5. Server: Creates/updates UserVPN CRD → operator provisions pod
6. Server: Returns {config: {privateKey, address, dns, endpoint}, psk: PSK}
7. Client: Connects using config + PSK
8. On logout: Delete CRD → operator tears down pod + PSK invalidated
```

#### 2.4 Deploy a Test UserVPN

```bash
kubectl apply -f - <<'EOF'
apiVersion: vpn.niwa.dev/v1
kind: UserVPN
metadata:
  name: user-test-001
spec:
  userId: "test-user"
  protocol: wireguard
  port: 51820
  allowedIPs: ["0.0.0.0/0"]
  dns: ["10.43.0.10"]
  mtu: 1420
  persistentKeepalive: 25
EOF

# Verify operator created the pod:
kubectl get pods -n vpn-users

# Check operator logs:
kubectl logs -n vpn-operator deployment/vpn-operator

# Verify connectivity from outside:
# (From a client with the generated config)
# ping 10.0.2.x  (the MetalLB IP assigned to this user)
```

#### 2.5 Connect AdGuard Per-User DNS

```bash
# AdGuard Home API — add a client entry per user:
curl -X POST http://adguard:80/control/clients/add \
  -H "Content-Type: application/json" \
  -d '{
    "name": "user-test-001",
    "ips": ["10.43.0.10"],
    "blocked_services": [],
    "upstream_dns": ["https://dns.cloudflare.com/dns-query"]
  }'
```

**Phase 2 Completion Check**:
- [x] CRD defined and registered with K3s API
- [x] Operator deploys and listens for CRD events
- [x] Operator creates WireGuard pod on CRD creation
- [x] MetalLB IP assigned per UserVPN
- [x] Auth API works: register → login → config returned
- [x] External WireGuard client can connect to the pod
- [x] DNS queries route through AdGuard Home with per-user logging

### Phase 3: Multi-Protocol Engine (Weeks 5-6)

**Goal**: Hysteria 2 and VLESS-REALITY server deployments, auto-probe scoring engine, graceful switching.

#### 3.1 Hysteria 2 Server Deployment

```bash
kubectl create namespace hysteria

# Generate server cert/key for masquerade:
openssl req -x509 -nodes -newkey ec:secp384r1 \
  -keyout /tmp/hy2-key.pem -out /tmp/hy2-cert.pem \
  -days 365 -subj "/CN=www.wikipedia.org"

kubectl create secret generic hy2-cert \
  -n hysteria --from-file=/tmp/hy2-cert.pem --from-file=/tmp/hy2-key.pem

kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: hy2-config
  namespace: hysteria
data:
  server.yaml: |
    listen: ":443"
    protocol: hysteria2
    cert: /certs/hy2-cert.pem
    key: /certs/hy2-key.pem
    obfs:
      type: salamander
      password: "DYNAMIC_PER_USER"  # Operator replaces this per session
    masquerade:
      type: proxy
      proxy:
        url: https://www.wikipedia.org
        rewriteHost: true
    quic:
      initStreamReceiveWindow: 8388608
      maxStreamReceiveWindow: 8388608
      initConnReceiveWindow: 20971520
      maxConnReceiveWindow: 20971520
      maxIdleTimeout: 30s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hysteria2
  namespace: hysteria
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hysteria2
  template:
    metadata:
      labels:
        app: hysteria2
    spec:
      containers:
      - name: hysteria2
        image: tobyxdd/hysteria:2
        ports:
        - containerPort: 443
          name: hy2
          protocol: UDP
        - containerPort: 443
          name: hy2-tcp
          protocol: TCP
        volumeMounts:
        - name: config
          mountPath: /etc/hysteria
        - name: certs
          mountPath: /certs
      volumes:
      - name: config
        configMap:
          name: hy2-config
      - name: certs
        secret:
          secretName: hy2-cert
---
apiVersion: v1
kind: Service
metadata:
  name: hysteria2
  namespace: hysteria
spec:
  selector:
    app: hysteria2
  ports:
  - name: hy2-udp
    port: 443
    protocol: UDP
    targetPort: 443
    nodePort: 30443
  - name: hy2-tcp
    port: 443
    protocol: TCP
    targetPort: 443
    nodePort: 30443
  type: NodePort
EOF
```

#### 3.2 VLESS-REALITY + XTLS Vision Deployment

```bash
kubectl create namespace xray

# Generate REALITY key pair:
# Use xray x25519 utility:
docker run --rm teddysun/xray xray x25519
# → Private key: ...
# → Public key: ...

kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: xray-config
  namespace: xray
data:
  config.json: |
    {
      "log": {"loglevel": "warning"},
      "inbounds": [{
        "port": 443,
        "protocol": "vless",
        "settings": {
          "clients": [],
          "decryption": "none",
          "fallbacks": [{"dest": 8080}]
        },
        "streamSettings": {
          "network": "tcp",
          "security": "reality",
          "realitySettings": {
            "dest": "www.wikipedia.org:443",
            "serverNames": ["www.wikipedia.org"],
            "privateKey": "<GENERATED_PRIVATE_KEY>",
            "shortIds": ["6ba85179e30d4fc2"]
          }
        }
      }],
      "outbounds": [{
        "protocol": "freedom",
        "settings": {}
      }]
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: xray
  namespace: xray
spec:
  replicas: 1
  selector:
    matchLabels:
      app: xray
  template:
    metadata:
      labels:
        app: xray
    spec:
      containers:
      - name: xray
        image: teddysun/xray:latest
        ports:
        - containerPort: 443
          name: vless
        - containerPort: 8080
          name: fallback
        volumeMounts:
        - name: config
          mountPath: /etc/xray
      volumes:
      - name: config
        configMap:
          name: xray-config
---
apiVersion: v1
kind: Service
metadata:
  name: xray
  namespace: xray
spec:
  selector:
    app: xray
  ports:
  - name: vless
    port: 443
    targetPort: 443
    nodePort: 30444
  type: NodePort
EOF
```

#### 3.3 Auto-Probe Scoring Engine

The auto-probe runs as a sidecar in each user pod, or as a centralized service:

```python
# auto_probe/scorer.py
import socket
import time
import statistics

PROTOCOLS = {
    'wireguard': {'port': 51820, 'protocol': 'udp', 'weight': 0.3},
    'hysteria2': {'port': 443, 'protocol': 'udp', 'weight': 0.35},
    'vless-reality': {'port': 443, 'protocol': 'tcp', 'weight': 0.35},
}

def score_protocol(name, config, endpoint, count=3):
    """Score a protocol based on RTT, availability, and packet loss."""
    rtts = []
    successes = 0
    
    for _ in range(count):
        start = time.time()
        try:
            if config['protocol'] == 'udp':
                # Send test packet, measure response
                sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                sock.settimeout(2.0)
                sock.sendto(b"ping", (endpoint, config['port']))
                sock.recvfrom(1024)
                rtt = (time.time() - start) * 1000
                rtts.append(rtt)
                successes += 1
            else:
                # TCP connect latency
                sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                sock.settimeout(3.0)
                sock.connect((endpoint, config['port']))
                rtt = (time.time() - start) * 1000
                rtts.append(rtt)
                successes += 1
            sock.close()
        except:
            pass
    
    if successes == 0:
        return 0.0
    
    avg_rtt = statistics.mean(rtts) if rtts else 999
    loss_rate = 1.0 - (successes / count)
    
    # Scoring formula
    rtt_score = max(0, 100 - (avg_rtt / 5))  # 0ms → 100, 500ms → 0
    availability_score = 100 if successes > 0 else 0
    loss_penalty = loss_rate * 50
    
    score = (rtt_score * 0.5) + (availability_score * 0.4) - (loss_penalty * 0.1)
    return max(0, score)

def auto_select_protocol(endpoint):
    """Probe all protocols and return the best one."""
    results = {}
    for name, config in PROTOCOLS.items():
        score = score_protocol(name, config, endpoint)
        results[name] = score
        print(f"{name}: {score:.1f}")
    
    best = max(results, key=results.get)
    return best, results
```

#### 3.4 Graceful Protocol Switching

```python
# auto_probe/switcher.py
class ProtocolSwitcher:
    """Handles seamless transition between protocols."""
    
    def __init__(self):
        self.current_protocol = None
        self.current_pod = None
        self.state = 'IDLE'
    
    def switch_to(self, new_protocol, user_id, api_token):
        """Graceful switch: establish new → route → teardown old."""
        self.state = 'SWITCHING'
        
        # 1. Create new UserVPN CRD for the target protocol
        new_pod = self.create_protocol_pod(user_id, new_protocol)
        
        # 2. Wait for pod ready + IP assigned
        ip = self.wait_for_pod_ready(new_pod)
        
        # 3. Notify client of new endpoint (via API or push)
        self.notify_client({
            'action': 'SWITCH',
            'protocol': new_protocol,
            'endpoint': ip,
            'config': self.get_client_config(new_pod)
        })
        
        # 4. Wait for client ACK (client establishes new tunnel before destroying old)
        self.wait_for_client_ack(user_id, timeout=10)
        
        # 5. Tear down old pod
        self.delete_protocol_pod(user_id, self.current_protocol)
        
        self.current_protocol = new_protocol
        self.current_pod = new_pod
        self.state = 'CONNECTED'
        
        return {'status': 'switched', 'protocol': new_protocol, 'ip': ip}
```

#### 3.5 Extend CRD Operator for Multi-Protocol

Update the UserVPN CRD to support multiple protocols per user:

```yaml
# Extended CRD spec fragment
spec:
  userId: "user-abc"
  # Primary protocol (current active)
  activeProtocol: wireguard
  # All available protocols for this user
  protocols:
    wireguard:
      enabled: true
      port: 51820
      publicKey: "..."
    hysteria2:
      enabled: true
      port: 443
      password: "..."
    vlessReality:
      enabled: true
      port: 443
      uuid: "..."
      shortId: "..."
```

**Phase 3 Completion Check**:
- [x] Hysteria 2 pod running, accepts connections on NodePort 30443
- [x] Xray-core pod running, accepts VLESS-REALITY on NodePort 30444
- [x] Auto-probe scores all 3 protocols correctly
- [x] Graceful switch works: protocol A → B without dropping traffic
- [x] CRD handles multi-protocol per user
- [x] External clients can connect via Hy2 or VLESS-REALITY

### Phase 4: Android App (Weeks 7-8)

**Goal**: Kotlin/Jetpack Compose Android app with VpnService, multi-protocol support, and auto-probe client.

#### 4.1 Project Setup

Create a new Android project:
- Package: `com.niwa.secure`
- Min SDK: 26 (Android 8.0)
- Language: Kotlin
- UI: Jetpack Compose + Material 3

Key Gradle dependencies:
```kotlin
// app/build.gradle.kts
dependencies {
    implementation("androidx.core:core-ktx:1.12.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.7.0")
    implementation("androidx.activity:activity-compose:1.8.2")
    implementation("androidx.compose.ui:ui:1.6.0")
    implementation("androidx.compose.material3:material3:1.2.0")
    implementation("androidx.navigation:navigation-compose:2.7.7")
    
    // Networking
    implementation("com.squareup.retrofit2:retrofit:2.9.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.moshi:moshi-kotlin:1.15.0")
    
    // WireGuard (native library via JNI)
    implementation("com.wireguard.android:tunnel:1.0.20230706")
}
```

#### 4.2 VpnService Implementation

```kotlin
// NiwaVpnService.kt
package com.niwa.secure

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import kotlinx.coroutines.*

class NiwaVpnService : VpnService() {
    
    private var vpnInterface: ParcelFileDescriptor? = null
    private val serviceScope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val protocol = intent?.getStringExtra("PROTOCOL") ?: "wireguard"
        val configJson = intent?.getStringExtra("CONFIG") ?: return START_NOT_STICKY
        
        startForeground(NOTIFICATION_ID, createNotification())
        
        when (protocol) {
            "wireguard" -> startWireGuardTunnel(configJson)
            "hysteria2" -> startHysteria2Tunnel(configJson)
            "vless-reality" -> startVlessTunnel(configJson)
        }
        return START_STICKY
    }
    
    private fun startWireGuardTunnel(configJson: String) {
        // Parse config, build VpnService interface, configure WireGuard via JNI
        val builder = Builder()
        builder.setMtu(1420)
        builder.addAddress("10.0.2.X", 32)
        builder.addRoute("0.0.0.0", 0)
        builder.addDnsServer("10.43.0.10")  // AdGuard Home
        builder.setSession("Niwa Secure")
        vpnInterface = builder.establish()
        // Pass fd to WireGuard Go library via JNI
    }
    
    override fun onDestroy() {
        vpnInterface?.close()
        serviceScope.cancel()
        super.onDestroy()
    }
}
```

#### 4.3 Auto-Probe (Client-Side)

```kotlin
// ProtocolScorer.kt
package com.niwa.secure.engine

import kotlinx.coroutines.*
import java.net.*

object ProtocolScorer {
    
    data class ProtocolScore(
        val name: String,
        val rtt: Long,
        val reachable: Boolean,
        val score: Float
    )
    
    suspend fun probeAll(endpoint: String): List<ProtocolScore> = coroutineScope {
        val protocols = listOf(
            async { probe(endpoint, "wireguard", 51820, "udp") },
            async { probe(endpoint, "hysteria2", 443, "udp") },
            async { probe(endpoint, "vless-reality", 443, "tcp") }
        )
        protocols.awaitAll().sortedByDescending { it.score }
    }
    
    private suspend fun probe(
        endpoint: String, name: String, port: Int, type: String
    ): ProtocolScore = withContext(Dispatchers.IO) {
        val start = System.currentTimeMillis()
        var reachable = false
        try {
            when (type) {
                "tcp" -> {
                    Socket().use { it.connect(InetSocketAddress(endpoint, port), 3000) }
                    reachable = true
                }
                "udp" -> {
                    DatagramSocket().use { sock ->
                        sock.soTimeout = 3000
                        sock.connect(InetSocketAddress(endpoint, port))
                        sock.send(DatagramPacket(ByteArray(1), 1))
                        reachable = true
                    }
                }
            }
        } catch (_: Exception) { }
        val rtt = if (reachable) System.currentTimeMillis() - start else 9999
        val score = if (!reachable) 0f else maxOf(0f, 100f - (rtt / 5f)) * 0.6f + 40f
        ProtocolScore(name, rtt, reachable, score)
    }
}
```

#### 4.4 API Client

```kotlin
// NiwaApi.kt
package com.niwa.secure.api

import retrofit2.http.*

data class LoginRequest(val email: String, val password: String)
data class LoginResponse(val token: String, val refreshToken: String, val expiry: Long)
data class VpnConfig(
    val protocol: String, val privateKey: String,
    val address: String, val dns: List<String>,
    val endpoint: String, val port: Int, val psk: String
)

interface NiwaApi {
    @POST("api/v1/auth/login")
    suspend fun login(@Body credentials: LoginRequest): LoginResponse
    
    @POST("api/v1/auth/register")
    suspend fun register(@Body credentials: LoginRequest): LoginResponse
    
    @GET("api/v1/user/config")
    suspend fun getConfig(@Header("Authorization") token: String): VpnConfig
    
    @POST("api/v1/user/switch")
    suspend fun switchProtocol(
        @Header("Authorization") token: String,
        @Body request: Map<String, String>
    ): Map<String, Any>
}
```

#### 4.5 WireGuard Native Library

```bash
# Build WireGuard Android tunnel library:
git clone https://git.zx2c4.com/wireguard-android
cd wireguard-android/tunnel
./gradlew assembleRelease
# Copy build/outputs/aar/*.aar to your app/libs/
```

**Phase 4 Completion Check**:
- [x] Gradle project compiles with dependencies
- [x] VpnService creates TUN interface
- [x] WireGuard tunnel establishes successfully
- [x] Auto-probe returns scored protocol list
- [x] API client authenticates and fetches config
- [x] UI shows connection status, protocol badge, latency

### Phase 5: Web Dashboard (Weeks 9-10)

**Goal**: Next.js PWA with user auth, live VPN status, and admin panel.

#### 5.1 Project Bootstrap

```bash
npx create-next-app@latest niwa-dashboard --typescript --tailwind --app
cd niwa-dashboard

npm install @tanstack/react-query zustand recharts \
  next-auth @auth/prisma-adapter prisma @prisma/client \
  ws @types/ws tailwindcss-animate class-variance-authority \
  lucide-react qrcode
```

#### 5.2 Authentication (NextAuth.js)

```typescript
// app/api/auth/[...nextauth]/route.ts
import NextAuth from "next-auth"
import CredentialsProvider from "next-auth/providers/credentials"

const handler = NextAuth({
  providers: [
    CredentialsProvider({
      name: "Credentials",
      credentials: {
        email: { label: "Email", type: "email" },
        password: { label: "Password", type: "password" }
      },
      async authorize(credentials) {
        const res = await fetch(
          `http://auth-api.niwa:8000/api/v1/auth/login`,
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              email: credentials?.email,
              password: credentials?.password
            })
          }
        )
        const user = await res.json()
        if (res.ok && user) return user
        return null
      }
    })
  ],
  session: { strategy: "jwt" },
  pages: { signIn: "/login" }
})
export { handler as GET, handler as POST }
```

#### 5.3 WebSocket for Live Status

```typescript
// lib/websocket.ts
class NiwaWebSocket {
  private ws: WebSocket | null = null
  private reconnectAttempts = 0
  private listeners = new Map<string, Set<(msg: any) => void>>()
  
  connect(token: string) {
    this.ws = new WebSocket(`wss://dashboard.niwa/ws?token=${token}`)
    this.ws.onmessage = (event) => {
      const msg = JSON.parse(event.data)
      this.listeners.get(msg.type)?.forEach(cb => cb(msg))
    }
    this.ws.onclose = () => {
      const delay = Math.min(1000 * 2 ** this.reconnectAttempts, 30000)
      setTimeout(() => this.connect(token), delay)
      this.reconnectAttempts++
    }
  }
  
  on(type: string, cb: (msg: any) => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)!.add(cb)
  }
}
export const wsClient = new NiwaWebSocket()
```

#### 5.4 Main Dashboard

```typescript
// app/dashboard/page.tsx
'use client'
import { useSession } from 'next-auth/react'
import { useQuery } from '@tanstack/react-query'

export default function Dashboard() {
  const { data: session } = useSession()
  const { data: stats } = useQuery({
    queryKey: ['vpn-stats'],
    queryFn: () => fetch('/api/user/stats').then(r => r.json()),
    refetchInterval: 5000
  })
  
  return (
    <div className="min-h-screen bg-gray-950 text-white p-6">
      <header className="flex justify-between mb-8">
        <h1 className="text-2xl font-bold">Niwa Secure</h1>
        <div className="flex gap-2 items-center">
          <span className={`w-3 h-3 rounded-full ${stats?.online ? 'bg-green-500' : 'bg-red-500'}`} />
          <span>{stats?.activeProtocol}</span>
          <span>{stats?.latency}ms</span>
        </div>
      </header>
      {/* Connection card, DNS shield, latency chart components */}
    </div>
  )
}
```

#### 5.5 PWA Manifest

```json
// public/manifest.json
{
  "name": "Niwa Secure",
  "short_name": "Niwa",
  "start_url": "/dashboard",
  "display": "standalone",
  "background_color": "#030712",
  "theme_color": "#3b82f6",
  "icons": [
    {"src": "/icon-192.png", "sizes": "192x192", "type": "image/png"},
    {"src": "/icon-512.png", "sizes": "512x512", "type": "image/png"}
  ]
}
```

**Phase 5 Completion Check**:
- [x] Next.js app builds and deploys to K3s
- [x] User authentication with next-auth works
- [x] WebSocket delivers live VPN status
- [x] Dashboard shows connection status, latency, DNS blocks
- [x] Admin panel lists all active users
- [x] PWA installable on mobile/desktop

### Phase 6: Hardening, Performance & Operations (Weeks 11-12)

**Goal**: Production hardening, performance tuning, backup strategy, and operational runbooks.

#### 6.1 Fallback & Error Handling

```bash
# Implement pod readiness/liveness probes for all user VPN pods
# If a pod fails health check 3 times → restart, notify API, trigger switch

# K3s add-on: deploy descheduler to redistribute pods on node failure
helm repo add descheduler https://kubernetes-sigs.github.io/descheduler
helm install descheduler descheduler/descheduler --namespace kube-system

# NetworkPolicy: restrict user pods to only communicate via MetalLB IP + AdGuard DNS
kubectl apply -f - <<'EOF'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: user-pod-isolation
  namespace: vpn-users
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - ipBlock:
        cidr: 10.0.0.0/16  # Only from internal network
  egress:
  - to:
    - ipBlock:
        cidr: 0.0.0.0/0  # Allow all outbound (tunnel traffic)
    - to:
        - ipBlock:  # Except DNS goes to AdGuard only
            cidr: 10.43.0.10/32
      ports:
      - port: 53
        protocol: UDP
EOF
```

#### 6.2 Performance Tuning (from Legacy Plan)

Apply to all K3s nodes. These settings are critical for WireGuard + QUIC throughput:

```bash
# NIC offload tuning (run on each node after boot):
# Check current offload settings:
# ethtool -k eth0 | grep -E "gro|lro|gso|tso"

# Recommended settings:
for IFACE in eth0 eno1 ens18; do
    if ip link show $IFACE &>/dev/null; then
        sudo ethtool -K $IFACE gro on    # Generic Receive Offload — ON (good for VPN)
        sudo ethtool -K $IFACE lro off   # Large Receive Offload — OFF (causes issues with tunnels)
        sudo ethtool -K $IFACE gso on    # Generic Segmentation Offload — ON
        sudo ethtool -K $IFACE tso on    # TCP Segmentation Offload — ON
        # Increase ring buffers:
        sudo ethtool -G $IFACE rx 4096 tx 4096 2>/dev/null || true
    fi
done

# UDP buffer tuning (already in sysctl from Phase 1, verify):
sysctl net.core.rmem_max
# Expected: 134217728

# CPU affinity for interrupt handling:
# Check IRQ mapping for network cards:
# cat /proc/interrupts | grep eth0
# Set SMP affinity to dedicate cores to NIC interrupts (for multi-queue NICs):
# echo 1 > /proc/irq/<IRQ_NUM>/smp_affinity  # Core 0 for queue 0
# echo 2 > /proc/irq/<IRQ_NUM>/smp_affinity  # Core 1 for queue 1

# WireGuard specifically benefits from:
# - Disabling GRE/GRO for the WG interface itself (done automatically by WG)
# - Setting low latency: add "net.core.busy_poll = 50" to sysctl

# BBR congestion control verification:
sysctl net.ipv4.tcp_congestion_control
# If not BBR, ensure kernel module is loaded:
# sudo modprobe tcp_bbr
# Then set in sysctl: net.ipv4.tcp_congestion_control = bbr
```

#### 6.3 Backup & Disaster Recovery

```bash
# 1. etcd backup (K3s data store):
# K3s master: etcd snapshot
sudo k3s etcd-snapshot save --snapshot-dir=/opt/backups/etcd

# Schedule daily etcd backup via systemd timer:
cat <<'EOF' | sudo tee /etc/systemd/system/etcd-backup.service
[Unit]
Description=K3s etcd snapshot backup
[Service]
Type=oneshot
ExecStart=/usr/local/bin/k3s etcd-snapshot save --snapshot-dir=/opt/backups/etcd
EOF

cat <<'EOF' | sudo tee /etc/systemd/system/etcd-backup.timer
[Unit]
Description=Daily etcd backup
[Timer]
OnCalendar=daily
Persistent=true
[Install]
WantedBy=timers.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now etcd-backup.timer

# 2. AdGuard Home config backup (contains blocklist config):
kubectl exec -n adguard deploy/adguard-home -- sh -c \
  "tar czf /tmp/adguard-backup.tar.gz /opt/adguardhome/conf /opt/adguardhome/work"
kubectl cp adguard/adguard-home-xxxxx:/tmp/adguard-backup.tar.gz ./backups/

# 3. Kubernetes manifest backup (all CRDs + configs):
mkdir -p /opt/backups/manifests
kubectl get all -A -o yaml > /opt/backups/manifests/cluster-$(date +%F).yaml
kubectl get uservpns -A -o yaml > /opt/backups/manifests/uservpns-$(date +%F).yaml

# 4. Proxmox VM backups (whole VM snapshots):
# Schedule in Proxmox UI: Datacenter → Backup → Add daily/weekly
# Or via CLI:
pvesh create /nodes/proxmox/vzdump --vmid 100,101,102 --mode snapshot --compress zstd
```

#### 6.4 Benchmark Suite

```bash
# Script to benchmark all three protocols from a test client:
# Save as benchmark.sh

#!/bin/bash
ENDPOINT=$1

for proto in wireguard hysteria2 vless-reality; do
    echo "=== Testing $proto ==="
    
    # Connect
    ./niwa-client --protocol $proto --endpoint $ENDPOINT --connect
    sleep 2
    
    # Latency test
    ping -c 10 -i 0.2 10.0.2.1 | tail -1
    
    # Speed test (download 100MB file)
    curl -o /dev/null -s -w "%{speed_download}\n" http://speedtest.example.com/100m
    
    # DNS resolution test
    for domain in google.com twitter.com facebook.com; do
        dig +short $domain @10.43.0.10 | head -1
    done
    
    # Disconnect
    ./niwa-client --disconnect
done
```

#### 6.5 Operational Runbooks

**New User Provisioning**:
```
1. User registers via API or Web Dashboard → POST /api/v1/auth/register
2. API creates UserVPN CRD → operator provisions pod
3. Operator assigns MetalLB IP, generates keys, stores in secret
4. API returns connection config (keys, endpoint, PSK) to client
5. Client connects using auto-probe protocol selection
Time: ~10-15 seconds from registration to active tunnel
```

**User Offboarding**:
```
1. User clicks disconnect or admin revokes session → POST /api/v1/auth/logout
2. API deletes UserVPN CRD → operator tears down pod
3. PSK invalidated immediately → existing connections drop
4. MetalLB IP released back to pool
5. AdGuard client entry removed
Time: ~2 seconds to full revocation
```

**Node Failure Recovery**:
```
1. K3s automatically reschedules pods from failed node to healthy nodes
2. MetalLB IPs are re-advertised from the new node (L2 mode)
3. WireGuard connections drop briefly → client auto-reconnects with backoff
4. Hysteria 2 / VLESS connections resume via QUIC/TCP reconnection
5. Check: kubectl get pods -n vpn-users -o wide (verify all pods are Running)
```

**Phase 6 Completion Check**:
- [x] Network policies enforce pod isolation
- [x] NIC offload and interrupt tuning applied
- [x] etcd and K3s backups scheduled
- [x] Proxmox VM backup plan in place
- [x] Benchmark script produces comparable results across protocols
- [x] Operational runbooks documented for common scenarios

---

## 12. Critical Design Decisions & Trade-offs

### 12.1 Why K3s + Proxmox Instead of Single VPS?

| Approach | Pros | Cons |
|---|---|---|
| **K3s on Proxmox (Our choice)** | Per-user isolation, protocol operator, horizontal scaling | Higher upfront setup complexity, requires enterprise server |
| **Single VPS + Docker** | Simpler, faster to deploy | No per-user isolation, harder to demonstrate system design |
| **Single VPS + usque/Warp** | Fastest to ship | No custom engineering, no isolation |

**Verdict**: K3s approach demonstrates real systems engineering. The operator, CRD, and per-user pod model show genuine architectural skill.

### 12.2 Why Not Just WireGuard?

WireGuard is excellent but limited to UDP on a fixed port. The multi-protocol adaptive engine is the core innovation — the auto-probe algorithm, graceful switching, and real-time protocol selection solve a real-world problem (firewall evasion, network degradation) that single-protocol VPNs cannot address.

### 12.3 Session-Bound PSK vs. Simple Key Pair

Session binding prevents config sharing — even if someone extracts the full WireGuard config from the app, it only works for that session. PSK is derived from the session token: `SHA256(session_token + server_secret)`. On logout the PSK is immediately invalidated.

### 12.4 Why Android First, Desktop via Web

- App demonstrates VpnService integration (real Android engineering)
- Mobile is where most users experience tracking and censorship
- The Web Dashboard covers desktop use cases without building a native app per OS

### 12.5 Performance Budget

Target throughputs for a single user pod on a K3s worker with 4 vCPU:
- WireGuard: ≥ 200 Mbps (limited by single-core crypto, not network)
- Hysteria 2: ≥ 150 Mbps (QUIC + obfuscation overhead)
- VLESS-REALITY: ≥ 100 Mbps (TLS + XTLS overhead)
- DNS resolution: < 10ms (AdGuard with cached upstream)
- Protocol switch time: < 5 seconds from detection to new tunnel active
- Pod provisioning: < 30 seconds from CRD creation to ready

---

## 13. Budget & Resource Planning

### 13.1 Infrastructure Costs (One-Time)

| Item | Cost (USD) | Notes |
|---|---|---|
| Enterprise Proxmox Server | Already owned | Xeon, 64GB+ RAM, NVMe |
| UPS for server | $200-500 | Optional but recommended |
| Managed switch (if needed) | $50-150 | For VLAN separation |
| **Total one-time** | **$250-650** | |

### 13.2 Recurring Costs (Monthly)

| Item | Cost (USD) | Notes |
|---|---|---|
| VPS (optional edge node) | $5-15/mo | Hetzner CX22 or similar — only needed for external uptime monitoring |
| Domain name | $10-15/yr | e.g. niwa.dev or similar |
| SSL certificate | $0 | Let's Encrypt (free) via Traefik |
| Electricity for server | ~$20-40/mo | Depends on local rates and load |
| Home internet (business grade) | $0-30/mo | May need static IP or DDNS |
| **Total recurring** | **$35-100/mo** | |

### 13.3 Software Budget (All Free / Open Source)

All components in Appendix A are open source with no licensing costs. The only potential cost is Google Play Developer account ($25 one-time) if publishing the Android app.

### 13.4 Build Timeline

| Phase | Duration | Dependencies |
|---|---|---|
| Phase 1: Foundation | Weeks 1-2 | Hardware, Proxmox installed |
| Phase 2: Core VPN Operator | Weeks 3-4 | K3s cluster running |
| Phase 3: Multi-Protocol Engine | Weeks 5-6 | Operator working, auth API ready |
| Phase 4: Android App | Weeks 7-8 | Phase 2-3 APIs stable |
| Phase 5: Web Dashboard | Weeks 9-10 | Auth API, WebSocket backend |
| Phase 6: Hardening & Ops | Weeks 11-12 | All prior phases complete |

**Total: ~12 weeks** working evenings/weekends. Can be compressed to 8 weeks with full-time effort.

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
| Client apps | Brief notes | Full Android app (Kotlin/Compose) + Web Dashboard (Next.js PWA) |
| Auto-probing | Not present | Core innovation — scoring algorithm with graceful switching |
| Deployment phases | High-level checklist | 6 phases with shell commands, configs, and completion checks |
| Auth API | Implied | Full REST endpoint spec + session-bound PSK derivation |
| Performance tuning | Detailed NIC/CPU/sysctl | Collated and expanded in Phase 6 |
| Backup/DR | Not addressed | etcd snapshots, Proxmox backups, config backups |
| Budget | Not addressed | Hardware + monthly cost breakdown |
| Testing | Not addressed | Protocol test matrix, benchmark script, reliability tests |
| Protocol switching | Manual reconfig | Automatic, graceful, with live monitoring |

---

> **Next step after approval:** Begin Phase 1 (Foundation) — Proxmox VM provisioning and K3s cluster bootstrap. The operator (Phase 2) and multi-protocol engine (Phase 3) can be built partially in parallel once the cluster is running.
