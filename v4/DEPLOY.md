# V4 Deployment — From Scratch

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

The script does everything:
- Enables BBR as system default CC
- Installs three ssserver instances (eco:8443, stealth:8444, strike:8445)
- Builds Brutal kernel module + LD_PRELOAD wrapper for Stealth
- Configures tc traffic shaping (5 Mbps cap on port 8443 for Eco)
- Configures tc traffic shaping (200 Mbps shared cap on port 8445 for Strike)
- Sets Brutal target rate to 48 Mbps
- Installs Caddy with rate limiting zones
- Installs PocketBase with JS hooks directory
- Opens firewall ports (8443, 8444, 8445, 80, 443)
- Sets up hourly offsite backup cron (requires B2 credentials)

### 4. Configure Backblaze B2

```bash
pip3 install b2
b2 authorize-application-key
# Create a bucket named "my-vpn-backup-bucket" via B2 web UI
# Test: b2 ls my-vpn-backup-bucket
# Then enable the backup cron: systemctl start pocketbase-backup.timer
```

### 5. Open PocketBase Admin

Visit `https://api.yourdomain.com/_/`. Create admin account.
Create collections: `codes`, `tier_configs`, `activation_attempts`.

### 6. Upload JS Hooks

```bash
scp server/pb_hooks/*.js root@your-vps:/opt/pocketbase/pb_hooks/
ssh root@your-vps "systemctl restart pocketbase"
```

### 7. Seed Tier Configs + Admin API Token

1. Set the admin API token: In PocketBase admin → Settings → App settings → set `admin_api_token` (or use the env var)
2. Enter the three Shadowsocks configs in `tier_configs` (see ARCHITECTURE.md). Copy passwords from `/root/.tier_passwords` on the VPS.
3. Enable the Caddy rate_limit zones

### 8. Generate Initial Codes

```bash
./scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 50
./scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN stealth 30
./scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN strike 20
```

### 9. Print Code Cards

```bash
./scripts/print_codes.sh eco-codes.txt eco-cards.pdf
./scripts/print_codes.sh stealth-codes.txt stealth-cards.pdf
./scripts/print_codes.sh strike-codes.txt strike-cards.pdf
```

## Troubleshooting

| Problem | Check |
|---------|-------|
| App won't connect | `systemctl status shadowsocks-eco shadowsocks-stealth shadowsocks-strike` on VPS |
| Brutal not working | `lsmod \| grep tcp_brutal` — module must be loaded |
| Eco cap not working | `tc -s class show dev eth0` — check stats on class 1:10 (port 8443) |
| Strike cap not working | `tc -s class show dev eth0` — check stats on class 1:30 (port 8445) |
| Brutal rate wrong | `cat /sys/module/tcp_brutal/parameters/target_rate` |
| BBR not active | `sysctl net.ipv4.tcp_congestion_control` should show `bbr` |
| UDP relay failing | `journalctl -u shadowsocks-strike -n 20` |
| Activation failing | Check `codes` collection — code exists? suspended? Luhn checksum valid? |
| Rate limit hit | Check `activation_attempts` collection for IP |
| Backup failing | `b2 ls my-vpn-backup-bucket` — check B2 credentials |
| Update crash | SSH to VPS → check `update.json` → check app logs |
