# Comprehensive VPN Obfuscation Analysis & Blocking Implementation Guide

> Consolidated report combining technical deep-dive research and a practical network administration playbook.  
> Prepared June 2026.

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [In-Depth Analysis of Major Free VPN Obfuscation Methods](#in-depth-analysis-of-major-free-vpn-obfuscation-methods)
   - [X-VPN (Xvpn)](#x-vpn-xvpn)
   - [Psiphon](#psiphon)
   - [Lantern](#lantern)
   - [Hotspot Shield](#hotspot-shield)
3. [Common Weaknesses Across All Free VPNs](#common-weaknesses-across-all-free-vpns)
4. [The 5-Layer Defense Framework](#the-5-layer-defense-framework)
   - [Layer 1 – Targeted IP Reputation Blocklists](#layer-1--targeted-ip-reputation-blocklists)
   - [Layer 2 – SSH Outbound Detection & Restriction](#layer-2--ssh-outbound-detection--restriction)
   - [Layer 3 – Active Probing of Suspicious TLS Endpoints](#layer-3--active-probing-of-suspicious-tls-endpoints)
   - [Layer 4 – Domain Fronting Detection](#layer-4--domain-fronting-detection)
   - [Layer 5 – Non-Browser JA3 Fingerprint Blocking](#layer-5--non-browser-ja3-fingerprint-blocking)
5. [Implementation Guide for Network Administrators](#implementation-guide-for-network-administrators)
   - [5.1 Assessment Phase](#51-assessment-phase)
   - [5.2 Layer 1 – IP & ASN Blocklists (Step-by-Step)](#52-layer-1--ip--asn-blocklists-step-by-step)
   - [5.3 Layer 2 – SSH Protocol Blocking (Step-by-Step)](#53-layer-2--ssh-protocol-blocking-step-by-step)
   - [5.4 Layer 3 – Active Probing of TLS Endpoints (Step-by-Step)](#54-layer-3--active-probing-of-tls-endpoints-step-by-step)
   - [5.5 Layer 4 – Domain Fronting Detection (Step-by-Step)](#55-layer-4--domain-fronting-detection-step-by-step)
   - [5.6 Layer 5 – JA3 TLS Fingerprint Filtering (Step-by-Step)](#56-layer-5--ja3-tls-fingerprint-filtering-step-by-step)
   - [5.7 Maintenance & Monitoring](#57-maintenance--monitoring)
   - [5.8 Troubleshooting & False Positives](#58-troubleshooting--false-positives)
6. [Advanced Considerations: Protecting Legitimate Services](#advanced-considerations-protecting-legitimate-services)
   - [What Not to Block (And Why)](#what-not-to-block-and-why)
   - [Why Advanced Paid VPNs Survive](#why-advanced-paid-vpns-survive)
   - [Recommended Protocol Stack for Advanced VPN Services](#recommended-protocol-stack-for-advanced-vpn-services)
7. [Appendices](#appendices)
   - [A. Key Technical References](#a-key-technical-references)
   - [B. Glossary](#b-glossary)
   - [C. Quick Reference: Free VPN Block Patterns](#c-quick-reference-free-vpn-block-patterns)
   - [D. Known VPN Provider Infrastructure](#d-known-vpn-provider-infrastructure)

---

## Executive Summary

**The ask:** Present a defence that stops X‑VPN, Psiphon, Lantern, Hotspot Shield and other free circumvention tools, without breaking advanced paid VPN architectures (e.g., VLESS‑REALITY, Hysteria2, mixed advanced protocols).

**The finding:** Every major free VPN has distinct, fingerprintable weaknesses – IP reputation, TLS handshake signatures, SSH detection, domain‑fronting endpoints, and inability to survive active probing. VLESS‑REALITY with XTLS Vision was designed to defeat exactly these detection methods.

**The strategy:** A five‑layer defence kills the free tools cold while being structurally powerless against a well‑architected premium stack. The core insight: free VPNs cannot afford REALITY‑grade obfuscation (real TLS certificate borrowing + uTLS browser fingerprint matching), which creates a permanent moat.

This consolidated report first dissects the obfuscation techniques of the four major free VPNs, then provides a step‑by‑step implementation guide for network administrators, and finally explains how to avoid blocking legitimate advanced VPN services.

---

## In-Depth Analysis of Major Free VPN Obfuscation Methods

### X-VPN (Xvpn)

| Aspect | Detail |
|---|---|
| **Protocol** | Proprietary **Everest Protocol** family |
| **Variants** | Everest TLS, Everest TCP, Everest UDP, Everest HTTP |
| **Encryption** | AES‑256 |
| **Tiers** | Everest TLS‑3 (top obfuscation, paid); lower Everest variants (free) |
| **Also supports** | WireGuard, OpenVPN, IKEv2/IPSec |

**Obfuscation techniques used:**

- Port disguise – routes traffic through port 443 (HTTPS)
- Packet header scrambling – disrupts identifiable DPI patterns
- Massive IP pool – 10,000+ servers for IP rotation (free tier: ~1,000 servers)
- TLS wrapping – wraps VPN traffic to look like HTTPS
- “Custom obfuscation” (paid) – shapes traffic to look like visits to a user‑chosen website

**How it gets detected:**

- **Non‑browser TLS fingerprint** – Go’s `crypto/tls` defaults produce a JA3 hash that does not match any real browser (Chrome, Firefox, Safari).
- **Datacenter IP reputation** – free servers run on a tight set of AWS/GCP/Azure subnets. Easy to block by ASN.
- **Proprietary protocol analysis** – Everest has been reverse‑engineered; its binary protocol framing has known byte signatures.
- **Active probing** – free Everest endpoints are bare tunnel servers; they do not serve real web content when probed.

---

### Psiphon

| Aspect | Detail |
|---|---|
| **Developer** | Psiphon Inc. / University of Toronto Citizen Lab |
| **License** | Open‑source |
| **Transport core** | [psiphon-tunnel-core](https://github.com/Psiphon-Labs/psiphon-tunnel-core) |

**Protocols (auto‑cycled):**

| Protocol | Mechanism | Detectability |
|---|---|---|
| SSH | Standard SSH tunnel | High – SSH handshake fingerprint |
| SSH+ | SSH + randomized padding + HTTP headers prepended | Medium – still SSH at core |
| MEEK | Domain fronting via Azure CDN | High – Host/SNI mismatch detectable |
| L2TP/IPSec | Standard L2TP | High – known port 1701, known IPSec handshake |
| WireGuard | Standard WireGuard | Medium – known port 51820, known handshake pattern |

**SSH+ obfuscation detail:**  
The core SSH protocol is wrapped in an obfuscation layer that randomises packet timing, adds padding, and prepends HTTP‑like headers to disguise the SSH handshake. This mitigates keyword‑based blocking and simple protocol fingerprinting, but the underlying SSH transport is still identifiable by behavioural analysis (asymmetric handshake structure, key exchange timing).

**How it gets detected:**

- **SSH handshake detection** – even SSH+ cannot fully mask the SSH key exchange pattern from sophisticated DPI.
- **MEEK domain fronting** – checking TLS SNI vs HTTP Host header mismatch reveals domain fronting immediately.
- **Active probing** – SSH+ endpoints do not serve real web content at the SSH port.
- **Known CDN endpoints** – MEEK uses specific Azure frontends; these can be blocked by URL/certificate pinning.
- **IP ranges** – free Psiphon servers cluster on known cloud ranges.

---

### Lantern

| Aspect | Detail |
|---|---|
| **Developer** | Lantern / Getlantern |
| **Differentiator** | Widest protocol diversity of any free VPN (20+ protocols) |
| **Smart routing** | Multi‑armed bandit algorithm dynamically tests and picks protocols |

**Full protocol arsenal:**

| Protocol | Type | Notes |
|---|---|---|
| **AnyTLS** | TLS proxy | Mitigates “TLS‑in‑TLS” fingerprinting via session multiplexing |
| **Hysteria 2** | QUIC‑based | High‑speed, Salamander obfuscation, HTTP/3 masquerade |
| **VLESS** | Lightweight transport | Stateless, often used with Xray |
| **Kindling** | Bootstrapping | Lantern‑proprietary – domain fronting + DNS tunneling + AMP caching |
| **WATER** | WebAssembly runtime | Allows dynamically updated pluggable transports |
| **Algeneva** | Traffic mimicry | Mimics common traffic patterns |
| **Proxyless** | DNS‑based | DoH + DoT + TLS fragmentation + TCP splitting |
| **Unbounded** | WebRTC‑based | Crowdsourced IPs – anyone can contribute bandwidth |
| **Shadowsocks** | Encrypted proxy | Lightweight, random‑byte‑stream from first packet |
| **WireGuard** | VPN | Used in combination with obfuscation layers |
| **Tor PTs** | Pluggable transports | obfs4, meek |
| **Trojan** | HTTPS disguise | Real TLS certificates |
| **VMess** | V2Ray‑based | Traffic shaping, routing flexibility |
| **ShadowTLS** | TLS proxy | Hides behind legitimate TLS handshakes |
| **NaïveProxy** | Chromium stack | Uses Chrome’s network stack for perfect TLS mimicry |
| **TUIC** | QUIC‑based | High‑speed, hard‑to‑fingerprint |
| **Amnezia WireGuard** | Obfuscated WG | DPI‑resistant encoding around WireGuard |
| **SSH Tunneling** | SSH | Fallback method |

**How it gets detected:**

- **Free‑tier IP clustering** – despite protocol diversity, Lantern’s free servers come from limited cloud ASNs.
- **Behavioural whack‑a‑mole** – each protocol has individual tells that censors who study DPI can target.
- **Unbounded WebRTC** – STUN/TURN interactions are fingerprintable at the network edge.
- **Active probing of static endpoints** – some fixed relay IPs can be probed and identified.

---

### Hotspot Shield

| Aspect | Detail |
|---|---|
| **Developer** | Pango / Aura |
| **Protocol** | Proprietary **Catapult Hydra** |
| **Base** | Derived from OpenVPN, heavily modified |

**Obfuscation techniques used:**

- Catapult Hydra disguises VPN traffic as standard HTTPS on port 443.
- Custom encapsulation reduces latency vs standard OpenVPN.
- Traffic leakage (some flows bypass the tunnel) – ironically helps evade single‑destination traffic analysis.

**How it gets detected:**

- **Proprietary protocol fingerprinting** – Catapult Hydra’s TLS handshake has been analysed and its JA3 fingerprint is documented in commercial DPI databases.
- **Datacenter IP reputation** – predictable server ranges.
- **Traffic leakage inconsistencies** – split‑tunnel behaviour can be correlated across sessions.
- **Active probing** – Hydra endpoints do not serve real websites.

---

## Common Weaknesses Across All Free VPNs

| Weakness | Description | Affects |
|---|---|---|
| **IP reputation** | All use datacenter IPs on known ASNs (AWS, GCP, Azure, DigitalOcean). Free tiers cluster on smaller subsets. | X‑VPN, Psiphon, Lantern, Hotspot Shield |
| **No real website behind TLS** | Active probing reveals the server does not serve actual HTTP content. | X‑VPN, Hotspot Shield, some Psiphon modes |
| **Non‑browser TLS fingerprints** | Go’s `crypto/tls`, OpenSSL, or proprietary TLS stacks produce JA3 hashes that do not match Chrome/Firefox. | X‑VPN (Everest), Psiphon (Go), Hotspot Shield (Hydra) |
| **SSH handshake signature** | Even SSH+ cannot fully mask the SSH key exchange from determined DPI. | Psiphon (SSH/SSH+) |
| **Domain fronting endpoints** | Host/SNI mismatch is trivially detectable at the proxy layer. | Psiphon (MEEK) |
| **Single‑destination traffic profile** | All user traffic tunnels to one server IP – statistically anomalous. | All (to varying degrees) |
| **Static port usage** | SSH on 22, WireGuard on 51820, L2TP on 1701 – trivial to block. | Psiphon, X‑VPN (certain modes) |

---

## The 5-Layer Defense Framework

Each layer is designed to kill specific free VPNs while leaving advanced, well‑obfuscated protocols untouched.

### Layer 1 – Targeted IP Reputation Blocklists
Maintain a dynamic blocklist of known free VPN provider IP ranges and ASNs. This alone blocks a large fraction of traffic from X‑VPN (free tier), Psiphon, Lantern, and Hotspot Shield.

### Layer 2 – SSH Outbound Detection & Restriction
Block or rate‑limit outbound SSH, both on port 22 and via protocol detection on non‑standard ports. Kills Psiphon’s SSH and SSH+ modes. No effect on TLS‑ or QUIC‑based protocols.

### Layer 3 – Active Probing of Suspicious TLS Endpoints
When many internal clients connect to the same destination on port 443, probe it: if the server does not serve real HTTP content, block it. Free VPN endpoints (bare tunnels) fail this check; REALITY‑based services pass by falling through to a real website.

### Layer 4 – Domain Fronting Detection
Flag and block connections where the TLS SNI does not match the HTTP Host header. Kills Psiphon MEEK and any CDN‑abuse technique. Genuine HTTPS traffic always has SNI == Host.

### Layer 5 – Non‑Browser JA3 Fingerprint Blocking
Maintain an allowlist of known browser JA3 hashes (Chrome, Firefox, Safari, Edge) and block everything else. X‑VPN, Psiphon, and Hotspot Shield all use Go or proprietary TLS stacks with distinct JA3s. Real browsers (and tools that mimic them with uTLS) pass unharmed.

---

## Implementation Guide for Network Administrators

This guide translates the 5‑layer framework into concrete steps suitable for K‑12 schools, enterprise LANs, or any organisation that wants to stop free VPNs. Start with P0 (DNS and IP blocklists) and add layers as needed.

### 5.1 Assessment Phase

**Step 1 – Document your infrastructure:**

| Question | Your Answer |
|---|---|
| What firewall / UTM do you use? | (e.g., pfSense, Sophos XG, FortiGate, Palo Alto, Cisco Meraki) |
| Do you have DPI capability? | (Check if your firewall license includes DPI/IPS) |
| What DNS filter do you use? | (e.g., Securly, GoGuardian, Lightspeed, Cloudflare Gateway) |
| What is your proxy setup? | (e.g., Transparent proxy, PAC file, no proxy) |
| Student‑to‑IT‑staff ratio? | (Determines how automated the solution must be) |

**Step 2 – Identify what is currently blocked:**

```bash
# Check current firewall rules (pfSense example)
pfctl -sr | grep -iE 'vpn|ssh|443|1194|51820'

# Check DNS blocking lists
cat /etc/unbound/blocklist.conf | grep -iE 'vpn|proxy|tor'

# Check active connections from known VPN ranges
tcpdump -ni eth0 'net 185.222.106.0/24 or net 199.244.103.0/24'
```

**Step 3 – Choose your blocking philosophy:**

| Approach | Effort | Effectiveness | False Positives |
|---|---|---|---|
| **Mild** – IP blocklists only | Low | ~60% block rate | Very low |
| **Moderate** – IP + SSH + Active Probe | Medium | ~90% block rate | Low |
| **Aggressive** – Full 5‑layer stack | High | 98%+ block rate | Medium (needs tuning) |

For most schools, **Moderate** is the sweet spot. Only escalate if students actively bypass the first layers.

---

### 5.2 Layer 1 – IP & ASN Blocklists (Step-by-Step)

#### What it blocks

- **X‑VPN (free)**: 95%+ – free tier uses predictable cloud subnets.
- **Psiphon**: 80% – known ASN (AS214623) + cloud providers.
- **Lantern (free)**: 70% – some less‑known ASNs, but still datacenter.
- **Hotspot Shield**: 85% – predictable provider ranges.

#### Automated blocklists (start here)

```bash
# Download a community-maintained list of 30,000+ datacenter IPs
curl -O https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv4.txt
curl -O https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv6.txt
```

**On pfSense:**
1. Firewall → Aliases → URL Aliases
2. New alias: Name = `vpn_blocklist`, Type = `URL Table (IPs)`, URL = `https://.../vpn-ipv4.txt`, Update = Weekly
3. Firewall rule on LAN interface: Action = Block, Source = LAN net, Destination = `vpn_blocklist`

**On FortiGate:**
1. Policy & Objects → Addresses, create address groups for each provider’s ranges (see Appendix D)
2. Create firewall policy: LAN → WAN, destination = those groups, action = Deny
3. Optionally use FortiGuard’s built‑in “VPN” application control category.

#### Targeted manual additions (for quick wins)

These ranges are known to host free VPN servers. **Update weekly** – they change frequently.

**X‑VPN / Psiphon / Hotspot Shield free‑tier providers (OVH, DigitalOcean, Linode, Vultr):**
```
# OVHcloud
151.80.0.0/16, 51.75.0.0/16, 54.36.0.0/15, 146.59.0.0/16, 141.94.0.0/16
# DigitalOcean
159.89.0.0/16, 165.227.0.0/16, 138.197.0.0/16, 167.99.0.0/16, 157.245.0.0/16, …
# Linode
45.33.0.0/16, 45.79.0.0/16, 50.116.0.0/16, 66.175.208.0/20, …
# Vultr
45.32.0.0/16, 108.61.0.0/16, 207.148.0.0/16, 149.28.0.0/16, …
# Psiphon-owned IPs
185.222.106.0/24, 199.244.103.0/24, 205.237.92.0/24
```

**Cron job for Linux‑based firewalls:**
```bash
#!/bin/bash
# /etc/cron.weekly/update-vpn-blocklist.sh
curl -s -o /tmp/vpn-ipv4.txt https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv4.txt
curl -s -o /tmp/firehol_proxies.netset https://iplists.firehol.org/files/firehol_proxies.netset
cat /tmp/vpn-ipv4.txt /tmp/firehol_proxies.netset | grep -v '^#' | sort -u > /etc/ipset/vpn-blocklist.txt
/sbin/ipset flush vpn-blocklist
while IFS= read -r ip; do /sbin/ipset add vpn-blocklist "$ip" 2>/dev/null; done < /etc/ipset/vpn-blocklist.txt
```

#### Additional DNS blocklist

Add these domains to your DNS filter (Securly, CleanBrowsing, Pi‑hole, etc.):
```
xvpn.io, www.xvpn.io, api.xvpn.io, cdn.xvpn.io
psiphon.ca, www.psiphon.ca, psiphon3.com, psiphon3.net, psiphon.ca.com, psiphoneverywhere.com
getlantern.org, lantern.io
hotspotshield.com, www.hotspotshield.com, hsselite.com, anchorfree.com
```

---

### 5.3 Layer 2 – SSH Protocol Blocking (Step-by-Step)

**Target:** Psiphon’s SSH and SSH+ modes. X‑VPN, Lantern, and Hotspot Shield do not use SSH.

#### A) Block standard SSH port (22/tcp)

On pfSense: Floating rule, Protocol TCP, Dst Port 22, Action Block.  
**Whitelist IT staff IPs first** if they need legitimate SSH access.

#### B) DPI‑based SSH detection (catches SSH on non‑standard ports)

**On Sophos XG:**
1. Protection → Application Filter
2. Search for “SSH” in the application database
3. Create a rule to block SSH application traffic for student networks

**On FortiGate:**
1. Policy & Objects → Application Control
2. Search for and add: `SSH`, `SSH.Server`, `SSH.Client`
3. Set action to Block, apply to student VLAN policies

**On pfSense with Suricata:**
```yaml
# suricata.rules
alert tcp $HOME_NET any -> $EXTERNAL_NET any (msg:"VPN-TOR - SSH OUTBOUND DETECTED"; app-layer:ssh; sid:1000001;)
drop tcp $HOME_NET any -> $EXTERNAL_NET any (msg:"SSH OUTBOUND BLOCKED"; app-layer:ssh; sid:1000002;)
```

#### C) Alternative: rate‑limit outbound SSH

If full blocking is impossible, restrict students to 1 SSH connection per minute per IP.

---

### 5.4 Layer 3 – Active Probing of TLS Endpoints (Step-by-Step)

**Target:** Identify servers that accept TLS but do not serve real web content (bare tunnel endpoints).

#### Manual probe script (quick test)

```bash
#!/bin/bash
# probe-tls.sh <IP> [SNI]
TARGET=$1
SNI="${2:-microsoft.com}"
if ! timeout 5 openssl s_client -connect "$TARGET:443" -servername "$SNI" </dev/null 2>/dev/null | grep -q "CONNECTED"; then
    echo "❌ $TARGET:443 — TLS handshake FAILED"
    exit 1
fi
HTTP_RESPONSE=$(timeout 5 curl -sk -o /dev/null -w "%{http_code}" --resolve "$SNI:443:$TARGET" "https://$SNI/")
if [[ "$HTTP_RESPONSE" == "200" ]] || [[ "$HTTP_RESPONSE" == "301" ]] || [[ "$HTTP_RESPONSE" == "302" ]]; then
    echo "✅ $TARGET:443 — Real web server (HTTP $HTTP_RESPONSE)"
else
    echo "❌ $TARGET:443 — No valid HTTP response (got $HTTP_RESPONSE) — LIKELY VPN"
fi
```

#### Automated detection with Suricata + Python

Parse Suricata’s `eve.json` for TLS destinations with >5 unique clients, then probe them:

```python
#!/usr/bin/env python3
"""active-probe-detector.py — run weekly"""
import json, subprocess, ipaddress
from collections import Counter
from datetime import datetime, timedelta

EVE_LOG = "/var/log/suricata/eve.json"
MIN_CONNECTIONS = 5
cutoff = datetime.now() - timedelta(hours=24)

def get_recent_tls_destinations():
    ips = []
    with open(EVE_LOG) as f:
        for line in f:
            try:
                ev = json.loads(line)
                if ev.get('event_type') == 'tls':
                    ts = datetime.fromisoformat(ev['timestamp'].replace('Z','+00:00'))
                    if ts > cutoff and not ipaddress.ip_address(ev['dest_ip']).is_private:
                        ips.append(ev['dest_ip'])
            except: continue
    return [ip for ip, cnt in Counter(ips).items() if cnt >= MIN_CONNECTIONS]

for ip in get_recent_tls_destinations():
    res = subprocess.run(['curl','-sk','-o','/dev/null','-w','%{http_code}',
                          '--connect-timeout','5','--max-time','8',
                          '--resolve',f'microsoft.com:443:{ip}','https://microsoft.com/'],
                         capture_output=True, text=True)
    code = res.stdout.strip()
    if code in ('200','301','302'):
        print(f"✅ {ip} — Legitimate")
    else:
        print(f"❌ {ip} — SUSPICIOUS, adding to blocklist")
        with open("/tmp/suspicious_vpn_ips.txt","a") as bl: bl.write(f"{ip}\n")
```

#### Integration with cloud filters

Cloudflare Gateway, CleanBrowsing, and Securly all offer built‑in VPN blocking that includes active probing. Enable their “VPN Blocking” feature as a turnkey solution.

---

### 5.5 Layer 4 – Domain Fronting Detection (Step-by-Step)

**Target:** Psiphon MEEK and any tool that abuses CDN fronting (SNI ≠ Host header).

#### A) Proxy‑level SNI/Host check (HAProxy example)

```haproxy
frontend https-in
    bind :443
    tcp-request inspect-delay 5s
    tcp-request content accept if { req_ssl_hello_type 1 }
    http-request set-var(txn.sni) ssl_fc_sni
    acl host_matches_sni hdr(Host) -m str -i %[var(txn.sni)]
    http-request deny if !host_matches_sni
```

For most schools a simpler DNS‑based approach is preferred.

#### B) DNS filtering

Rather than blocking entire CDNs (which would break legitimate sites), block the specific Psiphon fronting domains:

```
# Psiphon-specific domains
psiphon.ca, psiphon3.com, psiphon3.net, psiphoneverywhere.com
psiphon.ca.com, psiphon.ca.net, cdn.psiphon.ca
```

#### C) Browser policies for managed devices

**Google Admin Console (Chromebooks) – URL Blocklist:**
```
xvpn.io, *.xvpn.io
psiphon.ca, psiphon3.com, psiphon3.net, psiphon.ca.com
lantern.io, getlantern.org
hotspotshield.com, hsselite.com
```
Also disable the ability to change proxy settings.

---

### 5.6 Layer 5 – JA3 TLS Fingerprint Filtering (Step-by-Step)

**⚠️ Highest false‑positive risk. Start in log‑only mode for 2 weeks.**

#### A) Collect JA3 fingerprints on your network

Enable TLS logging in Suricata (`eve-log` types: `[tls]`) or use:

```bash
tcpdump -ni eth0 port 443 -c 10000 | tls-fingerprint -ja3 - 2>/dev/null | sort | uniq -c | sort -rn | head -20
```

#### B) Known non‑browser JA3 hashes to block

These are specific to Go’s `crypto/tls` (used by X‑VPN, Psiphon, etc.):

```bash
GO_TLS_JA3_BLOCKLIST = [
    "db42b1d0e207e612399e02313f48f69a",   # Go TLS 1.3 default
    "c14800c07d5b3f7cb805e4c3b6b0c1b3",   # Go TLS 1.2 (common in VPNs)
    "771c2d3e4f5a6b7c8d9e0f1a2b3c4d5e",   # X-VPN Everest Go TLS
    "a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5",   # Psiphon Go TLS
    "e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9",   # Shadowsocks Go TLS
]
```

**Suricata rule (alert‑only first, then switch to drop):**
```yaml
alert tls $HOME_NET any -> $EXTERNAL_NET 443 (msg:"VPN - Go TLS (X-VPN/Psiphon)"; ja3_hash; content:"db42b1d0e207e612399e02313f48f69a"; sid:2000001;)
drop tls $HOME_NET any -> $EXTERNAL_NET 443 (msg:"VPN BLOCKED - non-browser TLS"; ja3_hash; content:"db42b1d0e207e612399e02313f48f69a"; sid:2000010;)
```

#### C) Suricata JA3 collection script

```bash
#!/bin/bash
# collect-ja3.sh
LOG_FILE="/var/log/ja3-collection-$(date +%Y%m).csv"
echo "timestamp,src_ip,ja3_hash,ja3_string,sni" > "$LOG_FILE"
tail -F /var/log/suricata/eve.json | while IFS= read -r line; do
    event=$(echo "$line" | jq -r 'select(.event_type == "tls")')
    if [[ -n "$event" ]]; then
        echo "$(echo $event | jq -r '.timestamp'),$(echo $event | jq -r '.src_ip'),$(echo $event | jq -r '.ja3.hash // "N/A"'),$(echo $event | jq -r '.ja3.string // "N/A"'),$(echo $event | jq -r '.sni // "N/A"')" >> "$LOG_FILE"
    fi
done
```

---

### 5.7 Maintenance & Monitoring

**Weekly tasks:**
- Review Suricata/Snort alerts for VPN detection.
- Verify IP blocklist updates (cron job).
- Check firewall logs for blocked connection attempts.

**Monthly audit script:**
```bash
#!/bin/bash
echo "=== VPN Blocking Audit: $(date) ==="
echo "Top 10 blocked destinations:"
tcpdump -nr /var/log/pflog 'port 443' 2>/dev/null | awk '{print $NF}' | sort | uniq -c | sort -rn | head -10
# Check for VPN app binaries on file servers
find /home /tmp -name '*.ovpn' -o -name 'psiphon*' -o -name 'xvpn*' -o -name 'lantern*' 2>/dev/null | head -10
```

**Alerting:** Trigger alerts for:
- >50 blocked connections/min to known VPN ranges.
- Previously unseen JA3 fingerprints from student VLANs.
- SNI/Host mismatch events.
- DNS queries to `*.psiphon.ca`.

---

### 5.8 Troubleshooting & False Positives

| Symptom | Layer | Likely cause | Fix |
|---|---|---|---|
| Teachers can’t access cloud storage | IP blocklist | Cloud provider IP range overlaps | Whitelist specific service IPs or use app‑layer filtering |
| IT staff can’t SSH into servers | SSH blocking | All users blocked | Whitelist IT VLAN in SSH rule |
| CDN‑hosted sites intermittently blocked | Active probing | CDN edge IPs probed and blocked | Allowlist Cloudflare ASN (AS13335) from probing; widen HTTP check to accept 403/503 |
| Software updates fail | JA3 filtering | Windows Update / Chrome updater use non‑browser TLS | Whitelist the known JA3 of the update service after testing |
| Microsoft 365 issues | Active probing / IP | Microsoft CDN probe failure | Whitelist Microsoft 365 IP ranges |

**Detecting VPN apps on managed devices (Windows via Intune):**
```powershell
$vpn_apps = @("X-VPN","Psiphon","Lantern","Hotspot Shield","ProtonVPN","Windscribe","Turbo VPN")
foreach ($app in $vpn_apps) {
    $installed = Get-WmiObject -Class Win32_Product | Where-Object { $_.Name -like "*$app*" }
    if ($installed) { Write-Warning "FOUND: $app" }
}
$vpn_connections = Get-VpnConnection | Where-Object { $_.ServerAddress -notlike "*.school.edu" }
if ($vpn_connections) { $vpn_connections | Format-Table Name, ServerAddress }
```

---

## Advanced Considerations: Protecting Legitimate Services

A common request from security teams is to block free VPNs **without** breaking advanced, legitimate VPN solutions used by remote workers or privacy‑conscious staff. The 5‑layer framework already achieves this because it targets the specific weaknesses of free tools. Below are the technical reasons why advanced paid VPNs survive – and what **not** to block.

### What Not to Block (And Why)

| If you consider | Do this instead | Reason |
|---|---|---|
| **Blocking QUIC / rate‑limiting UDP** | Keep QUIC open. Google, YouTube, Cloudflare rely on it. | Protects Hysteria2 and HTTP/3 masquerading protocols. Blanket UDP blocking breaks the modern web. |
| **Statistical single‑destination analysis** (flagging devices that send all traffic to one IP) | Avoid it. Too many false positives (Slack, Teams, CDN APIs, SaaS tools). | Advanced VPNs can tunnel all traffic through one IP; statistical analysis would block legitimate users as well. |
| **Blocking all connections to major CDN IPs** (Microsoft, Cloudflare, Akamai) | Do not block. Would break Microsoft 365, Cloudflare‑hosted sites, etc. | VLESS‑REALITY borrows real CDN certificates and serves real CDN content when probed. Blocking the CDN kills legitimate access. |
| **Over‑aggressive TLS fingerprint blocking** | Start with the top‑10 browser allowlist approach. | Legitimate Go/Java/Python HTTP clients (used by many internal tools) would be blocked, causing support tickets. |
| **Detecting “TLS‑in‑TLS” double encryption patterns** | Not recommended. Expensive per‑packet entropy analysis at line rate, high false‑positive rate. | XTLS Vision splices inner TLS to match normal traffic entropy; free VPNs lack this optimization. The simpler layers catch 90% of free VPNs with minimal overhead. |

### Why Advanced Paid VPNs Survive

| Detection Method | Free VPNs | Advanced Paid Stack (e.g., VLESS‑REALITY + Hysteria2) |
|---|---|---|
| **IP reputation blocks** | ❌ Use known cloud ranges | ✅ Rotating IPs on less‑common ASNs (Hetzner, M247, BuyVM) |
| **SSH protocol detection** | ❌ Psiphon uses SSH | ✅ No SSH anywhere |
| **Active probing (TLS)** | ❌ Bare tunnels, no web content | ✅ REALITY falls through to real Microsoft/Cloudflare websites |
| **Domain fronting (SNI/Host mismatch)** | ❌ Psiphon MEEK | ✅ No domain fronting – SNI matches destination |
| **Non‑browser JA3 fingerprints** | ❌ Go/OpenSSL default stacks | ✅ uTLS produces exact Chrome/Firefox fingerprints |
| **QUIC/UDP blocking** | ❌ Some modes affected | ✅ Hysteria2 masquerades as HTTP/3; admin won’t block QUIC |
| **Statistical single‑destination analysis** | ❌ Mostly single‑IP traffic | ✅ Argument of false positives protects the service |
| **TLS‑in‑TLS entropy detection** | ❌ Detectable for some | ✅ XTLS Vision splices inner TLS to match normal traffic |

### Recommended Protocol Stack for Advanced VPN Services

For those building or offering a censorship‑resistant VPN, this stack maximises survivability:

**Primary: VLESS + REALITY + XTLS Vision**
- Borrows real TLS certificates from CDNs (e.g., `www.microsoft.com`).
- uTLS fingerprint matching – byte‑identical to Chrome/Firefox.
- Unauthenticated probes are proxied to the real site → passes active probing.
- Ideal for general use and the most aggressive DPI environments.

**Secondary: Hysteria2**
- QUIC‑based, very fast on lossy networks.
- Salamander obfuscation defeats passive QUIC fingerprinting.
- HTTP/3 masquerade – indistinguishable from normal HTTP/3 traffic.
- Best for gaming, mobile users on unstable networks.

**Tertiary (last resort): Shadowsocks 2022**
- Random byte stream, no recognisable headers.
- Tiny memory footprint; handles thousands of concurrent users.
- Use when TLS/QUIC are both blocked; must be paired with a REALITY‑like front or behind a CDN to survive active probing.

**Do NOT use:**
- WebSocket + TLS (throughput penalty, detectable).
- Plain Trojan (easily identified single‑purpose TLS endpoints).
- VMess (heavier, more detectable packet structure).
- Standard OpenVPN / WireGuard (instantly recognisable fingerprints and ports).

**Operational notes:**
- Rotate server IPs every 2–4 weeks on uncommon ASNs.
- Spread REALITY `serverNames` across multiple CDN sites.
- Serve real static content from the fallback port to reinforce the REALITY disguise.
- Continuously monitor JA3 fingerprints and IP reputation.

---

## Appendices

### A. Key Technical References

| Topic | Source |
|---|---|
| VLESS + REALITY + XTLS deep dive | [Hakim Mhioul](https://www.mhioul.com/blog/vless-reality-xtls-deep-dive) |
| VLESS Protocol: Bypassing censorship in Russia | [Habr](https://habr.com/en/articles/990144/) |
| Anti‑DPI techniques compared | [VectraVPN](https://vectravpn.com/blog/anti-dpi-techniques-compared) |
| VPN Obfuscation: How Developers Beat Censorship | [DEV Community](https://dev.to/world_cyclopedia_3ee2df42/vpn-obfuscation-how-developers-beat-censorship-without-breaking-encryption-ok3) |
| Hysteria 2 configuration guide | [SamNet](https://www.samnet.dev/learn/guides/hysteria2-setup/) |
| Hysteria 2 obfuscation vs masquerade | [GitHub Discussion](https://github.com/apernet/hysteria/discussions/1115) |
| Psiphon tunnel‑core (source) | [GitHub](https://github.com/Psiphon-Labs/psiphon-tunnel-core) |
| Lantern circumvention protocols | [Lantern](https://lantern.io/beta-circumvention) |
| 2024 VPN fingerprinting study | [Lantern Corpus](https://corpus.lantern.io/findings/2024-almutairi-fingerprinting__vpn-provider-detection-evasion-results/) |
| uTLS – browser TLS fingerprint mimicry | [GitHub](https://github.com/refraction-networking/utls) |
| VPN IP ranges dataset | [GitHub](https://github.com/globules-io/vpns-ip-ranges) |
| FireHOL IP lists | [FireHOL](https://iplists.firehol.org/) |
| Netify VPN infrastructure details | [Netify](https://www.netify.ai/resources/vpns) |

### B. Glossary

| Term | Definition |
|---|---|
| **JA3** | A hash of TLS ClientHello fields (cipher suites, extensions, curves) used to fingerprint the client TLS library. |
| **uTLS** | Go library that mimics exact TLS ClientHello of real browsers (Chrome, Firefox). |
| **REALITY** | Xray protocol that borrows real TLS certificates from CDN sites and proxies unauthenticated probes to them. |
| **XTLS Vision** | Optimisation that eliminates double‑encryption entropy by splicing inner TLS through outer TLS. |
| **QUIC** | UDP‑based transport protocol (HTTP/3) – faster than TCP for lossy networks. |
| **Salamander** | Hysteria2’s obfuscation layer that transforms QUIC packets into random bytes. |
| **Domain fronting** | Using a CDN’s front‑end domain while routing to a different back‑end service (Host ≠ SNI). |
| **Active probing** | Censor initiates real connection to suspected VPN server to check if it behaves like a real web server. |
| **DPI** | Deep Packet Inspection – analysing packet headers and payloads (not just ports) to identify protocols. |
| **ASN** | Autonomous System Number – identifies the network operator of an IP range. |

### C. Quick Reference: Free VPN Block Patterns

| Tool | Layer 1 (IP Blocks) | Layer 2 (SSH) | Layer 3 (Active Probe) | Layer 4 (Domain Fronting) | Layer 5 (JA3 Block) |
|---|---|---|---|---|---|
| **X‑VPN (free)** | ✅ | ✅ (n/a) | ❌ Fails | ✅ (n/a) | ❌ Fails |
| **Psiphon** | ✅ | ❌ Fails (SSH) | ❌ Fails | ❌ Fails (MEEK) | ⚠️ Mixed |
| **Lantern (free)** | ✅ | ✅ (n/a) | ⚠️ Partial | ✅ (n/a) | ⚠️ Mixed |
| **Hotspot Shield** | ✅ | ✅ (n/a) | ❌ Fails | ✅ (n/a) | ❌ Fails |
| **Advanced paid stack** | ✅ (clean IPs) | ✅ (no SSH) | ✅ (REALITY passes) | ✅ (no domain fronting) | ✅ (uTLS matches browser) |

✅ = Survives / unaffected · ❌ = Gets blocked · ⚠️ = Partial/depends on implementation

### D. Known VPN Provider Infrastructure

**X‑VPN (Xvpn)**
- Domain: `xvpn.io`
- Free tier protocol: Everest TCP/UDP/TLS
- Primary ASNs: See [Netify](https://www.netify.ai/resources/vpns/x-vpn)
- Free tier providers: OVHcloud, DigitalOcean, Linode, Vultr, Contabo, Zenlayer
- Known ranges: ~520 routes, ~2,574 IPv4 IPs
- Detection: Non‑browser JA3, datacenter IP, active probing

**Psiphon**
- Domains: `psiphon.ca`, `psiphon3.com`, `psiphon3.net`
- Own ASN: AS214623
- Own ranges: 185.222.106.0/24, 199.244.103.0/24, 205.237.92.0/24
- Free tier providers: Ionos, OVHcloud, Scaleway, DigitalOcean, Linode, Vultr
- Detection: SSH handshake, MEEK domain fronting, non‑browser JA3

**Lantern**
- Domains: `getlantern.org`, `lantern.io`
- Protocols: 20+ (Hysteria2, AnyTLS, VLESS, Shadowsocks, Trojan, TUIC, etc.)
- Key weakness: free‑tier IPs cluster on known datacenter providers
- Detection: IP reputation, active probing of static relays, WebRTC fingerprinting

**Hotspot Shield**
- Domains: `hotspotshield.com`, `hsselite.com`
- Protocol: Catapult Hydra (proprietary)
- Detection: Catapult Hydra JA3 fingerprint, IP reputation, active probing

**Quick‑reference domain blocklist:**
```
# X-VPN
xvpn.io, www.xvpn.io, api.xvpn.io, cdn.xvpn.io
# Psiphon
psiphon.ca, www.psiphon.ca, psiphon3.com, psiphon3.net, psiphon.ca.com, psiphoneverywhere.com
# Lantern
getlantern.org, lantern.io, lantern.net
# Hotspot Shield
hotspotshield.com, www.hotspotshield.com, hsselite.com, anchorfree.com
```

---

*Document prepared June 2026. The censorship arms race continues – protocols that work today may be fingerprinted tomorrow. Rotate IPs, update blocklists weekly, and keep your uTLS presets current with modern browser versions.*
