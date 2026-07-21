# V2 — Simplified Architecture with Artificial Bottlenecking

> **replaces:** Architectural-Plan.md (over-engineered v1)
> **principle:** Plans are just bandwidth caps. Updates are the lifeline. Codes unlock servers. Everything else is plumbing.

---

## 0. Design Priorities (In Order)

1. **Survive & adapt** — the app must be updatable in the field. School networks change blocking patterns overnight. If the app can't update, it's dead.
2. **Connect reliably** — tunnel traffic through Hysteria 2. Activation codes unlock the right server for the right tier.
3. **Monetize simply** — paper codes sold for cash by middlemen. No payment processing. Just codes → servers.
4. **Stay invisible** — minimal GUI, fake display names, no technical details exposed.

---

## 1. What "Artificial Bottlenecking" Means

Each plan tier gets a **hardcoded bandwidth ceiling** applied on the client:

| Tier | Engine | Speed Cap | Notes |
|------|--------|-----------|-------|
| Warp Lite | usque/Warp | 1 MB/s (8 Mbps) | Hardcoded cap, no server config needed |
| Stealth Browse | Hysteria 2 | 6 MB/s (48 Mbps) | Cap applied via `--speed` flag |
| Gaming Mid | Hysteria 2 | 6.25 MB/s (50 Mbps) | Cap applied via `--speed` flag |
| Gaming Max | Hysteria 2 | 12.5 MB/s (100 Mbps) | Cap applied via `--speed` flag |

If the user's network is slower than the cap, Hysteria 2's built-in congestion control handles it. No client-side monitor needed.

**Removed from v1:**
- ❌ Staged ramp speed probe (5 × 500KB tests with per-stage RTT+loss)
- ❌ EWMA background bandwidth monitor (15s pings + dynamic cap adjustment)
- ❌ Bufferbloat detection
- ❌ `internal/probe/` and `internal/monitor/` packages

---

## 2. ★ The Full Pipeline: Paper Code → Hysteria 2 Connection

