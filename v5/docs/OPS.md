# MyVPN Operations Manual

> Day-to-day management of the MyVPN server.

---

## Quick Reference

```bash
export VPS="root@networkingguides.duckdns.org"
export PB_API="https://networkingguides.duckdns.org"
export PB_TOKEN="your-pocketbase-admin-token"
export ADMIN_TOKEN="your-admin-api-token"
```

---

## Server Health

### All Services

```bash
ssh $VPS "systemctl is-active caddy pocketbase shadowsocks-eco shadowsocks-stealth shadowsocks-strike"
```

Expected output:
```
active
active
active
active
active
```

### Brutal CC

```bash
# Check module loaded
ssh $VPS "lsmod | grep tcp_brutal || echo 'NOT LOADED'"

# Check available congestion control algorithms
ssh $VPS "sysctl net.ipv4.tcp_available_congestion_control"

# Check target rate (default 48 Mbps)
ssh $VPS "cat /sys/module/tcp_brutal/parameters/target_rate"

# Brutal must be re-checked after every kernel update
# DKMS should auto-rebuild, but verify:
ssh $VPS "dkms status tcp-brutal"
```

### Traffic Shaping

```bash
# Check tc classes exist
ssh $VPS "tc -s class show dev \$(ip route show default | awk '\$5{print\$5;exit}')"

# Eco class (1:10) should show traffic
# Strike class (1:30) should show traffic
# Stealth has no tc class (Brutal manages its own rate)
```

### Logs

```bash
# Shadowsocks per-tier
ssh $VPS "journalctl -u shadowsocks-eco -n 20 --no-pager"
ssh $VPS "journalctl -u shadowsocks-stealth -n 20 --no-pager"
ssh $VPS "journalctl -u shadowsocks-strike -n 20 --no-pager"

# PocketBase
ssh $VPS "journalctl -u pocketbase -n 20 --no-pager"

# Caddy access
ssh $VPS "tail -20 /var/log/caddy/access.log"

# Backup logs
ssh $VPS "tail -10 /var/log/myvpn-backup.log"
```

---

## User Management

### Check Activation Codes

```bash
# All codes
curl -s "$PB_API/api/collections/codes/records?skipTotal=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[] | {code: .code, tier: .tier, used: .used, suspended: .suspended, bound_fingerprint: .bound_fingerprint}'
```

### Suspend a User

```bash
# Find the record ID
RECORD_ID=$(curl -s "$PB_API/api/collections/codes/records?filter=(code='MYVPN-ABCD-EFGH-IJKL-M')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq -r '.items[0].id')

# Suspend
curl -X PATCH "$PB_API/api/collections/codes/records/$RECORD_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": true}'
```

### Unbind a Device (Allow Re-activation)

```bash
curl -X POST "$PB_API/api/admin/unbind-code" \
  -H "Content-Type: application/json" \
  -d '{
    "admin_token": "'$ADMIN_TOKEN'",
    "code": "MYVPN-ABCD-EFGH-IJKL-M",
    "reason": "Device reported lost"
  }'
```

### Generate New Codes

```bash
# From your local machine
./v5/scripts/generate_codes.sh "$PB_API" "$ADMIN_TOKEN" eco 10
```

---

## Deployment

### Deploy a New VPS

```bash
# 1. Copy server code
scp -r v5/server root@new-vps:/root/

# 2. Run setup
ssh root@new-vps "DOMAIN=networkingguides.duckdns.org /root/server/setup.sh"

# 3. Create PocketBase collections, seed tier configs, generate codes
# See docs/DEPLOY.md for detailed steps
```

### Disaster Recovery (Restore from B2 Backup)

```bash
B2_APPLICATION_KEY_ID=xxx \
B2_APPLICATION_KEY=xxx \
B2_BUCKET=my-vpn-backup-bucket \
DOMAIN=networkingguides.duckdns.org \
ssh root@new-vps 'bash -s' < v5/server/restore.sh
```

### Reload After Config Change

