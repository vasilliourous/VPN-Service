# Locus PocketBase Setup Guide

> After the server modules run, PocketBase is installed and running — but it
> has no admin account, no collections, and no data. This guide walks through
> the remaining manual steps.
>
> **If you used the automated setup** (06-pocketbase.sh creates admin + collections
> automatically on first run), some of this is already done. Use this guide to
> verify or customise.
>
> **Read §4 (Install the JS Hooks) before writing any hook.** The PocketBase
> traps documented there — `findRecordsByFilter` silently returning nothing,
> helpers invisible inside `routerAdd`, `findFirstRecordByData` throwing — are
> the errors that cost the most time on this codebase.

---

## 1. Create Admin Account

**Normally this is already done for you.** `06-pocketbase.sh` + `seed-pb.py`
create the admin, the collections, the schema and the tier configs on first run,
using `PB_ADMIN_EMAIL` / `PB_ADMIN_PASS` from `secrets.env.age`, and record the
credentials in `/root/.pb_admin_creds` (mode 600). **A bootstrap failure is
fatal** — it used to be a warning, which let a hub that served 500s look
successfully deployed.

The manual path, if you need it:

```bash
# FIRST admin on PocketBase 0.22 can ONLY be created via the CLI:
pocketbase admin create <email> <pass> --dir /opt/pocketbase/pb_data
```

> **PocketBase 0.22 API shape.** This build uses the legacy `_admins` table and
> `/api/admins/auth-with-password`; the admin UI is at `/_/`. The newer
> `/api/collections/_superusers/*` path returns **404** — an earlier
> `seed-pb.py` used it and consequently seeded *nothing* while printing
> "✓ complete".
>
> **`admin create` never updates.** If `/root/.pb_admin_creds` disagrees with
> the live password, re-running cannot converge. `seed-pb.py` therefore resolves
> the password **env-first** and forces an `admin update` if the env value fails
> to log in.
>
> Prefer `/root/.pb_admin_creds`'s **`PB_TOKEN`** (a real JWT) for CLI/API work.
> The `ADMIN_API_TOKEN` is only accepted by the custom hooks — PocketBase 0.22
> rejects it for record access with 401.

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
| `unbound_at` | datetime | ❌ | ❌ | Last unbind (added 2026-09) |
| `unbind_reason` | text (plain) | ❌ | ❌ | Why the binding was released |
| `label` | text (plain) | ❌ | ❌ | Operator-facing label |
| `notes` | text (plain) | ❌ | ❌ | Free-form operator notes |

> `unbound_at` / `unbind_reason` were *written by `admin_unbind.pb.js` for
> months* before the columns existed — PocketBase silently discards unknown
> fields, so the audit trail looked implemented and recorded nothing. Any
> hand-built collection must include them.

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

> ⚠️ **The rate limit lives here.** Both `/api/activate` (5 per 10 min) and
> `/api/code-lookup` (10 per 10 min) count rows in this table via
> `findRecordsByExpr`. That counting call used `findRecordsByFilter` until
> 2026-09-19 — which **silently returns zero rows on PocketBase 0.22.21**, so
> neither limit was ever enforced and both endpoints were unbounded
> code-enumeration oracles. If you rewrite these hooks, count with
> `findRecordsByExpr` and verify the limit actually trips.

### Collection: `code_events`

Audit trail for every admin/console action on a code (generated, suspended,
reactivated, unbound, expired, edited). Written best-effort — a failure here
never fails the action itself.

| Field | Type | Required | Unique | Notes |
|-------|------|:--------:|:------:|-------|
| `code` | text (plain) | ✅ | ❌ | Code the event relates to. Releases are logged as `(release <ver>)` |
| `event` | text (plain) | ✅ | ❌ | e.g. `generated`, `suspended`, `unbound`, `release-set` |
| `detail` | text (plain) | ❌ | ❌ | Human-readable detail |
| `actor` | text (plain) | ❌ | ❌ | Who did it (`console`, `admin_unbind`, …) |
| `fingerprint` | text (plain) | ❌ | ❌ | Device fingerprint when relevant |

