# V4 Operations Manual (for Humans & Agents)

## Quick Reference

```bash
export PB_API="https://jeanette-qh9zbe.cloudserver.nz"
export PB_TOKEN="your-pocketbase-admin-token"
export ADMIN_TOKEN="your-admin-api-token"
export VPS="root@jeanette-qh9zbe.cloudserver.nz"
```

---

## Server Health

```bash
# All three Shadowsocks instances
ssh $VPS "systemctl is-active shadowsocks-eco shadowsocks-stealth shadowsocks-strike"

# Brutal module
ssh $VPS "lsmod | grep tcp_brutal || echo 'NOT LOADED'"

# BBR status
ssh $VPS "sysctl net.ipv4.tcp_congestion_control"

# tc stats (Eco cap — verify class 1:10 is getting traffic)
ssh $VPS "tc -s class show dev eth0"

# Brutal target rate
ssh $VPS "cat /sys/module/tcp_brutal/parameters/target_rate"

# Individual service logs
ssh $VPS "journalctl -u shadowsocks-eco -n 20 --no-pager"
ssh $VPS "journalctl -u shadowsocks-stealth -n 20 --no-pager"
ssh $VPS "journalctl -u shadowsocks-strike -n 20 --no-pager"

# Caddy + PocketBase
ssh $VPS "systemctl is-active caddy pocketbase"

# Brutal check — run after every kernel update
# If tcp_brutal isn't loaded, the Stealth tier silently falls back to BBR.
# DKMS should auto-rebuild it, but verify:
ssh $VPS "lsmod | grep tcp_brutal && echo 'OK' || echo 'WARNING: tcp_brutal not loaded — run: dkms install tcp-brutal-ng/1.0 && modprobe tcp_brutal'"

# tc qdisc status (Eco cap — verify after reboot)
ssh $VPS "tc -s class show dev eth0 | head -10 || echo 'WARNING: tc cap not applied — run: systemctl restart tc-eco-cap'"

# Strike cap (verify class 1:30 exists and is getting traffic)
ssh $VPS "tc -s class show dev eth0 | grep -A2 'class htb 1:30' || echo 'WARNING: Strike cap not applied — run: systemctl restart tc-strike-cap'"

# Last backup
ssh $VPS "tail -1 /var/log/pocketbase-backup.log"

### Helper Service Status (Client-Side Debug)

These must be checked on the client machine, not the VPS:

```bash
# macOS
launchctl list | grep myvpn-helper

# Windows (run in PowerShell as admin)
Get-Service MyVPNHelper

# Linux
systemctl is-active myvpn-helper
```

If the helper isn't running, the app can't create the TUN device.
Re-run the installer with `--install` flag to re-register it.
```

---

## User Management

### List All Users
```bash
curl -s "$PB_API/api/collections/codes/records?skipTotal=1" \
  -H "Authorization: Bearer $PB_TOKEN" | \
  jq '.items[] | {code, tier, used, suspended, middleman, fingerprint?}'
```

### Suspend a User
```bash
ID=$(curl -s "$PB_API/api/collections/codes/records?filter=(code='MYVPN-XXXX-XXXX-XXXX-C')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq -r '.items[0].id')

curl -X PATCH "$PB_API/api/collections/codes/records/$ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": true}'
```

### Unsuspend a User
```bash
curl -X PATCH "$PB_API/api/collections/codes/records/$ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": false}'
```

### List Codes by Middleman
```bash
curl -s "$PB_API/api/collections/codes/records?filter=(middleman='Alice')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[] | {code, tier, used}'
```

---

## Code Recovery (Device Transfer)

When a customer breaks their device and needs to re-activate on a new one:

```bash
curl -X POST "$PB_API/api/admin/unbind-code" \
  -H "Content-Type: application/json" \
  -d '{
    "admin_token": "'$ADMIN_TOKEN'",
    "code": "MYVPN-XXXX-XXXX-XXXX-C"
  }'
```

Response: `{"status":"ok","message":"Code unbound. Customer can re-activate on new device."}`

The customer then enters their existing code on the new device → normal activation flow → new device is bound.

---

## Updates

### Push an Update (Staged Rollout)

```bash
make bundle-all
VERSION="1.1.0"
ROLLOUT_PERCENT=5   # Start at 5%
WIN_SHA=$(sha256sum dist/myvpn-windows.zip | cut -d' ' -f1)
MAC_INTEL_SHA=$(sha256sum dist/myvpn-macos-intel.zip | cut -d' ' -f1)
MAC_ARM_SHA=$(sha256sum dist/myvpn-macos-arm.zip | cut -d' ' -f1)

scp dist/*.zip root@jeanette-qh9zbe.cloudserver.nz:/var/www/html/

ssh root@jeanette-qh9zbe.cloudserver.nz "cat > /var/www/html/update.json << EOF
{
  \"version\": \"$VERSION\",
  \"rollout_percent\": $ROLLOUT_PERCENT,
  \"windows\": {
    \"url\": \"https://jeanette-qh9zbe.cloudserver.nz/myvpn-windows.zip\",
    \"sha256\": \"$WIN_SHA\"
  },
  \"macos_intel\": {
    \"url\": \"https://jeanette-qh9zbe.cloudserver.nz/myvpn-macos-intel.zip\",
    \"sha256\": \"$MAC_INTEL_SHA\"
  },
  \"macos_arm\": {
    \"url\": \"https://jeanette-qh9zbe.cloudserver.nz/myvpn-macos-arm.zip\",
    \"sha256\": \"$MAC_ARM_SHA\"
  }
}
EOF"
```

