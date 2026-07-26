# V3 Operations Manual (for Humans & Agents)

Same as V2 but with Shadowsocks-specific commands and three server instances.

## Quick Reference

```bash
export PB_API="https://jeanette-qh9zbe.cloudserver.nz"
export PB_TOKEN="your-pocketbase-admin-token"
export VPS="root@jeanette-qh9zbe.cloudserver.nz"
```

### Server Health

```bash
# All three services
ssh $VPS "echo '=== Shadowsocks ===' && systemctl is-active shadowsocks-eco shadowsocks-stealth shadowsocks-strike"

# Brutal module
ssh $VPS "echo '=== Brutal Module ===' && lsmod | grep tcp_brutal || echo 'NOT LOADED'"

# BBR default
ssh $VPS "echo '=== CC ===' && sysctl net.ipv4.tcp_congestion_control"

# tc stats (Eco cap)
ssh $VPS "echo '=== tc stats ===' && tc -s qdisc show dev eth0"

# Individual logs
ssh $VPS "journalctl -u shadowsocks-eco -n 10 --no-pager"
ssh $VPS "journalctl -u shadowsocks-stealth -n 10 --no-pager"
ssh $VPS "journalctl -u shadowsocks-strike -n 10 --no-pager"

# PocketBase + Caddy
ssh $VPS "echo '=== PB + Caddy ===' && systemctl is-active pocketbase caddy"
```

### Generate Codes (Printable)

```bash
./scripts/print_codes.sh $PB_API $PB_TOKEN eco 50
./scripts/print_codes.sh $PB_API $PB_TOKEN stealth 30
./scripts/print_codes.sh $PB_API $PB_TOKEN strike 20
```

### Push an Update

```bash
make bundle-all
VERSION="1.1.0"
WIN_SHA=$(sha256sum dist/myvpn-windows.zip | cut -d' ' -f1)
MAC_INTEL_SHA=$(sha256sum dist/myvpn-macos-intel.zip | cut -d' ' -f1)
MAC_ARM_SHA=$(sha256sum dist/myvpn-macos-arm.zip | cut -d' ' -f1)

scp dist/*.zip root@jeanette-qh9zbe.cloudserver.nz:/var/www/html/

ssh root@jeanette-qh9zbe.cloudserver.nz "cat > /var/www/html/update.json << EOF
{
  \"version\": \"$VERSION\",
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

### User Management

Same as V2 — list, suspend, unsuspend via PocketBase API.

### Config Updates

To change passwords, ports, or tier configs:

```bash
# 1. Update the specific VPS config
ssh $VPS "nano /etc/shadowsocks/eco.json && systemctl restart shadowsocks-eco"
ssh $VPS "nano /etc/shadowsocks/stealth.json && systemctl restart shadowsocks-stealth"
ssh $VPS "nano /etc/shadowsocks/strike.json && systemctl restart shadowsocks-strike"

# 2. Update PocketBase tier_configs to match
curl -X PATCH "$PB_API/api/collections/tier_configs/records/CONFIG_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"config": {"server":"jeanette-qh9zbe.cloudserver.nz","server_port":8443,...}}'
```

### Change an Eco User's Password

```bash
# Generate new password
NEW_ECO=$(openssl rand -hex 16)

# Update server config
ssh $VPS "sed -i 's/\"password\": \".*\"/\"password\": \"$NEW_ECO\"/' /etc/shadowsocks/eco.json && systemctl restart shadowsocks-eco"

# Update PocketBase
curl -X PATCH "$PB_API/api/collections/tier_configs/records/ECO_CONFIG_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"config\": {\"password\": \"$NEW_ECO\"}}"
```

### Update Brutal Target Rate

```bash
# Change from 48 Mbps to 30 Mbps
ssh $VPS "sysctl -w net.ipv4.tcp_brutal.target_rate=30000000"
# Or via sysfs:
ssh $VPS "echo 30 > /sys/module/tcp_brutal/parameters/target_rate"
```

### Update Eco Bandwidth Cap

```bash
ssh $VPS "tc class change dev eth0 parent 1: classid 1:10 htb rate 10mbit ceil 10mbit"
```

## Auto-Refill

Same cron job as V2. Keeps unused code pool above 10 per tier.
