# MyVPN API Reference

> Complete API contracts for the MyVPN server, as implemented by the
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
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
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
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
  "fingerprint": "a1b2c3d4e5f6...abcdef"
}
```

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `code` | string | ✅ | Full activation code |
| `fingerprint` | string | ❌ | Device fingerprint (used for staged rollout hashing) |

**Response `200` (normal):**
```json
{
  "status": "ok",
  "server_time": "2026-07-24T14:30:00.000Z",
  "tier": "eco",
  "server_config": {
    "server": "networkingguides.duckdns.org",
    "server_port": 8443,
    "password": "...",
    "method": "aes-256-gcm"
  },
  "udp_relay": false
}
```

**Response `200` (with update available, within rollout bucket):**
```json
{
  "status": "ok",
  "server_time": "2026-07-24T14:30:00.000Z",
  "tier": "eco",
  "update_available": "1.1.0",
  "update_url": "https://networkingguides.duckdns.org/updates/v1.1.0/myvpn-linux-amd64",
  "update_sha256": "abc123...",
  "update_windows": "https://...myvpn.exe",
  "update_macos_intel": "https://...myvpn-darwin-amd64",
  "update_macos_arm": "https://...myvpn-darwin-arm64"
}
```

Update fields are only present when:
1. `update_config` collection has an active record with `rollout_percent > 0`
2. `hash(fingerprint || code) % 100 < rollout_percent`

**Error responses:**

| Status | Code | Message | Meaning |
|:------:|:----:|---------|---------|
| 400 | 400 | "Missing code" | No code query parameter |
| 403 | 403 | "Account suspended — contact your middleman" | Code is suspended |
| 404 | 404 | "Code not found" | Code doesn't exist |

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
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
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
  "macos_intel": null,
  "macos_arm": null,
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
Activation:    POST /api/activate         ─── JSON body
Heartbeat:     POST /api/heartbeat           ─── JSON body
Admin Unbind:  POST /api/admin/unbind-code ─── JSON body (with admin_token)
Health:        GET  /api/health           ─── Plain GET
Update Config: GET  /update.json          ─── Static file
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
| `/api/heartbeat` | 1 | 10 seconds | Caddy |
| `/api/*` (general) | 100 | 10 seconds | Caddy default zone |
| `/api/admin/unbind-code` | None | — | Not rate limited |

Rate limiting keys on `{remote_host}` (client IP) in Caddy. The activation hook
also implements fingerprint-keyed rate limiting in the JS hook as a second layer.
