# MyVPN PocketBase Setup Guide

> After the server modules run, PocketBase is installed and running — but it
> has no admin account, no collections, and no data. This guide walks through
> the remaining manual steps.
>
> **If you used the automated setup** (06-pocketbase.sh creates admin + collections
> automatically on first run), some of this is already done. Use this guide to
> verify or customise.

---

## 1. Create Admin Account

1. Open `https://networkingguides.duckdns.org/_/` in a browser
2. Create your admin account (email + password)
3. Save these credentials somewhere secure

**If automated setup was used:** An admin was created with credentials saved in
`/root/.pb_admin_creds` on the VPS. Log in with those, then change the password
via Settings → Admins → Edit.

---

## 2. Create Collections

In PocketBase admin UI, go to **Settings → Collections** and create these:

### Collection: `codes`

| Field | Type | Required | Unique | Notes |
|-------|------|:--------:|:------:|-------|
| `code` | text (plain) | ✅ | ✅ | Full code: `RQ-ABCD-EFGH-JKMN-T` |
| `tier` | select | ✅ | ❌ | Options: `eco`, `stealth`, `strike` |
| `used` | bool | ❌ | ❌ | Default: false |
| `suspended` | bool | ❌ | ❌ | Default: false |
| `bound_fingerprint` | text (plain) | ❌ | ❌ | SHA256 hash, set on activation |
| `expires_at` | datetime | ❌ | ❌ | Code expiry date |
| `activated_at` | datetime | ❌ | ❌ | First activation timestamp |
| `middleman` | text (plain) | ❌ | ❌ | Distributor identifier |

**API rules:**
```
List:   @request.auth.admin = true        (admins only)
View:   @request.auth.admin = true
Create: @request.auth.admin = true
Update: @request.auth.admin = true
Delete: @request.auth.admin = true
```

### Collection: `tier_configs`

| Field | Type | Required | Unique | Notes |
|-------|------|:--------:|:------:|-------|
| `tier` | select | ✅ | ✅ | Options: `eco`, `stealth`, `strike` |
| `config` | json | ✅ | ❌ | Shadowsocks server config object |
| `active` | bool | ❌ | ❌ | Default: true |
| `udp_relay` | bool | ❌ | ❌ | Default: false |

**API rules:**
```
List:   @request.auth.admin = true
View:   @request.auth.admin = true
Create: @request.auth.admin = true
Update: @request.auth.admin = true
Delete: @request.auth.admin = true
```

### Collection: `activation_attempts`

| Field | Type | Required | Unique | Notes |
|-------|------|:--------:|:------:|-------|
| `ip` | text (plain) | ❌ | ❌ | Client IP address |
| `rate_key` | text (plain) | ❌ | ❌ | Fingerprint hash or IP |
| `fingerprint` | text (plain) | ❌ | ❌ | Partial fingerprint (first 16 chars + ****) |
| `code_attempted` | text (plain) | ❌ | ❌ | Partial code (first 4 chars + ****) |

Auto-created fields: `created`, `updated`

**API rules:**
```
List:   @request.auth.admin = true
View:   @request.auth.admin = true
Create: @request.auth.admin = true  (or false — the JS hook creates records internally)
Update: @request.auth.admin = true
Delete: @request.auth.admin = true
```

**Important:** The `activation.pb.js` hook creates `activation_attempts` records
using PocketBase's internal DAO, which bypasses API rules. So even with
restrictive API rules, the hook will work.

### Collection: `update_config`

| Field | Type | Required | Unique | Notes |
|-------|------|:--------:|:------:|-------|
| `version` | text (plain) | ✅ | ❌ | e.g. `1.1.0` |
| `rollout_percent` | number | ❌ | ❌ | 0–100, default 0 |
| `active` | bool | ❌ | ❌ | Default: true |
| `update_url` | text | ❌ | ❌ | Generic download URL |
| `update_sha256` | text | ❌ | ❌ | SHA256 of update binary |
| `download_windows` | text | ❌ | ❌ | Windows-specific URL |

**API rules:**
```
List:   @request.auth.admin = true
View:   @request.auth.admin = true
Create: @request.auth.admin = true
Update: @request.auth.admin = true
Delete: @request.auth.admin = true
```

---

## 3. Seed Tier Configs

The tier configs tell the client which server, port, and password to use for
each tier. The passwords were generated during module 02-shadowsocks.sh and
saved to `/root/.tier_passwords` on the VPS.

