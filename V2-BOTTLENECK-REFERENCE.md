# V2 — Quick Reference

> **Three things matter:** (1) hardcoded speed caps, (2) background updater, (3) codes unlocking server configs.

---

## Part 1: The Full Pipeline

```
Paper code → Enter in app → Hub validates Luhn + checks DB →
Binds device fingerprint → Returns token + plan + Hysteria 2 config →
Client saves configs, starts engine with --speed <cap> --tun
```

**Activation response carries server configs:**
```json
{
    "token": "jwt...",
    "plan": "gaming_mid",
    "configs": [{
        "id": "hysteria2",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "pool-password",
            "tls": { "sni": "www.cloudflare.com" },
            "obfs": "salamander",
            "obfs-password": "pool-key"
        }
    }]
}
```

**Heartbeat keeps them fresh:**
```json
{
    "status": "active",
    "token": "new-jwt...",
    "configs": [...],         // updated server configs if changed
    "update": { ... }         // binary update if available
}
```

---

## Part 2: Artificial Bottlenecking

```go
func Get(tier string) int {
    switch tier {
    case "warp_lite":      return   8_000_000   //  1 MB/s
    case "stealth_browse": return  48_000_000   //  6 MB/s
    case "gaming_mid":     return  50_000_000   //  6.25 MB/s
    case "gaming_max":     return 100_000_000   // 12.5 MB/s
    default:               return           0   // uncapped
    }
}
```

One file, 40 lines. Applied as `hysteria2 client --speed 50000000 --tun -c config.json`.

---

## Part 3: Background Updater

```
Heartbeat has "update" → download to engines/myvpn.update in background
Next launch → verify SHA256 → backup exe as myvpn.prev → swap → restart
First heartbeat → confirm → delete .prev
Crash before first heartbeat → auto-restore .prev
```

---

## Part 4: Server-Side Collections (PocketBase)

**`codes`** — Each activation code:
| Field | Type | Notes |
|-------|------|-------|
| `code` | text (unique) | `MYVPN-ABCD-1234-EFGH-5` |
| `tier` | text | warp_lite / stealth_browse / gaming_mid / gaming_max |
| `used` | bool | false → true on activation |
| `fingerprint` | text (nullable) | SHA256 of device HW, set once |
| `suspended` | bool | admin toggle |
| `middleman` | text (optional) | tracking who sold it |

**`tier_configs`** — Hysteria 2 server configs per tier:
| Field | Type | Notes |
|-------|------|-------|
| `tier` | text (unique) | matches codes.tier |
| `configs` | json | array of protocol configs |
| `active` | bool | if false, clients get empty configs |

---

## Part 5: What to Delete From v1

```bash
rm -rf internal/probe/          # Replaced by caps.Get()
rm -rf internal/monitor/        # Replaced by caps.Get()
rm -rf internal/helper/         # Engine --tun flag
rm -f  internal/manager/disguise_windows.go
rm -f  internal/manager/process_windows.go
rm -f  internal/manager/process_darwin.go
```

---

## Part 6: What to Add

| File | Lines | Purpose |
|------|-------|---------|
| `internal/caps/caps.go` | 40 | Hardcoded tier speed caps |
| `internal/updater/updater.go` | 150 | Background download + auto-revert |
| `server/pb_hooks/activation.pb.js` | 80 | Server-side activation handler |
| `server/pb_hooks/heartbeat.pb.js` | 70 | Server-side heartbeat handler |
| `server/Caddyfile` | 40 | Reverse proxy + rate limiting |
| `scripts/generate_codes.sh` | 30 | Batch code generation |

---

## Part 7: What to Simplify

| File | Change |
|------|--------|
| `manager/process.go` | Remove sandbox, disguise, bandwidth param. Add `caps.Get()` lookup |
| `heartbeat/heartbeat.go` | Remove RemoteCommand dispatch. Add update + config fields |
| `gui/app.go` | Remove probe.Run(), monitor. Speed from caps.Get() |
| `updater/recover.go` | Replace sentinel machine with simple --revert + crash recovery |
| `storage/storage.go` | Add PendingUpdate + AppliedVersion fields |

---

## Part 8: Lines of Code

| Area | v1 Lines | v2 Lines | Saved |
|------|---------|---------|-------|
| Probe + Monitor + Helper + Sandbox | ~650 | 0 | 650 |
| Caps + Updater (new) | 0 | ~190 | -190 |
| Everything else (simplified) | ~1,726 | ~1,410 | 316 |
| **Total Go** | **~2,376** | **~1,600** | **~776** |
| Server hooks (new) | 0 | ~150 | -150 |
| **Total project** | **~2,376** | **~1,750** | **~626** |
