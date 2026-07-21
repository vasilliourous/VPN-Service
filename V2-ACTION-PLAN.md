# V2 — Simplified Action Plan

> **replaces:** Actionable-Plan.md (v3 Hardened — 4771 lines)
> **focus:** Artificial bottlenecking + background updater + activation-code-to-Hysteria-2 pipeline
> **v2 total:** ~1,600 lines of Go (down from ~2,376) + server-side hooks

---

## What Changed From v1

| v1 Feature | v2 Decision | Rationale |
|------------|-------------|-----------|
| Staged ramp probe (5 stages) | **Removed** | Hysteria 2 CC handles congestion |
| EWMA bandwidth monitor | **Removed** | Caps are static per tier |
| Privileged helper service | **Removed** | Engine's `--tun` flag creates interfaces |
| Process sandbox (job objects) | **Removed** | Not needed for first-party engine |
| Process disguise | **Removed** | Adds complexity for no real benefit |
| 6-state connection machine | **3 states** | Over-engineered for MVP |
| `internal/probe/`, `monitor/`, `helper/` | **Deleted** | See above |
| **Updater** | **Enhanced** | Background download + auto-revert on crash |
| **Heartbeat** | **Simplified** | Suspension + update object + config refresh |
| **Activation response** | **Enhanced** | Now returns Hysteria 2 server configs |
| **Server config delivery** | **Dual path** | Activation response + heartbeat refresh |

---

## Phase 0: Server-Side — PocketBase Setup

### Step 0.1 — VPS Base

```bash
# Ubuntu 22.04, 2GB RAM
apt update && apt upgrade -y
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 22/tcp
ufw --force enable

# Install Caddy (with rate_limit module)
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main" > /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy

# Install PocketBase
mkdir -p /opt/pocketbase && cd /opt/pocketbase
wget https://github.com/pocketbase/pocketbase/releases/download/v0.22.0/pocketbase_linux_amd64.zip
unzip pocketbase_linux_amd64.zip
chmod +x pocketbase
useradd --system --no-create-home --shell /usr/sbin/nologin pocketbase
chown -R pocketbase:pocketbase /opt/pocketbase

# Systemd service
cat > /etc/systemd/system/pocketbase.service << 'EOF'
[Unit]
Description=PocketBase
After=network.target
[Service]
Type=simple
User=pocketbase
Group=pocketbase
WorkingDirectory=/opt/pocketbase
ExecStart=/opt/pocketbase/pocketbase serve --http=127.0.0.1:8090
Restart=always
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable --now pocketbase
```

### Step 0.2 — Caddy Config

```nginx
api.yourdomain.com {
    reverse_proxy 127.0.0.1:8090

    rate_limit {
        zone activation {
            key {remote_host}
            match_path /api/collections/codes/activate
            rate 5
            burst 5
            window 10m
        }
        zone heartbeat {
            key {remote_host}
            match_path /api/heartbeat
            rate 6
            burst 10
            window 1m
        }
        zone general {
            key {remote_host}
            rate 100
            burst 200
            window 10s
        }
    }

    request_body /api/collections/codes/activate {
        max_size 2KB
    }

    @updatefiles path /files/updates/*
    handle @updatefiles {
        root * /var/www/updates
        file_server
    }
}
```

### Step 0.3 — Create PocketBase Collections

Create these collections through the PocketBase Admin UI (`https://api.yourdomain.com/_/`):

**`codes`** — Activation codes database:
```
code           Plain text    (unique, required)  — "MYVPN-ABCD-1234-EFGH-5"
tier          Plain text    (required)           — "warp_lite" | "stealth_browse" | "gaming_mid" | "gaming_max"
used          Bool          (default: false)
fingerprint   Plain text    (optional)           — SHA256 hash, set on activation
suspended     Bool          (default: false)
middleman     Plain text    (optional)           — who sold it
created_at    Autodate      (created)
```