This is the core flow that ties the business model to the technical implementation.

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         THE FULL PIPELINE                                │
│                                                                         │
│  ┌──────────────┐    ┌──────────────┐    ┌─────────────────────────┐   │
│  │ 1. CODE GEN  │───→│ 2. PAPER     │───→│ 3. USER ENTERS CODE    │   │
│  │  (Admin)     │    │    DISTRO    │    │    IN APP               │   │
│  │              │    │  (Middleman) │    │                         │   │
│  │ PocketBase   │    │              │    │ "MYVPN-ABCD-1234-..."   │   │
│  │ generates    │    │ Student pays │    │                         │   │
│  │ batch of     │    │ $5 cash      │    │ App sends code +        │   │
│  │ codes, each  │    │ gets paper   │    │ hardware fingerprint    │   │
│  │ tied to a    │    │ card with    │    │ to hub                  │   │
│  │ plan tier    │    │ code         │    │                         │   │
│  └──────┬───────┘    └──────────────┘    └─────────────┬───────────┘   │
│         │                                              │               │
│         │                                              ▼               │
│         │                                   ┌─────────────────────┐   │
│         │                                   │ 4. SERVER VALIDATES │   │
│         │                                   │                     │   │
│         │                                   │ • Luhn-mod-N check  │   │
│         │                                   │ • Code exists in DB?│   │
│         │                                   │ • Already used?     │   │
│         │                                   │ • Rate limit (5/10m)│   │
│         │                                   │ • Create device     │   │
│         │                                   │   binding record    │   │
│         │                                   └─────────────┬───────┘   │
│         │                                                 │           │
│         │                                                 ▼           │
│         │                                   ┌─────────────────────┐   │
│         │                                   │ 5. SERVER RESPONDS  │   │
│         │                                   │                     │   │
│         │                                   │ • JWT token         │   │
│         │                                   │ • Plan tier name    │   │
│         │                                   │ • ★ Protocol configs│   │
│         │                                   │   (server IP, port, │   │
│         │                                   │    auth password,   │   │
│         │                                   │    obfuscation)     │   │
│         │                                   └─────────────┬───────┘   │
│         │                                                 │           │
│         ▼                                                 ▼           │
│  ┌──────────────┐    ┌──────────────┐    ┌─────────────────────────┐   │
│  │ 6. ADMIN     │    │ 7. HEARTBEAT │    │ 8. CLIENT CONNECTS      │   │
│  │ CONTROLS     │    │ KEEPS FRESH  │    │                         │   │
│  │              │    │              │    │ Hysteria 2 client with  │   │
│  │ Suspension   │    │ Every 60s:   │    │ • server:port from      │   │
│  │ (set flag)   │    │ • Refresh    │    │   activation config     │   │
│  │ Change       │    │   token      │    │ • auth password from    │   │
│  │ server IPs   │    │ • Update     │    │   activation config     │   │
│  │ Change tiers │    │   protocol   │    │ • --speed from tier cap │   │
│  │              │    │   configs    │    │ • --tun (engine creates)│   │
│  └──────────────┘    │ • Push       │    └─────────────────────────┘   │
│                       │   updates    │                                 │
│                       └──────────────┘                                 │
└─────────────────────────────────────────────────────────────────────────┘
```

### Step-by-Step

**Step 1 — Admin generates codes in PocketBase:**
```json
// POST /api/collections/codes/records
{
    "code": "MYVPN-ABCD-1234-EFGH-5",   // Luhn-mod-N generated
    "tier": "gaming_mid",                // Which plan this unlocks
    "used": false,
    "fingerprint": null,
    "suspended": false,
    "middleman": "House 3 - John"        // Track which middleman sold it
}
```

The admin generates batches via the PocketBase admin UI or a small script. Codes are printed on paper cards with the tier name and a QR code (optional).

**Step 2 — Middleman distributes:** A middleman collects cash from a student, hands them a paper card with the code. No digital payment, no accounts, no email. The middleman has no access to the admin panel — they only have paper.

**Step 3 — User enters code in app:** The app sends a POST to the hub with the code + hardware fingerprint.

**Step 4 — Server validates:**
- Luhn-mod-N checksum valid?
- Code exists in `codes` collection?
- Code not already used?
- IP not rate-limited (5 attempts per 10 minutes)?
- All pass → bind fingerprint to code (mark `used=true`), create device binding record

**Step 5 — Server responds with Hysteria 2 config:**
```json
{
    "token": "jwt...",
    "plan": "gaming_mid",
    "configs": [{
        "id": "hysteria2",
        "display_name": "Speed Mode",
        "binary_name": "speedmode",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "GamingMid-Pool-A-Password",
            "obfs": "salamander",
            "obfs-password": "pool-a-obfuscation-key",
            "sni": "www.cloudflare.com",
            "insecure": false
        }
    }]
}
```

This is the **pairing of activation code with Hysteria 2 link**. The activation code unlocks the specific server config for that tier.

**Step 6 — Admin controls servers:** The admin can update server configs in PocketBase at any time. Next heartbeat delivers the new configs. If a server gets blocked, the admin changes the pool IP and all clients get it within 60 seconds.

**Step 7 — Heartbeat keeps configs fresh:** Every 60s, the heartbeat response can return updated protocol configs. This means:
- Change server IP without a client update
- Rotate auth passwords
- Switch obfuscation methods
- Add/remove server pools
- Rebalance tiers across servers

**Step 8 — Client connects:** The client starts Hysteria 2 with the config from activation (or latest heartbeat), applies the speed cap from `caps.Get(tier)`, and creates the TUN interface via `--tun`.

---

## 3. Protocol Config Delivery: How Codes Map to Servers

Server configs are tier-specific. Each tier can have multiple server pools for redundancy.

### PocketBase Collection Schema

**`codes` collection:**
```
code           (text, unique) — "MYVPN-ABCD-1234-EFGH-5"
tier           (text)         — "warp_lite" | "stealth_browse" | "gaming_mid" | "gaming_max"
used           (bool)         — false until first activation
fingerprint    (text)         — null until bound, then SHA256 hash
suspended      (bool)         — admin can suspend without clearing binding
middleman      (text)         — who sold it (for tracking)
created_at     (datetime)
```

**`tier_configs` collection:**
```
tier           (text)         — "gaming_mid" 
configs        (json)         — array of protocol config objects
active         (bool)         — which config set is currently active
updated_at     (datetime)
```

When a client activates, the server looks up `tier_configs` for the code's tier and returns the active configs alongside the token.

When a heartbeat arrives, the server can optionally include updated configs if the `tier_configs` record changed since the client's last check.

### Config JSON Structure (per protocol)

**Hysteria 2 config:**
```json
{
    "server": "sgp1.api.yourdomain.com:443",
    "auth": "pool-password",
    "tls": {
        "sni": "www.cloudflare.com",
        "insecure": false
    },
    "obfs": "salamander",
    "obfs-password": "pool-key",
    "fast-open": true,
    "hop-interval": 30
}
```

**usque/Warp config** (for Warp Lite tier):
```json
{
    "wg": {
        "private-key": "...",
        "peer-public-key": "...",
        "endpoint": "engage.cloudflareclient.com:2408"
    }
}
```

The client doesn't interpret these — it passes the JSON directly to the engine via `-c config.json`.

---

## 4. Business Model Integration

| Business Element | How It Works Technically |
|-----------------|------------------------|
| **Middlemen sell codes for cash** | Admin generates codes in PocketBase, prints them on paper cards. Middlemen get a stack of cards. No app access for middlemen. |
| **No payment processing** | No Stripe, no PayPal. Cash only. Admin generates codes at zero marginal cost. |
| **Tiered pricing ($2–$13/mo)** | Each code is generated with a specific tier in PocketBase. The tier maps to speed cap + server config. |
| **One code = one device forever** | Device binding is permanent. The `fingerprint` field on the code is set once and never cleared. |
| **Suspension without refund** | Admin sets `suspended=true` on the code record. Next heartbeat returns status "suspended" → client disconnects. Binding is preserved. |
| **Middleman commission tracking** | Each code has a `middleman` field. Admin can query `used=true` grouped by middleman to calculate commissions. |
| **No self-service portal** | Users have no web login. Everything happens through the app. If they lose their code, they buy a new one from a middleman. |

---

## 5. What We Keep (All Components)

| Component | Why It Stays |
|-----------|-------------|
| **Updater** | **Critical** — push new binaries when protocols get blocked |
| **Heartbeat** | Carries token refresh, suspension status, **updated server configs**, update commands |
| **Activation** | Luhn-mod-N + hardware fingerprint binding + protocol config delivery |
| **Storage** | Persist token, tier, device ID, **protocol configs**, pending update state |
| **Caps** | Hardcoded per-tier bandwidth limits (40 lines) |
| **Branding** | Fake protocol/plan names for intransparency |
| **Manager** | Start/stop engine with `--speed <bps>` cap using config from storage |
| **TUN + kill switch + DNS** | Route traffic, prevent leaks |
| **Health check** | Detect dead tunnel, trigger reconnect |
| **GUI** | Connect/disconnect, status, time, tier name |

---

## 6. Speed Caps (`internal/caps/caps.go`)

```go
package caps

