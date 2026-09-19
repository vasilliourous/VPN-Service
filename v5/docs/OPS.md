# Locus Operations Manual

> Day-to-day management of the Locus server.

---

## Quick Reference

```bash
export VPS="root@networkingguides.duckdns.org"
export PB_API="https://networkingguides.duckdns.org"
export PB_TOKEN="your-pocketbase-admin-token"
export ADMIN_TOKEN="your-admin-api-token"
```

> **Live host (from 2026-09-19):** `170.64.196.179` (DigitalOcean, Ubuntu
> 22.04, 1 vCPU / 512MB + 1GB swap, Sydney) — `networkingguides.duckdns.org`
> now resolves here. Previous hosts `114.23.136.59` and `134.199.155.166` are
> retired; the latter is fully offline. On a 512MB box, watch memory:
> `free -m` and `systemctl status <svc> | grep Memory`.

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

### Congestion Control

All three tiers use **BBR** (Linux kernel built-in) — no kernel modules to maintain.

```bash
# Confirm BBR is the system default
ssh $VPS "sysctl net.ipv4.tcp_congestion_control"
```

### Traffic Shaping

```bash
# Check tc classes exist
ssh $VPS "tc -s class show dev \$(ip route show default | awk '\$5{print\$5;exit}')"

# Eco class (1:10) should show traffic — 5 Mbps
# Stealth class (1:20) should show traffic — 100 Mbps
# Strike class (1:30) should show traffic — 200 Mbps
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
RECORD_ID=$(curl -s "$PB_API/api/collections/codes/records?filter=(code='RQ-ABCD-EFGH-JKMN-T')" \
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
    "code": "RQ-ABCD-EFGH-JKMN-T",
    "reason": "Device reported lost"
  }'
```

### Generate New Codes

```bash
# From your local machine (the token is the PocketBase ADMIN JWT, not the
# app-level ADMIN_API_TOKEN — see generate_codes.sh --help)
PB_TOKEN=$(ssh root@your-vps "grep PB_TOKEN /root/.pb_admin_creds | cut -d= -f2")
./scripts/generate_codes.sh "$PB_API" "$PB_TOKEN" eco 10
```

---

## Deployment

### Deploy a New VPS

Deploying to a **blank** box is a tested path (validated 2026-09-19 on a fresh
Ubuntu 22.04, 1 vCPU / 512MB droplet). Work through these steps in order.

```bash
# 1. Copy server code (secrets.env.age + age-key.txt are required —
#    setup.sh auto-decrypts them; without the key it hard-fails).
scp -r v5/server root@new-vps:/root/

# 2. Run setup. DNS must already point at this host or Caddy cannot get a
#    certificate; SKIP_DNS_CHECK=1 only skips the pre-flight, it does NOT
#    make TLS work. Setup is idempotent — safe to re-run.
ssh root@new-vps "apt-get install -y age"   # required for secret decryption
ssh root@new-vps "DOMAIN=networkingguides.duckdns.org /root/server/setup.sh"

# 3. Only needed if step 2 ran before DNS resolved: re-issue the certificate.
ssh root@new-vps "systemctl restart caddy && journalctl -u caddy -n 30 --no-pager | grep -i 'certificate obtained'"

# 4. Verify (expect: 23 passed / 0 failed / 0 warnings)
ssh root@new-vps "DOMAIN=networkingguides.duckdns.org bash /root/server/scripts/smoke-test.sh"

# 5. Seed activation codes — a fresh DB has an EMPTY codes collection, so
#    every activation fails until this is done. codes.json holds the RQ- set.
```

#### Blank-VPS gotchas (all previously caused failed deploys)

- **`/root/server/` is a stale staging copy.** `setup.sh` deploys hooks from
  `/root/server/pb_hooks/` (not from the repo), so after changing a hook you
  must update **both** the staging dir and the live dir, then restart
  PocketBase:
  ```bash
  scp v5/server/pb_hooks/code_lookup.pb.js root@$VPS:/root/server/pb_hooks/
  ssh $VPS "cp /root/server/pb_hooks/code_lookup.pb.js /opt/pocketbase/pb_hooks/ \
            && chown pocketbase:pocketbase /opt/pocketbase/pb_hooks/code_lookup.pb.js \
            && systemctl restart pocketbase"
  ```
  Re-running `setup.sh` copies staging → live, so a stale staging dir silently
  **reverts** live hook fixes (this happened on 2026-09-19).
- **512MB droplets**: the memory guard allows them, but add swap. No module
  creates it; `00-env.sh` warns if it is missing.
- **PocketBase admin**: `06-pocketbase.sh` now creates the first admin via the
  CLI (`pocketbase admin create`) because PB 0.22 has **no API** for it. If you
  ever bootstrap by hand, use the CLI — `/api/collections/_superusers/records`
  returns 404 on 0.22.x.