**`tier_configs`** — Hysteria 2 server configs per tier:
```
tier          Plain text    (required, unique)   — "gaming_mid"
configs       JSON          (required)           — array of protocol config objects
active        Bool          (default: true)
updated_at    Autodate      (updated)
```

Example config for a tier:
```json
[
    {
        "id": "hysteria2",
        "display_name": "Speed Mode",
        "binary_name": "speedmode",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "pool-a-password",
            "tls": { "sni": "www.cloudflare.com", "insecure": false },
            "obfs": "salamander",
            "obfs-password": "pool-a-obfs-key",
            "fast-open": true,
            "hop-interval": 30
        }
    }
]
```

**`heartbeats`** — Telemetry log (optional, for monitoring):
```
device_id     Plain text
tier          Plain text
connected     Bool
health        Plain text
ip            Plain text
created_at    Autodate
```

### Step 0.4 — Generate Initial Tier Configs

Add at least one server pool per tier. For MVP you can point all Hysteria 2 tiers to the same VPS with different auth passwords:

```
warp_lite:      usque/Warp config only (no Hysteria 2 server needed)
stealth_browse: Hysteria 2 → your-vps:443, auth=stealth-pool
gaming_mid:     Hysteria 2 → your-vps:443, auth=gaming-mid-pool
gaming_max:     Hysteria 2 → your-vps:443, auth=gaming-max-pool
```

### Step 0.5 — Deploy JS Hooks

Place these in `/opt/pocketbase/pb_hooks/`:

**`activation.pb.js`** — Handles the activation endpoint:
```javascript
// POST /api/collections/codes/activate
// Body: { "code": "MYVPN-...", "fingerprint": "sha256..." }
// Returns: { "token": "jwt...", "plan": "gaming_mid", "configs": [...] }

routerAdd("POST", "/api/collections/codes/activate", (c) => {
    const body = $apis.requestInfo(c).data;
    const code = body.code?.toUpperCase().trim();
    const fingerprint = body.fingerprint;

    // 1. Luhn-mod-N check
    if (!isValidLuhnModN(code)) {
        return c.json(400, { "message": "Invalid code format" });
    }

    // 2. Find code in DB
    const record = $app.dao().findFirstRecordByData("codes", "code", code);
    if (!record) {
        return c.json(404, { "message": "Code not found" });
    }

    // 3. Check if already used
    if (record.getBool("used")) {
        return c.json(403, { "message": "Code already activated on another device" });
    }

    // 4. Check if suspended
    if (record.getBool("suspended")) {
        return c.json(403, { "message": "Code has been suspended" });
    }

    // 5. Bind device
    record.set("used", true);
    record.set("fingerprint", fingerprint);
    $app.dao().saveRecord(record);

    // 6. Get tier configs
    const tier = record.getString("tier");
    const configRecord = $app.dao().findFirstRecordByData("tier_configs", "tier", tier);
    const configs = configRecord ? JSON.parse(configRecord.getString("configs")) : [];

    // 7. Generate JWT (valid 24h)
    const token = generateToken(fingerprint, tier);

    return c.json(200, {
        "token": token,
        "plan": tier,
        "configs": configs
    });
});

function isValidLuhnModN(s) { /* Luhn-mod-N over ABCDEFGHJKLMNPQRSTUVWXYZ23456789 */ }
function generateToken(fp, tier) { /* HMAC-SHA256 JWT with TOKEN_SECRET */ }
```

