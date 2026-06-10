# VPN Monetised Service — Production Implementation Guide

> **Target audience:** An AI agent with SSH access to your VPS.
> **Goal:** End-to-end deployment of a 3-tier paid VPN service on a single VPS.

> **⚠️ Important note on configs and scripts:** The configuration files, scripts, and code blocks throughout this guide are **representations of the intended end state** — not rigid, immutable instructions. They show the shape of what each config should look like. When using them:
> - Replace placeholder values (like `REPLACE_ME`, `${DOMAIN}`, `gen-me`) with your actual values
> - Adapt paths and ports if your VPS environment differs
> - Test each config against the running service before distributing to clients
> - The exact token/port/IP values will differ — the structure is what matters

---

## Table of Contents

1. [Prerequisites & Variables](#1-prerequisites--variables)
2. [VPS: Base Setup & Hardening](#2-vps-base-setup--hardening)
3. [Protocol Installation](#3-protocol-installation)
4. [Nginx Fallback & Probe Defense](#4-nginx-fallback--probe-defense)
5. [VPS NAT & Egress Setup](#5-vps-nat--egress-setup)
6. [Tier Configurations](#6-tier-configurations)
7. [Management Panel (Marzban)](#7-management-panel-marzban)
8. [Client App](#8-client-app)
9. [Activation Code & Subscription System](#9-activation-code--subscription-system)
10. [Security Hardening](#10-security-hardening)
11. [Monitoring & Alerting](#11-monitoring--alerting)
12. [Backup & Disaster Recovery](#12-backup--disaster-recovery)
13. [Operational Runbooks](#13-operational-runbooks)

---

## 1. Prerequisites & Variables

### 1.1 What You Need

| Resource | Purpose | Specs |
|----------|---------|-------|
| VPS (cloud) | Everything — protocol termination, egress, panel, API | 1 vCPU, 1GB RAM, Ubuntu 24.04 (minimum) |
| Domain name | TLS certificates, REALITY SNI | e.g., `yourdomain.com` |
| Cloudflare account | DNS proxy (hide VPS IP) + DNS-01 challenge for wildcard cert | Free tier |
| Cloudflare API token | DNS edit permission for `*.yourdomain.com` | Create at https://dash.cloudflare.com/profile/api-tokens |

A single VPS with these specs handles 50-100 concurrent clients across all four protocols without breaking a sweat. The protocols (Hysteria2, TUIC) are Go-based and ChaCha20-Poly1305 is hardware-accelerated on any modern cloud CPU.

### 1.2 Variables — FILL THESE IN BEFORE STARTING

```bash
# ===== DOMAIN =====
DOMAIN="yourdomain.com"                    # Your registered domain
EMAIL="you@example.com"                    # For Let's Encrypt

# ===== VPS =====
VPS_IP="1.2.3.4"                          # VPS public IPv4
VPS_SSH_PORT="22"                          # Will be changed to 42069 later

# ===== PORTS =====
HYSTERIA2_PORT="443"                       # Premium - Hysteria2 (QUIC/UDP)
VLESS_PORT="443"                           # Premium fallback - VLESS-REALITY (TCP/TLS)
TUIC_PORT="8443"                           # Lite - TUIC V5 (QUIC/UDP)
AMNEZIA_PORT="9443"                        # Standard - AmneziaWG (UDP)
MARZBAN_PORT="8444"                        # Management panel (HTTPS)
API_PORT="443"                             # Activation API (on api. subdomain)

# ===== CLOUDFLARE =====
CLOUDFLARE_EMAIL="you@example.com"              # Your Cloudflare login email
CLOUDFLARE_API_TOKEN="gen-me-dns-edit-token"    # Create at https://dash.cloudflare.com/profile/api-tokens
                                                # Permissions: Zone:DNS:Edit for your domain

# ===== ORCHESTRATOR MODE =====
# Set to "true" if this VPS is the orchestrator (runs collector + activation API).
# Set to "false" for pure workers (just protocols + load agent).
# Only ONE VPS in the pool should have this set to true.
# If you're running a single VPS (no scale-out), leave as "true".
ORCHESTRATOR_MODE="true"

# ===== PASSWORDS (generate fresh with: openssl rand -base64 32) =====
MARZBAN_DB_PASS="gen-me"
MARZBAN_ADMIN_PASS="gen-me"
ADMIN_API_KEY="gen-me-64-chars"
SALAMANDER_PASSWORD="gen-me"
HYSTERIA2_AUTH_PASSWORD="gen-me"
```

### 1.3 DNS Pre-Setup (Cloudflare)

```
# In Cloudflare DNS settings — ALL records proxied (orange cloud):
Type  Name           Content          Proxy
A     @              <VPS_IP>         ✅ Proxied
A     panel          <VPS_IP>         ✅ Proxied
A     api            <VPS_IP>         ✅ Proxied
CNAME *              yourdomain.com   ✅ Proxied

# SSL/TLS setting: Full (strict)
# Always Use HTTPS: ON
```

---

## 2. VPS: Base Setup & Hardening

### 2.1 Initial Update

```bash
apt update && apt upgrade -y
apt install -y \
  curl wget git vim tmux htop iftop iperf3 \
  ca-certificates gnupg lsb-release \
  ufw fail2ban nginx certbot python3-certbot-dns-cloudflare \
  iptables-persistent netfilter-persistent \
  vnstat net-tools tcpdump \
  jq unzip qrencode bc mailutils

systemctl enable --now vnstat
systemctl enable --now fail2ban
systemctl enable --now netfilter-persistent
```

### 2.2 Change SSH Port

```bash
sed -i 's/^#Port 22/Port 42069/' /etc/ssh/sshd_config
echo "Port 22" >> /etc/ssh/sshd_config.d/00-temp.conf

# Disable root password login
sed -i 's/^#PermitRootLogin prohibit-password/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
sed -i 's/^#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config

systemctl restart sshd

# TEST NEW PORT BEFORE REMOVING 22:
# From a NEW terminal: ssh -p 42069 root@${VPS_IP}
# If it works: rm /etc/ssh/sshd_config.d/00-temp.conf && systemctl restart sshd
```

### 2.3 UFW Firewall

```bash
ufw default deny incoming
ufw default allow outgoing

# SSH
ufw allow 42069/tcp

# Client-facing ports
ufw allow 443/tcp        # VLESS-REALITY + Nginx + API
ufw allow 443/udp        # Hysteria2 QUIC
ufw allow 8443/udp       # TUIC V5
ufw allow 9443/udp       # AmneziaWG

# Management
ufw allow 8444/tcp       # Marzban panel (restrict to specific IPs if possible)

ufw enable
ufw status verbose
```

### 2.4 Kernel Tuning

```bash
cat > /etc/sysctl.d/99-vpn.conf << 'EOF'
fs.file-max = 655350
net.core.rmem_default = 262144
net.core.wmem_default = 262144
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_syncookies = 1
EOF

sysctl --system
```

### 2.5 Fail2ban

```bash
cat > /etc/fail2ban/jail.local << 'EOF'
[DEFAULT]
bantime = 3600
findtime = 600
maxretry = 5
ignoreip = 127.0.0.1/8

[sshd]
enabled = true
port = 42069
maxretry = 3

[nginx-http-auth]
enabled = true

[nginx-botsearch]
enabled = true
EOF

systemctl restart fail2ban
```

---

## 3. Protocol Installation

### 3.1 Hysteria2 (Premium Tier)

```bash
# Download latest Hysteria2
mkdir -p /etc/hysteria
cd /tmp
HY2_VERSION=$(curl -s https://api.github.com/repos/apernet/hysteria/releases/latest | jq -r '.tag_name')
wget https://github.com/apernet/hysteria/releases/download/${HY2_VERSION}/hysteria-linux-amd64
chmod +x hysteria-linux-amd64
mv hysteria-linux-amd64 /usr/local/bin/hysteria

# Generate TLS certificate
openssl req -x509 -nodes -newkey ec:<(openssl ecparam -name prime256v1) \
  -keyout /etc/hysteria/server.key \
  -out /etc/hysteria/server.crt \
  -days 3650 \
  -subj "/CN=${DOMAIN}"

# Create ACL
cat > /etc/hysteria/acl.json << 'EOF'
{
  "rules": []
}
EOF

# Systemd service
cat > /etc/systemd/system/hysteria-premium.service << 'EOF'
[Unit]
Description=Hysteria2 Premium Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/hysteria server -c /etc/hysteria/premium.yaml
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable hysteria-premium
```

### 3.2 Xray + VLESS-REALITY (Premium Fallback)

```bash
# Install Xray
bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install

# Generate REALITY keypair
XRAY_KEYS=$(xray x25519)
REALITY_PRIVATE_KEY=$(echo "$XRAY_KEYS" | grep "Private" | awk '{print $3}')
REALITY_PUBLIC_KEY=$(echo "$XRAY_KEYS" | grep "Public" | awk '{print $3}')
SHORT_ID=$(openssl rand -hex 8)

echo "REALITY_PRIVATE_KEY=$REALITY_PRIVATE_KEY"
echo "REALITY_PUBLIC_KEY=$REALITY_PUBLIC_KEY"
echo "SHORT_ID=$SHORT_ID"
# SAVE THESE — you need them for client configs and activation API

systemctl enable xray
```

### 3.3 TUIC V5 (Lite Tier)

```bash
# Download TUIC server
mkdir -p /opt/tuic
cd /tmp
TUIC_VERSION=$(curl -s https://api.github.com/repos/EAimTY/tuic/releases/latest | jq -r '.tag_name')
wget https://github.com/EAimTY/tuic/releases/download/${TUIC_VERSION}/tuic-server-${TUIC_VERSION}-x86_64-unknown-linux-gnu
chmod +x tuic-server-*
mv tuic-server-* /opt/tuic/tuic-server

# Generate TLS cert
openssl req -x509 -nodes -newkey ec:<(openssl ecparam -name prime256v1) \
  -keyout /opt/tuic/server.key \
  -out /opt/tuic/server.crt \
  -days 3650 \
  -subj "/CN=${DOMAIN}"

# Systemd service
cat > /etc/systemd/system/tuic-lite.service << 'EOF'
[Unit]
Description=TUIC V5 Lite Server
After=network.target

[Service]
Type=simple
ExecStart=/opt/tuic/tuic-server -c /opt/tuic/lite.json
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable tuic-lite
```

### 3.4 AmneziaWG (Standard Tier)

```bash
# Install AmneziaWG
curl -s https://api.github.com/repos/amnezia-vpn/amneziawg-linux/releases/latest | \
  jq -r '.assets[] | select(.name | contains("amd64.deb")) | .browser_download_url' | \
  xargs wget -O /tmp/awg.deb

dpkg -i /tmp/awg.deb
apt install -f -y

# Generate keys
mkdir -p /etc/amnezia/standard
cd /etc/amnezia/standard
awg genkey | tee server_private.key | awg pubkey > server_public.key

# Systemd service
cat > /etc/systemd/system/awg-standard.service << 'EOF'
[Unit]
Description=AmneziaWG Standard Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/awg-quick up /etc/amnezia/standard/awg0.conf
ExecStop=/usr/bin/awg-quick down /etc/amnezia/standard/awg0.conf
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable awg-standard
```

---

## 4. Nginx Fallback & Probe Defense

Nginx serves as:
1. The fallback destination for VLESS-REALITY (unauthenticated probes get forwarded here by Xray)
2. A TLS termination point for Let's Encrypt
3. A reverse proxy for the Activation API

### 4.1 Nginx Configuration

```bash
# Remove default site
rm -f /etc/nginx/sites-enabled/default

# Real fallback (probe absorption — internal only, on 8080)
cat > /etc/nginx/sites-available/fallback << EOF
server {
    listen 127.0.0.1:8080;
    
    server_name _;
    
    root /var/www/fallback;
    index index.html;
    
    location / {
        try_files \$uri \$uri/ =404;
    }
    
    location ~ /\. {
        deny all;
    }
    
    limit_req_zone \$binary_remote_addr zone=probe:10m rate=10r/s;
    limit_req zone=probe burst=20 nodelay;
    
    add_header X-Content-Type-Options nosniff;
    add_header X-Frame-Options DENY;
    add_header Referrer-Policy strict-origin-when-cross-origin;
}
EOF

# Create the fake corporate website
mkdir -p /var/www/fallback
cat > /var/www/fallback/index.html << 'HTMLEOF'
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Cloud Services</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
               max-width: 800px; margin: 50px auto; padding: 0 20px; line-height: 1.6; color: #333; }
        h1 { color: #0078d4; }
    </style>
</head>
<body>
    <h1>Cloud Infrastructure Services</h1>
    <p>This domain hosts enterprise cloud infrastructure services.</p>
    <p>For inquiries, please contact the domain administrator.</p>
</body>
</html>
HTMLEOF

ln -s /etc/nginx/sites-available/fallback /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

### 4.2 Let's Encrypt Certificate (Wildcard DNS-01 Challenge)

> ⚠️ Wildcard certificates (`*.yourdomain.com`) **cannot** use HTTP validation. We use Cloudflare's DNS-01 challenge instead. This requires the Cloudflare API token you added to your variables in Section 1.2.

```bash
# Create Cloudflare credentials file for certbot
mkdir -p /root/.secrets
cat > /root/.secrets/cloudflare.ini << EOF
# Cloudflare API token used by Certbot
dns_cloudflare_api_token = ${CLOUDFLARE_API_TOKEN}
EOF
chmod 600 /root/.secrets/cloudflare.ini

# Request the wildcard certificate via DNS-01 challenge
certbot certonly \
  --dns-cloudflare \
  --dns-cloudflare-credentials /root/.secrets/cloudflare.ini \
  -d ${DOMAIN} \
  -d "*.${DOMAIN}" \
  --email ${EMAIL} \
  --agree-tos \
  --non-interactive

# Verify certificate was issued
ls -la /etc/letsencrypt/live/${DOMAIN}/

# Auto-renewal — certbot handles this automatically with a systemd timer,
# but we add a hook to reload nginx and restart services that use the cert
mkdir -p /etc/letsencrypt/renewal-hooks/post
cat > /etc/letsencrypt/renewal-hooks/post/reload-services.sh << 'EOF'
#!/bin/bash
systemctl reload nginx 2>/dev/null || true
systemctl restart hysteria-premium 2>/dev/null || true
systemctl restart tuic-lite 2>/dev/null || true
docker restart marzban-nginx 2>/dev/null || true
EOF
chmod +x /etc/letsencrypt/renewal-hooks/post/reload-services.sh

# Test renewal process (dry run)
certbot renew --dry-run
```

---

## 5. VPS NAT & Egress Setup

All client traffic exits through the VPS public IP. Simple MASQUERADE does the job.

```bash
#!/bin/bash
# /usr/local/bin/vps-nat-setup.sh
# This script is a REPRESENTATION of the NAT setup. The exact interface name
# and rules may vary by VPS provider — adjust VPS_MAIN_IF if needed.
set -e

VPS_MAIN_IF=$(ip route get 1.1.1.1 | grep -oP 'dev \K\S+')
echo "VPS main interface: $VPS_MAIN_IF"

# Ensure IP forwarding is on
sysctl -w net.ipv4.ip_forward=1

# NAT masquerade — all outbound traffic gets VPS IP
iptables -t nat -C POSTROUTING -o ${VPS_MAIN_IF} -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -o ${VPS_MAIN_IF} -j MASQUERADE

echo "[+] NAT masquerade on ${VPS_MAIN_IF}"

# Save rules using netfilter-persistent (survives reboot)
mkdir -p /etc/iptables
iptables-save > /etc/iptables/rules.v4
ip6tables-save > /etc/iptables/rules.v6 2>/dev/null || true

echo "[+] NAT setup complete. Rules saved to /etc/iptables/rules.v4"
```

```bash
chmod +x /usr/local/bin/vps-nat-setup.sh
/usr/local/bin/vps-nat-setup.sh

# The netfilter-persistent service (installed in Section 2.1) automatically
# loads /etc/iptables/rules.v4 on boot. This is the PRIMARY persistence mechanism.
# The systemd service below is a SECONDARY safety net.

# Persist across reboots (safety net — primary is netfilter-persistent)
cat > /etc/systemd/system/vps-nat.service << 'EOF'
[Unit]
Description=VPS NAT Rules (safety net)
After=network-online.target netfilter-persistent.service
Wants=network-online.target
Before=hysteria-premium.service tuic-lite.service awg-standard.service xray.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/vps-nat-setup.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now vps-nat
```

### Verification

```bash
# Test: VPS can reach internet
curl -s https://ifconfig.me && echo ""
# Should return your VPS IP

# Check NAT rule
iptables -t nat -L POSTROUTING -v | grep MASQUERADE

# Verify rules are saved for reboot
iptables-save | grep -q MASQUERADE && echo "NAT rules saved for reboot: OK"
```

---

## 6. Tier Configurations

### 6.1 Lite Plan ($5) — TUIC V5, Web Browsing Only

Key constraints:
- Congestion control: `cubic` (not BBR — intentionally slower)
- **No UDP relay** — this is critical. Gaming, voice calls, and streaming UDP are impossible without it.
- Low bandwidth via token bucket

```bash
cat > /opt/tuic/lite.json << 'EOF'
{
  "port": 8443,
  "ip": "0.0.0.0",
  "token": ["REPLACE_ME_TUIC_TOKEN"],

  "tls": {
    "certificate": "/opt/tuic/server.crt",
    "private_key": "/opt/tuic/server.key"
  },

  "auth_timeout": 1000,
  "max_external_packet_size": 1500,
  "gc_interval": 3000,
  "gc_lifetime": 15000,

  "congestion_control": "cubic",
  "heartbeat": "10s",

  "log_level": "warn"
}
EOF

systemctl restart tuic-lite
```

**CRITICAL for Lite clients:** In the client config distributed to Lite users, set:
```json
{
  "udp_relay": false,
  "congestion_control": "cubic"
}
```

This means the client won't even attempt UDP, making gaming impossible while web browsing works fine over TCP.

### 6.2 Standard Plan ($9) — AmneziaWG, Deliberately Held Back

The Standard plan uses AmneziaWG with intentionally high obfuscation overhead — more junk packets, wider jitter range, lower MTU. It's usable but feels noticeably worse than Premium.

```bash
cat > /etc/amnezia/standard/awg0.conf << EOF
[Interface]
Address = 10.88.0.1/24
ListenPort = 9443
PrivateKey = $(cat /etc/amnezia/standard/server_private.key)

# HIGH obfuscation = more overhead = deliberately worse latency than Premium
Jc = 8
Jmin = 60
Jmax = 120
S1 = 82
S2 = 182
H1 = 1120574779
H2 = 1792308163
H3 = 1191537151
H4 = 1627368923

# Lower MTU = more fragmentation = worse for gaming
MTU = 1280
EOF

# Rate limit Standard plan to 30 Mbps PER CLIENT (not total)
# The --hashlimit-mode srcip flag is CRITICAL — without it, the entire
# Standard tier shares one 30 Mbps pool instead of each client getting 30 Mbps.
iptables -A FORWARD -i awg0 -m hashlimit \
    --hashlimit-name std_limit \
    --hashlimit-above 30mbit \
    --hashlimit-burst 5mbit \
    --hashlimit-mode srcip \
    -j DROP

systemctl restart awg-standard
```

### 6.3 Premium Plan ($10) — Hysteria2 + VLESS-REALITY Fallback

The full experience. Hysteria2 with Brutal CC for gaming, Salamander obfuscation to beat DPI. VLESS-REALITY as automatic fallback when UDP is blocked.

```bash
cat > /etc/hysteria/premium.yaml << EOF
listen: :443

tls:
  cert: /etc/hysteria/server.crt
  key: /etc/hysteria/server.key

obfs:
  type: salamander
  salamander:
    password: "${SALAMANDER_PASSWORD}"

auth:
  type: password
  password: "${HYSTERIA2_AUTH_PASSWORD}"

bandwidth:
  up: 500 mbps
  down: 500 mbps

ignoreClientBandwidth: false

congestion:
  type: brutal
  brutal:
    up: 500
    down: 500

quic:
  initStreamReceiveWindow: 16777216
  maxStreamReceiveWindow: 16777216
  initConnReceiveWindow: 33554432
  maxConnReceiveWindow: 33554432
  maxIdleTimeout: 120s
  keepAlivePeriod: 30s
  disablePathMTUDiscovery: false

speedTest: false

sniff:
  enable: true
  timeout: 2s
  rewriteDomain: false
  tcpPorts: 80,443,8080,8443
  udpPorts: 443,8443,3478,3479,5060,5061,8801-8810

resolver:
  type: udp
  udp:
    addr: 1.1.1.1:53

acl:
  file: /etc/hysteria/acl.json

log:
  level: warn
  file: /var/log/hysteria/premium.log
EOF

systemctl restart hysteria-premium
```

VLESS-REALITY fallback config:

```bash
cat > /usr/local/etc/xray/config.json << XEOF
{
  "log": {
    "loglevel": "warning",
    "access": "/var/log/xray/access.log",
    "error": "/var/log/xray/error.log"
  },
  "inbounds": [
    {
      "tag": "vless-reality-premium",
      "listen": "0.0.0.0",
      "port": 443,
      "protocol": "vless",
      "settings": {
        "clients": [],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "127.0.0.1:8080",
          "xver": 0,
          "serverNames": [
            "www.microsoft.com",
            "microsoft.com",
            "windowsupdate.microsoft.com"
          ],
          "privateKey": "${REALITY_PRIVATE_KEY}",
          "minClient": "",
          "maxClient": "",
          "maxTimediff": 0,
          "shortIds": [
            "${SHORT_ID}"
          ]
        }
      },
      "sniffing": {
        "enabled": true,
        "destOverride": ["http", "tls", "quic"]
      }
    }
  ],
  "outbounds": [
    {
      "tag": "direct",
      "protocol": "freedom",
      "settings": {
        "domainStrategy": "UseIP"
      }
    },
    {
      "tag": "block",
      "protocol": "blackhole",
      "settings": {}
    }
  ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      {
        "type": "field",
        "ip": ["geoip:private"],
        "outboundTag": "block"
      },
      {
        "type": "field",
        "domain": ["geosite:category-ads-all"],
        "outboundTag": "block"
      }
    ]
  }
}
XEOF

systemctl restart xray
```

### 6.4 Port Conflict Note

Hysteria2 (QUIC/UDP) and Xray (TCP/TLS) both use port 443. This is fine — QUIC and TCP are different IP protocols on the same port and don't conflict. The port map:

| Port | Protocol | Service | Tier |
|------|----------|---------|------|
| 443/udp | QUIC | Hysteria2 (Salamander, Brutal CC) | Premium |
| 443/tcp | TCP+TLS+REALITY | Xray VLESS-REALITY (uTLS Chrome) | Premium fallback |
| 8443/udp | QUIC | TUIC V5 (cubic, no UDP relay) | Lite |
| 9443/udp | UDP | AmneziaWG (high obfuscation) | Standard |
| 8080/tcp | HTTP | Nginx probe absorption (internal) | — |
| 8444/tcp | HTTPS | Marzban panel | — |

### 6.5 Certificate Trust Note (Hysteria2 Client Config)

The Hysteria2 server uses a **self-signed certificate** (generated by `openssl req -x509` in Section 3.1). Client apps will reject this unless you configure trust:

**Option A — pinSHA256 (recommended):** Generate the cert hash and include it in the client config:
```bash
# Generate the SHA-256 fingerprint of your self-signed cert
openssl x509 -in /etc/hysteria/server.crt -noout -fingerprint -sha256 | cut -d= -f2 | tr -d ':'
```
Then in the client config (generated by the activation API), add:
```json
{
  "skip_cert_verify": false,
  "pinSHA256": "PASTE_THE_FINGERPRINT_HERE"
}
```

**Option B — skip_cert_verify (easier, less secure):**
```json
{
  "skip_cert_verify": true
}
```
This trusts any cert but makes MITM possible. Fine for a school VPN service; not fine if you're protecting dissidents.

**If you use a proper CA-signed cert (e.g., from Let's Encrypt):** Set `skip_cert_verify: false` with no pin — the client will trust it normally. Update the Hysteria2 config to use `/etc/letsencrypt/live/${DOMAIN}/fullchain.pem` instead of the self-signed cert.

---

## 7. Management Panel (Marzban)

### 7.1 Install Docker & Marzban

```bash
# Install Docker
curl -fsSL https://get.docker.com | bash

# Create Marzban directory
mkdir -p /opt/marzban/mysql /opt/marzban/logs

# Docker Compose
cat > /opt/marzban/docker-compose.yml << 'EOF'
services:
  marzban:
    image: gozargah/marzban:latest
    restart: always
    container_name: marzban
    ports:
      - "127.0.0.1:8000:8000"
    env_file: .env
    volumes:
      - /opt/marzban:/var/lib/marzban
      - /opt/marzban/logs:/var/log/marzban
    depends_on:
      mariadb:
        condition: service_healthy

  mariadb:
    image: mariadb:11
    restart: always
    container_name: marzban-db
    environment:
      MYSQL_ROOT_PASSWORD: ${MARZBAN_DB_PASS}
      MYSQL_DATABASE: marzban
      MYSQL_USER: marzban
      MYSQL_PASSWORD: ${MARZBAN_DB_PASS}
    volumes:
      - /opt/marzban/mysql:/var/lib/mysql
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 5

  nginx:
    image: nginx:alpine
    restart: always
    container_name: marzban-nginx
    ports:
      - "8444:443"
    volumes:
      - /opt/marzban/nginx.conf:/etc/nginx/nginx.conf:ro
      - /etc/letsencrypt/live/${DOMAIN}/fullchain.pem:/etc/ssl/certs/server.crt:ro
      - /etc/letsencrypt/live/${DOMAIN}/privkey.pem:/etc/ssl/private/server.key:ro
    depends_on:
      - marzban
EOF

# Environment file
cat > /opt/marzban/.env << EOF
UVICORN_HOST=0.0.0.0
UVICORN_PORT=8000
SUDO_USERNAME=admin
SUDO_PASSWORD=${MARZBAN_ADMIN_PASS}

SQLALCHEMY_DATABASE_URL=mysql+pymysql://marzban:${MARZBAN_DB_PASS}@mariadb:3306/marzban

XRAY_EXECUTABLE_PATH=/usr/local/bin/xray
XRAY_ASSETS_PATH=/usr/local/share/xray
XRAY_CONFIG_PATH=/usr/local/etc/xray/config.json

XRAY_SUBSCRIPTION_URL_PREFIX=https://panel.${DOMAIN}:8444
SUB_UPDATE_INTERVAL=12

UVICORN_SSL_CERTFILE=/var/lib/marzban/certs/fullchain.pem
UVICORN_SSL_KEYFILE=/var/lib/marzban/certs/privkey.pem
EOF

# Nginx config for Marzban container
cat > /opt/marzban/nginx.conf << EOF
events {
    worker_connections 1024;
}

http {
    server {
        listen 443 ssl http2;
        server_name panel.${DOMAIN};

        ssl_certificate /etc/ssl/certs/server.crt;
        ssl_certificate_key /etc/ssl/private/server.key;

        location / {
            proxy_pass http://marzban:8000;
            proxy_set_header Host \$host;
            proxy_set_header X-Real-IP \$remote_addr;
            proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto \$scheme;
        }

        location ~ ^/(api|docs|redoc|static)/ {
            proxy_pass http://marzban:8000;
            proxy_set_header Host \$host;
            proxy_set_header X-Real-IP \$remote_addr;
        }
    }
}
EOF

# Start
cd /opt/marzban && docker compose up -d
```

### 7.2 Access Marzban

```
URL: https://panel.${DOMAIN}:8444
Username: admin
Password: ${MARZBAN_ADMIN_PASS}

> **⚠️ Panel security note:** The Marzban panel is exposed on port 8444 with only basic auth protecting it. Anyone who discovers the URL can attempt to brute-force the password.
>
> **Recommended mitigation — Cloudflare Access (free):**
> 1. Go to Cloudflare Zero Trust → Access → Applications
> 2. Add a self-hosted application: `https://panel.${DOMAIN}:8444`
> 3. Require Google/GitHub OAuth login before anyone reaches the panel
> 4. This adds a full authentication layer in front of Marzban, entirely separate from the VPN traffic
>
> **Alternative:** Use `ufw allow from YOUR_IP to any port 8444 proto tcp` to whitelist only your IP.

---

## 8. Client App

The client app is a separate project — its specifications have not been finalized here. The server-side API contract is what matters for this implementation guide.

### 8.1 What the App Must Do

- Accept an activation code from the user
- Call `POST /api/v1/activate` with the code and a persistent device ID
- Import the returned protocol config and connect to the specified server
- If the connection fails for a sustained period, call `POST /api/v1/migrate` to get a new server assignment
- Support the VPN protocols in use: Hysteria2, VLESS-REALITY, TUIC V5, AmneziaWG

### 8.2 Distribution

Distribute the app as an APK directly (not through app stores). The app is not part of this server-side implementation guide.

---

## 9. Activation Code & Subscription System

### 9.1 Setup

```bash
mkdir -p /opt/activation-api
apt install -y python3 python3-pip python3-venv
pip3 install fastapi uvicorn sqlalchemy pydantic
```

### 9.2 API Server

```python
# /opt/activation-api/main.py
from fastapi import FastAPI, HTTPException, Header
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from pydantic import BaseModel
from sqlalchemy import create_engine, Column, String, Integer, Boolean, DateTime, Text
from sqlalchemy.ext.declarative import declarative_base
from sqlalchemy.orm import sessionmaker
from datetime import datetime, timedelta
import secrets, os, subprocess, re, fcntl, time
from collections import defaultdict
from threading import Lock

app = FastAPI(title="VPN Activation API")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])

SQLALCHEMY_DATABASE_URL = os.getenv("DATABASE_URL", "sqlite:////opt/activation-api/activation.db")
engine = create_engine(SQLALCHEMY_DATABASE_URL)
SessionLocal = sessionmaker(bind=engine)
Base = declarative_base()

class ActivationCode(Base):
    __tablename__ = "activation_codes"
    id = Column(Integer, primary_key=True)
    code = Column(String(32), unique=True, index=True)
    tier = Column(String(16))
    duration_days = Column(Integer, default=30)
    created_at = Column(DateTime, default=datetime.utcnow)
    used = Column(Boolean, default=False)
    used_by_device = Column(String(64), nullable=True)
    used_at = Column(DateTime, nullable=True)
    middleman_id = Column(String(32), nullable=True)

class Device(Base):
    __tablename__ = "devices"
    id = Column(Integer, primary_key=True)
    device_id = Column(String(64), unique=True)
    device_name = Column(String(128))
    tier = Column(String(16))
    subscription_expiry = Column(DateTime)
    created_at = Column(DateTime, default=datetime.utcnow)
    protocol_config = Column(Text)
    active = Column(Boolean, default=True)

Base.metadata.create_all(bind=engine)

# ----- RATE LIMITING (in-memory, resets on restart) -----
admin_rate_limit_store = defaultdict(list)
RATE_LIMIT_WINDOW = 60  # seconds
RATE_LIMIT_MAX = 5      # max requests per window

def check_admin_rate_limit(client_ip: str):
    now = time.time()
    window_start = now - RATE_LIMIT_WINDOW
    admin_rate_limit_store[client_ip] = [
        t for t in admin_rate_limit_store[client_ip] if t > window_start
    ]
    if len(admin_rate_limit_store[client_ip]) >= RATE_LIMIT_MAX:
        raise HTTPException(status_code=429, detail="Rate limit exceeded. Try again later.")
    admin_rate_limit_store[client_ip].append(now)

class ActivateRequest(BaseModel):
    activation_code: str
    device_id: str
    device_name: str = "Unknown"

# ----- CONFIG GENERATORS -----

def generate_lite_config(device_id: str, server: str = None) -> dict:
    if server is None:
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")
    return {
        "protocol": "tuic", "version": 5,
        "server": server,
        "port": 8443,
        "uuid": secrets.token_hex(16),
        "password": secrets.token_hex(16),
        "udp_relay": False,
        "congestion_control": "cubic",
        "alpn": ["h3"],
        "sni": server,
        "skip_cert_verify": False,
    }

def generate_standard_config(device_id: str, server: str = None) -> dict:
    if server is None:
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")
    client_priv = subprocess.run(["awg", "genkey"], capture_output=True, text=True).stdout.strip()
    client_pub = subprocess.run(["awg", "pubkey"], capture_output=True, text=True, input=client_priv).stdout.strip()
    ip_last = abs(hash(device_id)) % 253 + 2
    client_ip = f"10.88.0.{ip_last}"

    # Add peer with file locking (prevents race conditions on concurrent activations)
    with open("/etc/amnezia/standard/awg0.conf", "a") as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        f.write(f"\n[Peer]\nPublicKey = {client_pub}\nAllowedIPs = {client_ip}/32\n")
        fcntl.flock(f, fcntl.LOCK_UN)
    subprocess.run(["awg", "addconf", "awg0", "/etc/amnezia/standard/awg0.conf"])

    return {
        "protocol": "amneziawg",
        "server": server,
        "port": 9443,
        "client_ip": client_ip,
        "client_private_key": client_priv,
        "server_public_key": open("/etc/amnezia/standard/server_public.key").read().strip(),
        "jc": 8, "jmin": 60, "jmax": 120,
        "s1": 82, "s2": 182,
        "h1": 1120574779, "h2": 1792308163,
        "h3": 1191537151, "h4": 1627368923,
        "mtu": 1280,
    }

def generate_premium_config(device_id: str, server: str = None) -> dict:
    if server is None:
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")
    hy2_config = open("/etc/hysteria/premium.yaml").read()
    hy2_pass = re.search(r'password:\s*"(.+)"', hy2_config).group(1)
    reality_pubkey = os.getenv("REALITY_PUBLIC_KEY", "")
    reality_sid = os.getenv("REALITY_SHORT_ID", "")

    return {
        "primary": {
            "protocol": "hysteria2",
            "server": server,
            "port": 443,
            "password": hy2_pass,
            "salamander_password": os.getenv("SALAMANDER_PASSWORD", ""),
            "sni": server,
            "skip_cert_verify": False,
        },
        "fallback": {
            "protocol": "vless",
            "server": server,
            "port": 443,
            "uuid": secrets.token_hex(16),
            "flow": "xtls-rprx-vision",
            "network": "tcp",
            "security": "reality",
            "reality_pbk": reality_pubkey,
            "sni": "www.microsoft.com",
            "fingerprint": "chrome",
            "short_id": reality_sid,
        }
    }

# ----- ENDPOINTS -----

@app.post("/api/v1/activate", response_model=dict)
async def activate(req: ActivateRequest):
    db = SessionLocal()
    code = db.query(ActivationCode).filter(
        ActivationCode.code == req.activation_code,
        ActivationCode.used == False
    ).first()

    if not code:
        db.close()
        raise HTTPException(status_code=404, detail="Invalid or already used code")

    # Check if device already exists
    existing = db.query(Device).filter(Device.device_id == req.device_id).first()
    if existing and existing.active:
        db.close()
        raise HTTPException(status_code=400, detail="Device already activated")

    # --- Orchestrator: read best worker from collector output ---
    # If /tmp/least_loaded_vps.json exists (written by collector every 60s),
    # use the least-loaded worker as the server. Otherwise fall back to
    # this VPS's own hostname (single-VPS mode or first boot).
    try:
        with open("/tmp/least_loaded_vps.json") as f:
            lb_data = json.load(f)
            server = lb_data["selected_worker"]
    except (FileNotFoundError, json.JSONDecodeError, KeyError):
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")

    # Generate tier-specific config
    generators = {
        "lite": generate_lite_config,
        "standard": generate_standard_config,
        "premium": generate_premium_config,
    }
    config = generators[code.tier](req.device_id, server)

    # Create device record
    device = Device(
        device_id=req.device_id,
        device_name=req.device_name,
        tier=code.tier,
        subscription_expiry=datetime.utcnow() + timedelta(days=code.duration_days),
        protocol_config=str(config),
    )
    db.add(device)

    # Mark code used
    code.used = True
    code.used_by_device = req.device_id
    code.used_at = datetime.utcnow()
    db.commit()
    db.close()

    return {
        "success": True,
        "tier": code.tier,
        "expires_at": device.subscription_expiry.isoformat(),
        "config": config,
    }

class MigrateRequest(BaseModel):
    device_id: str

@app.post("/api/v1/migrate")
async def migrate(req: MigrateRequest):
    """Reissue config for an existing device, pointing to the current best worker.

    Used when a worker VPS dies and the client needs a new server address.
    Does NOT modify the device record — same tier, same expiry, same device_id.
    """
    db = SessionLocal()
    device = db.query(Device).filter(
        Device.device_id == req.device_id,
        Device.active == True
    ).first()

    if not device:
        db.close()
        raise HTTPException(status_code=404, detail="Device not found or subscription expired")

    # Read best worker (same logic as activate)
    try:
        with open("/tmp/least_loaded_vps.json") as f:
            server = json.load(f)["selected_worker"]
    except (FileNotFoundError, json.JSONDecodeError, KeyError):
        server = os.getenv("VPS_DOMAIN", "yourdomain.com")

    generators = {
        "lite": generate_lite_config,
        "standard": generate_standard_config,
        "premium": generate_premium_config,
    }
    config = generators[device.tier](device.device_id, server)

    db.close()
    return {
        "success": True,
        "tier": device.tier,
        "expires_at": device.subscription_expiry.isoformat(),
        "config": config,
    }

@app.post("/api/v1/admin/generate-codes")
async def generate_codes(
    tier: str,
    count: int = 1,
    duration_days: int = 30,
    middleman_id: str = "",
    x_admin_key: str = Header(None, alias="X-Admin-Key"),
    x_forwarded_for: str = Header(None, alias="X-Forwarded-For"),
):
    # Use X-Admin-Key header instead of query parameter (prevents logging in nginx/cloudflare)
    if x_admin_key != os.getenv("ADMIN_API_KEY", "CHANGE_ME"):
        raise HTTPException(status_code=403)

    # Rate limiting
    client_ip = (x_forwarded_for or "unknown").split(",")[0].strip()
    check_admin_rate_limit(client_ip)

    db = SessionLocal()
    codes = []
    for _ in range(count):
        code = ActivationCode(
            code=f"VPN-{secrets.token_hex(4).upper()}",
            tier=tier,
            duration_days=duration_days,
            middleman_id=middleman_id
        )
        db.add(code)
        codes.append(code.code)
    db.commit()
    db.close()
    return {"codes": codes}

@app.get("/api/v1/admin/stats")
async def admin_stats(x_admin_key: str = Header(None, alias="X-Admin-Key")):
    if x_admin_key != os.getenv("ADMIN_API_KEY", "CHANGE_ME"):
        raise HTTPException(status_code=403)

    db = SessionLocal()
    total_codes = db.query(ActivationCode).count()
    used_codes = db.query(ActivationCode).filter(ActivationCode.used == True).count()
    active_devices = db.query(Device).filter(Device.active == True).count()

    tier_prices = {"lite": 5, "standard": 9, "premium": 10}
    revenue = sum(tier_prices.get(d.tier, 0) for d in db.query(Device).filter(Device.active == True).all())

    db.close()
    return {
        "total_codes_issued": total_codes,
        "codes_used": used_codes,
        "active_devices": active_devices,
        "estimated_monthly_revenue": revenue,
        "utilization_pct": round((used_codes / total_codes * 100) if total_codes > 0 else 0, 1)
    }

if __name__ == "__main__":
    import uvicorn
    # Binding to 127.0.0.1:9000(TCP) is fine for most setups — only nginx on the
    # same machine can reach it. For extra security, bind to a Unix socket instead:
    # uvicorn.run(app, uds="/opt/activation-api/api.sock")
```

### 9.3 Run the API

```bash
# Environment file
cat > /opt/activation-api/.env << EOF
ADMIN_API_KEY=${ADMIN_API_KEY}
VPS_DOMAIN=${DOMAIN}
DATABASE_URL=sqlite:////opt/activation-api/activation.db
REALITY_PUBLIC_KEY=${REALITY_PUBLIC_KEY}
REALITY_SHORT_ID=${SHORT_ID}
SALAMANDER_PASSWORD=${SALAMANDER_PASSWORD}
LITE_PRICE=5
STANDARD_PRICE=9
PREMIUM_PRICE=10
EOF

# Systemd service
cat > /etc/systemd/system/vpn-activation-api.service << 'EOF'
[Unit]
Description=VPN Activation API
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/activation-api
EnvironmentFile=/opt/activation-api/.env
ExecStart=/usr/bin/python3 -m uvicorn main:app --host 127.0.0.1 --port 9000
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now vpn-activation-api
```

### 9.4 Nginx Reverse Proxy for API

```bash
cat > /etc/nginx/sites-available/api << EOF
limit_req_zone \$binary_remote_addr zone=activation:10m rate=5r/m;

server {
    listen 443 ssl http2;
    server_name api.${DOMAIN};

    ssl_certificate /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256;

    location / {
        proxy_pass http://127.0.0.1:9000;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        limit_req zone=activation burst=5 nodelay;
        proxy_connect_timeout 10s;
        proxy_read_timeout 30s;
    }

    # Admin routes — auth is handled by the API itself (X-Admin-Key header).
    # No need for nginx-level deny here. The API returns 403 on bad key.
    # Uncomment the lines below to restrict admin access to specific IPs only:
    # location ~ ^/api/v1/admin/ {
    #     allow YOUR_HOME_IP;
    #     deny all;
    #     proxy_pass http://127.0.0.1:9000;
    #     proxy_set_header Host \$host;
    # }
}
EOF

ln -s /etc/nginx/sites-available/api /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

### 9.5 Generate Activation Codes

> ⚠️ Admin key goes in the `X-Admin-Key` **header**, not the URL. Query parameters get logged by nginx, Cloudflare, and reverse proxies. Headers do not.

```bash
# Generate 20 Premium codes (admin key in HEADER, not URL)
curl -X POST "https://api.${DOMAIN}/api/v1/admin/generate-codes?tier=premium&count=20&duration_days=30&middleman_id=john_house_a" \
  -H "X-Admin-Key: ${ADMIN_API_KEY}"

# Response: {"codes": ["VPN-A1B2C3D4", "VPN-E5F6G7H8", ...]}

# Check stats
curl -s "https://api.${DOMAIN}/api/v1/admin/stats" \
  -H "X-Admin-Key: ${ADMIN_API_KEY}" | python3 -m json.tool
```

---

## 10. Security Hardening

### 10.1 SSH Hardening

```bash
cat > /etc/ssh/sshd_config.d/99-hardening.conf << 'EOF'
Port 42069
PermitRootLogin prohibit-password
PasswordAuthentication no
PubkeyAuthentication yes
MaxAuthTries 3
MaxSessions 5
ClientAliveInterval 300
ClientAliveCountMax 2
AllowAgentForwarding no
AllowTcpForwarding no
X11Forwarding no
EOF

systemctl restart sshd
```

### 10.2 Kernel Hardening

```bash
cat > /etc/sysctl.d/99-security.conf << 'EOF'
net.ipv4.conf.all.accept_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv4.conf.all.log_martians = 1
EOF

sysctl -p /etc/sysctl.d/99-security.conf
```

### 10.3 Automatic Security Updates

```bash
apt install -y unattended-upgrades

cat > /etc/apt/apt.conf.d/50unattended-upgrades << 'EOF'
Unattended-Upgrade::Allowed-Origins {
    "${distro_id}:${distro_codename}-security";
    "${distro_id}ESMApps:${distro_codename}-apps-security";
    "${distro_id}ESM:${distro_codename}-infra-security";
};
Unattended-Upgrade::AutoFixInterruptedDpkg "true";
Unattended-Upgrade::MinimalSteps "true";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
Unattended-Upgrade::Automatic-Reboot "false";
EOF

systemctl enable --now unattended-upgrades
```

### 10.4 Disable Unused Services

```bash
systemctl list-unit-files --state=enabled | grep -vE '(ssh|nginx|ufw|fail2ban|hysteria|tuic|xray|awg|cron|systemd|docker|containerd|dbus|polkit|systemd-|vps-nat|vpn-activation)'
# Disable anything you don't recognize
```

### 10.5 UDP Rate Limiting (Anti-Flood)

UDP ports 443, 8443, and 9443 are open to the world. Without rate limiting, a single student with a grudge can flood them and spike the VPS CPU. Even though Hysteria2 and TUIC drop unauthenticated packets at the app layer, the kernel still processes the incoming traffic.

```bash
# Limit each source IP to 1000 UDP packets/sec on client-facing ports
# This stops floods without affecting legitimate clients (who send 10-50 pkt/s)
for port in 443 8443 9443; do
    iptables -A INPUT -p udp --dport ${port} -m hashlimit \
        --hashlimit-name udp_flood_${port} \
        --hashlimit-above 1000/sec \
        --hashlimit-burst 2000 \
        --hashlimit-mode srcip \
        --hashlimit-htable-size 32768 \
        --hashlimit-htable-max 65536 \
        --hashlimit-htable-expire 60000 \
        -j DROP
done

# Save rules
iptables-save > /etc/iptables/rules.v4
```

> Adjust the `1000/sec` threshold based on your traffic. Gaming clients typically send 20-100 UDP packets per second. Set it high enough that legitimate usage isn't affected.

---

## 11. Monitoring & Alerting

### 11.1 Health Check Script

```bash
cat > /usr/local/bin/vpn-health-check.sh << 'SCRIPT'
#!/bin/bash
# Run via cron every 5 minutes: */5 * * * * root /usr/local/bin/vpn-health-check.sh

ALERT_EMAIL="${ALERT_EMAIL:-root}"
LOGFILE="/var/log/vpn-health.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOGFILE"
}

alert() {
    log "ALERT: $1"
    if command -v mail &>/dev/null; then
        echo "$1" | mail -s "VPN ALERT: $(hostname) - $1" "$ALERT_EMAIL"
    fi
}

# --- Check protocol services ---
for svc in hysteria-premium tuic-lite awg-standard xray; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        log "Service $svc: RUNNING"
    else
        alert "Service $svc is DOWN. Attempting restart..."
        systemctl restart "$svc" 2>/dev/null || true
        sleep 3
        if systemctl is-active --quiet "$svc" 2>/dev/null; then
            log "Service $svc: RESTARTED OK"
        else
            alert "Service $svc: FAILED TO RESTART"
        fi
    fi
done

# --- Check ports ---
check_port() {
    local port=$1 proto=$2 name=$3
    # The colon before ${port} prevents false matches (e.g. :8443 matching "443"):
    if ss -tulnp | grep -q ":${port} "; then
        log "Port ${port}/${proto} (${name}): LISTENING"
    else
        alert "Port ${port}/${proto} (${name}): NOT LISTENING"
    fi
}

check_port 443 udp "Hysteria2"
check_port 443 tcp "VLESS-REALITY"
check_port 8443 udp "TUIC V5"
check_port 9443 udp "AmneziaWG"

# --- Check NAT ---
if ! iptables -t nat -L POSTROUTING | grep -q MASQUERADE; then
    alert "NAT MASQUERADE rule missing. Reapplying..."
    /usr/local/bin/vps-nat-setup.sh
fi

# --- Check resources ---
MEM_AVAIL=$(free -m | awk '/Mem:/ {print $7}')
if [ "$MEM_AVAIL" -lt 256 ]; then
    alert "Low memory: ${MEM_AVAIL}MB available"
else
    log "Memory OK: ${MEM_AVAIL}MB available"
fi

DISK_USE=$(df / | awk 'NR==2 {print $5}' | tr -d '%')
if [ "$DISK_USE" -gt 90 ]; then
    alert "Disk usage at ${DISK_USE}%"
else
    log "Disk OK: ${DISK_USE}% used"
fi

LOAD=$(uptime | awk -F'load average:' '{print $2}' | awk '{print $1}' | tr -d ',')
if (( $(echo "$LOAD > 10" | bc -l) )); then
    alert "High load: ${LOAD}"
else
    log "Load OK: ${LOAD}"
fi

# --- Check connection count ---
CONN_COUNT=$(ss -s | grep TCP | awk '{print $2}')
if [ "$CONN_COUNT" -gt 1000 ]; then
    log "INFO: High connection count: ${CONN_COUNT}"
fi
SCRIPT

chmod +x /usr/local/bin/vpn-health-check.sh
echo "*/5 * * * * root /usr/local/bin/vpn-health-check.sh" > /etc/cron.d/vpn-health
```

### 11.2 Traffic Monitoring

```bash
# vnstat — continuous bandwidth monitoring (already installed)
vnstat -i eth0 -d   # Daily stats
vnstat -l -i eth0   # Live traffic

# Per-protocol bandwidth (iptables accounting)
iptables -N PROTO_ACCT 2>/dev/null || true
iptables -I INPUT -p udp --dport 443 -j PROTO_ACCT
iptables -I INPUT -p udp --dport 8443 -j PROTO_ACCT
iptables -I INPUT -p udp --dport 9443 -j PROTO_ACCT
iptables -I INPUT -p tcp --dport 443 -j PROTO_ACCT

# View: iptables -L PROTO_ACCT -n -v -x
```

---

## 12. Backup & Disaster Recovery

### 12.1 What to Back Up

| Item | Location | Frequency |
|------|----------|-----------|
| Activation DB | `/opt/activation-api/activation.db` | Daily |
| Marzban DB | `/opt/marzban/mysql/` | Daily |
| Protocol configs | `/etc/hysteria/`, `/opt/tuic/`, `/etc/amnezia/`, `/usr/local/etc/xray/` | On change |
| TLS certs | `/etc/letsencrypt/` | Weekly |
| Nginx configs | `/etc/nginx/sites-available/` | On change |
| iptables rules | `/etc/iptables/` | On change |
| Environment files | `/opt/activation-api/.env`, `/opt/marzban/.env` | On change |

### 12.2 Backup Script

```bash
cat > /usr/local/bin/vpn-backup.sh << 'SCRIPT'
#!/bin/bash
set -e

BACKUP_DIR="/root/vpn-backups"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/vpn-backup-${DATE}.tar.gz"
LOG="/var/log/vpn-backup.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG"
}

mkdir -p "$BACKUP_DIR"

log "Starting backup..."

# Dump Marzban MySQL database properly (raw file copy of running DB can be inconsistent)
MYSQL_DUMP_FILE="${BACKUP_DIR}/marzban-db-${DATE}.sql"
if docker exec marzban-db mariadb-dump -u marzban -p"${MARZBAN_DB_PASS}" marzban > "$MYSQL_DUMP_FILE" 2>/dev/null; then
    log "MariaDB dump: OK (${MYSQL_DUMP_FILE})"
else
    log "WARNING: MariaDB dump failed. DB might be skipped."
    MYSQL_DUMP_FILE=""
fi

tar czf "$BACKUP_FILE" \
    /opt/activation-api/activation.db \
    /opt/activation-api/.env \
    /opt/marzban/.env \
    /opt/marzban/docker-compose.yml \
    /etc/hysteria/ \
    /opt/tuic/*.json \
    /etc/amnezia/ \
    /etc/letsencrypt/live/ \
    /etc/nginx/sites-available/ \
    /etc/iptables/ \
    /usr/local/etc/xray/config.json \
    /usr/local/bin/vps-*.sh \
    /usr/local/bin/vpn-*.sh \
    ${MYSQL_DUMP_FILE} \
    /etc/systemd/system/vps-*.service \
    /etc/systemd/system/vpn-*.service \
    /etc/systemd/system/hysteria-*.service \
    /etc/systemd/system/tuic-*.service \
    /etc/systemd/system/awg-*.service \
    /etc/sysctl.d/99-*.conf \
    2>/dev/null

BACKUP_SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
log "Backup created: $BACKUP_FILE ($BACKUP_SIZE)"

# Rotate: keep last 30 backups
BACKUP_COUNT=$(ls -1 "$BACKUP_DIR"/vpn-backup-*.tar.gz 2>/dev/null | wc -l)
if [ "$BACKUP_COUNT" -gt 30 ]; then
    ls -1t "$BACKUP_DIR"/vpn-backup-*.tar.gz | tail -n +31 | xargs rm -f
    log "Rotated old backups (kept 30, removed $((BACKUP_COUNT - 30)))"
fi

log "Backup complete."
SCRIPT

chmod +x /usr/local/bin/vpn-backup.sh
echo "0 3 * * * root /usr/local/bin/vpn-backup.sh" > /etc/cron.d/vpn-backup
```

### 12.3 Emergency VPS Swap

If your VPS IP gets blocked or the provider has an outage:

```bash
#!/bin/bash
# /usr/local/bin/vps-emergency-swap.sh
# Run AFTER restoring backup to a new VPS

NEW_VPS_IP="$1"
if [ -z "$NEW_VPS_IP" ]; then
    echo "Usage: $0 <new-vps-ip>"
    exit 1
fi

# Update Cloudflare DNS via API
CF_TOKEN="YOUR_CLOUDFLARE_API_TOKEN"
CF_ZONE_ID="YOUR_ZONE_ID"

# Get the A record ID for @
RECORD_ID=$(curl -s -X GET "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?type=A&name=${DOMAIN}" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" | jq -r '.result[0].id')

# Update A record
curl -s -X PUT "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${RECORD_ID}" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" \
    --data "{\"type\":\"A\",\"name\":\"@\",\"content\":\"${NEW_VPS_IP}\",\"proxied\":true}"

echo "DNS updated to ${NEW_VPS_IP}. Propagation may take 1-2 minutes."
```

---

## 13. Operational Runbooks

### 13.1 Generate Activation Codes

```bash
# Admin key goes in X-Admin-Key HEADER, not URL (prevents logging exposure)
curl -X POST "https://api.${DOMAIN}/api/v1/admin/generate-codes?tier=premium&count=50&duration_days=30&middleman_id=new_middleman" \
  -H "X-Admin-Key: ${ADMIN_API_KEY}"
```

### 13.2 Expire Devices Automatically

```bash
cat > /usr/local/bin/expire-devices.sh << 'SCRIPT'
#!/bin/bash
cd /opt/activation-api
python3 << 'PYEOF'
import sys
sys.path.insert(0, '/opt/activation-api')
from main import SessionLocal, Device
from datetime import datetime

db = SessionLocal()
expired = db.query(Device).filter(
    Device.subscription_expiry < datetime.utcnow(),
    Device.active == True
).all()

for device in expired:
    print(f"Expiring: {device.device_id} (tier={device.tier})")
    device.active = False

db.commit()
print(f"Expired {len(expired)} devices")
db.close()
PYEOF
SCRIPT

chmod +x /usr/local/bin/expire-devices.sh
echo "0 2 * * * root /usr/local/bin/expire-devices.sh" > /etc/cron.d/vpn-expire
```

### 13.3 Block an Abusive User

```bash
# Find device
sqlite3 /opt/activation-api/activation.db "SELECT * FROM devices WHERE device_name LIKE '%search%';"

# Deactivate
sqlite3 /opt/activation-api/activation.db "UPDATE devices SET active=0 WHERE device_id='THEIR_DEVICE_ID';"

# Block at Hysteria2 level
echo '{"rule": {"type": "device", "id": "THEIR_DEVICE_ID", "action": "reject"}}' >> /etc/hysteria/acl.json
systemctl reload hysteria-premium

# Or block their IP at iptables level
# iptables -A INPUT -s <THEIR_IP> -j DROP
```

### 13.4 Check Active Users & Revenue

```bash
curl -s "https://api.${DOMAIN}/api/v1/admin/stats" \
  -H "X-Admin-Key: ${ADMIN_API_KEY}" | python3 -m json.tool
```

---

## Troubleshooting Common Issues

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| Clients can connect but no internet | NAT not working | `iptables -t nat -L POSTROUTING`, check `ip_forward=1` |
| Hysteria2 slow | Brutal CC not enabled | Check `congestion` block in `/etc/hysteria/premium.yaml` |
| VLESS-REALITY not connecting | REALITY keys mismatch | Regenerate with `xray x25519`, update config |
| Port 443 conflict | Hysteria2 and Xray fight over same port | They don't — QUIC/UDP and TCP/TLS are different protocols. Check `ss -tulnp \| grep 443` |
| Activation codes not working | API not reachable or DB corrupted | Check `systemctl status vpn-activation-api`, check nginx logs |
| VPS IP blocked by school | DNS still resolving to old IP | Swap VPS (Section 12.3), update Cloudflare DNS |
| Memory leaks on VPS | Protocol bug | Weekly auto-reboot: `echo "0 4 * * 0 root /sbin/reboot" > /etc/cron.d/weekly-reboot` |

---

*Implementation guide compiled for single-VPS architecture. Test all configurations before distributing to clients. Monitor protocol detection research and be prepared to rotate strategies.*
