# Deployment & Operations

## VPS Setup (One-Time)

### 1. Order a VPS

| Provider | Spec | Cost |
|----------|------|------|
| Hetzner CCX13 | 2 vCPU, 4GB RAM | ~$8/mo |
| DigitalOcean Basic | 1 vCPU, 2GB RAM | ~$12/mo |
| Vultr Regular Cloud | 1 vCPU, 2GB RAM | ~$12/mo |

Choose the cheapest. For 20 concurrent users, even a $6 VPS is fine.

### 2. Point DNS

```
A record: networkingguides.duckdns.org → YOUR_VPS_IP
```

### 3. Run Setup Script

```bash
ssh root@your-vps
DOMAIN=networkingguides.duckdns.org bash -s < scripts/setup_vps.sh
```

### 4. Configure Caddy TLS

If using Caddy with Let's Encrypt auto-TLS:

```bash
# Edit /etc/caddy/Caddyfile with your actual domain
sed -i 's/networkingguides.duckdns.org/networkingguides.duckdns.org/g' /etc/caddy/Caddyfile
caddy fmt --overwrite /etc/caddy/Caddyfile
systemctl reload caddy
```

Caddy automatically provisions Let's Encrypt certificates. Check with `caddy cert-info`.

### 5. Create PocketBase Admin

Open `https://networkingguides.duckdns.org/_/` in a browser.
Create your admin account (first-run setup).

### 6. Create Collections

In PocketBase admin UI:

**Collection: `codes`**
- `code` (text, required, unique)
- `tier` (select: `stealth`, `strike`)
- `used` (bool, default: false)
- `suspended` (bool, default: false)
- `middleman` (text, optional)

**Collection: `tier_configs`**
- `tier` (text, required, unique)
- `config_json` (json, required)
- `active` (bool, default: true)

Also disable public create/update/delete on `codes` — only admins should create codes. Public needs only read access to check activation.

For `tier_configs`, the activation hook reads it — ensure the API rule allows read access with the right filter.

### 7. Upload JS Hooks

```bash
# From your local machine
scp server/pb_hooks/*.js root@your-vps:/opt/pocketbase/pb_hooks/
ssh root@your-vps "systemctl restart pocketbase"
```

### 8. Configure Hysteria 2 Server

Edit the auth passwords in `/etc/hysteria/stealth.yaml` and `/etc/hysteria/strike.yaml`:

```bash
ssh root@your-vps
# Generate random passwords
openssl rand -base64 32  # for stealth pool
openssl rand -base64 32  # for strike pool

# Edit the server config
nano /etc/hysteria/server.yaml   # change all three passwords

# Start the service
systemctl start hysteria
systemctl enable hysteria
```

### 9. Update Tier Configs in PocketBase

Create the `tier_configs` entries with the actual server address and auth tokens.

**Eco config** (in PocketBase tier_configs collection):
```json
{
  "server": "networkingguides.duckdns.org:443",
  "auth": "THE-ECO-AUTH-TOKEN-YOU-GENERATED",
  "tls": {
    "sni": "www.bing.com",
    "insecure": false
  },
  "obfs": "salamander",
  "obfs-password": "eco-obfs-key",
  "recv_window_conn": 1048576,
  "recv_window": 4194304,
  "disable_mtu_discovery": true,
  "speed": 5000000
}
```

**Stealth config** (in PocketBase tier_configs collection):
```json
{
  "server": "networkingguides.duckdns.org:443",
  "auth": "THE-STEALTH-AUTH-TOKEN-YOU-GENERATED",
  "tls": {
    "sni": "www.bing.com",
    "insecure": false
  },
  "obfs": "salamander",
  "obfs-password": "stealth-obfs-key",
  "recv_window_conn": 4194304,
  "recv_window": 33554432,
  "disable_mtu_discovery": true,
  "speed": 48000000
}
```

**Strike config**:
```json
{
  "server": "networkingguides.duckdns.org:443",
  "auth": "THE-STRIKE-AUTH-TOKEN-YOU-GENERATED",
  "tls": {
    "sni": "www.bing.com",
    "insecure": false
  },
  "obfs": "salamander",
  "obfs-password": "strike-obfs-key",
  "brutal": {
    "enabled": true,
    "up_mbps": 50,
    "down_mbps": 50
  },
  "recv_window_conn": 16777216,
  "recv_window": 67108864,
  "disable_mtu_discovery": true,
  "hop-interval": 10
}
```

### 10. Create Probe File (for Strike Bandwidth Detection)

The Strike tier does a quick bandwidth probe at connect time — it downloads a
~500KB file from the server and measures throughput. This file needs to exist:

```bash
# Generate a 500KB random probe file
ssh root@your-vps "
  mkdir -p /var/www/html
  dd if=/dev/urandom of=/var/www/html/probe-file bs=1024 count=500
  chmod 644 /var/www/html/probe-file
"

# Serve it through Caddy (ensure /var/www/html is in your Caddyfile)
# Add this to /etc/caddy/Caddyfile:
#   root * /var/www/html
# Then:
caddy fmt --overwrite /etc/caddy/Caddyfile
systemctl reload caddy
```

Verify the file is accessible:
```bash
curl -o /dev/null -w "%{speed_download}" https://networkingguides.duckdns.org/probe-file
```

### 11. Generate Activation Codes

```bash
# Get your admin token from PocketBase admin UI → Settings → API tokens
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_ADMIN_TOKEN eco 10
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_ADMIN_TOKEN stealth 5
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_ADMIN_TOKEN strike 5

# For a specific middleman:
./scripts/generate_codes.sh https://networkingguides.duckdns.org YOUR_ADMIN_TOKEN eco 10 "Bob"
```

## Daily Operations

### Create New Codes

Via PocketBase admin UI (`/_/`):
- Go to `codes` collection → New record
- Enter code, tier, optionally middleman name
- Save

Or use the script above.

### Suspend a User

Via PocketBase admin UI:
- Find the user's code in `codes` collection
- Check `suspended = true`
- Save
- Next time they try to connect, they see "Account suspended — contact your middleman"

### Unsuspend a User

Same path, set `suspended = false`.

### Check Who's Using What

PocketBase `codes` collection shows:
- `used = true` → code has been activated
- `tier` → what tier they're on
- `middleman` → who sold it

There's no real-time connection tracking (no heartbeat) so you won't know if they're currently connected. For 20 users, just ask them.

### Change Server Config (Push Updates to Clients)

Edit the `tier_configs` entry in PocketBase:
- Change `config_json` with new server settings
- Save
- Next time each user connects, the activation endpoint returns the new config
- You can change: server address, SNI, obfuscation password, bandwidth caps, Brutal settings

This is how you rotate if school IT starts blocking your VPS IP or fingerprint.

### Backup

```bash
# Manual backup
ssh root@your-vps "cp /opt/pocketbase/data/data.db ~/pb-backup-$(date +%Y%m%d).db"

# Or set up hourly cron:
# 0 * * * * cp /opt/pocketbase/data/data.db /backups/pb-$(date +\%Y\%m\%d-\%H).db
# Keep last 7 days, delete older
```

For 20 users, manual weekly backup is fine.

## When Things Go Wrong

### "The app won't connect"

Most likely causes:
1. VPS is down → SSH in, check `systemctl status hysteria pocketbase`
2. School blocked the port → Change Hysteria 2 server port from 443/8443 to 2053 or 8080, update config in PocketBase
3. School blocked your IP → Create new VPS, update DNS, update config in PocketBase
4. TLS certificate expired → `certbot renew` or check Caddy auto-renew

### "The app says 'Invalid activation code'"

1. Check if the code was typed correctly (case-insensitive, dashes required)
2. Check in PocketBase admin that the code exists
3. Check if the code is marked as `suspended`

### "Hysteria 2 engine won't start"

1. Check the engine log in `%APPDATA%/MyVPN/logs/` (Windows) or `~/Library/Application Support/MyVPN/logs/` (macOS)
2. Verify the engine binary exists in the `engines/` subdirectory next to the app
3. Try running the engine from command line with the config file to test

### "The app crashes on launch"

1. Windows: SmartScreen might block it. Instruct user to click "More info" → "Run anyway"
2. macOS: Gatekeeper might block it. Instruct user to Ctrl+Click → Open
3. If it crashes after that, check for missing CGO dependencies (Fyne requires graphics libraries on Linux)

## Recommended Monitoring (Minimal)

For 20 users, you don't need Grafana. Just check:

```bash
# Quick health check
ssh root@your-vps "
  systemctl is-active pocketbase && echo 'PB OK' || echo 'PB DOWN'
  systemctl is-active hysteria && echo 'Hysteria OK' || echo 'Hysteria DOWN'
  df -h / | tail -1
"
```

Optional: set up UptimeRobot (free tier) to ping `https://networkingguides.duckdns.org/api/health` every 5 minutes.

## Shutting Down

If you decide to stop the service:

1. Export PocketBase data (if needed)
2. `systemctl stop hysteria pocketbase`
3. `apt remove caddy`
4. Delete the VPS from your provider dashboard
5. Remove DNS record
6. Inform middlemen that the service is discontinued (they should inform users)

The app will show "Could not connect to server" if the VPS is gone. Users can delete it.