**`heartbeat.pb.js`** — Handles heartbeat POST:
```javascript
// POST /api/heartbeat
// Body: { "token": "...", "device_id": "...", ... }
// Returns: { "status": "active", "token": "...", "configs": [...], "update": {...} }

routerAdd("POST", "/api/heartbeat", (c) => {
    const body = $apis.requestInfo(c).data;

    // 1. Verify JWT token
    const claims = verifyToken(body.token);
    if (!claims) {
        return c.json(401, { "message": "Invalid token" });
    }

    // 2. Check if device is suspended
    const codeRecord = $app.dao().findFirstRecordByData("codes", "fingerprint", body.device_id);
    if (codeRecord && codeRecord.getBool("suspended")) {
        return c.json(200, { "status": "suspended", "message": "Account suspended" });
    }

    // 3. Generate fresh token
    const newToken = generateToken(claims.fingerprint, claims.tier);

    // 4. Check if tier configs have changed
    const configRecord = $app.dao().findFirstRecordByData("tier_configs", "tier", claims.tier);
    const configs = configRecord ? JSON.parse(configRecord.getString("configs")) : [];

    // 5. Check if there's a pending update for this platform
    const update = checkForUpdate(body.os_platform);

    // 6. Log heartbeat (optional)
    logHeartbeat(body);

    return c.json(200, {
        "status": "active",
        "token": newToken,
        "configs": configs,
        "update": update   // null if no update pending
    });
});
```

### Step 0.6 — Generate Initial Code Batch

Run a script or use the PocketBase admin UI to create codes:

```bash
# scripts/generate_codes.sh
# Generates 50 codes for each tier
for tier in warp_lite stealth_browse gaming_mid gaming_max; do
    for i in $(seq 1 50); do
        code=$(generate_luhn_code "MYVPN")
        curl -X POST https://api.yourdomain.com/api/collections/codes/records \
            -H "Authorization: Bearer $ADMIN_TOKEN" \
            -H "Content-Type: application/json" \
            -d "{\"code\": \"$code\", \"tier\": \"$tier\", \"middleman\": \"unassigned\"}"
    done
done
```

Print codes on paper cards:
```
┌──────────────────────┐
│   MyVPN              │
│   Gaming Mid         │
│                      │
│   MYVPN-ABCD-1234-   │
│   EFGH-5             │
│                      │
│   1 month: $8        │
│   Cash only          │
└──────────────────────┘
```

---

## Phase 1: Strip v1 Over-Engineering

### Step 1.1 — Delete Unused Packages

```bash
rm -rf internal/probe/
rm -rf internal/monitor/
rm -rf internal/helper/
rm -f  internal/manager/disguise_windows.go
rm -f  internal/manager/process_windows.go
rm -f  internal/manager/process_darwin.go
```

### Step 1.2 — Simplify `internal/manager/process.go`

```go
func StartEngine(cfg storage.ProtocolConfig, planTier string) error {
    // ... lookup binary, build command with --speed from caps.Get(planTier)
}

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

Remove: `sandbox()`, `disguiseProcess()`, `bandwidthBps` parameter.

---

## Phase 2: Background Updater (The Lifeline)

### Step 2.1 — Create `internal/updater/updater.go`

Full implementation (~150 lines) covering:

| Function | Purpose |
|----------|---------|
| `BackgroundDownload(url, sha256)` | Goroutine: download to app data dir, verify, mark pending |
| `Startup() bool` | Called from `main()` — check pending update or crash recovery |
| `CheckOnStartup() bool` | Verify SHA256, backup current, swap, return true if swapped |
| `recoverFromCrash() bool` | If "applied but not confirmed" + `.prev` exists → restore old binary |
| `ConfirmUpdate()` | Called after first successful heartbeat — delete `.prev`, clear state |

The key flow:
```
Heartbeat has "update" field → BackgroundDownload() → engines/myvpn.update
Next launch → Startup() → CheckOnStartup() → syscall.Exec → new binary
First heartbeat → ConfirmUpdate() → delete .prev
Crash before first heartbeat → recoverFromCrash() → restore .prev
```

### Step 2.2 — Update `main.go`

```go
func main() {
    storage.Init()

    // ★ Update check runs before anything else
    if updater.Startup() {
        exe, _ := os.Executable()
        syscall.Exec(exe, os.Args, os.Environ())
    }

    if !storage.IsActivated() {
        gui.ShowActivationPrompt(*adminHubURL)
        return
    }

    gui.Run(*adminHubURL)
}
```

---

## Phase 3: Heartbeat (Config Carrier)

### Step 3.1 — Heartbeat Response Structure

```go
type HeartbeatResponse struct {
    Status    string               `json:"status"`
    Plan      string               `json:"plan"`
    Token     string               `json:"token"`
    Configs   []storage.ProtocolConfig `json:"configs,omitempty"`    // ★ Server config refresh
    Update    *UpdateCommand       `json:"update,omitempty"`          // ★ Binary update
    Message   string               `json:"message,omitempty"`
}

