# MyVPN Server Deployment Guide

> Deploy the complete MyVPN server infrastructure on a blank Ubuntu 22.04 VPS.

---

## 1. Prerequisites

- **VPS:** Ubuntu 22.04, x86_64, 2GB RAM minimum, 20GB disk
- **Domain:** A domain name pointing to your VPS IP (A record)
- **Backblaze B2 account** (optional, for backups)

### Recommended VPS Providers

| Provider | Plan | Spec | Cost |
|----------|------|------|------|
| Hetzner | CCX13 | 2 vCPU, 4GB RAM | ~$8/mo |
| DigitalOcean | Basic | 1 vCPU, 2GB RAM | ~$12/mo |
| Vultr | Regular Cloud | 1 vCPU, 2GB RAM | ~$12/mo |
| Voyager (tested) | — | 2GB RAM | Verified working |

---

## 2. DNS Setup

Create an A record for your VPS IP:

```
api.yourdomain.com  A  YOUR_VPS_IP
```

TTL of 60s recommended during initial setup. Increase to 300s+ after stable.

---

## 3. Deploy

### Option A: One-Command Deploy

```bash
# Copy the server directory to your VPS
scp -r v5/server root@your-vps:/root/

# SSH in and run
ssh root@your-vps
DOMAIN=api.yourdomain.com /root/server/setup.sh
```

### Option B: Pipe from Local Machine

```bash
DOMAIN=api.yourdomain.com ssh root@your-vps 'bash -s' < v5/server/setup.sh
```

The setup script does **everything** automatically:
1. Validates environment (OS, arch, root, disk, memory, DNS)
2. Enables BBR + TCP kernel tuning
3. Installs 3 ssserver instances (eco:8443, stealth:8444, strike:8445)
4. Builds and loads `tcp-brutal` kernel module + LD_PRELOAD wrapper
5. Applies tc traffic shaping (Eco 5 Mbps, Strike 200 Mbps)
6. Installs Caddy with rate limiting and Let's Encrypt TLS
7. Installs PocketBase with JS hooks and SQLite WAL mode
8. Configures hourly B2 backup systemd timer
9. Sets up UFW firewall (SSH rate-limited, all needed ports open)

---

## 4. Post-Deployment Configuration

### 4.1 Create PocketBase Admin

1. Visit `https://api.yourdomain.com/_/` in a browser
2. Create your admin account (first-run wizard)

### 4.2 Create Collections

In PocketBase admin UI, create these collections:

**Collection: `codes`**
- `code` (text, required, unique) — activation code string
- `tier` (select, required, options: `eco`, `stealth`, `strike`)
- `used` (bool, default: false) — whether code has been used
- `suspended` (bool, default: false) — admin suspension
- `bound_fingerprint` (text) — SHA256 device fingerprint
- `expires_at` (datetime) — code expiry
- `activated_at` (datetime) — first activation timestamp
- `middleman` (text) — distributor identifier
- API rules: only admins can create/update/delete; public can read with filter

**Collection: `tier_configs`**
- `tier` (text, required, unique) — `eco`, `stealth`, `strike`
- `config` (json, required) — Shadowsocks server config
- `active` (bool, default: true)
- `udp_relay` (bool, default: false) — whether tier supports UDP
- API rules: public can read

**Collection: `activation_attempts`**
- `ip` (text) — client IP
- `rate_key` (text) — fingerprint hash or IP
- `fingerprint` (text, optional) — partial fingerprint for audit
- `code_attempted` (text, optional) — partial code for audit
- Auto-created: `created`, `updated`
- API rules: no public access (internal use only)

**Collection: `update_config`** (optional, for staged rollouts)
- `version` (text) — version string e.g. `1.1.0`
- `rollout_percent` (number, 0-100)
- `active` (bool)
- `update_url` (text) — generic download URL
- `update_sha256` (text) — generic SHA256
- `download_windows` (text) — Windows-specific URL
- `download_macos_intel`, `download_macos_arm` (text)
- API rules: only admins can create/update/delete

### 4.3 Configure Admin API Token

Set the `ADMIN_API_TOKEN` environment variable on the VPS:

```bash
ssh root@your-vps
echo 'ADMIN_API_TOKEN=your-secure-random-token-here' >> /etc/environment
systemctl restart pocketbase
```

This token is used by the admin unbind endpoint and by `generate_codes.sh`.

### 4.4 Seed Tier Configs

Read passwords from the VPS:

```bash
ssh root@your-vps "cat /root/.tier_passwords"
```

Create entries in the `tier_configs` PocketBase collection:

**Eco:**
```json
{
  "tier": "eco",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8443,
    "password": "<ECO_PASS>",
    "method": "aes-256-gcm"
  },
  "active": true,
  "udp_relay": false
}
```

**Stealth:**
```json
{
  "tier": "stealth",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8444,
    "password": "<STEALTH_PASS>",
    "method": "aes-256-gcm"
  },
  "active": true,
  "udp_relay": false
}
```

**Strike:**
```json
{
  "tier": "strike",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8445,
    "password": "<STRIKE_PASS>",
    "method": "aes-256-gcm"
  },
  "active": true,
  "udp_relay": true
}
```

### 4.5 Generate Activation Codes

```bash
# From your local machine
./v5/scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 50
./v5/scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN stealth 30
./v5/scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN strike 20
```

### 4.6 Print Code Cards

```bash
./v5/scripts/print_codes.sh eco-codes.txt eco-cards.pdf
./v5/scripts/print_codes.sh stealth-codes.txt stealth-cards.pdf
./v5/scripts/print_codes.sh strike-codes.txt strike-cards.pdf
```

### 4.7 Configure Backups (Optional)

```bash
ssh root@your-vps
# Install b2 CLI and authorize
pip3 install b2
b2 authorize-application-key

# Set environment variables
export B2_APPLICATION_KEY_ID="your-key-id"
export B2_APPLICATION_KEY="your-key"
export B2_BUCKET="my-vpn-backup-bucket"

# Run backup module to enable timer
cd /root/server
B2_APPLICATION_KEY_ID=xxx B2_APPLICATION_KEY=xxx B2_BUCKET=my-vpn-backup-bucket \
  bash modules/07-backups.sh
```

---

## 5. Verification Checklist

After deployment, verify:

- [ ] `systemctl is-active caddy` → active
- [ ] `systemctl is-active pocketbase` → active
- [ ] `systemctl is-active shadowsocks-eco` → active
- [ ] `systemctl is-active shadowsocks-stealth` → active
- [ ] `systemctl is-active shadowsocks-strike` → active
- [ ] `lsmod | grep tcp_brutal` → loaded
- [ ] `sysctl net.ipv4.tcp_congestion_control` → bbr
- [ ] `tc -s class show dev eth0` → classes for 8443 and 8445
- [ ] `curl -sf https://api.yourdomain.com/api/health` → 200
- [ ] `ufw status` → active with all rules
- [ ] `openssl s_client -connect api.yourdomain.com:443 -servername api.yourdomain.com </dev/null 2>/dev/null | openssl x509 -noout -dates` → valid cert

---

## 6. Disaster Recovery

See `v5/server/restore.sh` for full restore from B2 backup.

```bash
B2_APPLICATION_KEY_ID=xxx \
B2_APPLICATION_KEY=xxx \
B2_BUCKET=my-vpn-backup-bucket \
DOMAIN=api.yourdomain.com \
ssh root@new-vps 'bash -s' < v5/server/restore.sh
```

This will:
1. Provision the new VPS from scratch (runs full setup.sh)
2. Download the latest backup from B2
3. Verify SHA256 checksum
4. Restore PocketBase database
5. Verify collections integrity
