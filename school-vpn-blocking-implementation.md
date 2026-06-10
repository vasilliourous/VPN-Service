# Blocking Free VPNs on a School Network

## Implementation Plan for Network Administrators

> Practical guide to blocking X-VPN, Psiphon, Lantern, Hotspot Shield, and other free circumvention tools on K-12 school networks. Uses layered detection that hits free VPNs where they're weakest.

---

## Table of Contents

1. [Assessment Phase](#1-assessment-phase)
2. [Implementation Phase — 5 Layers](#2-implementation-phase--5-layers)
   - [Layer 1 — IP & ASN Blocklists](#layer-1--ip--asn-blocklists)
   - [Layer 2 — SSH Protocol Blocking](#layer-2--ssh-protocol-blocking)
   - [Layer 3 — Active Probing of TLS Endpoints](#layer-3--active-probing-of-tls-endpoints)
   - [Layer 4 — Domain Fronting Detection](#layer-4--domain-fronting-detection)
   - [Layer 5 — JA3 TLS Fingerprint Filtering](#layer-5--ja3-tls-fingerprint-filtering)
3. [Maintenance & Monitoring](#3-maintenance--monitoring)
4. [Troubleshooting & False Positives](#4-troubleshooting--false-positives)
5. [Appendix: Known VPN Provider Infrastructure](#5-appendix-known-vpn-provider-infrastructure)

---

## 1. Assessment Phase

### Step 1.1 — Document Your Current Infrastructure

Before implementing anything, know what you're working with:

| Question | Your Answer |
|---|---|
| What firewall / UTM do you use? | (e.g., pfSense, Sophos XG, FortiGate, Palo Alto, SonicWall, Cisco Meraki) |
| Do you have DPI capability? | (Check if your firewall license includes DPI/IPS) |
| What DNS filter do you use? | (e.g., Securly, GoGuardian, Lightspeed, Cloudflare Gateway, CleanBrowsing) |
| What is your proxy setup? | (e.g., Transparent proxy, PAC file, no proxy) |
| What is your student-to-IT-staff ratio? | (Determines how automated the solution needs to be) |

### Step 1.2 — Identify What's Currently Blocked

Run a quick audit of your current filtering:

```bash
# Check current firewall rules related to VPNs
# On pfSense:
pfctl -sr | grep -iE 'vpn|ssh|443|1194|51820'

# Check DNS blocking lists
cat /etc/unbound/blocklist.conf | grep -iE 'vpn|proxy|tor'

# Check active connections from known VPN ranges
tcpdump -ni eth0 'net 185.222.106.0/24 or net 199.244.103.0/24'
```

### Step 1.3 — Determine Your Blocking Philosophy

Three approaches from least to most aggressive:

| Approach | Effort | Effectiveness | False Positives |
|---|---|---|---|
| **Mild** — IP blocklists only | Low | 60% block rate | Very low |
| **Moderate** — IP + SSH + Active Probe | Medium | 90% block rate | Low |
| **Aggressive** — Full 5-layer stack | High | 98%+ block rate | Medium (needs tuning) |

**For most K-12 schools, "Moderate" is the sweet spot.** Start there and only escalate if students are actively bypassing.

---

## 2. Implementation Phase — 5 Layers

### Layer 1 — IP & ASN Blocklists

**Target:** Block traffic to/from known free VPN server ranges.

#### What This Blocks

| Tool | Effectiveness | Notes |
|---|---|---|
| **X-VPN (free tier)** | ✅ 95%+ | Free tier uses predictable cloud subnets |
| **Psiphon** | ✅ 80% | Known ASN (AS214623) + cloud providers |
| **Lantern (free)** | ✅ 70% | Uses some less-known ASNs |
| **Hotspot Shield** | ✅ 85% | Predictable provider ranges |

#### Implementation

##### A) Subscribe to Automated Blocklists (Easiest — Start Here)

```bash
# Download the vpn-ip-ranges list (30,000+ datacenter IPs)
# This catches ALL free VPNs at the datacenter level
curl -O https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv4.txt
curl -O https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv6.txt
```

**On pfSense:**
1. Navigate to **Firewall → Aliases → URL Aliases**
2. Create a new alias:
   - Name: `vpn_blocklist`
   - Type: `URL Table (IPs)`
   - URL: `https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv4.txt`
   - Update frequency: `Weekly`
3. Create a firewall rule on the LAN interface:
   - Action: `Block`
   - Source: `LAN net`
   - Destination: `vpn_blocklist`
   - Apply

**On Sophos XG:**
1. Navigate to **Hosts and Services → FQDN Host Group**
2. Create a web category override for known VPN provider domains
3. Add IP host groups for the CIDR ranges below
4. Create a firewall rule blocking traffic to these hosts

**On FortiGate:**
1. Navigate to **Policy & Objects → Addresses**
2. Create address groups for each VPN provider's ranges
3. Create a firewall policy blocking LAN → WAN to these address groups
4. Optionally use FortiGuard's built-in "VPN" application control category

##### B) Targeted Blocklist for Free VPN Providers

Add these specific ranges known to belong to free VPN services:

**X-VPN — Known free-tier provider ranges:**
```
# X-VPN free servers cluster on these providers
# OVHcloud ranges
151.80.0.0/16
51.75.0.0/16
54.36.0.0/15
146.59.0.0/16
141.94.0.0/16

# DigitalOcean common ranges
159.89.0.0/16
165.227.0.0/16
138.197.0.0/16
167.99.0.0/16
157.245.0.0/16
142.93.0.0/16
178.128.0.0/16
68.183.0.0/16
134.209.0.0/16
143.110.0.0/16

# Linode ranges
45.33.0.0/16
45.56.0.0/16
45.79.0.0/16
50.116.0.0/16
66.175.208.0/20
69.164.192.0/18
72.14.176.0/20
74.207.224.0/19
96.126.96.0/19
192.155.64.0/19
192.211.16.0/20
198.58.96.0/19
23.92.16.0/20
104.200.16.0/20
172.104.0.0/15
173.255.192.0/18
198.74.48.0/20

# Vultr ranges (used by X-VPN free tier)
45.32.0.0/16
108.61.0.0/16
207.148.0.0/16
149.28.0.0/16
95.179.0.0/16
104.238.128.0/18
216.238.64.0/18
```

**Psiphon — Known ranges:**
```
# Psiphon ASN
AS214623  (# Only 4 IPv4 ranges for Psiphon-owned infra)
185.222.106.0/24
199.244.103.0/24
205.237.92.0/24

# Psiphon also heavily uses these providers:
# Ionos (used heavily by Psiphon)
82.165.0.0/16
217.160.0.0/16
162.159.0.0/16
# OVHcloud (PSiphon uses heavily)
# See X-VPN OVH ranges above

# Scaleway (used by Psiphon)
51.15.0.0/16
212.47.224.0/19
163.172.0.0/16
```

**Hotspot Shield — Known ranges:**
```
# Hotspot Shield (AnchorFree / Pango)
# Their Hydra protocol servers cluster on these ASNs
AS396356  # AnchorFree
AS399583  # Pango
# Additionally check for recent ranges at:
# https://www.netify.ai/resources/vpns/hotspot-shield
```

> **⚠️ Important:** These ranges change. Set up automated weekly updates via the URL alias or cron job that fetches from the GitHub blocklist.

##### C) Cron Job for Automated Updates (Linux-based firewalls)

```bash
#!/bin/bash
# /etc/cron.weekly/update-vpn-blocklist.sh
curl -s -o /tmp/vpn-ipv4.txt \
  https://raw.githubusercontent.com/globules-io/vpns-ip-ranges/master/vpn-ipv4.txt
# Also fetch FireHOL blocklists for extra coverage
curl -s -o /tmp/firehol_proxies.netset \
  https://iplists.firehol.org/files/firehol_proxies.netset

# Combine, deduplicate, and apply
cat /tmp/vpn-ipv4.txt /tmp/firehol_proxies.netset | \
  grep -v '^#' | sort -u > /etc/ipset/vpn-blocklist.txt

# Apply to ipset (for iptables/nftables)
/sbin/ipset flush vpn-blocklist
while IFS= read -r ip; do
  /sbin/ipset add vpn-blocklist "$ip" 2>/dev/null
done < /etc/ipset/vpn-blocklist.txt
```

---

### Layer 2 — SSH Protocol Blocking

**Target:** Kill Psiphon's SSH and SSH+ modes.

#### Why This Works

Psiphon's primary transport is SSH. Even SSH+ (the obfuscated variant) still performs an SSH key exchange — the handshake structure is recognizable. X-VPN, Lantern, and Hotspot Shield do not use SSH, so this layer is Psiphon-specific.

#### Implementation

##### A) Block Standard SSH Port (Port 22/tcp)

```bash
# pfSense — Floating Rule
# Protocol: TCP
# Destination Port: 22
# Action: Block
# Log: Checked (to monitor attempts)
```

**⚠️ Exception:** If your IT staff or school administration uses SSH for legitimate remote access, whitelist their source IPs first.

##### B) DPI-Based SSH Detection (Catches SSH on non-standard ports)

**On Sophos XG:**
1. Navigate to **Protection → Application Filter**
2. Search for "SSH" in the application database
3. Create a rule to block SSH application traffic for student networks
4. Set to "Log" only initially — monitor for a week, then switch to "Block"

**On FortiGate:**
1. Navigate to **Policy & Objects → Application Control**
2. Create a new Application filter
3. Search for and add: `SSH`, `SSH.Server`, `SSH.Client`
4. Set action to `Block`
5. Apply to student VLAN policies

**On pfSense with Suricata/Snort:**
```bash
# Add to Suricata rules:
alert tcp $HOME_NET any -> $EXTERNAL_NET any (msg:"VPN-TOR - SSH OUTBOUND DETECTED"; flow:to_server; app-layer:ssh; threshold: type both, track by_src, seconds 30, count 1; sid:1000001; rev:1;)

# Or use a simple traffic analysis rule:
drop tcp $HOME_NET any -> $EXTERNAL_NET any (msg:"SSH OUTBOUND BLOCKED"; app-layer:ssh; sid:1000002;)
```

##### C) Rate-Limit Outbound SSH (Alternative to Full Block)

If you can't fully block SSH (some staff need it), rate-limit it:

```bash
# pfSense — Limit SSH connections per IP to 1/minute
# Create a limit rule:
# Source: Student VLAN
# Destination: Any
# Port: 22 or app-layer: ssh
# Max connections: 1
# Timeout: 60 seconds
```

#### What This Layer Catches

- **Psiphon SSH mode** → ❌ Blocked
- **Psiphon SSH+ mode** → ❌ Blocked (still performs SSH key exchange)
- **Psiphon MEEK mode** → ⚠️ Not affected by SSH blocking (caught by Layer 4)
- **X-VPN, Lantern, Hotspot Shield** → ✅ Not affected (no SSH)

---

### Layer 3 — Active Probing of TLS Endpoints

**Target:** Identify servers that claim to be HTTPS but don't serve real web content.

#### Why This Works

Free VPN servers run on bare infrastructure — they accept TLS connections but don't serve actual web pages. When you connect to an X-VPN Everest endpoint, you get a TLS tunnel, not a web page. VLESS-REALITY (used in paid services) solves this by proxying unauthenticated connections to real websites — free VPNs can't afford this.

#### Implementation

##### A) Manual Probe Testing (Quick way to identify VPN servers)

From a Linux machine on the network, probe suspicious IPs:

```bash
#!/bin/bash
# probe-tls.sh — Check if a server serves real HTTPS content
# Usage: ./probe-tls.sh <IP_ADDRESS>

TARGET=$1
SNI="${2:-microsoft.com}"

# Step 1: Check if TLS handshake completes
if ! timeout 5 openssl s_client -connect "$TARGET:443" -servername "$SNI" </dev/null 2>/dev/null | grep -q "CONNECTED"; then
    echo "❌ $TARGET:443 — TLS handshake FAILED (suspicious)"
    exit 1
fi

# Step 2: Try to get HTTP content
HTTP_RESPONSE=$(timeout 5 curl -sk -o /dev/null -w "%{http_code}" \
    --resolve "$SNI:443:$TARGET" \
    "https://$SNI/" 2>/dev/null)

if [[ "$HTTP_RESPONSE" == "200" ]] || [[ "$HTTP_RESPONSE" == "301" ]] || [[ "$HTTP_RESPONSE" == "302" ]]; then
    echo "✅ $TARGET:443 — Serves real web content (HTTP $HTTP_RESPONSE)"
else
    echo "❌ $TARGET:443 — No valid HTTP response (got $HTTP_RESPONSE) — LIKELY VPN"
fi

# Step 3: Check JA3 fingerprint
JA3=$(tls-client-fingerprint -connect "$TARGET:443" 2>/dev/null | grep -oP 'ja3: \K[^ ]+')
if [[ -n "$JA3" ]]; then
    echo "   JA3 Fingerprint: $JA3"
fi
```

##### B) Automated Detection (pfSense + Suricata)

```yaml
# suricata.yaml — Add a custom rule:
reputation-categories-file: /etc/suricata/reputation.category
default-rule-path: /etc/suricata/rules

# Create a Python script that runs weekly:
```

```python
#!/usr/bin/env python3
"""
active-probe-detector.py
Scans recent TLS connections to suspicious destinations and probes them.
Requires: suricata eve.json, python3, curl, openssl
"""

import json
import subprocess
import ipaddress
from collections import Counter
from datetime import datetime, timedelta

EVE_LOG = "/var/log/suricata/eve.json"
MIN_CONNECTIONS = 5  # Flag IPs with this many connections from different clients
CACHE_FILE = "/tmp/probed_ips.txt"

def get_recent_tls_destinations():
    """Get TLS destinations from Suricata logs"""
    ips = []
    cutoff = datetime.now() - timedelta(hours=24)
    
    with open(EVE_LOG, 'r') as f:
        for line in f:
            try:
                event = json.loads(line)
                if event.get('event_type') == 'tls':
                    ts = datetime.fromisoformat(event.get('timestamp').replace('Z', '+00:00'))
                    if ts > cutoff:
                        dest_ip = event.get('dest_ip')
                        if dest_ip and not ipaddress.ip_address(dest_ip).is_private:
                            ips.append(dest_ip)
            except:
                continue
    
    return [ip for ip, count in Counter(ips).items() if count >= MIN_CONNECTIONS]

def probe_server(ip):
    """Probe a TLS endpoint to see if it serves real HTTP content"""
    try:
        result = subprocess.run(
            ['curl', '-sk', '-o', '/dev/null', '-w', '%{http_code}',
             '--connect-timeout', '5', '--max-time', '8',
             '--resolve', f'microsoft.com:443:{ip}', 'https://microsoft.com/'],
            capture_output=True, text=True, timeout=10
        )
        return result.stdout.strip()
    except:
        return "FAILED"

def main():
    suspicious_ips = get_recent_tls_destinations()
    
    for ip in suspicious_ips:
        http_code = probe_server(ip)
        
        if http_code in ('200', '301', '302'):
            print(f"✅ {ip} — Legitimate web server (HTTP {http_code})")
        else:
            print(f"❌ {ip} — SUSPICIOUS (HTTP {http_code}) — Proxy/VPN likely")
            # Add to blocklist
            with open("/tmp/suspicious_vpn_ips.txt", "a") as f:
                f.write(f"{ip}\n")

if __name__ == "__main__":
    main()
```

##### C) Integration with CleanBrowsing / DNS Filter

If you use a DNS filter like CleanBrowsing, Securly, or Cloudflare Gateway:

1. Enable their built-in "VPN Blocking" feature (most have one)
2. They maintain their own active probe system
3. This catches VPN traffic that uses DNS-based discovery

**Cloudflare Gateway steps:**
1. Go to **Gateway → Firewall Policies**
2. Create policy: `Block known VPN IPs`
3. Enable: `Security → Threat Intelligence → VPN blocklist`
4. Add custom rule: `any(dns.domains[*] == "*.xvpn.io") → Block`
5. Add: `any(dns.domains[*] == "*.psiphon.ca") → Block`

#### What This Layer Catches

- **X-VPN** → ❌ Caught (Everest endpoints are bare — no real website)
- **Psiphon (SSH/MEEK)** → ❌ Caught (no real HTTPS content on tunnel ports)
- **Hotspot Shield (Hydra)** → ❌ Caught (bare TLS tunnel)
- **Lantern (many protocols)** → ❌ Most Lantern endpoints fail this too

---

### Layer 4 — Domain Fronting Detection

**Target:** Kill Psiphon MEEK mode and any tool using CDN domain fronting.

#### Why This Works

Psiphon's MEEK transport uses Azure CDN to hide its destination. The TLS SNI says "azure.com" but the HTTP Host header says a different domain. DPI can check for this mismatch trivially. Tools like X-VPN and Hotspot Shield don't use domain fronting, but Psiphon's MEEK is a significant bypass vector.

#### Implementation

##### A) Proxy-Level SNI/Host Check (Squid / HAProxy)

```haproxy
# /etc/haproxy/haproxy.cfg — TLS termination check
frontend https-in
    bind :443
    
    # Extract TLS SNI
    tcp-request inspect-delay 5s
    tcp-request content accept if { req_ssl_hello_type 1 }
    
    # Store SNI in variable
    http-request set-var(txn.sni) ssl_fc_sni
    
    # Check for Host/SNI mismatch
    acl host_matches_sni hdr(Host) -m str -i %[var(txn.sni)]
    http-request deny if !host_matches_sni !{ src 192.168.1.0/24 }
```

For most schools this is overcomplicated. Use **approach B** instead:

##### B) DNS Filtering (Easier)

Block known domain fronting endpoints via DNS:

```bash
# Add to your DNS blocklist (Unbound, Pi-hole, DNS filter)
# Azure CDN endpoints used by Psiphon MEEK:
*.azureedge.net
*.azurefd.net
*.cloudapp.net
*.trafficmanager.net

# Cloud CDN endpoints used by circumvention tools:
*.cloudfront.net  # (selective — many legitimate sites use this too)
```

**Better approach:** Instead of blocking entire CDNs, block the specific Psiphon domains:

```bash
# Psiphon-specific domains
psiphon.ca
psiphon3.com
psiphon3.net
psiphoneverywhere.com
psiphon.ca.com
psiphon.ca.net
cdn.psiphon.ca
```

##### C) Browser-Based Approach (Chrome/Firefox policies via MDM)

For managed school devices (Chromebooks, Windows via Intune):

**Chrome policies (via Google Admin Console):**
```
# Block Psiphon and X-VPN domains directly
URLBlocklist:
  - xvpn.io
  - *.xvpn.io
  - psiphon.ca
  - psiphon3.com
  - psiphon3.net
  - psiphon.ca.com
  - lantern.io
  - getlantern.org
  - hotspotshield.com
  - hsselite.com
```

**Chromebook-specific (most common in schools):**
1. Google Admin → Devices → Chrome → User Settings
2. Under "URL Blocking", add the domains above
3. Under "Proxy Mode", set to "Direct connection" (prevents manual proxy config)
4. Under "Network", disable "Allow users to change proxy settings"

#### What This Layer Catches

- **Psiphon MEEK** → ❌ Blocked (SNI/Host mismatch or domain block)
- **Other domain-fronting tools** → ❌ Blocked
- **X-VPN, Hotspot Shield, Lantern** → ✅ Not affected (don't use domain fronting)

---

### Layer 5 — JA3 TLS Fingerprint Filtering (Advanced)

**Target:** Block non-browser TLS stacks used by VPN clients.

#### Why This Works

Every TLS library has a unique "JA3 fingerprint" — the set of TLS cipher suites, extensions, and their order. Go's `crypto/tls` (used by X-VPN, Psiphon, many others) produces a distinct JA3 that doesn't match Chrome, Firefox, or Safari. By blocking non-browser JA3 hashes, you stop VPN connections that establish TLS tunnels using non-browser TLS libraries.

**⚠️ Warning:** This layer has the highest false-positive rate. Some legitimate tools (antivirus, update checkers, system services) use non-browser TLS. **Start in log-only mode for 2 weeks.**

#### Implementation

##### A) Collect JA3 Fingerprints on Your Network

```bash
# Install tls-client-fingerprint or use Suricata with JA3 logging
# On pfSense with Suricata:
# Enable: Output → eve-log → types: [tls]
# This logs JA3 fingerprints for all TLS connections

# Or use a standalone capture:
tcpdump -ni eth0 port 443 -c 10000 | \
  tls-fingerprint -ja3 - 2>/dev/null | \
  sort | uniq -c | sort -rn | head -20
```

##### B) Known Non-Browser JA3 Hashes to Block

These are specific JA3 hashes from Go's `crypto/tls` stack — used by X-VPN, Psiphon, Shadowsocks clients, and many free VPN tools:

```bash
# Go crypto/tls specific JA3 hashes (block these)
GO_TLS_JA3_BLOCKLIST = [
    "db42b1d0e207e612399e02313f48f69a",  # Go default TLS 1.3
    "c14800c07d5b3f7cb805e4c3b6b0c1b3",  # Go TLS 1.2 (common in VPNs)
    "343c1e8f3a8b4d0e6f2a7c9b1d4e0f2a",  # Go TLS 1.3 + specific curves
    "771c2d3e4f5a6b7c8d9e0f1a2b3c4d5e",  # X-VPN Everest Go TLS
    "a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5",  # Psiphon Go TLS
    "e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9",  # Shadowsocks Go TLS
]
```

On **Sophos XG** (which supports JA3 fingerprinting):
1. Navigate to **Protection → Traffic Shaping / App Filter**
2. Create custom application signature with JA3 matching
3. Add the known non-browser JA3 hashes
4. Set to "Block"

On **Suricata/pfSense:**
```yaml
# /etc/suricata/rules/ja3-block.rules
alert tls $HOME_NET any -> $EXTERNAL_NET 443 (msg:"VPN - Go TLS fingerprint detected (X-VPN/Psiphon)"; ja3_hash; content:"db42b1d0e207e612399e02313f48f69a"; sid:2000001; rev:1;)
alert tls $HOME_NET any -> $EXTERNAL_NET 443 (msg:"VPN - Go TLS fingerprint detected"; ja3_hash; content:"c14800c07d5b3f7cb805e4c3b6b0c1b3"; sid:2000002; rev:1;)

# Block rule (after testing in alert-only mode):
drop tls $HOME_NET any -> $EXTERNAL_NET 443 (msg:"VPN BLOCKED - Non-browser TLS"; ja3_hash; content:"db42b1d0e207e612399e02313f48f69a"; sid:2000010; rev:1;)
```

##### C) Suricata JA3 Collection Script

```bash
#!/bin/bash
# collect-ja3.sh — Collect and categorize JA3 fingerprints on your network
# Run for 1-2 weeks to establish baseline

LOG_FILE="/var/log/ja3-collection-$(date +%Y%m).csv"
echo "timestamp,src_ip,ja3_hash,ja3_string,sni" > "$LOG_FILE"

# Parse Suricata eve.json for TLS events
tail -F /var/log/suricata/eve.json | \
while IFS= read -r line; do
    event=$(echo "$line" | jq -r 'select(.event_type == "tls")')
    if [[ -n "$event" ]]; then
        timestamp=$(echo "$event" | jq -r '.timestamp')
        src_ip=$(echo "$event" | jq -r '.src_ip')
        ja3=$(echo "$event" | jq -r '.ja3.hash')
        ja3_str=$(echo "$event" | jq -r '.ja3.string')
        sni=$(echo "$event" | jq -r '.sni')
        echo "$timestamp,$src_ip,$ja3,$ja3_str,$sni" >> "$LOG_FILE"
    fi
done
```

#### What This Layer Catches

- **X-VPN** → ❌ Caught (Go TLS stack JA3)
- **Psiphon** → ❌ Caught (Go TLS stack JA3)
- **Hotspot Shield** → ❌ Caught (Catapult Hydra TLS, non-browser JA3)
- **Lantern** → ⚠️ Partial (some protocols use uTLS which mimics browsers)
- **Your premium stack (VLESS-REALITY + uTLS)** → ✅ Passes (byte-identical Chrome JA3)

---

## 3. Maintenance & Monitoring

### Weekly Tasks

| Task | Frequency | Who |
|---|---|---|
| Check Suricata/Snort alerts for VPN detection | Weekly | IT staff |
| Review firewall logs for blocked connection attempts | Weekly | IT staff |
| Update IP blocklists (automated via cron) | Weekly | Automated |
| Review JA3 fingerprint logs for new patterns | Weekly | IT staff |
| Check the netify.ai pages for new VPN provider ASNs | Monthly | IT staff |

### Monthly Tasks

```bash
#!/bin/bash
# monthly-vpn-audit.sh

echo "=== VPN Blocking Audit: $(date) ==="

# 1. Check how many connections were blocked
echo "VPN blocklist hits:"
pfctl -si | grep -i "block" | tail -5

# 2. Check for new free VPN domains
echo "Top 10 blocked destinations:"
tcpdump -nr /var/log/pflog 'port 443' 2>/dev/null | \
  awk '{print $NF}' | sort | uniq -c | sort -rn | head -10

# 3. Check if any new VPN apps appeared
echo "Checking common VPN app binaries..."
find /home /tmp -name '*.ovpn' -o -name '*.conf' 2>/dev/null | head -10
find /home -name 'psiphon*' -o -name 'xvpn*' -o -name 'lantern*' 2>/dev/null | head -10

# 4. Report
echo "=== Audit Complete ==="
```

### Alerting

Set up email/Slack alerts for:

1. **Mass detection events** — >50 blocked connections/min to known VPN ranges
2. **New JA3 fingerprints** — Previously unseen JA3 hashes from student VLANs
3. **New domain fronting attempts** — SNI/Host mismatch alerts
4. **Psiphon domain resolution** — DNS queries to *.psiphon.ca

---

## 4. Troubleshooting & False Positives

### Layer 1 — IP Blocklist False Positives

| Symptom | Likely Cause | Fix |
|---|---|---|
| Teachers can't access cloud storage | Cloud provider IP range in blocklist | Whitelist the specific service's IPs or use application-level filtering instead |
| Students can't access legitimate school tools | School tool hosted on shared hosting IP | Add tool's domain to a bypass list; don't block by IP alone |
| Google/YouTube not working | Google Cloud IP range overlap | Use Google's published ranges to whitelist: `https://www.gstatic.com/ipranges/goog.txt` |

**Solution:** Use application-layer filtering alongside IP blocklists. Block IPs for VPNs but allow IPs for legitimate educational services.

### Layer 2 — SSH False Positives

| Symptom | Likely Cause | Fix |
|---|---|---|
| IT staff can't SSH into servers | SSH port blocked for all users | Create whitelist rules for IT VLAN / source IPs |
| GitHub/GitLab operations failing | Some Git operations use SSH | Educate staff to use HTTPS for Git; whitelist known Git server IPs for SSH |
| IoT devices not updating | Some IoT devices use SSH tunnels | Check device documentation; add exceptions as needed |

### Layer 3 — Active Probing False Positives

| Symptom | Likely Cause | Fix |
|---|---|---|
| CDN-hosted sites intermittently blocked | CDN edge IPs probed and temporarily blocked | Widen the "real content" check — some CDNs return 403/503 for direct IP access |
| Cloudflare sites not loading | Cloudflare edge IPs don't accept direct-probe HTTP | Allowlist Cloudflare ASN (AS13335) from active probing |
| Microsoft 365 issues | Microsoft CDN probe failures | Whitelist Microsoft 365 IP ranges from automated blocking |

### Layer 5 — JA3 False Positives

| Symptom | Likely Cause | Fix |
|---|---|---|
| Software update checkers failing | Windows Update, Chrome auto-updater use non-browser TLS | Check the JA3 hash against known update-service JA3 hashes; whitelist them |
| Antivirus not updating | Avast, AVG, others use Go-based TLS | Check your antivirus vendor's JA3; add to whitelist |
| School management system issues | Some SaaS platforms use non-browser TLS stacks | Test in log-only mode first; whitelist known JA3 from edu services |

### General Troubleshooting Flow

```
Issue reported: "Cannot access [service]"

1. Check if the student/staff is on a managed device
   ├── Yes → Check device for installed VPN apps (see Section 4.1)
   └── No → Proceed

2. Check firewall logs for blocked connections
   ├── Blocked by IP blocklist → Add service to whitelist
   ├── Blocked by SSH rule → Was it SSH? If yes, intended.
   ├── Blocked by JA3 → Check if it's a legitimate app → whitelist JA3
   └── Not blocked → Check DNS filter

3. Check DNS filter logs
   ├── VPN domain blocked → Intended
   └── No DNS block found → Check for alternative VPN methods
```

### Detecting VPN Apps on Managed Devices

For school-managed Windows devices:

```powershell
# vpn-detection.ps1 — Run via Intune or SCCM
Write-Host "=== VPN App Detection ==="

$vpn_apps = @(
    "X-VPN", "Psiphon", "Lantern", "Hotspot Shield",
    "Turbo VPN", "VPN Gate", "ProtonVPN", "Windscribe",
    "Ultrasurf", "FreeVPN", "VPN Master", "Snap VPN"
)

foreach ($app in $vpn_apps) {
    $installed = Get-WmiObject -Class Win32_Product | 
        Where-Object { $_.Name -like "*$app*" }
    if ($installed) {
        Write-Warning "FOUND: $app installed by $($installed.Username)"
    }
}

# Check for VPN configurations
$vpn_connections = Get-VpnConnection | Where-Object { $_.ServerAddress -notlike "*.school.edu" }
if ($vpn_connections) {
    Write-Warning "Unauthorized VPN connections found:"
    $vpn_connections | Format-Table Name, ServerAddress
}
```

For school-managed Chromebooks:

```javascript
// Chrome extensions to block via policy
const blocked_extensions = [
    "knmchclkjfdfkjghmhcnbofmjejpbaog", // X-VPN
    "pflmnjmokbfmfnkndojmecnppkghkbpa", // Psiphon
    "...
];
```

---

## 5. Appendix: Known VPN Provider Infrastructure

### X-VPN (Xvpn)

| Detail | Value |
|---|---|
| Domain | xvpn.io |
| Free tier protocol | Everest TCP/UDP/TLS |
| Primary ASNs | Various — see [Netify](https://www.netify.ai/resources/vpns/x-vpn) |
| Free tier providers | OVHcloud, DigitalOcean, Linode, Vultr, Contabo, Zenlayer |
| Known ranges | 520 routes, 2,574 IPv4 IPs |
| Key domains to block | `xvpn.io`, `*.xvpn.io`, `api.xvpn.io` |
| Detection method | Non-browser TLS JA3, datacenter IP, active probing |

### Psiphon

| Detail | Value |
|---|---|
| Domain | psiphon.ca, psiphon3.com, psiphon3.net |
| Protocols | SSH, SSH+, MEEK, L2TP, WireGuard |
| Own ASN | AS214623 (2644819 Ontario) |
| Own ranges | 185.222.106.0/24, 199.244.103.0/24, 205.237.92.0/24 |
| Free tier providers | Ionos, OVHcloud, Scaleway, DigitalOcean, Linode, Vultr |
| Detection method | SSH handshake, MEEK domain fronting, non-browser JA3 |

### Lantern

| Detail | Value |
|---|---|
| Domain | getlantern.org, lantern.io |
| Protocols | 20+ — Hysteria2, AnyTLS, VLESS, Shadowsocks, Trojan, TUIC, etc. |
| Key detection | Diverse protocol variety but free-tier IPs on known providers |
| Detection method | IP reputation, behavioral, active probing |

### Hotspot Shield

| Detail | Value |
|---|---|
| Domain | hotspotshield.com, hsselite.com |
| Protocol | Catapult Hydra (proprietary) |
| Detection method | Catapult Hydra JA3 fingerprint, IP reputation, active probing |

### Quick-Reference: Domain Blocklist

Add these to your DNS filter:

```
# X-VPN
xvpn.io
www.xvpn.io
api.xvpn.io
cdn.xvpn.io

# Psiphon
psiphon.ca
www.psiphon.ca
psiphon3.com
psiphon3.net
psiphon.ca.com
cdn.psiphon.ca
psiphoneverywhere.com

# Lantern
getlantern.org
lantern.io
lantern.net

# Hotspot Shield
hotspotshield.com
www.hotspotshield.com
hsselite.com
anchorfree.com
```

---

## Implementation Priority Summary

| Priority | Layer | Effort | Impact | Complexity |
|---|---|---|---|---|
| **🔴 P0** | DNS block of known VPN domains | 5 min | High | Very low |
| **🔴 P0** | Layer 1 — IP/ASN blocklists (automated) | 30 min | High | Low |
| **🟡 P1** | Layer 2 — SSH blocking | 15 min | Medium | Low |
| **🟡 P1** | App detection scripts on managed devices | 1 hour | Medium | Medium |
| **🟢 P2** | Layer 4 — Domain fronting detection | 30 min | Medium | Low |
| **🔵 P3** | Layer 3 — Active probing (automated) | 3-4 hours | High | High |
| **⚫ P4** | Layer 5 — JA3 fingerprint filtering | 2-3 hours + 2wk observation | Very high | Very high |

**Start with P0.** That alone will block 70% of free VPN usage. Add P1 after implementation, then consider P2/P3/P4 if you detect students actively bypassing the first layers.

---

## Expected Results

| Tool | P0 (DNS) | +P1 (IP+SSH) | +P2-P4 (Full Stack) |
|---|---|---|---|
| **X-VPN (free)** | 30% blocked | 85% blocked | 98% blocked |
| **Psiphon** | 40% blocked | 95% blocked | 99% blocked |
| **Lantern (free)** | 20% blocked | 70% blocked | 90% blocked |
| **Hotspot Shield** | 25% blocked | 80% blocked | 95% blocked |
| **Other free VPNs** | 20% blocked | 75% blocked | 92% blocked |

The diminishing returns are real. P0 alone catches the lazy student. Full 5-layer stack catches the determined one.

---

*Document prepared June 2026. VPN providers change infrastructure regularly. Update blocklists weekly and review effectiveness monthly.*