type UpdateCommand struct {
    Version  string `json:"version"`
    Platform string `json:"platform"`
    URL      string `json:"url"`
    SHA256   string `json:"sha256"`
}
```

### Step 3.2 — Heartbeat Loop

```go
func doHeartbeat() {
    token := storage.LoadToken()
    if token == "" { return }

    resp := sendHeartbeat(token, buildPayload())

    // 1. Suspension
    if resp.Status == "suspended" {
        manager.StopEngine()
        return
    }

    // 2. Token rotation
    if resp.Token != "" {
        storage.SaveToken(resp.Token)
    }

    // 3. ★ Server config refresh (new IPs, ports, auth)
    if len(resp.Configs) > 0 {
        storage.SetProtocols(resp.Configs)
        if manager.IsRunning() {
            go reconnectWithNewConfigs(resp.Configs)
        }
    }

    // 4. ★ Background update
    if resp.Update != nil {
        go updater.BackgroundDownload(resp.Update.URL, resp.Update.SHA256)
    }

    // 5. Confirm update on first successful heartbeat
    if storage.GetAppliedUpdateVersion() != "" {
        updater.ConfirmUpdate()
    }

    consecutiveFails = 0
    graceDeadline = time.Now().Add(graceDuration)
}
```

---

## Phase 4: Activation

### Step 4.1 — Client-Side Activation Call

```go
type ActivationRequest struct {
    Code        string `json:"code"`
    Fingerprint string `json:"fingerprint"`
}