> **Never bulk-delete this collection.** It is the audit trail for real
> customer codes.

### Collection: `update_config`

| Field | Type | Required | Unique | Notes |
|-------|------|:--------:|:------:|-------|
| `version` | text (plain) | ✅ | ❌ | e.g. `1.1.0` |
| `rollout_percent` | number | ❌ | ❌ | 0–100, default 0 |
| `active` | bool | ❌ | ❌ | Default: true |
| `update_url` | text | ❌ | ❌ | Generic download URL |
| `update_sha256` | text | ❌ | ❌ | Legacy single hash (populated with the **Linux** hash) |
| `download_linux` | text | ❌ | ❌ | Per-platform URL |
| `download_windows` | text | ❌ | ❌ | Windows-specific URL |
| `download_macos_intel` | text | ❌ | ❌ | macOS Intel URL |
| `download_macos_arm` | text | ❌ | ❌ | macOS Apple Silicon URL |
| `sha256_linux` | text | ❌ | ❌ | Per-platform hash |
| `sha256_windows` | text | ❌ | ❌ | Per-platform hash |
| `sha256_macos_intel` | text | ❌ | ❌ | Per-platform hash |
| `sha256_macos_arm` | text | ❌ | ❌ | Per-platform hash |

> **Why per-platform hashes are required.** A single `update_sha256` cannot
> describe four different binaries, and the updater **refuses to apply an update
> whose hash is empty** for the platform it is running on. The legacy
> `update_sha256` field is still populated (with the Linux hash) so clients
> predating the per-platform fields verify *something*.

> **Exactly one row should be active.** An earlier `seed-pb.py` inserted a new
> `update_config` row on every deploy, leaving several active rows; the heartbeat
> reads a single row via `findFirstRecordByFilter("update_config", "active = true")`.

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

## 4. Install the JS Hooks

The API endpoints are PocketBase JS hooks, not collections. Copy them to
`/opt/pocketbase/pb_hooks/`:

```bash
scp v5/server/pb_hooks/*.pb.js root@your-vps:/opt/pocketbase/pb_hooks/
systemctl restart pocketbase
```

| Hook | Endpoint(s) | Purpose |
|------|-------------|---------|
| `activation.pb.js` | `POST /api/activate` | Validates + binds a code to a device |
| `heartbeat.pb.js` | `POST /api/heartbeat` | Suspension check, server-config refresh, staged-rollout update signal |
| `code_lookup.pb.js` | `POST /api/code-lookup` | Read-only pre-check ("is this code recognised?") — never binds |
| `admin_unbind.pb.js` | `POST /api/admin/unbind-code` | Release a device binding |
| `admin_console.pb.js` | `POST /api/admin/console` | Single route + `action` discriminator backing the web console |
| `hiddify.pb.js` | `POST /api/hiddify/*` | Legacy/hiddify compatibility export |

### Two rules that will bite you

1. **A NEW hook file requires a restart.** Editing an existing `*.pb.js`
   hot-reloads, but *adding* one does not — PocketBase only registers routes at
   load time. A newly deployed endpoint that 404s is almost always this.
2. **Never use `$app.dao().findRecordsByFilter(...)`.** On PocketBase 0.22.21 it
   returns an **empty array for every collection and filter, with no error** —
   `try/catch` never fires, so callers just see "no records". Use
   `findFirstRecordByFilter` (single row, works with bools),
   `findRecordsByExpr` (lists/counts), or `findFirstRecordByData`. Two more
   gotchas in the same family:
   - `findFirstRecordByData` **throws** `"sql: no rows in result set"` on a miss
     rather than returning null — without a `try/catch` an ordinary unknown code
     becomes HTTP 500 instead of 404.
   - Helper functions declared at **file scope are invisible** inside a
     `routerAdd` callback ("helperFn is not defined"). Declare helpers *inside*
     the handler.

If activation returns a generic 400/500 after a fresh deploy, check
`journalctl -u pocketbase` for hook load errors first.

---

## 5. Configure ADMIN_API_TOKEN

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

## 6. Seed Update Config (Staged Rollouts)

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

## 7. Generate Activation Codes

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

## 8. Verify the Setup

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