```bash
# Shadowsocks
ssh $VPS "systemctl restart shadowsocks-eco"
ssh $VPS "systemctl restart shadowsocks-stealth"
ssh $VPS "systemctl restart shadowsocks-strike"

# Caddy
ssh $VPS "caddy fmt --overwrite /etc/caddy/Caddyfile && systemctl reload caddy"

# PocketBase (hook changes)
ssh $VPS "systemctl restart pocketbase"

# tc rules after reboot
ssh $VPS "systemctl restart tc-eco-cap tc-strike-cap"
```

---

## Staged Rollouts

### Create or Update rollout config

In PocketBase admin UI → `update_config` collection → create record:

```json
{
  "version": "1.1.0",
  "rollout_percent": 5,
  "active": true,
  "update_url": "https://networkingguides.duckdns.org/updates/v1.1.0/myvpn-linux-amd64",
  "update_sha256": "sha256-of-the-binary",
  "download_windows": "https://...myvpn-windows-amd64.exe",
  "download_macos_intel": "https://...myvpn-darwin-amd64",
  "download_macos_arm": "https://...myvpn-darwin-arm64"
}
```

### Rollout Progression

```text
Day 1:  rollout_percent = 5   (internal testers)
Day 3:  rollout_percent = 25  (early adopters)
Day 7:  rollout_percent = 100 (everyone)
Day 8:  rollout_percent = 0   (mark complete; set active=false)
```

---

## Backups

### Check Backup Status

```bash
ssh $VPS "systemctl status pocketbase-backup.timer --no-pager | head -10"
ssh $VPS "systemctl status pocketbase-backup.service --no-pager | head -15"
```

### Manual Backup

```bash
ssh $VPS "/usr/local/bin/myvpn-backup.sh"
```

### List Backups in B2

```bash
b2 ls my-vpn-backup-bucket backups/
```

### Restore from Specific Backup

```bash
# Download
b2 download-file-by-name my-vpn-backup-bucket backups/data-20260724-143000.db /tmp/restore.db

# Verify
sha256sum /tmp/restore.db
head -c 16 /tmp/restore.db | xxd  # Should show "SQLite format 3\000"

# Restore
ssh $VPS "systemctl stop pocketbase"
scp /tmp/restore.db root@networkingguides.duckdns.org:/opt/pocketbase/pb_data/data.db
ssh $VPS "chown pocketbase:pocketbase /opt/pocketbase/pb_data/data.db && systemctl start pocketbase"

# Verify
curl -s "$PB_API/api/health"
```

---

## Updating the Server

### Kernel Update (requires Brutal recheck)

```bash
# After kernel update:
ssh $VPS "dkms install tcp-brutal/1.0 && modprobe tcp_brutal"
ssh $VPS "lsmod | grep tcp_brutal || echo 'WARNING: Brutal not loaded'"
```

### Updating PocketBase

```bash
ssh $VPS
PB_VER="0.22.22"  # Check latest release
wget -q "https://github.com/pocketbase/pocketbase/releases/download/v${PB_VER}/pocketbase_${PB_VER}_linux_amd64.zip"
unzip -qo "pocketbase_${PB_VER}_linux_amd64.zip"
systemctl stop pocketbase
cp pocketbase /opt/pocketbase/
chmod +x /opt/pocketbase/pocketbase
systemctl start pocketbase
rm -f pocketbase pocketbase_${PB_VER}_linux_amd64.zip
```

### Updating Caddy

```bash
# Caddy with ratelimit plugin must be custom-downloaded
ssh $VPS
curl -sL 'https://caddyserver.com/api/download?os=linux&arch=amd64&p=github.com/mholt/caddy-ratelimit' \
  -o /tmp/caddy-new
chmod +x /tmp/caddy-new
/tmp/caddy-new list-modules | grep rate_limit || echo "Plugin missing!"
systemctl stop caddy
cp /tmp/caddy-new /usr/bin/caddy
systemctl start caddy
```