type ActivationResponse struct {
    Token   string                `json:"token"`
    Plan    string                `json:"plan"`
    Configs []ProtocolConfig      `json:"configs"`  // ★ Hysteria 2 server configs
}
```

The activation call is nearly identical to v1, but the response now includes `configs`:

```go
func Validate(apiBase, code string) (*ActivationResponse, error) {
    // ... Luhn-mod-N check
    // ... POST with code + fingerprint
    // ... Parse response including configs array
    // ... Return token + plan + configs
}
```

### Step 4.2 — Save Configs on Activation

When activation succeeds, save both the token and the server configs:

```go
storage.SaveActivation(result.Token, result.Plan)
storage.SetProtocols(result.Configs)  // ★ Save Hysteria 2 server configs
```

---

## Phase 5: Caps Package

```go
// internal/caps/caps.go — 40 lines
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
func Floor(tier string) int { return 3_000_000 }
```

---

## Phase 6: GUI (No Probe, No Monitor)

```go
func connect() {
    state.ConnectBtn.SetText("Connecting...")
    go func() {
        planTier := storage.LoadPlanTier()

        tunnel.Setup()
        tunnel.Engage()
        tunnel.Guard()

        protos := storage.GetProtocols()  // Configs from activation or heartbeat
        if len(protos) > 0 {
            manager.StartEngine(protos[0], planTier)  // Cap applied internally
        }

        state.healthChecker = health.New(state.adminHubURL)
        state.healthChecker.OnDead(func() { disconnect() })
        state.healthChecker.Start()

        state.connected = true
        state.startTime = time.Now()
        state.ConnectBtn.SetText("Disconnect")
        state.StatusLabel.SetText("Connected")
    }()
}
```

Speed label shows tier cap from `caps.Get()`, not from a monitor.

---

## Phase 7: Storage — Add Update + Config State

```go
type AppData struct {
    Token             string            `json:"token"`
    DeviceFingerprint string            `json:"device_fingerprint"`
    PlanTier          string            `json:"plan_tier"`
    Protocols         []ProtocolConfig  `json:"protocols"`         // Server configs
    PendingUpdate     *PendingUpdate    `json:"pending_update,omitempty"`
    AppliedVersion    string            `json:"applied_version,omitempty"`
    // ...v1 fields kept: TelemetryOptOut, EngineCache, ActiveConn
}
```

---

## Phase 8: Testing — Full Pipeline

### Activation-to-Hysteria-2 Tests

| Test | Expected |
|------|----------|
| Admin generates code via API | Code appears in PocketBase `codes` collection |
| User enters valid code | Server returns token + plan + Hysteria 2 config |
| User enters used code | Server rejects with "already activated" |
| User enters suspended code | Server rejects with "suspended" |
| User enters invalid Luhn code | Server rejects before DB lookup |
| Same IP activates 6× in 10 min | 6th attempt blocked by Caddy rate limit |
| User activates with wrong fingerprint | Activation succeeds, fingerprint bound |
| Second activation same code, different device | Rejected — code already bound |
| Admin suspends active code | Next heartbeat returns "suspended" → client disconnects |
| Admin changes Hysteria 2 server IP | Next heartbeat delivers new config → client reconnects |
| Client receives configs on activation | Storage has protocol configs, engine can start |

### Update Tests

| Test | Expected |
|------|----------|
| Heartbeat returns `update` field | Background download starts, no UI impact |
| Download completes | `.update` file saved, pending state in storage |
| User relaunches | Binary swapped, new version runs |
| New version crashes before first heartbeat | Next launch auto-reverts to `.prev` |
| SHA256 mismatch | Temp file deleted, error logged, no update |
| Network drops during download | Fails, retries on next heartbeat |
| First heartbeat succeeds | Update confirmed, `.prev` cleaned |

### Core Tests

| Test | Expected |
|------|----------|
| Launch with no activation | Activation prompt shown |
| Enter valid code | Token + configs saved, main window opens |
| Tap Connect | Engine starts with configs + speed cap, "Connected" |
| Change server IP on hub mid-session | Next heartbeat → new config → reconnect |
| Disconnect | Engine stops, TUN torn down, "Disconnected" |
| Health check fails 3× | Auto-disconnect, "Tap to Retry" |
| Heartbeat fails 7 days | Grace expires, re-activation required |

---

## Phase 9: Operations — Code Management

### Generating Codes (Weekly Task)

```bash
# Generate 100 codes for each tier, assign to middlemen
./scripts/generate_codes.sh \
    --tier gaming_mid \
    --count 100 \
    --middleman "House 3 - John" \
    --admin-token "$ADMIN_TOKEN"
```

### Monitoring (Quick PocketBase Queries)

```sql
-- All codes sold by a middleman
SELECT * FROM codes WHERE middleman = 'House 3 - John' AND used = true;

-- All active devices (heartbeat in last 5 min)
SELECT DISTINCT device_id FROM heartbeats WHERE created_at > datetime('now', '-5 minutes');

-- Suspended devices
SELECT * FROM codes WHERE suspended = true;
```

### Suspending a User

In PocketBase admin UI, find the code record → set `suspended = true`. The device's next heartbeat returns `"status": "suspended"` → client disconnects. Binding is preserved — unsuspending re-enables the device.

---

## Estimated Effort

| Task | Time |
|------|------|
| VPS setup (Caddy + PocketBase) | 30 min |
| Create collections + deploy hooks | 1 hour |
| Generate initial codes | 10 min |
| Delete unused v1 packages | 5 min |
| Create `internal/caps/caps.go` | 10 min |
| Build `internal/updater/updater.go` | 1 hour |
| Update `main.go` for startup update | 10 min |
| Simplify `manager/process.go` | 20 min |
| Simplify `heartbeat/heartbeat.go` | 20 min |
| Simplify `gui/app.go` | 20 min |
| Update `storage/storage.go` | 15 min |
| Test full activation→config→connect pipeline | 30 min |
| Test update→crash→revert cycle | 30 min |
| **Total** | **~4.5 hours** |
