# Locus Server Deployment Guide

> Deploy the complete Locus server infrastructure on a blank Ubuntu 22.04 VPS.

> **Rebrand note:** The product is now **Locus**, but the live VPS and every
> deployment script under `v5/server/` still name their installed artifacts
> **`myvpn-*`** (`/etc/myvpn`, `/usr/local/bin/myvpn-*.sh`, `/var/log/myvpn-*.log`,
> `/root/…`, systemd units). Those literal `myvpn` paths remain **correct as
> written** until the server is redeployed/renamed — do not "fix" them to `locus`.

---

## 1. Prerequisites

- **VPS:** Ubuntu 22.04, x86_64. **512MB RAM works** — the live host runs 454MB
  with 1GB swap, but that is the floor; if you can afford 1–2GB it will be more
  comfortable. 10GB disk minimum.
- **Domain:** A domain name pointing to your VPS IP (A record)
- **Backblaze B2 account** (optional, for backups)
- **Age encryption key** (see `SECRETS-MANAGEMENT.md` for one-time setup)

> **Memory gotcha:** `00-env.sh` has a memory guard. It previously compared
> against 512 **MiB**, which made a "512MB" droplet (reports 454MB) impossible to
> pass — fixed 2026-09-19. If the guard fails on a small box, check that fix is
> present before assuming the host is too small.

### Recommended VPS Providers

| Provider | Plan | Spec | Cost |
|----------|------|------|------|
| DigitalOcean (**currently live**) | Basic | 1 vCPU, 512MB + 1GB swap | ~$6/mo |
| Hetzner | CCX13 | 2 vCPU, 4GB RAM | ~$8/mo |
| Vultr | Regular Cloud | 1 vCPU, 2GB RAM | ~$12/mo |

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

### Staging the server tree (read this before Option A or B)

`setup.sh` reads **two things that a bare `scp -r v5/server` does not put
there for you**. The first is a hard failure; the second already ships in the
repo, but is worth knowing about because module 05 refuses to deploy without
it:

| Path on the VPS | Required by | Produce it with |
|---|---|---|
| `/root/server/console-dist.tar.gz` | `05-caddy.sh` → `deploy_console()` | `v5/server/scripts/deploy-console.sh` (builds the SPA with npm, uploads the tarball) |
| `/root/server/scripts/fetch-release.py` | `05-caddy.sh` → `install_fetch_service()` | already in the repo — it arrives with `scp -r v5/server` |

So on a **fresh** host the order is:

```bash
# 1. Stage the server tree (fetch-release.py comes along; the console bundle does not)
scp -r v5/server age-key.txt root@your-vps:/root/server/

# 2. Build + upload the console bundle to /root/server/console-dist.tar.gz
VPS=root@your-vps DOMAIN=hub.example.com \
  v5/server/scripts/deploy-console.sh

# 3. Deploy
ssh root@your-vps "/root/server/setup.sh"
```

`deploy-console.sh` needs `npm` on **your** machine (not the VPS — the VPS has
no Node and the console source is not shipped to it). Both `VPS` and `DOMAIN`
MUST be set explicitly: the script has no usable default for a new host.

If you cannot build the console, deploy with `SKIP_CONSOLE=1` — the hub, the
tiers and the API all come up without it, and you can add the console later by
running `deploy-console.sh` and re-running `setup.sh` (module 05 extracts the
bundle if it is present).

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
4. Applies tc traffic shaping (Eco 5 Mbps, Stealth 100 Mbps, Strike 200 Mbps,
   each with an `fq_codel` leaf qdisc to keep latency flat under load)
5. Installs **sing-box UDP-over-TCP on :8446** for the Strike gaming tier
   (open the firewall for it too; `ENABLE_UOT=0` opts out)
6. Installs Caddy with rate limiting, Let's Encrypt TLS, `/admin/` and `/updates/`
7. Installs PocketBase with JS hooks and SQLite WAL mode, **creates the admin,
   the collections, the schema and the tier configs** (a failure here is fatal —
   it used to be a warning, which made a hub serving 500s look deployed)
8. Deploys the **admin console** to `/admin/` (required, not optional — a
   missing bundle fails the deploy; see "Staging the server tree" below)
   and installs the **release fetch service** (`locus-fetch` on
   `127.0.0.1:8091`, required — it is what the console's Releases page
   calls to pull a GitHub Release onto the hub)
9. Configures hourly B2 backup systemd timer
10. Sets up UFW firewall (including 8446 TCP+UDP) + **fail2ban** for SSH

**A fresh deploy needs no follow-up steps.** The console is live, tier configs
are seeded, backups are scheduled, and Strike clients receive `uot_port` on
their next heartbeat. The one thing a deploy does *not* do is invent activation
codes for you — see the optional batch flag below.

### Optional: generate a first batch of codes during deploy

```bash
FIRST_BATCH=50 FIRST_BATCH_MIDDLEMAN=Sarah \
  ssh root@your-vps "/root/server/setup.sh"
```