- **Secrets**: `setup.sh` needs `age` installed *and* `age-key.txt` beside
  `secrets.env.age`. Missing either aborts immediately (by design).
- **SSH**: UFW allows 22/tcp plainly; brute-force protection is fail2ban
  (see below). **Do not** re-add `ufw limit 22/tcp` — 6 conns/30s breaks
  scripted deployment — and do not hand-inject limiter chains into
  `/etc/ufw/before.rules` (this locks SSH out entirely and needs console
  recovery). See FIXES.md 2026-09-19.
- **DNS/TLS**: confirm `dig +short networkingguides.duckdns.org` returns this
  host's IP before expecting HTTPS.

### Verify SSH Protection (fail2ban)

```bash
ssh $VPS "fail2ban-client status sshd"
# Expect: log lines and a non-empty 'Journal matches'.
# Ban policy lives in /etc/fail2ban/jail.d/locus-sshd.local
# (6 failures / 10m -> 1h ban, banaction=ufw).

# Unban an IP you locked out of by testing:
ssh $VPS "fail2ban-client set sshd unbanip 203.0.113.10"
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
ssh $VPS "systemctl restart tc-eco-cap tc-stealth-cap tc-strike-cap"
```

---

## Client Update System

Fully wired and verified end-to-end (2026-09-19). Releasing an update is:
tag → wait for CI → run one script.

### How it works

1. **CI** (`.github/workflows/build.yml`) builds on a `v*` tag and attaches to a
   GitHub Release:
   - `locus-<OS>-<arch>.zip` + `checksums.sha256` — for humans downloading the
     installer from a website.
   - **raw** `locus-linux-amd64`, `locus-windows-amd64.exe`,
     `locus-darwin-amd64`, `locus-darwin-arm64` — for the auto-updater.
   - `manifest.json` — version, per-platform filename + SHA256. The build
     **fails** if any platform artifact is missing.
2. **`v5/server/scripts/publish-release.sh`** copies the raw binaries to the VPS
   (`/var/www/updates/<version>/`), verifies each is downloadable over HTTPS
   with a matching hash, then points `update_config` at them.
3. **`heartbeat.pb.js`** reads `update_config` and, only when
   `hash(fingerprint) % 100 < rollout_percent`, adds `update_available`, the
   per-platform `update_<platform>` URLs and `update_sha256_<platform>` hashes.
4. **`internal/updater`** downloads, verifies SHA256, writes `.update-pending`,
   swaps the binary, forks, and auto-reverts if the new build fails to confirm.
   It refuses anything that is not **strictly newer** than the running version.

Serving is done by Caddy: `handle_path /updates/*` → `file_server` on
`/var/www/updates`, listings disabled. Update URLs are therefore
`https://<domain>/updates/<version>/<file>` — clients never depend on GitHub
being reachable from inside a school network.

### Publishing a release

```bash
# 1. Tag — CI builds, creates the GitHub Release and manifest.json
git tag v1.1.0 && git push origin v1.1.0

# 2. Download the raw artifacts + manifest from the release into ./release-artifacts
#    (the files named locus-linux-amd64, locus-windows-amd64.exe,
#     locus-darwin-amd64, locus-darwin-arm64, manifest.json)

# 3. Check it before you ship it
RELEASE_DIR=./release-artifacts DRY_RUN=1 \
  v5/server/scripts/publish-release.sh 1.1.0

# 4. Publish, starting at a small rollout
RELEASE_DIR=./release-artifacts \
PB_ADMIN_EMAIL=admin@networkingguides.duckdns.org PB_ADMIN_PASS=... \
  v5/server/scripts/publish-release.sh 1.1.0      # ROLLOUT_PERCENT defaults to 5
```

Useful env: `ROLLOUT_PERCENT`, `VPS`, `PB_API`, `PB_TOKEN` (skips login),
`DRY_RUN=1`.

**The script refuses to publish** when an artifact is under 1MB (truncated
download / Git LFS pointer), or when a file's hash disagrees with
`manifest.json` — so a mismatched or partial release cannot reach clients.

### Rollout progression

```text
Day 1:  rollout_percent = 5    (internal testers)
Day 3:  rollout_percent = 25   (early adopters)
Day 7:  rollout_percent = 100  (everyone)
Day 8:  active = false         (stop advertising; keeps the version recorded)
```

Update `update_config` in the admin UI or with:
`sqlite3 /opt/pocketbase/pb_data/data.db "update update_config set rollout_percent=25;"`

The gate is deterministic per device: `hash(fingerprint) % 100`, so a client
cannot flip in and out of the rollout between heartbeats.

### Verify it is actually working