The heartbeat endpoint returns `update_available` when `update.json` version changes.
Clients will check within 5 minutes of the heartbeat detecting the new version.

### Rollout Stages

| Stage | rollout_percent | Duration | Action |
|:-----:|:---------------:|:--------:|--------|
| Canary | 5% | 24h | Monitor heartbeat crash signals |
| Partial | 25% | 24h | Confirm telemetry looks normal |
| Full | 100% | — | Full deployment |
| Halt | 0 | Immediate | Stop rollout, no new clients take it |

To advance stages without rebuilding:
```bash
# Use jq to safely update rollout_percent (more reliable than sed on JSON)
ssh root@jeanette-qh9zbe.cloudserver.nz "jq '.rollout_percent = 25' /var/www/html/update.json > /tmp/update.json && mv /tmp/update.json /var/www/html/update.json"
```

To halt a bad rollout:
```bash
ssh root@jeanette-qh9zbe.cloudserver.nz "jq '.rollout_percent = 0' /var/www/html/update.json > /tmp/update.json && mv /tmp/update.json /var/www/html/update.json"
# Existing clients already on the bad version will auto-revert on next crash/restart
```

### Force-Update All Clients

```bash
# Set to 100% — all clients get it on next heartbeat
ssh root@jeanette-qh9zbe.cloudserver.nz "jq '.rollout_percent = 100' /var/www/html/update.json > /tmp/update.json && mv /tmp/update.json /var/www/html/update.json"
```

1. All active clients get `update_available` on their next heartbeat
2. Clients automatically download and apply the update
3. If any client's update crashes → auto-rollback to `.prev`
4. If many clients roll back → investigate, fix, push again

### Manual Revert (Customer-Side)

If a customer reports that the latest update broke something:

```bash
# Instruct the customer to run in terminal:
myvpn --revert
```

Or on Windows, hold **Ctrl** while launching the app.

---

## Config Changes

### Change a Tier Password
```bash
# 1. Generate new password
NEW_PASS=$(openssl rand -hex 16)

# 2. Update the server config
ssh $VPS "sed -i 's/\"password\": \".*\"/\"password\": \"$NEW_PASS\"/' /etc/shadowsocks/eco.json && systemctl restart shadowsocks-eco"

# 3. Update PocketBase tier_configs
curl -X PATCH "$PB_API/api/collections/tier_configs/records/ECO_CONFIG_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"config\": {\"password\": \"$NEW_PASS\"}}"
```

### Change Eco Bandwidth Cap
```bash
# From 5 Mbps to 10 Mbps
ssh $VPS "tc class change dev eth0 parent 1: classid 1:10 htb rate 10mbit ceil 10mbit"
```

### Change Brutal Target Rate
```bash
# From 48 Mbps to 30 Mbps
ssh $VPS "sysctl -w net.ipv4.tcp_brutal.target_rate=30000000"
```

---

## Backup Management

### Check Last Backup
```bash
ssh $VPS "tail -5 /var/log/pocketbase-backup.log"
b2 ls my-vpn-backup-bucket --prefix backups/ | tail -5
```

### Manual Backup
```bash
ssh $VPS "/opt/pocketbase/backup.sh"
```

### Restore from Backup
```bash
# 1. Download latest backup from B2
b2 download-file-by-name my-vpn-backup-bucket backups/data-20260724-143000.db /tmp/restore.db

# 2. Stop PocketBase
ssh $VPS "systemctl stop pocketbase"

# 3. Restore
scp /tmp/restore.db root@jeanette-qh9zbe.cloudserver.nz:/opt/pocketbase/pb_data/data.db

# 4. Start PocketBase
ssh $VPS "systemctl start pocketbase"

# 5. Verify
curl -s "$PB_API/api/collections/codes/records?skipTotal=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items | length'
```

---

## Rate Limiting

### Check if a Rate Key is Rate-Limited
```bash
# Rate key is fingerprint hash prefix (or IP fallback for non-fingerprint requests)
curl -s "$PB_API/api/collections/activation_attempts/records?filter=(rate_key='a1b2c3d4e5f6g7h8')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items | length'
```

### Clear Rate Limit for a Key
```bash
# Delete all attempts for that rate key
IDS=$(curl -s "$PB_API/api/collections/activation_attempts/records?filter=(rate_key='a1b2c3d4e5f6g7h8')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq -r '.items[].id')
for ID in $IDS; do
  curl -X DELETE "$PB_API/api/collections/activation_attempts/records/$ID" \
    -H "Authorization: Bearer $PB_TOKEN"
done
```

### Check Rate Limit by IP (audit fallback)
```bash
# Useful when investigating a problem where fingerprint wasn't provided
curl -s "$PB_API/api/collections/activation_attempts/records?filter=(ip='203.0.113.42')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items | length'
```
