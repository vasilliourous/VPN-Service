# MyVPN Server Deployment Guide

> Deploy the complete MyVPN server infrastructure on a blank Ubuntu 22.04 VPS.

---

## 1. Prerequisites

- **VPS:** Ubuntu 22.04, x86_64, 2GB RAM minimum, 20GB disk
- **Domain:** A domain name pointing to your VPS IP (A record)
- **Backblaze B2 account** (optional, for backups)
- **Age encryption key** (see `SECRETS-MANAGEMENT.md` for one-time setup)

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
networkingguides.duckdns.org  A  YOUR_VPS_IP
```

TTL of 60s recommended during initial setup. Increase to 300s+ after stable.

---

## 3. Secrets Setup (One-Time)

Before your first deploy, create an age-encrypted secrets file. This eliminates
the need to manually pass environment variables on every deploy.

**See the full guide:** [`SECRETS-MANAGEMENT.md`](SECRETS-MANAGEMENT.md)

Quick summary:

```bash
# 1. Install age
curl -sLO "https://github.com/FiloSottile/age/releases/download/v1.2.1/age-v1.2.1-linux-amd64.tar.gz"
tar -xzf age-v1.2.1-linux-amd64.tar.gz
sudo cp age/age age/age-keygen /usr/local/bin/

# 2. Generate a key pair
age-keygen -o age-key.txt

# 3. Create .secrets.env with all credentials:
#    DOMAIN, ADMIN_API_TOKEN, B2_APPLICATION_KEY_ID/KEY, B2_BUCKET,
#    ECO_PASS, STEALTH_PASS, STRIKE_PASS, PB_ADMIN_EMAIL, PB_ADMIN_PASS
#    (See SECRETS-MANAGEMENT.md for the full template)

# 4. Encrypt it
age -r "$(age-keygen -y age-key.txt)" -o v5/server/secrets.env.age .secrets.env
shred -u .secrets.env

# 5. Commit the encrypted file
git add v5/server/secrets.env.age
```

Once this is done, **all credentials are auto-injected at deploy time** from
`secrets.env.age`. You never type them again.

---

## 4. Deploy

### Option A: Local Machine with Key File (Recommended)

```bash
# Copy server code AND age key to the VPS
scp -r v5/server age-key.txt root@your-vps:/root/server/

# One-command deploy — secrets auto-decrypt from age-key.txt
ssh root@your-vps "/root/server/setup.sh"
```

No environment variables needed — `DOMAIN`, `ADMIN_API_TOKEN`, B2 creds,
tier passwords, and PB admin credentials are all decrypted from
`secrets.env.age` automatically.

### Option B: Pipe with AGE_KEY (CI/CD or no key file on disk)

```bash
AGE_KEY=$(cat age-key.txt) ssh root@your-vps 'bash -s' < v5/server/setup.sh
```

### Option C: Manual Env Vars (No Secrets File)

If you haven't set up the age-encrypted secrets file yet:

```bash
DOMAIN=networkingguides.duckdns.org \
ADMIN_API_TOKEN=your-token \
B2_APPLICATION_KEY_ID=xxx \
B2_APPLICATION_KEY=xxx \
B2_BUCKET=my-vpn-backup-bucket \
ssh root@your-vps 'bash -s' < v5/server/setup.sh
```

The setup script does **everything** automatically:
1. Validates environment (OS, arch, root, disk, memory, DNS)
2. Enables BBR + TCP kernel tuning
3. Installs 3 ssserver instances (eco:8443, stealth:8444, strike:8445)
4. Applies tc traffic shaping (Eco 5 Mbps, Stealth 100 Mbps, Strike 200 Mbps)
5. Installs Caddy with rate limiting and Let's Encrypt TLS
6. Installs PocketBase with JS hooks and SQLite WAL mode
7. Configures hourly B2 backup systemd timer
8. Sets up UFW firewall (SSH rate-limited, all needed ports open)

---

## 5. Post-Deployment Verification

After deployment, verify:

- [ ] `systemctl is-active caddy` → active
- [ ] `systemctl is-active pocketbase` → active
- [ ] `systemctl is-active shadowsocks-eco` → active
- [ ] `systemctl is-active shadowsocks-stealth` → active
- [ ] `systemctl is-active shadowsocks-strike` → active
- [ ] `sysctl net.ipv4.tcp_congestion_control` → bbr
- [ ] `tc -s class show dev eth0` → classes 1:10 (Eco 5 Mbps), 1:20 (Stealth 100 Mbps), 1:30 (Strike 200 Mbps)
- [ ] `systemctl is-active pocketbase-backup.timer` → active (hourly B2 backups; setup auto-runs the first backup)
- [ ] `tail -5 /var/log/myvpn-backup.log` → last line "Backup completed (exit 0)"
- [ ] `curl -sf https://networkingguides.duckdns.org/update.json` → JSON manifest
- [ ] `curl -sf https://networkingguides.duckdns.org/api/health` → 200
- [ ] `ufw status` → active with all rules
- [ ] `openssl s_client -connect networkingguides.duckdns.org:443 -servername networkingguides.duckdns.org </dev/null 2>/dev/null | openssl x509 -noout -dates` → valid cert

