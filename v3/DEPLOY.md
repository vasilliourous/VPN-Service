# V3 Deployment — From Scratch

## One-Time VPS Setup

### 1. Order a VPS

| Provider | Spec | Cost |
|----------|------|------|
| Hetzner CCX13 | 2 vCPU, 4GB RAM | ~$8/mo |
| DigitalOcean Basic | 1 vCPU, 2GB RAM | ~$12/mo |
| Vultr Regular Cloud | 1 vCPU, 2GB RAM | ~$12/mo |

Any works. 2GB RAM is plenty for 20 users.

### 2. Point DNS

```
A record: api.yourdomain.com → YOUR_VPS_IP
```

### 3. Run Setup Script

```bash
DOMAIN=api.yourdomain.com ssh root@your-vps 'bash -s' < scripts/setup_vps.sh
```

The script does everything: installs shadowsocks, enables BBR as system default, builds CUBIC and Brutal LD_PRELOAD wrappers, compiles TCP Brutal kernel module, configures tc traffic shaping for Eco (5 Mbps), sets Brutal target rate (48 Mbps), sets up three ssserver instances (eco:8443, stealth:8444, strike:8445), installs PocketBase + Caddy, opens firewall ports.

### 4. Open PocketBase Admin

Visit `https://api.yourdomain.com/_/` in a browser. Create admin account. Create `codes` and `tier_configs` collections.

### 5. Upload JS Hooks

```bash
scp server/pb_hooks/*.js root@your-vps:/opt/pocketbase/pb_hooks/
ssh root@your-vps "systemctl restart pocketbase"
```

### 6. Seed Tier Configs

Enter the three Shadowsocks configs in the `tier_configs` collection (see ARCHITECTURE.md for exact JSON). Match the passwords to what the setup script generated (see `/root/.tier_passwords` on the VPS).

### 7. Generate Initial Codes

```bash
./scripts/print_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 50 eco-cards.txt
./scripts/print_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN stealth 30 stealth-cards.txt
./scripts/print_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN strike 20 strike-cards.txt
```

## Daily Ops

### List Users

```bash
curl -s "$PB_API/api/collections/codes/records?skipTotal=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[] | {code, tier, used, suspended}'
```

### Suspend a User

```bash
# Find record ID
ID=$(curl -s "$PB_API/api/collections/codes/records?filter=(code='ABC1-DEF2-GHI3')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq -r '.items[0].id')
# Suspend
curl -X PATCH "$PB_API/api/collections/codes/records/$ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": true}'
```

### Push an Update

```bash
make bundle-all
# Upload zips to VPS
scp dist/*.zip root@api.yourdomain.com:/var/www/html/
# Update update.json with new version + SHA256 hashes
```

## Troubleshooting

| Problem | Check |
|---------|-------|
| App won't connect | `systemctl status shadowsocks-eco shadowsocks-stealth shadowsocks-strike` on VPS |
| Brutal not working | `lsmod \| grep tcp_brutal` on VPS — module must be loaded |
| Eco cap not working | `tc -s qdisc show dev eth0` — check tc stats on port 8443 |
| Brutal rate wrong | `cat /sys/module/tcp_brutal/parameters/target_rate` or `sysctl net.ipv4.tcp_brutal.target_rate` |
| BBR not active | `sysctl net.ipv4.tcp_congestion_control` should show `bbr` |
| UDP relay failing | `journalctl -u shadowsocks-strike -n 20` — check for UDP errors |
| Activation failing | Check PocketBase `codes` collection — code exists? suspended? |
| Firewall blocking | `ufw status` — ensure 8443, 8444, and 8445 are open |