func Get(tier string) int {
    switch tier {
    case "warp_lite":      return   8_000_000
    case "stealth_browse": return  48_000_000
    case "gaming_mid":     return  50_000_000
    case "gaming_max":     return 100_000_000
    default:               return           0
    }
}

func Floor(tier string) int {
    return 3_000_000
}
```

---

## 7. Connection Flow (End to End)

```
User taps Connect
        │
        ▼
  Load protocol configs from storage
  (received during activation or last heartbeat)
        │
        ▼
  Look up tier cap: caps.Get(planTier) → int bps
        │
        ▼
  Setup TUN → Engage kill switch → Guard DNS
        │
        ▼
  Start engine with:
    • -c config.json (server, auth, obfs from activation/heartbeat)
    • --tun (engine creates interface)
    • --speed <bps> (artificial bottleneck)
        │
        ▼
  Start health check (15s ping through tunnel)
        │
        ▼
  GUI: "Connected" + tier name + elapsed time
```

**Disconnect:** Stop health check → Stop engine → UnGuard DNS → Disengage kill switch → Teardown TUN

**State machine:** 3 states — `IDLE → CONNECTING → CONNECTED`. Health fails 3× → auto-disconnect → "Tap to Retry."

---

## 8. ★ Background Updater (The Lifeline)

### Why Updates Matter More Than Anything

School IT blocks protocols, not just IPs. Today Hysteria 2 over QUIC on port 443 works. Tomorrow the school's DPI might fingerprint it and drop it. When that happens, you need to push a new binary with different obfuscation to every client **within hours**. The updater is not a nice-to-have — it's the app's immune system.

### Update Trigger

Heartbeat response carries an optional `update` field:

```json
{
    "status": "active",
    "token": "new-jwt...",
    "update": {
        "version": "1.0.3",
        "platform": "windows_amd64",
        "url": "https://api.yourdomain.com/files/updates/myvpn_1.0.3.exe",
        "sha256": "a1b2c3d4..."
    },
    "configs": [...]        // ← can also update server configs here
}
```

### Background Download Flow

```
Heartbeat response has "update" field
        │
        ▼
  Spawn goroutine:
    1. Download to engines/myvpn.update.tmp (app data dir — user-writable)
    2. SHA256 verify
    3. Rename → engines/myvpn.update
    4. Save {version, sha256} to storage
    5. User keeps using VPN — no interruption
```

### Apply on Next Launch

```
main() starts:
  1. Check storage for pending update
  2. Verify SHA256 again
  3. Backup current exe → myvpn.prev
  4. Swap .update → exe path
  5. Mark "applied, not confirmed"
  6. syscall.Exec → new binary runs
```

### Auto-Revert on Crash

```
New binary crashes before first heartbeat
        │
        ▼  (user relaunches)
  Storage says "applied but not confirmed"
  myvpn.prev exists
  → Delete crashed binary
  → Rename myvpn.prev → exe
  → Log: "Update v1.0.3 crashed, reverted to v1.0.2"
