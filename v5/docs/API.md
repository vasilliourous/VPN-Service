# Locus API Reference

> Complete API contracts for the Locus server, as implemented by the
> PocketBase JS hooks and Caddy reverse proxy.

---

## Base URL

`https://networkingguides.duckdns.org`

All endpoints are served through Caddy reverse proxy to PocketBase on `127.0.0.1:8090`.

---

## 1. Activation

### `POST /api/activate`

Activates a device with an activation code and binds it to the hardware fingerprint.

**Rate limited:** 5 attempts per 10 minutes per IP (Caddy + JS hook double-gated).

**Request:**
```json
{
  "code": "RQ-ABCD-EFGH-JKMN-T",
  "fingerprint": "a1b2c3d4e5f6...abcdef"
}
```

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `code` | string | ✅ | Activation code with hyphens |
| `fingerprint` | string | ✅ | SHA256 of MAC + disk serial + motherboard UUID |

**Response `200` (success):**
```json
{
  "code": 200,
  "message": "Activation successful",
  "tier": "eco",
  "device_fingerprint": "a1b2c3d4e5f6...abcdef",
  "server_config": {
    "server": "networkingguides.duckdns.org",
    "server_port": 8443,
    "password": "...",
    "method": "aes-256-gcm"
  },
  "udp_relay": false
}
```

**Response `200` (re-activation — same device):**
```json
{
  "code": 200,
  "message": "Device already activated",
  "tier": "eco",
  "device_fingerprint": "a1b2c3d4e5f6...abcdef"
}
```

**Error responses:**

| Status | Code | Message | Meaning |
|:------:|:----:|---------|---------|
| 400 | 400 | "Missing code" | No code provided |
| 400 | 400 | "Missing device fingerprint" | No fingerprint provided |
| 400 | 400 | "Invalid code format — checksum failed" | Luhn-mod-N validation failed |
| 403 | 403 | "Code is already bound to another device" | Fingerprint mismatch |
| 403 | 403 | "Code is suspended" | Admin suspended the code |
| 404 | 404 | "Code not found" | Code doesn't exist in DB |
| 410 | 410 | "Code has expired" | Past expires_at date |
| 429 | 429 | "Too many activation attempts..." | Rate limit hit |

---

## 2. Heartbeat

### `POST /api/heartbeat`

Periodic health check. Returns suspension status, staged rollout updates,
and refreshed tier config.

**Rate limited:** 1 request per 10 seconds per IP.

**Request:**
```json
{
  "code": "RQ-ABCD-EFGH-JKMN-T",
  "fingerprint": "a1b2c3d4e5f6...abcdef"
}
```

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `code` | string | ✅ | Full activation code |
| `fingerprint` | string | ❌ | Device fingerprint (used for staged rollout hashing) |

**Response `200` (strike tier — carries a UoT endpoint):**
```json
{
  "status": "ok",
  "server_time": "2026-09-19T10:09:42.945Z",
  "tier": "strike",
  "server_config": {
    "server": "networkingguides.duckdns.org",
    "server_port": 8445,
    "password": "...",
    "method": "aes-256-gcm",
    "uot_port": 8446
  },
  "udp_relay": true
}
```

> **`uot_port` is a frozen wire key.** The tier config JSON is passed through
> verbatim, so the UoT endpoint arrives under its stored name. Do **not** rename
> it to `server_port_uot`: clients already in the field read `uot_port`, and an
> unrecognised key is a silent no-op rather than an error — which is exactly how
> UDP-over-TCP was dead fleet-wide until 2026-09-19 (FIXES.md 29). `udp_relay`
> and `uot_port` are **both** required before the client sends UDP over TCP.

**Response `200` (with update available, within rollout bucket):**
```json
{
  "status": "ok",
  "server_time": "2026-09-19T10:09:42.945Z",
  "tier": "eco",
  "update_available": "2.1.0",
  "update_url": "https://networkingguides.duckdns.org/updates/2.1.0/locus-linux-amd64",
  "update_sha256": "abc123...",
  "update_linux": "https://.../updates/2.1.0/locus-linux-amd64",
  "update_windows": "https://.../updates/2.1.0/locus-windows-amd64.exe",
  "update_macos_intel": "https://.../updates/2.1.0/locus-darwin-amd64",
  "update_macos_arm": "https://.../updates/2.1.0/locus-darwin-arm64",
  "update_sha256_linux": "abc123...",
  "update_sha256_windows": "def456...",
  "update_sha256_macos_intel": "ghi789...",
  "update_sha256_macos_arm": "jkl012..."
}
```

Update fields are only present when:
1. `update_config` collection has an active record with `rollout_percent > 0`
2. `hash(fingerprint || code) % 100 < rollout_percent`