### Retrieve Passwords

```bash
ssh root@networkingguides.duckdns.org "cat /root/.tier_passwords"
```

This will output:
```bash
ECO_PASS=abc123...
STEALTH_PASS=def456...
STRIKE_PASS=ghi789...
```

### Create Tier Config Records

In PocketBase admin UI → `tier_configs` collection → **Create new** for each tier:

**Eco:**
```
tier:   eco
active: ✅ (checked)
udp_relay: ❌ (unchecked)
config:
{
  "server": "networkingguides.duckdns.org",
  "server_port": 8443,
  "password": "<ECO_PASS from above>",
  "method": "aes-256-gcm"
}
```

**Stealth:**
```
tier:   stealth
active: ✅ (checked)
udp_relay: ❌ (unchecked)
config:
{
  "server": "networkingguides.duckdns.org",
  "server_port": 8444,
  "password": "<STEALTH_PASS from above>",
  "method": "aes-256-gcm"
}
```

**Strike:**
```
tier:   strike
active: ✅ (checked)
udp_relay: ✅ (checked)
config:
{
  "server": "networkingguides.duckdns.org",
  "server_port": 8445,
  "password": "<STRIKE_PASS from above>",
  "method": "aes-256-gcm"
}
```

---

## 4. Configure ADMIN_API_TOKEN

The `ADMIN_API_TOKEN` is used by the admin unbind endpoint. The code
generator needs a different credential — the PocketBase ADMIN JWT
(`grep PB_TOKEN /root/.pb_admin_creds | cut -d= -f2` on the VPS), because
PocketBase 0.22 rejects the app-level token with 401 (see
`scripts/generate_codes.sh --help`).

### On the VPS

```bash
ssh root@networkingguides.duckdns.org

# Add the token to environment
echo 'ADMIN_API_TOKEN=YOUR_ADMIN_TOKEN' >> /etc/environment

# Restart PocketBase to pick it up
systemctl restart pocketbase
```

**Your ADMIN_API_TOKEN (keep this secret):**
```
YOUR_ADMIN_TOKEN
```

---

## 5. Seed Update Config (Staged Rollouts)

Create a record in the `update_config` collection:

```
version:          1.0.0
rollout_percent:  0
active:           ✅ (checked)
update_url:       (leave empty until first update)
update_sha256:    (leave empty)
```

Setting `rollout_percent` to 0 means no clients will receive an update signal.
When you're ready to roll out an update, change this value (5 → 25 → 100).

---

## 6. Generate Activation Codes

```bash
# From your local machine
./scripts/generate_codes.sh \
  https://networkingguides.duckdns.org \
  YOUR_ADMIN_TOKEN \
  eco 50

./scripts/generate_codes.sh \
  https://networkingguides.duckdns.org \
  YOUR_ADMIN_TOKEN \
  stealth 30

./scripts/generate_codes.sh \
  https://networkingguides.duckdns.org \
  YOUR_ADMIN_TOKEN \
  strike 20
```

---

## 7. Verify the Setup

### Health Check
```bash
curl -sf https://networkingguides.duckdns.org/api/health
# Expected: {"message":"API is healthy.","code":200}
```

### Test Activation (dry run with a real code)
```bash
# Get a code from the eco-codes.txt generated above
CODE=$(head -1 eco-codes.txt)

curl -s -X POST https://networkingguides.duckdns.org/api/activate \
  -H "Content-Type: application/json" \
  -d "{\"code\": \"$CODE\", \"fingerprint\": \"test-fingerprint-123\"}"
# Expected: {"code":200, "tier":"eco", ...}
```

---

## Quick Reference

| Item | Value / Location |
|------|-----------------|
| Admin UI | `https://networkingguides.duckdns.org/_/` |
| API base | `https://networkingguides.duckdns.org` |
| All credentials | Encrypted in `v5/server/secrets.env.age` — see [`SECRETS-MANAGEMENT.md`](SECRETS-MANAGEMENT.md) |
| Tier passwords | `/root/.tier_passwords` on VPS (auto-deployed from secrets) |
| Admin API token | `/root/.admin_api_token` on VPS (auto-deployed from secrets) |
| B2 creds file | `/root/.b2-creds` on VPS (auto-deployed from secrets) |
| PB admin creds | `/root/.pb_admin_creds` on VPS (auto-created from secrets if provided) |