```

First heartbeat success → `ConfirmUpdate()` → delete `.prev` → update permanent.

---

## 9. Heartbeat (Update Carrier + Config Refresher)

```go
func doHeartbeat() {
    token := storage.LoadToken()
    if token == "" { return }

    resp := sendHeartbeat(token, buildPayload())

    // Suspension check
    if resp.Status == "suspended" {
        manager.StopEngine()
        return
    }

    // Token rotation
    if resp.Token != "" {
        storage.SaveToken(resp.Token)
    }

    // ★ Update protocol configs (server IP, auth, obfuscation)
    if len(resp.Configs) > 0 {
        storage.SetProtocols(resp.Configs)
        // If currently connected, restart engine with new config
        if manager.IsRunning() {
            go reconnectWithNewConfig()
        }
    }

    // ★ Background update download
    if resp.Update != nil {
        go updater.BackgroundDownload(resp.Update.URL, resp.Update.SHA256)
    }

    // Confirm update on first successful heartbeat
    if storage.GetAppliedUpdateVersion() != "" {
        updater.ConfirmUpdate()
    }

    consecutiveFails = 0
    graceDeadline = time.Now().Add(graceDuration)
}
```

---

## 10. Server-Side Architecture

```
                    Internet
                       │
                  ┌────┴────┐
                  │  Caddy  │  ← TLS termination, rate limiting
                  │  :443   │
                  └────┬────┘
                       │
                  ┌────┴────┐
                  │  Caddy  │
                  │  :8090  │  ← reverse proxy to PocketBase
                  └────┬────┘
                       │
                  ┌────┴──────────┐
                  │  PocketBase   │
                  │  (SQLite WAL) │
                  │               │
                  │ Collections:  │
                  │ • codes       │  ← activation codes + device bindings
                  │ • tier_configs│  ← Hysteria 2 server configs per tier
                  │ • heartbeats  │  ← telemetry log
                  └────┬──────────┘
                       │
                  ┌────┴────┐
                  │  Files  │
                  │ /var/www/updates/  ← update binaries
                  │ /var/www/speedtest/ ← (removed in v2 — not needed)
                  └─────────┘
```

### PocketBase JS Hooks

**`on_codes_create`** — validates code format before saving:
```js
// Server-side Luhn-mod-N validation
if (!isValidLuhnModN(record.code)) throw new Error("Invalid code format");
```

**`on_codes_activate`** (custom endpoint) — handles the activation POST:
```js
// 1. Rate limit: 5 attempts per 10 min per IP
// 2. Validate Luhn-mod-N
// 3. Check code exists + not used + not suspended
// 4. Bind fingerprint to code
// 5. Generate JWT token
// 6. Return token + plan + tier configs
```

**`on_heartbeat`** (custom endpoint) — handles heartbeat POST:
```js
// 1. Verify JWT token
// 2. Check suspension status
// 3. Rotate token if needed
// 4. Check if tier_configs changed since last heartbeat
// 5. Return status + maybe new token + maybe new configs + maybe update cmd
```

---

## 11. TUN (Simplified)

**v1 had:** A privileged helper service with named-pipe IPC, caller verification, service install/uninstall.

**v2 has:** Delegate TUN creation to the engine. Hysteria 2 has a `--tun` flag that creates the interface and routes traffic.

```go
func buildCommand(binaryPath, protocolID string, configJSON json.RawMessage, planTier string) *exec.Cmd {
    switch protocolID {
    case "hysteria2":
        return exec.Command(binaryPath, "client",
            "-c", string(configJSON),
            "--tun",
            "--speed", fmt.Sprintf("%d", caps.Get(planTier)),
        )
    case "usque":
        return exec.Command(binaryPath, "-c", string(configJSON))
    }
}
```

Kill switch and DNS guard remain as OS-level commands in `internal/tunnel/`.

---

## 12. v1 → v2 Summary

| Area | v1 (Over-Engineered) | v2 (Simplified + Complete) |
|------|---------------------|---------------------------|
| Activation → server config | Implicit, undocumented pipeline | **Explicit pipeline** — code → tier → server config → Hysteria 2 |
| Business model | Separate doc, not integrated | **Integrated** — middlemen, paper codes, no payments, suspension |
| Server config delivery | Only via heartbeat | **Dual path** — activation response + heartbeat updates |
| Rate limiting | Mentioned in threat model | **Built into activation flow** — 5 attempts per 10 min per IP |
| Device binding | Permanent, one-way | Same, documented in pipeline |
| Speed limiting | 5-stage probe + EWMA + dynamic | **Hardcoded lookup** (40 lines) |
| Helper service | IPC + install + verify | **Dropped** — engine `--tun` flag |
| Update mechanism | Two-phase sentinel | **Background download + auto-revert on crash** |
| State machine | 6 states + GRACE | **3 states** — IDLE/CONNECTING/CONNECTED |
| Server-side | Caddy + PocketBase + hooks | Same, simplified hooks |