```bash
# A fingerprint inside the rollout gets update fields; one outside does not.
ssh $VPS 'curl -s -X POST http://127.0.0.1:8090/api/heartbeat \
  -H "Content-Type: application/json" \
  -d "{\"code\":\"<valid-code>\",\"fingerprint\":\"<16+ chars>\"}"' | python3 -m json.tool

# Confirm a published artifact is served with the expected hash
curl -s https://$DOMAIN/updates/1.1.0/locus-linux-amd64 | sha256sum
```

### Rollback

```bash
# Stop OFFERING the update (clients already updated stay updated):
sqlite3 /opt/pocketbase/pb_data/data.db "update update_config set rollout_percent=0;"
```

**There is no server-driven downgrade.** The in-client `.update-pending`
sentinel protects the machine that installed a broken build, but a bad build
that reached 100% can only be recovered by publishing a higher version. Treat
the first hours at a low `rollout_percent` as the safety net.

### Notes & limitations

- **sing-box is not updated** by this pipeline — it is bundled in the installer.
  Updating it means shipping a new installer and re-publishing.
- **macOS builds are unsigned**, so Gatekeeper blocks first launch
  (right-click → Open). Tracked as gap #3, out of scope.
- `update_config.version` must match the git tag (CI derives the in-binary
  version from the tag; `publish-release.sh` sets the column from its argument).
- `/update.json` is still a static placeholder written by `05-caddy.sh`. The
  updater reads `update_config`, **not** this file — it is informational only.
- `update_sha256` (the legacy single field) is populated with the **Linux**
  hash so any client predating the per-platform fields still verifies
  something sane.

---

## #7 Gaming UDP (Strike) — enable & verify

> The product is Locus; deployed server artifacts keep the legacy `myvpn` names
> until redeploy. This runbook enables the **sing-box UDP-over-TCP (UoT)**
> endpoint so Strike's raw-UDP-blocked school networks can carry game/voice UDP
> inside an allowed TCP flow.

**There is no sandbox — the live VPS is production.** Sequence below is
reversible and stepwise, so validate each gate before the next.

### 0. Baseline first (do this BEFORE enabling)

```bash
# From a device on the same school/restricted network, WITHOUT locus connected,
# see if raw UDP to the VPS is actually blocked (this is the whole point):
timeout 3 bash -c 'echo -n > /dev/udp/<DOMAIN>/8445' ; echo "exit=$?"
# Expected: blocked/fails on restricted nets (exit nonzero or hang).
```

Also note the VPS raw-UDP egress forwards fine over the open internet; the block
is the *ingress school LAN*, so raw UDP must be tunneled.

### 1. Install + start the UoT endpoint (idempotent)

```bash
# On the VPS, from the repo copy:
UOT_PORT="${UOT_PORT:-8446}" bash v5/server/scripts/enable-uot.sh
systemctl is-active sing-box-uot   # -> active
```

### 2. Advertise UoT to Strike clients

```bash
# On the VPS, from the repo copy (reads passwords, patches tier_configs):
cd v5/server && ENABLE_UOT=1 UOT_PORT=8446 python3 scripts/seed-live.py
# Tactically: PocketBase admin -> tier_configs -> Strike ->
#   config JSON add "uot_port": 8446    and    udp_relay=true
```

### 3. Compare (client probe builds AFTER the endpoint is up)

Point a Locus client at Strike on the restricted net, connect, then:

- **TCP**: `curl -sf https://<DOMAIN>/api/health` still works (unchanged TCP path).
- **UDP through tunnel**: run a UDP query that must traverse the UoT outbound,
  e.g. `ping` a game/voice flow or a DNS-over-UoT check to a public resolver
  (`8.8.8.8:53` / `1.1.1.1:53`) while connected. Expect an answer.
- A 5-minute real game/voice check is the acceptance gate (this was never run —
  see GAMING-UDP.md). Record latency vs. the Section 0 baseline.

### 4. Healthy? Grow rollout; else rollback

Rollback (seconds): `systemctl disable --now sing-box-uot && rm -f /etc/sing-box/config.json`,
then re-run the seed WITHOUT `ENABLE_UOT` (or flip Strike `udp_relay=false` /
drop `uot_port`) — TCP tiers (8443/44/45) are never touched by the enable script.

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
# Current b2 CLI needs b2:// URIs (plain bucket names fail)
b2 ls --recursive "b2://my-vpn-backup-bucket/backups/" | grep '\.db\.gz$'
```

### Restore from Specific Backup

> **Prefer the one-command restore** (`v5/server/restore.sh`) — it provisions a
> blank VPS, downloads the latest backup, verifies the SHA256, restores the DB,
> aligns the admin password with the secrets file, re-enables the backup timer
> and smoke-tests everything. The manual steps below are the equivalent for a
> running VPS.

```bash
# Download (current CLI syntax)
b2 file download "b2://my-vpn-backup-bucket/backups/data-20260724-143000.db.gz" /tmp/restore.db.gz
b2 file download "b2://my-vpn-backup-bucket/backups/data-20260724-143000.db.gz.sha256" /tmp/restore.db.gz.sha256