`update_url` / `update_sha256` are the **legacy single-platform** fields and
describe the linux binary only. They exist so clients predating per-platform
support still update. A client that finds its own platform's field missing falls
back to them — which means a record missing `download_<platform>` silently
offers Windows and macOS a Linux executable. The `update_<platform>` fields come
from the record's `download_<platform>` keys; see `internal/updatecfg` for the
full three-layer contract (FIXES.md 31).

**Error responses:**

| Status | Code | Message | Meaning |
|:------:|:----:|---------|---------|
| 400 | 400 | "Missing code" | No `code` in the request body |
| 403 | 403 | "Account suspended — contact your middleman" | Code is suspended |
| 404 | 404 | "Code not found" | Code doesn't exist or is unparseable |

> **Expiry is enforced on every heartbeat, not just at activation.** A code that
> passes its `expires_at` stops working here even on a device that is already
> bound, which is what makes the 7-day grace period bounded.

---

## 3. Admin Unbind

### `POST /api/admin/unbind-code`

Clears the device fingerprint from a code, allowing re-activation on a new device.
Requires valid admin API token.

**Rate limit:** None (admin endpoint).

**Request:**
```json
{
  "admin_token": "your-secure-token",
  "code": "RQ-ABCD-EFGH-JKMN-T",
  "reason": "Device lost/stolen"
}
```

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `admin_token` | string | ✅ | Must match `ADMIN_API_TOKEN` env variable on VPS |
| `code` | string | ✅ | Code to unbind |
| `reason` | string | ❌ | Optional reason for audit log |

**Response `200`:**
```json
{
  "code": 200,
  "message": "Code unbound successfully. Can now be activated on a new device.",
  "tier": "eco",
  "middleman": ""
}
```

**Error responses:**

| Status | Code | Message |
|:------:|:----:|---------|
| 400 | 400 | "Missing code" |
| 400 | 400 | "Code is not bound to any device" |
| 403 | 403 | "Invalid admin token" |
| 404 | 404 | "Code not found" |

---

## 4. Health

### `GET /api/health`

Standard PocketBase health check.

**Rate limit:** 100 requests per 10 seconds.

**Response `200`:**
```json
{
  "message": "API is healthy.",
  "code": 200
}
```

---

## 5. Update Manifest

### `GET /update.json`

Static file served by Caddy. Contains the current update manifest for staged rollouts.

**Response `200`:**
```json
{
  "version": "1.0.0",
  "rollout_percent": 0,
  "windows": null,
  "linux_amd64": null
}
```

---

## 6. PocketBase Admin UI

### `GET /_/`

PocketBase admin interface at `https://networkingguides.duckdns.org/_/`.

---

## 7. Client→Server Protocol Summary

```
Activation:    POST /api/activate            ─── JSON body
Code lookup:   POST /api/code-lookup         ─── JSON body (read-only pre-check)
Heartbeat:     POST /api/heartbeat           ─── JSON body
Admin Unbind:  POST /api/admin/unbind-code   ─── JSON body (with admin_token)
Admin API:     POST /api/admin/*             ─── JSON body (admin_token; Web UI)
Release upload:POST /api/admin/upload        ─── Multipart; separate uploader
Health:        GET  /api/health              ─── Plain GET
Update Config: GET  /update.json             ─── Static file
Update assets: GET  /updates/<version>/<file>─── Static file (no directory listing)
```

---

## 8. Error Response Format

All error responses follow this structure:

```json
{
  "code": 400,
  "message": "Human-readable error description"
}
```

HTTP status code matches the `code` field in the JSON body.

---

## 9. Rate Limiting

| Endpoint | Limit | Window | Mechanism |
|----------|:-----:|:------:|-----------|
| `/api/activate` | 5 | 10 minutes | Caddy + JS hook |
| `/api/code-lookup` | 10 | 10 minutes | JS hook (own bucket) |
| `/api/heartbeat` | 1 | 10 seconds | Caddy |
| `/api/*` (general) | 100 | 10 seconds | Caddy default zone |
| `/api/admin/unbind-code` | None | — | Admin token required instead |
| `/api/admin/upload` | None | — | Admin token; routed to the uploader |
| `/updates/*` | None | — | Large one-shot downloads |

Caddy keys on `{remote_host}` (client IP). The two PocketBase hooks add a
second, fingerprint-keyed layer in the `activation_attempts` table.

**The two hook buckets are deliberately separate**, via a key prefix
(`activate_…` vs `lookup_…`). They previously shared one, and that was actively
harmful: `/api/code-lookup` exists so a student can confirm a code is recognised
*before* committing to an activation, but mistyping on the activation screen
consumed the lookup allowance — locking the student out of the affordance that
would have explained the mistake. Confirmed live 2026-09-19. See FIXES.md 33.

A successful activation clears that device's `activate_…` rows, so a student who
finds their code is not counted against themselves afterwards. Lookups are never
cleared, since they are the enumeration surface.
