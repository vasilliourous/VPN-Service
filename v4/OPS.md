# V4 Operations Manual (for Humans & Agents)

## Quick Reference

```bash
export PB_API="https://api.yourdomain.com"
export PB_TOKEN="your-pocketbase-admin-token"
export ADMIN_TOKEN="your-admin-api-token"
export VPS="root@api.yourdomain.com"
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

# Last backup
ssh $VPS "tail -1 /var/log/pocketbase-backup.log"
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

### Push an Update

```bash
make bundle-all
VERSION="1.1.0"
WIN_SHA=$(sha256sum dist/myvpn-windows.zip | cut -d' ' -f1)
MAC_INTEL_SHA=$(sha256sum dist/myvpn-macos-intel.zip | cut -d' ' -f1)
MAC_ARM_SHA=$(sha256sum dist/myvpn-macos-arm.zip | cut -d' ' -f1)

scp dist/*.zip root@api.yourdomain.com:/var/www/html/

ssh root@api.yourdomain.com "cat > /var/www/html/update.json << EOF
{
  \"version\": \"$VERSION\",
  \"windows\": {
    \"url\": \"https://api.yourdomain.com/myvpn-windows.zip\",
    \"sha256\": \"$WIN_SHA\"
  },
  \"macos_intel\": {
    \"url\": \"https://api.yourdomain.com/myvpn-macos-intel.zip\",
    \"sha256\": \"$MAC_INTEL_SHA\"
  },
  \"macos_arm\": {
    \"url\": \"https://api.yourdomain.com/myvpn-macos-arm.zip\",
    \"sha256\": \"$MAC_ARM_SHA\"
  }
}
EOF"
```

The heartbeat endpoint returns `update_available` when `update.json` version changes.
Clients will check within 5 minutes of the heartbeat detecting the new version.

### Force-Update All Clients

1. Push `update.json` with new version
2. All active clients get `update_available` on their next heartbeat
3. Clients automatically download and apply the update
4. If any client's update crashes → auto-rollback to `.prev`
5. If many clients roll back → investigate, fix, push again

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
scp /tmp/restore.db root@api.yourdomain.com:/opt/pocketbase/pb_data/data.db

# 4. Start PocketBase
ssh $VPS "systemctl start pocketbase"

# 5. Verify
curl -s "$PB_API/api/collections/codes/records?skipTotal=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items | length'
```

---

## Rate Limiting

### Check if an IP is Rate-Limited
```bash
curl -s "$PB_API/api/collections/activation_attempts/records?filter=(ip='203.0.113.42')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items | length'
```

### Clear Rate Limit for an IP
```bash
# Delete all attempts for that IP
IDS=$(curl -s "$PB_API/api/collections/activation_attempts/records?filter=(ip='203.0.113.42')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq -r '.items[].id')
for ID in $IDS; do
  curl -X DELETE "$PB_API/api/collections/activation_attempts/records/$ID" \
    -H "Authorization: Bearer $PB_TOKEN"
done
```