# Verify
cat /tmp/restore.db.gz.sha256
sha256sum /tmp/restore.db.gz
gzip -d /tmp/restore.db.gz
head -c 16 /tmp/restore.db | xxd  # Should show "SQLite format 3\000"

# Restore
ssh $VPS "systemctl stop pocketbase"
scp /tmp/restore.db root@networkingguides.duckdns.org:/opt/pocketbase/pb_data/data.db
ssh $VPS "
  rm -f /opt/pocketbase/pb_data/data.db-wal /opt/pocketbase/pb_data/data.db-shm  # stale WAL must go
  chown pocketbase:pocketbase /opt/pocketbase/pb_data/data.db
  systemctl start pocketbase
  # IMPORTANT: pocketbase-backup.timer has Requires=pocketbase.service — stopping
  # PocketBase above also stopped the timer (Requires propagates stops, not
  # starts). Restart it explicitly:
  systemctl restart pocketbase-backup.timer
"

# If the restored admin password doesn't match the secrets file (old DB era),
# align it so documented credentials work:
ssh $VPS "cd /opt/pocketbase && ./pocketbase admin update admin@networkingguides.duckdns.org '<PB_ADMIN_PASS>'"

# Verify
curl -s "$PB_API/api/health"
# Hooks loaded? Expect 400 {"message":"Missing code"} — a generic 400 means hook load error
curl -s -X POST "$PB_API/api/activate" -H 'Content-Type: application/json' -d '{}'
curl -s -X POST "$PB_API/api/activate" -H 'Content-Type: application/json' -d '{}'
# Backup timer back?
ssh $VPS "systemctl is-active pocketbase-backup.timer"
```

---

## Deploying the Code-Lookup Hook (`/api/code-lookup`)

The client's activation screen calls a READ-ONLY endpoint to tell a student
whether their code is actually recognised *before* they commit to activating.
Clients that ship before this hook is deployed degrade gracefully (they fall
back to "we'll confirm when you activate"), so the client can ship first.

The endpoint **never binds a device** and reuses the existing `activation_attempts`
table for rate limiting (10 lookups / 10 min per fingerprint), so it cannot be
used as an unlimited code-enumeration oracle.

```bash
VPS=root@networkingguides.duckdns.org
PB_API=https://networkingguides.duckdns.org   # adjust to the hub base URL

# 1. Back up the hooks directory (reversible).
ssh $VPS "cp -a /opt/pocketbase/pb_hooks /root/pb_hooks.bak.$(date +%s)"

# 2. Deploy just the new hook file (does not touch the existing hooks).
scp v5/server/pb_hooks/code_lookup.pb.js $VPS:/opt/pocketbase/pb_hooks/
ssh $VPS "chown pocketbase:pocketbase /opt/pocketbase/pb_hooks/code_lookup.pb.js"

# 3. A NEW hook file only registers on restart (edits to existing files hot-reload).
ssh $VPS "systemctl restart pocketbase"

# 4. Verify. A malformed code returns 200 with status "not_found" (format was
#    rejected locally by the hook) — a generic 400/500 means a hook load error.
curl -s -X POST "$PB_API/api/code-lookup" -H 'Content-Type: application/json' \
  -d '{"code":"RQ-AAAA-AAAA-AAAA-A","fingerprint":"0123456789abcdef0123456789abcdef"}'
# Expect: {"status":"not_found","message":"Invalid code format"}

# 5. Confirm a real code is reported correctly (substitute a real seeded code).
curl -s -X POST "$PB_API/api/code-lookup" -H 'Content-Type: application/json' \
  -d '{"code":"<REAL-CODE>","fingerprint":"0123456789abcdef0123456789abcdef"}'
# Expect one of: ok | unbound | bound_this_device | bound_other | suspended | expired

# Rollback (seconds): remove the file and restart.
ssh $VPS "rm -f /opt/pocketbase/pb_hooks/code_lookup.pb.js && systemctl restart pocketbase"
```

**Rollback safety:** the hook only *reads* `codes`; the sole writes are to
`activation_attempts`. Removing the file fully reverts the behaviour, and
pre-existing clients are unaffected either way.

---

## Updating the Server

### Kernel Update (re-apply tc caps)

The tc cap services are oneshot — after a kernel/reboot change, re-apply them:

```bash
ssh $VPS "systemctl restart tc-eco-cap tc-stealth-cap tc-strike-cap"
ssh $VPS "tc -s class show dev \$(ip route show default | awk '\$5{print\$5;exit}')"
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