> **⚠️ Backup timer gotcha:** the timer has `Requires=pocketbase.service`, so
> **stopping PocketBase also stops the timer** (systemd `Requires=` propagates
> stops, not starts). After any `systemctl stop pocketbase`, bring it back with
> `systemctl restart pocketbase-backup.timer` (or `enable --now` on a fresh
> install). `restore.sh` does this automatically.

> **Smoke test:** `setup.sh` runs `v5/server/scripts/smoke-test.sh` automatically
> at the end (log: `/var/log/myvpn-smoke-test.log`). A fresh deploy should end
> with "✅ All critical checks passed!".

### Create PocketBase Admin (First Run Only)

1. Visit `https://networkingguides.duckdns.org/_/` in a browser
2. Create your admin account (first-run wizard)

> If `PB_ADMIN_EMAIL` and `PB_ADMIN_PASS` were set in `secrets.env.age`, the
> admin account is created automatically by `06-pocketbase.sh`. Check
> `/root/.pb_admin_creds` on the VPS.

### Generate Activation Codes

```bash
# The ADMIN_API_TOKEN is already on the VPS from secrets.env.age
# Check it:
ssh root@your-vps "cat /root/.admin_api_token"

# Generate codes for each tier
./scripts/generate_codes.sh https://networkingguides.duckdns.org $(ssh root@your-vps "cat /root/.admin_api_token") eco 50
./scripts/generate_codes.sh https://networkingguides.duckdns.org $(ssh root@your-vps "cat /root/.admin_api_token") stealth 30
./scripts/generate_codes.sh https://networkingguides.duckdns.org $(ssh root@your-vps "cat /root/.admin_api_token") strike 20
```

### Print Code Cards

```bash
./scripts/print_codes.sh eco-codes.txt eco-cards.pdf
./scripts/print_codes.sh stealth-codes.txt stealth-cards.pdf
./scripts/print_codes.sh strike-codes.txt strike-cards.pdf
```

---

## 6. Maintaining Secrets

When credentials change (e.g., B2 application key rotated):

```bash
# Decrypt, edit, re-encrypt
age -d -i age-key.txt v5/server/secrets.env.age > .secrets.env
vim .secrets.env
age -r "$(age-keygen -y age-key.txt)" -o v5/server/secrets.env.age .secrets.env
shred -u .secrets.env
git add v5/server/secrets.env.age
git commit -m "Update credentials"
```

See [`SECRETS-MANAGEMENT.md`](SECRETS-MANAGEMENT.md) for full details on key
rotation, CI/CD integration, and troubleshooting.

---

## 7. Disaster Recovery

### Using Age Secrets (Recommended)

```bash
# With key file on VPS:
scp -r v5/server age-key.txt root@new-vps:/root/server/
ssh root@new-vps "/root/server/restore.sh"

# Or with AGE_KEY via pipe:
AGE_KEY=$(cat age-key.txt) ssh root@new-vps 'bash -s' < v5/server/restore.sh
```

### Manual Override

```bash
B2_APPLICATION_KEY_ID=xxx \
B2_APPLICATION_KEY=xxx \
B2_BUCKET=my-vpn-backup-bucket \
DOMAIN=networkingguides.duckdns.org \
ssh root@new-vps 'bash -s' < v5/server/restore.sh
```

This will:
1. Provision the new VPS from scratch (runs full `setup.sh` — BBR, 3× Shadowsocks, tc caps 5/100/200 Mbps, Caddy + TLS, PocketBase, backups, UFW)
2. Install the b2 CLI and authenticate with the secrets' B2 key
3. Find the **latest** backup in `b2://<bucket>/backups/` (or use `BACKUP_PATH=...` to pick a specific one)
4. Download + verify the SHA256 checksum (aborts on mismatch)
5. Restore the PocketBase database (backs up the fresh-seed DB to `data.db.pre-restore`, removes stale `-wal`/`-shm`)
6. **Align the admin password with the secrets file** (`pocketbase admin update`) so the documented credentials work
7. Restart the backup timer (stopping PocketBase stops it via `Requires=`) and verify it is active
8. Verify: codes count from the restored DB, JS hooks loaded (400 "Missing code"), health endpoints

> **One-command recovery = plug-n-play.** A blank Ubuntu 22.04 VPS + this repo
> + `age-key.txt` is all you need: `setup.sh` for a fresh deploy, `restore.sh`
> for a full migration/disaster recovery. Both were validated live on
> 2026-08-01 (see `v5/docs/FIXES.md`).