| Variable | Meaning |
|----------|---------|
| `FIRST_BATCH` | Codes per tier (applies to all three) |
| `FIRST_BATCH_ECO` / `_STEALTH` / `_STRIKE` | Per-tier overrides |
| `FIRST_BATCH_MIDDLEMAN` | Recorded against every code in the batch |
| `FIRST_BATCH_EXPIRES` | Optional expiry, `YYYY-MM-DD` |

Minted **once** and recorded in `/root/.first_batch_done`, so a re-run never
creates a second batch of unsold inventory. Delete that file to re-arm.

### Opting out

| Variable | Effect |
|----------|--------|
| `ENABLE_UOT=0` | Skips the sing-box UoT endpoint *and* its firewall rule. Re-run `seed-pb.py` so the strike tier stops advertising `uot_port`. |
| `SKIP_CONSOLE=1` | Skips deploying the admin console (and stops requiring its bundle). Not recommended. |
| `SKIP_DNS_CHECK=1` | Proceeds even if the domain does not resolve to this host. |

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
- [ ] `systemctl is-active locus-fetch` → active (only if publishing releases)
- [ ] `curl -sf https://networkingguides.duckdns.org/api/health` → 200
- [ ] `curl -s -o /dev/null -w '%{http_code}' https://networkingguides.duckdns.org/admin/` → 200 (and `/admin` → 301)
- [ ] `ufw status` → active with all rules
- [ ] `systemctl is-active fail2ban` → active
- [ ] `openssl s_client -connect networkingguides.duckdns.org:443 -servername networkingguides.duckdns.org </dev/null 2>/dev/null | openssl x509 -noout -dates` → valid cert

> **`/update.json` is not a health check.** It is a stale placeholder written by
> `05-caddy.sh`. The client updater reads the **`update_config`** record, not
> that file — do not use it to judge whether updates are working. See
> `OPS.md` → "Client Update System" → "Verify it is actually working".

> **⚠️ Do not rate-limit SSH with ufw.** Earlier revisions used
> `ufw limit 22/tcp` (6 conns/30s), which locked out legitimate automation, and a
> custom limiter chain in `/etc/ufw/before.rules` locked SSH out **completely**
> (port 22 timed out while 80/443 served), needing provider-console recovery.
> SSH protection is **fail2ban** (`/etc/fail2ban/jail.d/locus-sshd.local`,
> 6 fails/10m → 1h ban, `banaction=ufw`).

> **⚠️ Backup timer gotcha:** the timer has `Requires=pocketbase.service`, so
> **stopping PocketBase also stops the timer** (systemd `Requires=` propagates
> stops, not starts). After any `systemctl stop pocketbase`, bring it back with
> `systemctl restart pocketbase-backup.timer` (or `enable --now` on a fresh
> install). `restore.sh` does this automatically.

> **Smoke test:** `setup.sh` runs `v5/server/scripts/smoke-test.sh` automatically
> at the end (log: `/var/log/myvpn-smoke-test.log`). A fresh deploy should end
> with **23 passed / 0 failed / 0 warnings**.

### Create PocketBase Admin (First Run Only)

1. Visit `https://networkingguides.duckdns.org/_/` in a browser
2. Create your admin account (first-run wizard)

> If `PB_ADMIN_EMAIL` and `PB_ADMIN_PASS` were set in `secrets.env.age`, the
> admin account is created automatically by `06-pocketbase.sh` /
> `seed-pb.py`. Check `/root/.pb_admin_creds` on the VPS.
>
> **PocketBase 0.22 note:** `06-pocketbase.sh` can only bootstrap the first
> admin from the **CLI** (`pocketbase admin create … --dir /opt/pocketbase/pb_data`).
> `/api/collections/_superusers/*` returns **404** on this build — an earlier
> `seed-pb.py` used exactly that path and therefore seeded *nothing* while
> reporting success. `admin create` never updates an existing admin, so if the
> credentials file disagrees with the live password, `seed-pb.py` forces an
> `admin update` when the env value fails to log in.

### Deploy the Admin Console (Recommended)

```bash
# Builds locally, uploads, extracts to /var/www/admin, reloads Caddy, verifies
v5/server/scripts/deploy-console.sh
```

Day-to-day operations then happen at `https://<domain>/admin/` with the admin
token. Note `05-caddy.sh` extracts `console-dist.tar.gz` if present, so a full
`setup.sh` re-run keeps the deployed console rather than reverting it.

### Generate Activation Codes

```bash
# Generate codes for each tier (token = PocketBase ADMIN JWT from
# /root/.pb_admin_creds — NOT /root/.admin_api_token, which PocketBase 0.22
# rejects with 401)
PB_TOKEN=$(ssh root@your-vps "grep PB_TOKEN /root/.pb_admin_creds | cut -d= -f2")
./scripts/generate_codes.sh https://networkingguides.duckdns.org "$PB_TOKEN" eco 50
./scripts/generate_codes.sh https://networkingguides.duckdns.org "$PB_TOKEN" stealth 30
./scripts/generate_codes.sh https://networkingguides.duckdns.org "$PB_TOKEN" strike 20
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
