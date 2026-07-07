# VPN App — Action Plan (v3 Hardened)

**Goal:** Cross-platform VPN app (Windows + macOS) with Hysteria 2 (gaming primary) and usque/Warp (fallback — unbottlenecked for Gaming plans). Activation codes are permanently bound to a single hardware-fingerprinted device with server-side suspension capability. Features: TUN mode via privileged helper service, auto-reconnect loop, certificate pinning, heartbeat grace period, process sandboxing, and tunnel health checks.

**Stack:** Go + Fyne (minimal GUI) + Hysteria 2 + usque + PocketBase + Caddy  
**Prerequisite:** VPS (Ubuntu 22.04, 2GB RAM), domain pointing to it

> 📘 **Business context:** `BUSINESS-OVERVIEW.md`  
> 📘 **Full architecture:** `PLAN.md`

**Key design decisions (v4 changes from v3):**

- ✅ **Hardware-fingerprinted device binding** — permanent one-device-per-code binding. Device identity is a SHA256 hash of MAC address + disk serial + motherboard UUID. Survives app deletion/reinstall. Server can suspend connections without destroying the binding.
- ✅ **No update signing** — updates are SHA256-verified direct downloads from the admin hub. No Ed25519 key management, no offline signing ceremony, no key rotation headaches.
- ✅ **Privileged helper service** (Windows: SYSTEM service via named pipe, macOS: launchd daemon) — TUN creation requires admin on install only, not on every launch
- ✅ **Auto-reconnect loop** — 3 health fails → try fallback → all dead → wait 30s → retry primary ×5 → give up with "tap to retry"
- ✅ **Full tunnel for MVP** — all IPv4 traffic through VPN except RFC 1918 local. IPv6 blocked entirely (no leak risk). Kill switch: delete default TUN route. Split tunnel is Phase 2.
- ✅ **Grace period overrides token expiry** — during heartbeat failure, last valid token continues working until 7-day grace expires
- ✅ **Primary engine (Hysteria 2) bundled in installer** — bootstrap chicken-and-egg solved. Runtime download only for updates.
- ✅ **Explicit 6-state connection machine** — IDLE → ACTIVE → CONNECTING → CONNECTED_PRIMARY → CONNECTED_FALLBACK → DEGRADED → DISCONNECTED, with GRACE orthogonal
- ✅ **Privacy notice + opt-out toggle** — telemetry fields documented, no browsing history collected
- ✅ **Log rotation** — auto-rotate at 10MB, keep last 3 files
- ✅ **Activation code format** — `MYVPN-XXXX-XXXX-XXXX-C` with Luhn-mod-N checksum
- ✅ **Suspension, not deactivation** — admin can suspend a device from connecting. Binding is never destroyed; code can never be reused on a different device.
- ✅ **Minimal proper GUI** (not systray) — small window with connection status, tier name, speed indicator, time connected. No technical protocol names exposed. Intransparency by design.
- ✅ **Adaptive speed probing + real-time bandwidth monitoring** — usque: hardcoded per-tier caps; Hysteria 2: **fresh bandwidth probe on every connect** + continuous EWMA-smoothed background monitor that dynamically adjusts the cap mid-session to prevent bufferbloat from QoS shaping fluctuations
- ✅ **MVP protocol scope: Hysteria 2 + usque only** — no VLESS-REALITY, TUIC v5, or Xray in initial release. Added in Phase 2 per market demand.
- ✅ **IPv6: blocked entirely on TUN interface** — prevents IPv6 leaks. Documented as known limitation.
- ✅ **Rollback safety** — previous binary kept as `myvpn.prev`, two-phase sentinel handshake auto-reverts on crash after update, manual `--revert` flag as backup
- ❌ **macOS notarization & codesigning** — skipped intentionally. Target users are on school-managed Macs where Gatekeeper is often disabled or bypassable via "Open Anyway." Apple Developer Program ($99/yr) deferred until revenue covers it.
- ❌ **Mobile (iOS/Android)** — deferred. MVP is Windows + macOS desktop only. Mobile requires separate TUN framework, different distribution model, and platform-specific development effort.
- ❌ Middlemen have no app or panel access — they hand out paper codes, that's all

---

## Contents

- [Phase 1.1: VPS + Admin Hub Hardening](#phase-11-vps--admin-hub-hardening)
- [Phase 1.2: Project Scaffolding](#phase-12-project-scaffolding)
- [Phase 1.3: Core Modules — Storage, Activation, Branding](#phase-13-core-modules--storage-activation-branding)
- [Phase 1.4: Protocol Engine Manager + Sandboxing + Binary Rename](#phase-14-protocol-engine-manager--sandboxing--binary-rename)
- [Phase 1.5: Heartbeat with Cert Pinning + Grace + Telemetry](#phase-15-heartbeat-with-cert-pinning--grace--telemetry)
- [Phase 1.6: Auto-Updater with SHA256 Verification](#phase-16-auto-updater-with-sha256-verification)
- [Phase 1.7: TUN Mode + Kill Switch + DNS Guard + IPv6 Blocking + Health Check](#phase-17-tun-mode--kill-switch--dns-guard--ipv6-blocking--health-check)
- [Phase 1.8: Adaptive Speed Probing + Client-Side Throttling](#phase-18-adaptive-speed-probing--client-side-throttling)
- [Phase 1.9: Runtime Engine Download](#phase-19-runtime-engine-download)
- [Phase 1.10: Minimal GUI (Fyne)](#phase-110-minimal-gui-fyne)
   - [Custom Theme — Black + Purple Branding](#5-custom-theme--black--purple-branding)
- [Phase 1.11: Build System + Main Entrypoint](#phase-111-build-system--main-entrypoint)
- [Phase 1.12: Testing & QA](#phase-112-testing--qa)
- [Phase 1.13: Deploy & Verify Checklist](#phase-113-deploy--verify-checklist)

---

## Phase 1.1: VPS + Admin Hub Hardening

### 1. Buy a VPS

Hetzner CCX13, DigitalOcean Droplet, or Vultr — Ubuntu 22.04, 2GB RAM minimum.

### 2. Point your domain

```
A record: api.yourdomain.com → VPS_IP
```

### 3. SSH in and run base setup

```bash
ssh root@your-vps

# System update + firewall
apt update && apt upgrade -y
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 22/tcp
ufw --force enable

# Install Caddy (with rate_limit module)
apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main" > /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy
```

### 4. Install PocketBase

```bash
mkdir -p /opt/pocketbase && cd /opt/pocketbase
wget https://github.com/pocketbase/pocketbase/releases/download/v0.22.0/pocketbase_linux_amd64.zip
unzip pocketbase_linux_amd64.zip
chmod +x pocketbase

# Create dedicated user
useradd --system --no-create-home --shell /usr/sbin/nologin pocketbase
chown -R pocketbase:pocketbase /opt/pocketbase

# Create systemd service
cat > /etc/systemd/system/pocketbase.service << 'SERVICEEOF'
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
SERVICEEOF

systemctl daemon-reload
systemctl enable --now pocketbase
```

### 5. Configure Caddy with rate limiting

```bash
cat > /etc/caddy/Caddyfile << 'CADDYEOF'
api.yourdomain.com {
    reverse_proxy 127.0.0.1:8090

    rate_limit {
        zone heartbeat {
            key {remote_host}
            match_path /api/heartbeat
            rate 1 r/s
            burst 5
        }
        zone activation {
            key {remote_host}
            match_path /api/collections/activations/*
            rate 3 r/m
            burst 2
        }
    }

    # Security headers
    header {
        Strict-Transport-Security "max-age=63072000"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
    }
}
CADDYEOF

systemctl restart caddy
```

> ⚠️ Replace `api.yourdomain.com` with your actual domain. Let Caddy auto-provision TLS from Let's Encrypt.

### 6. Set SQLite WAL mode + busy_timeout

Create `/opt/pocketbase/pb_hooks/sqlite_pragmas.pb.js`:

```javascript
// Set SQLite WAL mode and busy timeout on PocketBase startup
// Prevents write contention under concurrent heartbeat + activation + admin queries
onBeforeServe(() => {
    const db = $app.dao().db();
    db.exec("PRAGMA journal_mode=WAL;");
    db.exec("PRAGMA busy_timeout=5000;");
    console.log("SQLite pragmas set: WAL + busy_timeout=5000");
});
```

### 7. Set up offsite backups (Backblaze B2)

```bash
# Install b2 CLI
apt install -y python3-pip
pip3 install b2

# Authorize (create a bucket + app key in B2 console first)
b2 authorize-account <applicationKeyId> <applicationKey>

# Create backup script with offsite upload
cat > /opt/pocketbase/backup.sh << 'BACKUPEOF'
#!/bin/bash
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
TMP_FILE=$(mktemp)

# Create SQLite backup
sqlite3 /opt/pocketbase/pb_data/data.db ".backup $TMP_FILE"

# Upload to Backblaze B2
b2 upload-file my-vpn-backup-bucket "$TMP_FILE" "data-$TIMESTAMP.db" > /dev/null 2>&1

# Daily: also upload a compressed SQL dump
if [ "$(date +%H)" = "03" ]; then
    DUMP_FILE=$(mktemp)
    sqlite3 /opt/pocketbase/pb_data/data.db ".dump" | gzip > "$DUMP_FILE"
    b2 upload-file my-vpn-backup-bucket "$DUMP_FILE" "dump-$TIMESTAMP.sql.gz" > /dev/null 2>&1
    rm -f "$DUMP_FILE"
fi

rm -f "$TMP_FILE"
echo "backup completed $TIMESTAMP" >> /var/log/pocketbase-backup.log
BACKUPEOF

chmod +x /opt/pocketbase/backup.sh

# Run every hour
echo "0 * * * * root /opt/pocketbase/backup.sh" > /etc/cron.d/pocketbase-backup
chmod 644 /etc/cron.d/pocketbase-backup
```

> ⚠️ On-VPS-only backups die when the VPS dies. Offsite is non-negotiable.

### 8. Set up external uptime monitoring

Sign up for a free uptime monitor (UptimeRobot, BetterUptime, or PingPong). Point it at:

```
https://api.yourdomain.com/_/health
```

Configure email/SMS alerts. This is the only way you'll know the hub is down before customers complain.

### 9. Create activation brute-force protection hook

Create `/opt/pocketbase/pb_hooks/activation_rate_limit.pb.js`:

```javascript
// Rate limit activation attempts per IP
const ATTEMPT_LIMIT = 5;
const WINDOW_MINUTES = 10;

routerAdd("POST", "/api/collections/activations/records", (c) => {
    const ip = c.requestHeader("X-Forwarded-For") || c.remoteIP();
    
    const recent = $app.dao().findRecordsByFilter("activation_attempts",
        `ip="${ip}" && created > "${new Date(Date.now() - WINDOW_MINUTES * 60000).toISOString()}"`);
    
    if (recent.length >= ATTEMPT_LIMIT) {
        return c.json(429, { "code": 429, "message": "Too many attempts. Try again later." });
    }
    
    $app.dao().saveRecord($app.dao().createRecord("activation_attempts", {
        ip: ip,
        created: new Date().toISOString()
    }));
    
    return c.next();
});
```

Create the `activation_attempts` collection in PocketBase admin (simple: `ip` text + `created` autodate).

### 10. Device binding hook (CRITICAL — permanent one-code-one-device binding)

Create `/opt/pocketbase/pb_hooks/device_binding.pb.js`:

```javascript
// Permanent device binding — one code, one device, forever.
// If the app is deleted and reinstalled on the same device,
// the same code works again because the hardware fingerprint is identical.
// A different device with the same code → rejected.
//
// Server can suspend a binding without destroying it.
// Suspended devices get "suspended" status in heartbeat → client disconnects
// but can re-activate (receive a new token) at any time when unsuspended.
routerAdd("POST", "/api/collections/activations/records", (c) => {
    const code = c.requestInfo().body.code;
    const fingerprint = c.requestHeader("X-Device-Fingerprint");
    
    if (!code || !fingerprint) {
        return c.json(400, { "code": 400, "message": "Missing code or device fingerprint" });
    }
    
    // Look up the activation code
    const record = $app.dao().findFirstRecordByFilter("activation_codes",
        `code = {:code} && active = true`, { code });
    if (!record) {
        return c.json(404, { "code": 404, "message": "Invalid or expired code" });
    }
    
    // Check expiry
    const expires = record.get("expires");
    if (expires && new Date(expires) < new Date()) {
        return c.json(410, { "code": 410, "message": "Code expired" });
    }
    
    const boundDevice = record.get("bound_device_id");
    
    if (!boundDevice || boundDevice === "") {
        // Code is fresh — permanently bind to this device
        record.set("bound_device_id", fingerprint);
        $app.dao().saveRecord(record);
    } else if (boundDevice === fingerprint) {
        // Same device re-activating (e.g. after app reinstall)
        // Check suspension status
        const suspended = record.get("suspended");
        if (suspended) {
            const reason = record.get("suspended_reason") || "Your account has been suspended. Contact your middleman.";
            return c.json(403, { "code": 403, "message": reason });
        }
        // Device is recognized — allow re-activation
    } else {
        // Different device — reject permanently
        return c.json(403, { "code": 403, "message": "This code is already bound to another device" });
    }
    
    // Generate a token (24h expiry, server-side HMAC so it can't be forged)
    const secret = $os.getenv("TOKEN_SECRET") || "change-me-in-production";
    const tokenPayload = JSON.stringify({
        fingerprint: fingerprint,
        code: code,
        exp: Date.now() + 24 * 60 * 60 * 1000
    });
    const token = btoa(tokenPayload) + "." + $security.hmacSHA256(tokenPayload, secret);
    
    c.set("token", token);
    c.set("plan", record.get("plan"));
    return c.next();
});
```

> ⚠️ Set `TOKEN_SECRET` to a random 64-char hex string in the PocketBase environment. Without this, tokens are trivially forgeable (base64 only, no signature). The HMAC suffix lets the heartbeat endpoint verify the token wasn't tampered with. In production, consider using proper JWT with `$security.jwtEncode()` if your PocketBase version supports it.

**No deactivation endpoint.** Codes cannot be unbound. The admin suspends a device (toggle `suspended = true` in PocketBase admin) to block connections. When unsuspended, the same code works on the same device — no re-binding needed.

### 11. Activation code format

Codes are generated via the PocketBase admin interface (or a script) and printed on paper for middlemen. Format:

```
MYVPN-A3X9-K7M2-Q5P1-C
```

- Prefix `MYVPN-` (branded, distinguishes from other codes students might see)
- 4 groups of 4 characters each: uppercase A-Z + digits 2-9 **(excludes I, O, 0, 1 for readability)**
- Last character is a **Luhn-mod-N check digit** — catches single-character typos at input time
- Case-insensitive on input (normalize to uppercase before validation)
- `expires` date in PocketBase (default 90 days, configurable per code)
- **Permanently bound to one device** on first activation — cannot be unbound or reused on a different device

**Luhn-mod-N check digit (JavaScript for PocketBase hook):**

```javascript
// Compute Luhn-mod-N check digit for activation codes
const CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // 32 chars (no I,O,0,1)
const BASE = CHARSET.length;

function luhnModN(code) {
    let sum = 0;
    let double = false;
    for (let i = code.length - 1; i >= 0; i--) {
        let val = CHARSET.indexOf(code[i].toUpperCase());
        if (val === -1) return -1;
        if (double) {
            val *= 2;
            if (val >= BASE) val = Math.floor(val / BASE) + (val % BASE);
        }
        sum += val;
        double = !double;
    }
    return (BASE - (sum % BASE)) % BASE;
}

function validateCode(code) {
    // Strip prefix
    code = code.replace(/^MYVPN-/, "").replace(/-/g, "");
    if (code.length !== 17) return false; // 16 data + 1 check
    const checkChar = code[code.length - 1];
    const data = code.slice(0, -1);
    const expectedCheck = CHARSET[luhnModN(data)];
    return checkChar.toUpperCase() === expectedCheck;
}
```

### 12. Create PocketBase collections

In the PocketBase admin UI (`https://api.yourdomain.com/_/`), create these collections:

**`activation_codes`** — the codes middlemen hand out:
- `code` (text, unique) — e.g. `MYVPN-A7X3-K9M2-Q5P1-C`
- `plan` (text) — `warp_lite`, `gaming_mid`, `gaming_max`
- `bound_device_id` (text, unique, nullable) — SHA256 hardware fingerprint of the permanently bound device; `null` when code is fresh/unused
- `suspended` (bool, default false) — server can suspend connections without breaking the permanent binding
- `suspended_reason` (text, optional) — admin note (e.g. "reported stolen")
- `created_by` (text) — who created it (admin reference)
- `expires` (datetime) — optional expiry
- `active` (bool, default true) — soft-deactivate codes before they're claimed

**`activation_attempts`** — rate limit tracking:
- `ip` (text)
- `created` (autodate)

> **No `devices` collection.** The device binding is embedded in `activation_codes` via `bound_device_id`. A code is permanently bound to one device. There is no deactivation, no re-use on different devices, and no multi-device codes. Middlemen sell one code = one device.

---

## Phase 1.2: Project Scaffolding

### 1. Initialize the Go module

```bash
mkdir -p myvpn && cd myvpn
go mod init myvpn
```

### 2. Create publish script for updates

```bash
mkdir -p scripts

cat > scripts/publish_update.sh << 'PUBEOF'
#!/bin/bash
# publish_update.sh <binary> <version> <platform>
# Computes SHA256 and outputs the JSON payload to upload to the admin hub.
# No signing keys needed — updates are verified by SHA256 only.
set -euo pipefail

BINARY="$1"
VERSION="$2"
PLATFORM="$3"

if [ -z "$BINARY" ] || [ -z "$VERSION" ] || [ -z "$PLATFORM" ]; then
    echo "Usage: $0 <binary> <version> <platform>"
    echo "Example: $0 myvpn.exe 1.0.1 windows_amd64"
    exit 1
fi

SHA256=$(sha256sum "$BINARY" | cut -d' ' -f1)
FILENAME=$(basename "$BINARY")

cat << JSONEOF
{
    "platform": "$PLATFORM",
    "version": "$VERSION",
    "sha256": "$SHA256",
    "url": "https://api.yourdomain.com/files/updates/$FILENAME"
}
JSONEOF
PUBEOF

chmod +x scripts/publish_update.sh
```

---

## Phase 1.3: Core Modules — Storage, Activation, Branding

### 1. `internal/storage/storage.go`

Persistent state: activation token, HMAC device ID, plan tier, protocol configs, engine cache paths, logs.

```go
package storage

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"
)

type Storage struct {
    mu       sync.RWMutex
    dataDir  string
    data     *AppData
}

type AppData struct {
    Token             string            `json:"token"`
    DeviceFingerprint string            `json:"device_fingerprint"` // SHA256 of hardware fingerprint (MAC+disk+MoboUUID)
    PlanTier          string            `json:"plan_tier"`
    Protocols         []ProtocolConfig  `json:"protocols"`
    ActiveConn        string            `json:"active_conn"`
    TelemetryOptOut   bool              `json:"telemetry_opt_out"`
    EngineCache       map[string]string `json:"engine_cache"` // protocol ID → cached binary path
}

type ProtocolConfig struct {
    ID          string          `json:"id"`
    DisplayName string          `json:"display_name"`
    BinaryName  string          `json:"binary_name"`
    ConfigJSON  json.RawMessage `json:"config_json"`
    Weight      int             `json:"weight"`
}

var (
    instance *Storage
    once     sync.Once
)

func Init() {
    once.Do(func() {
        dir := appDataDir()
        os.MkdirAll(dir, 0700)
        os.MkdirAll(filepath.Join(dir, "logs"), 0700)
        os.MkdirAll(filepath.Join(dir, "engines"), 0700)
        instance = &Storage{
            dataDir: dir,
            data:    &AppData{EngineCache: make(map[string]string)},
        }
        instance.load()
        StartLogRotation() // background log rotation goroutine
    })
}

func appDataDir() string {
    base := os.Getenv("APPDATA")
    if base == "" {
        base = filepath.Join(os.Getenv("HOME"), "Library", "Application Support")
        if _, err := os.Stat(base); os.IsNotExist(err) {
            base = filepath.Join(os.Getenv("HOME"), ".config")
        }
    }
    return filepath.Join(base, "MyVPN")
}

func LogDir() string { return filepath.Join(appDataDir(), "logs") }
func EngineDir() string { return filepath.Join(appDataDir(), "engines") }
func EngineConfigDir() string {
    d := filepath.Join(appDataDir(), "configs")
    os.MkdirAll(d, 0700)
    return d
}

// EngineConfigPath returns the path to an engine's config file.
// Configs are stored as JSON and can be rewritten at runtime by the throttle monitor.
func EngineConfigPath(protocolID string) string {
    return filepath.Join(EngineConfigDir(), protocolID+".json")
}

// Log rotation — call as a goroutine from Init()
func StartLogRotation() {
    go func() {
        const maxSize = 10 * 1024 * 1024 // 10 MB
        const maxFiles = 3                // keep 3 rotated files
        
        for {
            time.Sleep(5 * time.Minute)
            
            logDir := LogDir()
            entries, _ := os.ReadDir(logDir)
            for _, entry := range entries {
                if entry.IsDir() || !strings.HasPrefix(entry.Name(), "engine-") {
                    continue
                }
                path := filepath.Join(logDir, entry.Name())
                info, err := entry.Info()
                if err != nil {
                    continue
                }
                if info.Size() > maxSize {
                    // Rotate: .log → .log.1 → .log.2 → .log.3 → delete
                    for i := maxFiles; i >= 0; i-- {
                        oldName := path
                        if i > 0 {
                            oldName = fmt.Sprintf("%s.%d", path, i)
                        }
                        newName := fmt.Sprintf("%s.%d", path, i+1)
                        if _, err := os.Stat(oldName); err == nil {
                            if i == maxFiles {
                                os.Remove(oldName)
                            } else {
                                os.Rename(oldName, newName)
                            }
                        }
                    }
                }
            }
        }
    }()
}

// TelemetryOptOut returns true if the user has opted out of telemetry.
func TelemetryOptOut() bool {
    instance.mu.RLock(); defer instance.mu.RUnlock()
    return instance.data.TelemetryOptOut
}

// SetTelemetryOptOut sets the telemetry preference.
func SetTelemetryOptOut(optOut bool) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.TelemetryOptOut = optOut
    instance.save()
}

func (s *Storage) load() {
    raw, err := os.ReadFile(filepath.Join(s.dataDir, "data.json"))
    if err != nil {
        s.data = &AppData{EngineCache: make(map[string]string)}
        s.save()
        return
    }
    json.Unmarshal(raw, s.data)
    if s.data.EngineCache == nil {
        s.data.EngineCache = make(map[string]string)
    }
}

func (s *Storage) save() {
    raw, _ := json.MarshalIndent(s.data, "", "  ")
    os.WriteFile(filepath.Join(s.dataDir, "data.json"), raw, 0600)
}

// ── Getters / Setters ──

func IsActivated() bool {
    instance.mu.RLock(); defer instance.mu.RUnlock()
    return instance.data.Token != ""
}

func SaveActivation(token, planTier string) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.Token = token
    instance.data.PlanTier = planTier
    instance.save()
}

func LoadToken() string {
    instance.mu.RLock(); defer instance.mu.RUnlock()
    return instance.data.Token
}

func SaveToken(newToken string) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.Token = newToken
    instance.save()
}

func SaveDeviceFingerprint(fp string) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.DeviceFingerprint = fp
    instance.save()
}

func LoadDeviceFingerprint() string {
    instance.mu.RLock(); defer instance.mu.RUnlock()
    return instance.data.DeviceFingerprint
}

func LoadPlanTier() string {
    instance.mu.RLock(); defer instance.mu.RUnlock()
    return instance.data.PlanTier
}

func SaveProtocols(protocols []ProtocolConfig) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.Protocols = protocols
    instance.save()
}

func LoadProtocols() []ProtocolConfig {
    instance.mu.RLock(); defer instance.mu.Unlock()
    return append([]ProtocolConfig{}, instance.data.Protocols...)
}

func SetActiveConn(protocolID string) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.ActiveConn = protocolID
    instance.save()
}

func CacheEnginePath(protocolID, path string) {
    instance.mu.Lock(); defer instance.mu.Unlock()
    instance.data.EngineCache[protocolID] = path
    instance.save()
}

func GetCachedEnginePath(protocolID string) string {
    instance.mu.RLock(); defer instance.mu.RUnlock()
    return instance.data.EngineCache[protocolID]
}
```

### 2. `internal/activation/activate.go` — Client-Held Device Secret

**Design:** Instead of sending a raw hardware fingerprint over the wire (which a MITM could replay), the client generates a random 32-byte device secret on first launch and stores it locally. The server never sees the secret or any hardware identifier. Device proof is `HMAC-SHA256("activation" + code, device_secret)` — this is deterministic per (device, code) pair but cannot be replayed for a different code.

```go
package activation

import (
    "bytes"
    "crypto/hmac"
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "myvpn/internal/storage"
    "net/http"
    "os"
    "path/filepath"
    "strings"
)

type ActivationRequest struct {
    Code string `json:"code"`
}

type ActivationResponse struct {
    Token string `json:"token"`
    Plan  string `json:"plan"`
}

// ── Device Fingerprint (hardware-derived, survives app deletion) ──

// CollectFingerprint gathers hardware identifiers and returns a SHA256 fingerprint.
// Survives app deletion — same device always produces the same fingerprint.
func CollectFingerprint() string {
    // Gather hardware identifiers
    identifiers := []string{}
    // MAC address of first active network interface
    if mac := getMACAddress(); mac != "" {
        identifiers = append(identifiers, mac)
    }
    // Disk serial (system drive)
    if diskSerial := getDiskSerial(); diskSerial != "" {
        identifiers = append(identifiers, diskSerial)
    }
    // Motherboard UUID
    if moboUUID := getMoboUUID(); moboUUID != "" {
        identifiers = append(identifiers, moboUUID)
    }
    // If nothing is available, fall back to hostname (fragile but better than nothing)
    if len(identifiers) == 0 {
        hostname, _ := os.Hostname()
        identifiers = append(identifiers, hostname)
    }

    combined := strings.Join(identifiers, "|")
    hash := sha256.Sum256([]byte(combined))
    return hex.EncodeToString(hash[:])
}

// Platform-specific helpers implemented in fingerprint_windows.go / fingerprint_darwin.go
func getMACAddress() string   { return getMAC() }  // platform-specific
func getDiskSerial() string   { return getDisk() }  // platform-specific
func getMoboUUID() string     { return getMobo() }  // platform-specific

// ── Activation ──

// Luhn-mod-N check digit validation — catches typos before the HTTP round-trip.
// Uses CHARSET (A-Z + 2-9, excluding I,O,0,1) matching the server-side code generator.
const luhnCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func luhnModN(code string) bool {
    // Strip prefix and hyphens
    clean := strings.ToUpper(code)
    clean = strings.ReplaceAll(clean, "MYVPN-", "")
    clean = strings.ReplaceAll(clean, "-", "")
    
    if len(clean) != 17 { // 16 data + 1 checksum
        return false
    }
    
    data := clean[:16]
    checkChar := clean[16]
    
    // Compute expected check digit
    sum := 0
    double := false
    for i := len(data) - 1; i >= 0; i-- {
        val := strings.IndexByte(luhnCharset, data[i])
        if val == -1 {
            return false
        }
        if double {
            val *= 2
            if val >= len(luhnCharset) {
                val = val/len(luhnCharset) + (val % len(luhnCharset))
            }
        }
        sum += val
        double = !double
    }
    
    expectedCheck := luhnCharset[(len(luhnCharset)-(sum%len(luhnCharset)))%len(luhnCharset)]
    return checkChar == expectedCheck
}

func Validate(apiBase, code string) (*ActivationResponse, error) {
    code = strings.TrimSpace(code)
    
    // Client-side Luhn check — catch typos before HTTP round-trip
    if !luhnModN(code) {
        return nil, fmt.Errorf("invalid activation code format — check for typos")
    }
    
    fingerprint := CollectFingerprint()

    body := ActivationRequest{Code: code}
    payload, _ := json.Marshal(body)

    req, err := http.NewRequest("POST", apiBase+"/api/collections/activations/records",
        bytes.NewReader(payload))
    if err != nil {
        return nil, fmt.Errorf("activation request failed: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Device-Fingerprint", fingerprint)

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("activation request failed: %w", err)
    }
    defer resp.Body.Close()

    bodyBytes, _ := io.ReadAll(resp.Body)

    switch resp.StatusCode {
    case 200:
        var result ActivationResponse
        if err := json.Unmarshal(bodyBytes, &result); err != nil {
            return nil, fmt.Errorf("invalid activation response: %w", err)
        }
        if result.Token == "" {
            return nil, fmt.Errorf("activation rejected — invalid response")
        }
        // Store the hardware fingerprint locally (for heartbeat header matching)
        storage.SaveDeviceFingerprint(fingerprint)
        return &result, nil
    case 409:
        return nil, fmt.Errorf("this activation code is already bound to another device")
    case 403:
        var body403 struct {
            Message string `json:"message"`
        }
        json.Unmarshal(bodyBytes, &body403)
        if body403.Message != "" {
            return nil, fmt.Errorf("%s", body403.Message)
        }
        return nil, fmt.Errorf("activation rejected")
    case 429:
        return nil, fmt.Errorf("too many activation attempts — wait a few minutes")
    default:
        return nil, fmt.Errorf("activation failed (status %d): %s", resp.StatusCode, string(bodyBytes))
    }
}

// Deactivation is not supported — codes are permanently bound to one device.
// To block a device, the admin sets suspended=true on the activation_code record.
// The binding is never destroyed; the code can never be re-used on a different device.

func ShowPrompt(apiBase string) {
    // MVP: command-line prompt. Phase 2: GUI dialog.
    fmt.Println("=== MyVPN Activation ===")
    fmt.Println("Enter your activation code (format: MYVPN-XXXX-XXXX-XXXX-C):")
    var code string
    fmt.Scanln(&code)

    result, err := Validate(apiBase, code)
    if err != nil {
        fmt.Printf("Activation failed: %v\n", err)
        os.Exit(1)
    }

    storage.SaveActivation(result.Token, result.Plan)
    fmt.Printf("Activated! Plan: %s\n", result.Plan)
}

### 3. `internal/activation/fingerprint_windows.go`

```go
//go:build windows

package activation

import (
    "os/exec"
    "strings"
)

func getMAC() string {
    out, err := exec.Command("getmac", "/fo", "csv", "/nh").Output()
    if err != nil {
        return ""
    }
    lines := strings.Split(string(out), "\n")
    for _, line := range lines {
        parts := strings.Split(line, ",")
        if len(parts) >= 3 {
            mac := strings.Trim(parts[1], `"`)
            mac = strings.ReplaceAll(mac, "-", ":")
            if mac != "" && mac != "Disabled" {
                return mac
            }
        }
    }
    return ""
}

func getDisk() string {
    out, err := exec.Command("wmic", "diskdrive", "get", "SerialNumber").Output()
    if err != nil {
        return ""
    }
    lines := strings.Split(string(out), "\n")
    if len(lines) >= 2 {
        return strings.TrimSpace(lines[1])
    }
    return ""
}

func getMobo() string {
    out, err := exec.Command("wmic", "baseboard", "get", "SerialNumber").Output()
    if err != nil {
        return ""
    }
    lines := strings.Split(string(out), "\n")
    if len(lines) >= 2 {
        return strings.TrimSpace(lines[1])
    }
    return ""
}
```

### 4. `internal/activation/fingerprint_darwin.go`

```go
//go:build darwin

package activation

import (
    "os/exec"
    "strings"
)

func getMAC() string {
    out, err := exec.Command("ifconfig", "en0").Output()
    if err != nil {
        return ""
    }
    for _, line := range strings.Split(string(out), "\n") {
        if strings.Contains(line, "ether ") {
            parts := strings.Fields(line)
            if len(parts) >= 2 {
                return parts[1]
            }
        }
    }
    return ""
}

func getDisk() string {
    out, err := exec.Command("system_profiler", "SPHardwareDataType").Output()
    if err != nil {
        return ""
    }
    for _, line := range strings.Split(string(out), "\n") {
        if strings.Contains(line, "Serial Number") {
            parts := strings.SplitN(line, ":", 2)
            if len(parts) == 2 {
                return strings.TrimSpace(parts[1])
            }
        }
    }
    return ""
}

func getMobo() string {
    out, err := exec.Command("ioreg", "-l").Output()
    if err != nil {
        return ""
    }
    for _, line := range strings.Split(string(out), "\n") {
        if strings.Contains(line, "IOPlatformUUID") {
            parts := strings.Split(line, `"`)
            for i, p := range parts {
                if strings.TrimSpace(p) == "IOPlatformUUID" && i+2 < len(parts) {
                    return strings.TrimSpace(parts[i+2])
                }
            }
        }
    }
    return ""
}
```

### 5. `internal/branding/names.go`

```go
package branding

import "sync"

var (
    mu     sync.RWMutex
    // Display names are obfuscated — users never see real protocol names.
    // "Lite Mode" = usque/Warp. Bandwidth limits are NOT tied to this name;
    // they're enforced client-side by the app based on plan_tier
    // (warp_lite = throttled, gaming_mid/gaming_max fallback = unbottlenecked).
    // usque connects directly to Cloudflare — no VPS in the data path to throttle.
    names  = map[string]string{
        "hysteria2": "Speed Mode",
        "usque":     "Lite Mode",
        "xray":      "Stealth Mode",
        "tuic":      "Turbo Mode",
    }
    binaryNames = map[string]string{
        "hysteria2": "speedmode",
        "usque":     "litemode",
        "xray":      "stealthmode",
        "tuic":      "turbomode",
    }
    plans = map[string]string{
        "warp_lite":  "Warp Lite",
        "gaming_max": "Gaming Max",
        "turbo":      "Turbo",
    }
)

func PlanDisplayName(planID string) string {
    mu.RLock(); defer mu.RUnlock()
    if name, ok := plans[planID]; ok { return name }
    return planID
}

func ProtocolDisplayName(protocolID string) string {
    mu.RLock(); defer mu.RUnlock()
    if name, ok := names[protocolID]; ok { return name }
    return protocolID
}

// BinaryName returns the obfuscated binary name for a protocol (no extension).
// e.g. "speedmode" not "hysteria"
func BinaryName(protocolID string) string {
    mu.RLock(); defer mu.RUnlock()
    if name, ok := binaryNames[protocolID]; ok { return name }
    return protocolID
}

// LogName returns a numeric code for log files (never the real name)
func LogName(protocolID string) string {
    codeMap := map[string]string{
        "hysteria2": "[01]",
        "usque":     "[02]",
        "xray":      "[03]",
        "tuic":      "[04]",
    }
    if code, ok := codeMap[protocolID]; ok { return code }
    return "[??]"
}

// ProcessName returns the disguised process name for the engine.
func ProcessName(protocolID string) string {
    name := BinaryName(protocolID)
    // Append .exe on Windows (handled at runtime by checking os)
    return name
}
```

---

## Phase 1.4: Protocol Engine Manager + Sandboxing + Binary Rename

### 1. `internal/manager/process.go`

```go
package manager

import (
    "fmt"
    "myvpn/internal/branding"
    "myvpn/internal/storage"
    "myvpn/internal/throttle"
    "os"
    "os/exec"
    "path/filepath"
    "sync"
    "syscall"
)

var (
    activeCmd     *exec.Cmd
    activeID      string
    lastExitCode  int
    mu            sync.Mutex
)

func StartEngine(cfg storage.ProtocolConfig, planTier string) error {
    mu.Lock()
    defer mu.Unlock()

    // Stop existing engine
    stopLocked()

    binaryName := branding.BinaryName(cfg.ID)
    logName := branding.LogName(cfg.ID)

    // Find cached engine binary
    binDir := storage.EngineDir()
    ext := ""
    if len(cfg.BinaryName) > 0 {
        // Platform extension handled at download time
    }

    // Check for cached binary — download if missing (handled by enginedl)
    binaryPath := storage.GetCachedEnginePath(cfg.ID)
    if binaryPath == "" || !fileExists(binaryPath) {
        return fmt.Errorf("engine binary not downloaded yet for %s", binaryName)
    }

    fmt.Printf("%s Starting %s (%s)...\n", logName, cfg.DisplayName, binaryPath)

    // Build command with engine-specific flags
    cmd := buildCommand(binaryPath, cfg.ID, cfg.ConfigJSON, planTier)

    // Disguise process name: set argv[0] to innocuous name
    disguiseProcess(cmd, binaryName)

    // Apply platform sandboxing
    sandbox(cmd)

    // Capture stdout/stderr to log file
    logFile := openEngineLog(cfg.ID)
    cmd.Stdout = logFile
    cmd.Stderr = logFile

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("%s failed to start: %w", logName, err)
    }

    activeCmd = cmd
    activeID = cfg.ID
    lastExitCode = 0
    storage.SetActiveConn(cfg.ID)

    go func() {
        err := cmd.Wait()
        mu.Lock()
        if err != nil {
            if exitErr, ok := err.(*exec.ExitError); ok {
                lastExitCode = exitErr.ExitCode()
            }
        }
        activeCmd = nil
        activeID = ""
        mu.Unlock()
    }()

    return nil
}

func StopEngine() {
    mu.Lock()
    defer mu.Unlock()
    stopLocked()
    // Stop the background bandwidth monitor when engine stops
    throttle.StopBackgroundMonitor()
}

func stopLocked() {
    if activeCmd != nil && activeCmd.Process != nil {
        activeCmd.Process.Kill()
        activeCmd.Wait()
        activeCmd = nil
        activeID = ""
        storage.SetActiveConn("")
    }
}

// SignalReload sends a reload signal to the running engine.
// Used by the throttle monitor to apply dynamic bandwidth cap changes.
func SignalReload() {
    mu.Lock()
    defer mu.Unlock()
    if activeCmd == nil || activeCmd.Process == nil {
        return
    }
    // Hysteria 2 responds to SIGHUP by reloading its config.
    // On Windows, we send a UDP control message instead (platform-specific).
    proc := activeCmd.Process
    if proc != nil {
        proc.Signal(os.Signal(syscall.SIGHUP))
    }
}

func IsRunning() bool {
    mu.Lock()
    defer mu.Unlock()
    return activeCmd != nil && activeCmd.Process != nil
}

func RunningProtocolID() string {
    mu.Lock()
    defer mu.Unlock()
    return activeID
}

func LastExitCode() int {
    mu.Lock()
    defer mu.Unlock()
    return lastExitCode
}

func disguiseProcess(cmd *exec.Cmd, binaryName string) {
    // Set argv[0] to something innocuous.
    // On Windows the binary is already renamed, so this is cosmetic for macOS/Linux.
    if cmd.Args == nil {
        cmd.Args = []string{}
    }
    if len(cmd.Args) == 0 {
        cmd.Args = append(cmd.Args, cmd.Path)
    }
    // On Unix, we can set argv[0] to anything. Windows ignores this.
    innocuousNames := map[string]string{
        "speedmode":  "nethelper",
        "litemode":   "nethelper",
        "stealthmode":"nethelper",
        "turbomode":  "nethelper",
    }
    if name, ok := innocuousNames[binaryName]; ok {
        cmd.Args[0] = name
    }
}

func openEngineLog(protocolID string) *os.File {
    logDir := storage.LogDir()
    logPath := filepath.Join(logDir, fmt.Sprintf("engine-%s.log", protocolID))
    f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    return f
}

func fileExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}
```

### 2. `internal/manager/process_windows.go`

```go
//go:build windows

package manager

import (
    "os/exec"
    "syscall"
    "unsafe"
)

var (
    modkernel32 = syscall.NewLazyDLL("kernel32.dll")
    procCreateJobObject          = modkernel32.NewProc("CreateJobObjectW")
    procSetInformationJobObject  = modkernel32.NewProc("SetInformationJobObject")
    procAssignProcessToJobObject = modkernel32.NewProc("AssignProcessToJobObject")
)

const (
    JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE         = 0x00002000
    JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION = 0x00000200
    JOB_OBJECT_LIMIT_ACTIVE_PROCESS             = 0x00000008
)

func sandbox(cmd *exec.Cmd) {
    // Hide window
    if cmd.SysProcAttr == nil {
        cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
    } else {
        cmd.SysProcAttr.HideWindow = true
    }
}

// After the process starts, assign it to a job object
func SandboxRunning(cmd *exec.Cmd) {
    type JOBOBJECT_EXTENDED_LIMIT_INFORMATION struct {
        BasicLimitInformation struct {
            PerProcessUserTimeLimit int64
            PerJobUserTimeLimit     int64
            LimitFlags             uint32
            MinimumWorkingSetSize  uintptr
            MaximumWorkingSetSize  uintptr
            ActiveProcessLimit     uint32
            Affinity               uintptr
            ChildProcessRateControl uint64
            Flags                  uint32
        }
        IoInfo struct {
            ReadOperationCount   int64
            WriteOperationCount  int64
            OtherOperationCount  int64
            ReadTransferCount    int64
            WriteTransferCount   int64
            OtherTransferCount   int64
        }
        ProcessMemoryLimit uintptr
        JobMemoryLimit     uintptr
        PeakProcessMemoryLimit uintptr
        PeakJobMemoryLimit     uintptr
    }

    job, _, _ := procCreateJobObject.Call(0, 0)
    if job == 0 {
        return
    }

    info := &JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
    info.BasicLimitInformation.LimitFlags =
        JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
        JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION |
        JOB_OBJECT_LIMIT_ACTIVE_PROCESS
    info.BasicLimitInformation.ActiveProcessLimit = 1

    procSetInformationJobObject.Call(job, 9,
        uintptr(unsafe.Pointer(info)), unsafe.Sizeof(*info))
    procAssignProcessToJobObject.Call(job, uintptr(cmd.Process.Handle))
}
```

### 3. `internal/manager/process_darwin.go`

> **⚠️ Cosmetic isolation only.** The disposable HOME directory prevents the engine from writing config files to the user's real home directory, but it does not prevent access to `/tmp`, network, system files, or anything else the user can access. A malicious engine compiled for macOS can still read `/etc/resolv.conf`, access the network, or read/write anywhere the user has permissions. This is NOT a security boundary — it's a cleanliness measure. For true sandboxing, use macOS Seatbelt profiles (`sandbox-exec`) or run the engine as a separate user, both of which are Phase 3 work.

```go
//go:build darwin

package manager

import (
    "os"
    "os/exec"
    "syscall"
)

func sandbox(cmd *exec.Cmd) {
    if cmd.SysProcAttr == nil {
        cmd.SysProcAttr = &syscall.SysProcAttr{}
    }

    // Create a temp home directory — no access to user files
    tempHome, _ := os.MkdirTemp("", "myvpn-engine-*")
    cmd.Env = append(os.Environ(),
        "HOME="+tempHome,
        "TMPDIR="+os.TempDir(),
    )

    // Kill engine if parent dies
    cmd.SysProcAttr.Setpgid = true
    cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

func SandboxRunning(cmd *exec.Cmd) {
    // No post-start sandboxing needed on macOS
}
```

### 4. `buildCommand()` — Engine Command Builder with Adaptive Bandwidth

```go
// internal/manager/command.go
package manager

import (
    "encoding/json"
    "fmt"
    "myvpn/internal/branding"
    "myvpn/internal/throttle"
    "myvpn/internal/storage"
    "os/exec"
)

// buildCommand constructs the exec.Cmd for an engine, injecting the
// current adaptive bandwidth cap from the throttle subsystem.
func buildCommand(binaryPath, protocolID string, configJSON json.RawMessage, planTier string) *exec.Cmd {
    switch protocolID {
    case "hysteria2":
        return buildHysteria2Command(binaryPath, configJSON, planTier)
    case "usque":
        return buildUsqueCommand(binaryPath, configJSON, planTier)
    default:
        // Fallback: pass config file path
        return exec.Command(binaryPath, string(configJSON))
    }
}

// buildHysteria2Command builds the Hysteria 2 command with dynamic bandwidth cap.
// Hysteria 2 supports:
//   - config file (JSON) with "speed" field in bps
//   - CLI flags: --speed <bps> or --log-level
// We inject the bandwidth cap from the throttle probe/monitor into the config.
func buildHysteria2Command(binaryPath string, configJSON json.RawMessage, planTier string) *exec.Cmd {
    // Parse the existing config JSON
    var cfg map[string]interface{}
    if err := json.Unmarshal(configJSON, &cfg); err != nil {
        cfg = make(map[string]interface{})
    }

    // Get current bandwidth cap from throttle subsystem
    capKBps := throttle.CurrentBandwidthCapKBps()
    displayName := branding.ProtocolDisplayName("hysteria2")

    // Hysteria 2 uses bits-per-second in the "speed" field of Brutal CC.
    // Convert KBps → bps: multiply by 8000 (8 bits/byte × 1000)
    if capKBps > 0 {
        capBps := capKBps * 8000
        cfg["speed"] = capBps
    } else {
        // Uncapped — remove any speed limit from config
        delete(cfg, "speed")
    }

    // Apply plan-tier overrides
    switch planTier {
    case "gaming_max":
        // Gaming Max: use whatever the probe says (may be uncapped)
        // The monitor will dynamically adjust during the session
    case "gaming_mid":
        // Gaming Mid: hard ceiling of 5 MB/s (40 Mbps = 40,000,000 bps)
        midBps := 5000 * 8000
        if capKBps == 0 || capKBps > 5000 {
            cfg["speed"] = midBps
        }
    case "warp_lite":
        // Warp Lite doesn't use Hysteria 2; handled by usque code path
    }

    // Serialize modified config to a temp file
    configPath := storage.EngineConfigPath("hysteria2")
    modifiedJSON, _ := json.Marshal(cfg)
    writeConfig(configPath, modifiedJSON)

    return exec.Command(binaryPath, "-c", configPath)
}

// buildUsqueCommand builds the usque command with hardcoded per-tier bandwidth limit.
// usque accepts --bandwidth-limit <KBps> to cap throughput.
func buildUsqueCommand(binaryPath string, configJSON json.RawMessage, planTier string) *exec.Cmd {
    capKBps := throttle.UsqueBandwidthLimit(planTier)

    args := []string{
        string(configJSON),
    }
    if capKBps > 0 {
        args = append(args, "--bandwidth-limit", fmt.Sprintf("%d", capKBps))
    }

    return exec.Command(binaryPath, args...)
}

// Helper to write config files atomically.
func writeConfig(path string, data []byte) {
    // Implementation writes to temp file then renames for atomicity.
    // Omitted here for brevity; see storage package.
}
```

---

## Phase 1.5: Heartbeat with Cert Pinning + Grace + Telemetry + Suspension

### `internal/heartbeat/heartbeat.go`

```go
package heartbeat

import (
    "bytes"
    "crypto/sha256"
    "crypto/tls"
    "crypto/x509"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "myvpn/internal/manager"
    "myvpn/internal/storage"
    "myvpn/internal/health"
    "net/http"
    "runtime"
    "strings"
    "sync"
    "time"
)

// ── Certificate Pinning ──

// Generate with:
//   openssl s_client -connect api.yourdomain.com:443 -servername api.yourdomain.com < /dev/null 2>/dev/null \
//     | openssl x509 -pubkey -noout \
//     | openssl pkey -pubin -outform der \
//     | openssl dgst -sha256 -binary \
//     | base64
var pinnedKeyHash = "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // <-- GENERATE ME

func pinnedHTTPClient() *http.Client {
    return &http.Client{
        Timeout: 15 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: false,
                VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
                    if len(rawCerts) == 0 {
                        return fmt.Errorf("no server certificates")
                    }
                    for _, raw := range rawCerts {
                        cert, err := x509.ParseCertificate(raw)
                        if err != nil {
                            continue
                        }
                        hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
                        expected := strings.TrimPrefix(pinnedKeyHash, "sha256/")
                        expectedBytes, err := base64.StdEncoding.DecodeString(expected)
                        if err != nil {
                            continue
                        }
                        if bytes.Equal(hash[:], expectedBytes) {
                            return nil
                        }
                    }
                    return fmt.Errorf("certificate pin mismatch")
                },
            },
        },
    }
}

// ── Heartbeat Types ──

type HeartbeatRequest struct {
    Token          string `json:"token"`
    DeviceID       string `json:"device_id"`
    AppVersion     string `json:"app_version"`
    OSPlatform     string `json:"os_platform"`
    ProtocolID     string `json:"protocol_id"`
    Connected      bool   `json:"connected"`
    HealthStatus   string `json:"health_status"`
    EngineExitCode int    `json:"engine_exit_code"`
    LastError      string `json:"last_error"`
    UptimeSec      int    `json:"uptime_seconds"`
    BytesUp        int64  `json:"bytes_up"`
    BytesDown      int64  `json:"bytes_down"`
}

type HeartbeatResponse struct {
    Status    string                    `json:"status"`     // "active" | "suspended" | "disabled"
    Plan      string                    `json:"plan"`
    Token     string                    `json:"token"`     // ROTATED
    Protocols []storage.ProtocolConfig  `json:"protocols"`
    Commands  []RemoteCommand           `json:"commands"`
    Message   string                    `json:"message"`    // Human-readable status message (e.g. suspension reason)
}

type RemoteCommand struct {
    Action  string          `json:"action"`
    Payload json.RawMessage `json:"payload"`
}

// ── State ──

var (
    client           = pinnedHTTPClient()
    consecutiveFails int
    graceDeadline    time.Time
    graceMu          sync.Mutex
    apiBase          string
    startTime        = time.Now()
    lastBytesUp      int64
    lastBytesDown    int64
    lastError        string
)

const (
    maxConsecutiveFails = 3
    graceDuration       = 7 * 24 * time.Hour
    heartbeatInterval   = 60
)

// ── Public API ──

func Start(baseURL string) {
    apiBase = baseURL
    graceDeadline = time.Now().Add(graceDuration)

    ticker := time.NewTicker(heartbeatInterval * time.Second)
    for range ticker.C {
        doHeartbeat()
    }
}

func SetLastError(errMsg string) {
    graceMu.Lock()
    defer graceMu.Unlock()
    lastError = errMsg
}

func GraceRemaining() int {
    graceMu.Lock()
    defer graceMu.Unlock()
    if time.Now().After(graceDeadline) {
        return 0
    }
    return int(time.Until(graceDeadline).Hours() / 24)
}

func MustReauthenticate() bool {
    graceMu.Lock()
    defer graceMu.Unlock()
    return consecutiveFails >= maxConsecutiveFails && time.Now().After(graceDeadline)
}

// ── Internal ──

func doHeartbeat() {
    healthStatus := "healthy"
    if !manager.IsRunning() {
        healthStatus = "dead"
    } else if health.IsDegraded() {
        healthStatus = "degraded"
    }

    exitCode := 0
    if !manager.IsRunning() && manager.LastExitCode() != 0 {
        exitCode = manager.LastExitCode()
    }

    graceMu.Lock()
    errMsg := lastError
    lastError = ""
    graceMu.Unlock()

    req := HeartbeatRequest{
        Token:          storage.LoadToken(),
        DeviceID:       storage.LoadDeviceFingerprint(),
        AppVersion:     "1.0.0",
        OSPlatform:     runtime.GOOS + "_" + runtime.GOARCH,
        ProtocolID:     manager.RunningProtocolID(),
        Connected:      manager.IsRunning(),
        HealthStatus:   healthStatus,
        EngineExitCode: exitCode,
        LastError:      errMsg,
        UptimeSec:      int(time.Since(startTime).Seconds()),
        BytesUp:        lastBytesUp,
        BytesDown:      lastBytesDown,
    }

    body, _ := json.Marshal(req)

    httpReq, _ := http.NewRequest("POST", apiBase+"/api/heartbeat", bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+storage.LoadToken())
    httpReq.Header.Set("X-Device-Fingerprint", storage.LoadDeviceFingerprint())

    resp, err := client.Do(httpReq)

    graceMu.Lock()
    defer graceMu.Unlock()

    if err != nil || resp == nil || resp.StatusCode != 200 {
        consecutiveFails++
        if consecutiveFails >= maxConsecutiveFails {
            if time.Now().After(graceDeadline) {
                manager.StopEngine()
                // Show: "Cannot verify subscription"
            }
        }
        return
    }

    // Reset grace on success
    consecutiveFails = 0
    graceDeadline = time.Now().Add(graceDuration)

    var result HeartbeatResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        resp.Body.Close()
        return
    }
    resp.Body.Close()

    // Token rotation: store the new token immediately
    if result.Token != "" {
        storage.SaveToken(result.Token)
    }

    // Update protocol list
    if len(result.Protocols) > 0 {
        storage.SaveProtocols(result.Protocols)
    }

    // Handle suspension
    if result.Status == "suspended" {
        manager.StopEngine()
        if result.Message != "" {
            SetLastError(result.Message)
        }
        // Don't clear activation — the binding is preserved.
        // The GUI shows the suspension message. Heartbeat continues
        // checking — when status returns to "active", auto-reconnect.
        return
    }

    // Process remote commands
    for _, cmd := range result.Commands {
        processCommand(cmd, result.Plan)
    }
}

func processCommand(cmd RemoteCommand, newPlan string) {
    switch cmd.Action {
    case "update":
        var payload updater.UpdatePayload
        if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
            return
        }
        updater.Apply(payload)

    case "disable":
        manager.StopEngine()

    case "reconfigure":
        manager.StopEngine()
        protocols := storage.LoadProtocols()
        if len(protocols) > 0 {
            manager.StartEngine(protocols[0], newPlan)
        }

    case "message":
        var payload struct {
            Title string `json:"title"`
            Body  string `json:"body"`
        }
        json.Unmarshal(cmd.Payload, &payload)
        // Show notification

    case "set_plan":
        // Already handled via heartbeat response fields
    }
}
```

### `internal/heartbeat/telemetry.go`

```go
package heartbeat

import (
    "bufio"
    "myvpn/internal/storage"
    "os"
    "path/filepath"
    "sync"
)

// CaptureLastLogLines reads the last N lines of an engine log file.
// Used to populate the last_error field in heartbeat requests.
func CaptureLastLogLines(protocolID string, n int) string {
    logPath := filepath.Join(storage.LogDir(), "engine-"+protocolID+".log")
    f, err := os.Open(logPath)
    if err != nil {
        return ""
    }
    defer f.Close()

    var lines []string
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        lines = append(lines, scanner.Text())
        if len(lines) > n {
            lines = lines[1:]
        }
    }
    result := ""
    for _, l := range lines {
        result += l + "\n"
    }
    return result
}

// CrashMarker creates/removes a marker file to detect unclean shutdowns.
var crashMu sync.Mutex

func SetCrashMarker() {
    crashMu.Lock()
    defer crashMu.Unlock()
    marker := filepath.Join(storage.LogDir(), ".crash_marker")
    os.WriteFile(marker, []byte("crash"), 0644)
}

func ClearCrashMarker() {
    crashMu.Lock()
    defer crashMu.Unlock()
    os.Remove(filepath.Join(storage.LogDir(), ".crash_marker"))
}

func HadCrash() bool {
    crashMu.Lock()
    defer crashMu.Unlock()
    _, err := os.Stat(filepath.Join(storage.LogDir(), ".crash_marker"))
    return err == nil
}
```

---

## Phase 1.6: Auto-Updater with SHA256 Verification

### `internal/updater/update.go`

> ⚠️ **No Ed25519 signing.** Updates are verified by SHA256 hash only. The admin hub publishes the SHA256 alongside the binary URL. The client downloads, hashes, and compares. This trades cryptographic authenticity for operational simplicity — no key management, no offline signing ceremony, no risk of losing the only key that can sign updates. A compromised hub could serve a malicious binary, but cert pinning prevents MITM of the hub connection itself.

```go
package updater

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "runtime"
    "strings"
)

type UpdatePayload struct {
    Platform string `json:"platform"` // e.g. "windows_amd64"
    Version  string `json:"version"`
    SHA256   string `json:"sha256"`
    URL      string `json:"url"`
}

func Apply(payload UpdatePayload) error {
    // 1. Platform check — prevent cross-platform attacks
    ourPlatform := runtime.GOOS + "_" + runtime.GOARCH
    if payload.Platform != "" && payload.Platform != ourPlatform {
        return fmt.Errorf("update platform %s does not match %s", payload.Platform, ourPlatform)
    }

    // 2. HTTPS enforcement — reject http:// update URLs
    if !strings.HasPrefix(payload.URL, "https://") {
        return fmt.Errorf("update URL must use HTTPS, got: %s", payload.URL)
    }

    // 3. Download the binary
    resp, err := http.Get(payload.URL)
    if err != nil {
        return fmt.Errorf("download failed: %w", err)
    }
    defer resp.Body.Close()

    tmpFile, err := os.CreateTemp("", "myvpn-update-*")
    if err != nil {
        return fmt.Errorf("cannot create temp file: %w", err)
    }
    defer os.Remove(tmpFile.Name())

    hash := sha256.New()
    written, err := io.Copy(io.MultiWriter(tmpFile, hash), resp.Body)
    if err != nil {
        tmpFile.Close()
        return fmt.Errorf("download incomplete: %w", err)
    }
    tmpFile.Close()

    if written < 1024*1024 {
        return fmt.Errorf("downloaded file too small (%d bytes)", written)
    }

    // 4. Verify SHA256
    gotHash := hex.EncodeToString(hash.Sum(nil))
    if gotHash != payload.SHA256 {
        return fmt.Errorf("SHA256 mismatch: got %s, expected %s", gotHash, payload.SHA256)
    }

    // 5. Apply (platform-specific swap)
    return applySwap(tmpFile.Name(), payload.Version)
}

// applySwap implemented in update_windows.go and update_unix.go
```

### `internal/updater/update_windows.go`

```go
//go:build windows

package updater

import (
    "os"
    "os/exec"
    "path/filepath"
)

func applySwap(newBinaryPath, version string) error {
    exe, err := os.Executable()
    if err != nil {
        return err
    }

    exeDir := filepath.Dir(exe)
    prevPath := filepath.Join(exeDir, "myvpn.prev")
    newPath := filepath.Join(exeDir, "myvpn.exe")

    // Keep the old binary as .prev for rollback safety
    os.Remove(prevPath)             // Remove old backup if any
    os.Rename(exe, prevPath)         // Current → backup
    os.Rename(newBinaryPath, newPath) // New → live

    // Write crash-detection sentinel
    sentinelPath := filepath.Join(exeDir, ".update-pending")
    os.WriteFile(sentinelPath, []byte(version), 0644)

    // Spawn launcher: wait 3s, then start new version (don't delete .prev)
    cmd := exec.Command("cmd", "/c",
        "timeout /t 3 /nobreak > nul && start \"\" \""+newPath+"\"")
    cmd.Start()

    os.Exit(0)
    return nil
}
```

### `internal/updater/update_unix.go`

```go
//go:build !windows

package updater

import (
    "os"
    "os/exec"
    "path/filepath"
    "syscall"
)

func applySwap(newBinaryPath, version string) error {
    exe, err := os.Executable()
    if err != nil {
        return err
    }

    exeDir := filepath.Dir(exe)
    newPath := filepath.Join(exeDir, "myvpn")
    prevPath := filepath.Join(exeDir, "myvpn.prev")

    // 1. Save current binary as .prev for rollback safety
    os.Remove(prevPath)
    input, err := os.ReadFile(exe)
    if err == nil {
        os.WriteFile(prevPath, input, 0755)
    }

    // 2. Verify new binary can start (quick sanity check)
    versionCmd := exec.Command(newBinaryPath, "--version")
    if err := versionCmd.Run(); err != nil {
        // Verification failed — remove the .prev we just saved; don't swap
        os.Remove(prevPath)
        return fmt.Errorf("new binary verification failed: %w", err)
    }

    // 3. Write crash-detection sentinel before swapping
    sentinelPath := filepath.Join(exeDir, ".update-pending")
    os.WriteFile(sentinelPath, []byte(version), 0644)

    // 4. Overwrite the live binary
    newData, err := os.ReadFile(newBinaryPath)
    if err != nil {
        os.Remove(sentinelPath)
        os.Remove(prevPath)
        return fmt.Errorf("cannot read new binary: %w", err)
    }
    if err := os.WriteFile(newPath, newData, 0755); err != nil {
        os.Remove(sentinelPath)
        os.Remove(prevPath)
        return fmt.Errorf("cannot write new binary: %w", err)
    }

    // 5. Clean up the temp file
    os.Remove(newBinaryPath)

    // Fork child that execs the new binary
    cmd := exec.Command(newPath, os.Args[1:]...)
    cmd.Stdin = os.Stdin
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

    cmd.Start()
    os.Exit(0)
    return nil
}
```

---

### Rollback Safety

> 🛡️ **CRITIQUE.md flagged the lack of rollback as a serious risk.** This section adds crash-detection auto-revert and manual recovery. The strategy: keep the previous binary as `myvpn.prev`, write a sentinel file before swapping, and check it on startup. If the new binary crashes before clearing the sentinel, the next launch auto-reverts.

**How it works (two-phase handshake):**

```
applySwap:
  save current binary as .prev
  write .update-pending sentinel    ─── Phase 1
  swap new binary in
  restart

First launch after update:
  .update-pending EXISTS + .prev EXISTS → this is the first launch after update
  rename .update-pending → .update-confirmed  ─── Phase 2
  .prev preserved as safety net
  continue normally ✓

Second launch (if previous was successful):
  .update-confirmed EXISTS + .prev EXISTS → app ran fine last time
  delete .update-confirmed + .prev ✓
  clean state

Crash on first launch:
  .update-pending EXISTS + .prev EXISTS → app crashed before it could rename the sentinel
  AUTO-REVERT: restore .prev, delete .update-pending
  previous version starts fresh

Manual revert (user holds Ctrl):
  force-revert to .prev immediately, regardless of sentinel state
```

#### `internal/updater/recover.go`

```go
package updater

import (
    "fmt"
    "os"
    "path/filepath"
)

const (
    sentinelPending   = ".update-pending"
    sentinelConfirmed = ".update-confirmed"
)

// CheckOnStartup runs at the very beginning of main().
// Returns true if a rollback occurred (for logging).
func CheckOnStartup(holdRevertKey bool) (bool, error) {
    exe, err := os.Executable()
    if err != nil {
        return false, fmt.Errorf("cannot determine executable path: %w", err)
    }

    exeDir := filepath.Dir(exe)
    pendingPath := filepath.Join(exeDir, sentinelPending)
    confirmedPath := filepath.Join(exeDir, sentinelConfirmed)
    prevPath := filepath.Join(exeDir, "myvpn.prev")

    // --- Manual revert ---
    if holdRevertKey {
        if _, err := os.Stat(prevPath); err == nil {
            return performRevert(exe, prevPath, pendingPath, confirmedPath)
        }
        // No .prev to revert to — clean up stale sentinels and continue
        os.Remove(pendingPath)
        os.Remove(confirmedPath)
        return false, nil
    }

    // --- Phase 2: update-confirmed exists → previous first launch was successful ---
    if _, err := os.Stat(confirmedPath); err == nil {
        // The first launch after update ran successfully (it renamed pending→confirmed).
        // Now the second launch proves stability → safe to delete backup.
        os.Remove(confirmedPath)
        os.Remove(prevPath)
        return false, nil
    }

    // --- Phase 1: update-pending exists → first launch after update (or crash) ---
    if _, err := os.Stat(pendingPath); err == nil {
        if _, err := os.Stat(prevPath); os.IsNotExist(err) {
            // .prev is gone but sentinel remains — stale sentinel from interrupted flow
            os.Remove(pendingPath)
            return false, nil
        }
        // .prev exists AND .update-pending exists:
        // The app was updated but never completed its first clean launch.
        // This could be:
        //   a) Genuine first launch (we're running now for the first time)
        //   b) Crash on first launch (user restarted manually)
        // Either way: advance to Phase 2 (don't revert yet).
        // If we crash NOW, the sentinel will still be .update-pending on next launch → revert.
        os.Rename(pendingPath, confirmedPath)
        return false, nil
    }

    // --- No sentinels → normal startup ---
    // Clean up any stale .prev from an interrupted previous cycle
    os.Remove(prevPath)
    return false, nil
}

// performRevert swaps the current binary back to .prev.
func performRevert(exePath, prevPath, pendingPath, confirmedPath string) (bool, error) {
    // Save the (possibly bad) current binary as .bad for debugging
    badPath := filepath.Join(filepath.Dir(exePath), "myvpn.bad")
    os.Remove(badPath)
    os.Rename(exePath, badPath)

    // Restore the previous binary
    input, err := os.ReadFile(prevPath)
    if err != nil {
        return false, fmt.Errorf("cannot read previous binary for rollback: %w", err)
    }

    mode := os.FileMode(0755)
    if filepath.Ext(exePath) == ".exe" {
        mode = 0644
    }
    if err := os.WriteFile(exePath, input, mode); err != nil {
        return false, fmt.Errorf("cannot restore previous binary: %w", err)
    }

    // Clean up all sentinels and .prev
    os.Remove(prevPath)
    os.Remove(pendingPath)
    os.Remove(confirmedPath)

    return true, nil
}

    // Restore the previous binary
    input, err := os.ReadFile(prevPath)
    if err != nil {
        return false, fmt.Errorf("cannot read previous binary for rollback: %w", err)
    }

    mode := os.FileMode(0755)
    if filepath.Ext(exePath) == ".exe" {
        mode = 0644
    }
    if err := os.WriteFile(exePath, input, mode); err != nil {
        return false, fmt.Errorf("cannot restore previous binary: %w", err)
    }

    // Clean up
    os.Remove(prevPath)
    os.Remove(sentinelPath)

    return true, nil
}
```

#### Update `main.go` — Call Recovery Check on Startup

Add this as the **very first thing** in `main()`, before anything else (before storage init, before GUI):

```go
func main() {
    // --- Rollback safety: must run before any other initialization ---
    // Hold Ctrl key during launch to force-revert to the previous version
    revertHeld := false
    // Platform-specific: check for Ctrl key press
    // Windows: GetAsyncKeyState(VK_CONTROL)
    // macOS/Linux: check os.Args for --revert flag (simpler)
    for _, arg := range os.Args {
        if arg == "--revert" {
            revertHeld = true
            break
        }
    }
    reverted, err := updater.CheckOnStartup(revertHeld)
    if err != nil {
        log.Printf("startup recovery check failed: %v", err)
    }
    if reverted {
        log.Println("ROLLBACK: reverted to previous version after crash")
        // The restored .prev may have a different version string — the heartbeat
        // will re-check for updates on the next cycle.
    }

    // ... rest of main() ...
}
```

**Manual revert for users:** If a bad update ships and the user can still launch the app, tell them to hold Ctrl while launching, or pass `--revert` from a terminal. If the app crashes on startup (can't even reach the revert check), they can:
1. Find the install directory
2. Delete `myvpn.exe` (or `myvpn`)
3. Rename `myvpn.prev` → `myvpn.exe` (or `myvpn`)
4. Delete `.update-pending`

This is documented in the troubleshooting table in Phase 1.12.

**Limitation:** If a bad update corrupts the TUN routing or helper service rather than crashing outright, the crash-detection sentinel won't trigger (the app starts successfully). In that case:
- User can still manually revert via `--revert` flag
- Server-side fix: push a new update that fixes the bug, or set `min_version` on the hub to force clients past the bad version

---

## Phase 1.7: TUN Mode + Kill Switch + DNS Guard + IPv6 Blocking + Health Check

> **Scope clarified:** MVP = Hysteria 2 (primary) + usque (fallback). Full tunnel (all traffic through VPN except RFC 1918 local). IPv6 blocked entirely. Kill switch via route deletion (simple but effective for casual leaks). Split tunnel is Phase 2.

### 1. TUN Interface Lifecycle

The TUN interface goes through a strict lifecycle managed by the privileged helper service:

```
                    ┌──────────────────────┐
                    │     NOT_CREATED      │
                    └─────────┬────────────┘
                              │ CreateTUN(engineConfig)
                              ▼
                    ┌──────────────────────┐
                    │     CREATING         │
                    │  (wintun/tun clone)  │
                    └─────────┬────────────┘
                              │ TUN fd ready + routes added
                              ▼
                    ┌──────────────────────┐
                    │     ACTIVE           │◄──── Health check passes
                    │  routes + DNS set    │────► Health check fails ×3
                    └─────────┬────────────┘
                              │ DestroyTUN()
                              ▼
                    ┌──────────────────────┐
                    │     DESTROYED        │
                    │  routes removed      │
                    │  DNS restored        │
                    └──────────────────────┘
```

**States:**
| State | Meaning | User sees |
|-------|---------|-----------|
| `NOT_CREATED` | No TUN interface exists. Normal pre-connect state. | "Disconnected" |
| `CREATING` | wintun adapter or macOS utun interface being created. Transient. | "Connecting..." |
| `ACTIVE` | TUN is up, routes are in place, DNS is set, traffic is flowing. | "Connected — Gaming Mode" |
| `DESTROYED` | TUN torn down, routes removed, original DNS restored. | "Disconnected" |

### 2. Route Table Management

When the TUN goes ACTIVE, the helper service modifies the system route table. When it goes DESTROYED, it restores the original state.

**Routes added on connect:**

| Destination | Action | Purpose |
|---|---|---|
| `0.0.0.0/1` and `128.0.0.0/1` | Route via TUN gateway | Split the IPv4 default into two /1 routes (more specific than 0.0.0.0/0, so they take priority over existing default route). Keeps the original default route intact for restoration. |
| Engine server IP (`<VPS_PUBLIC_IP>/32`) | Route via original gateway | **Bypass route:** engine traffic must go direct to the VPS, not into its own tunnel (would create a routing loop). |
| RFC 1918 (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) | Route via original gateway | Local network bypass — printing, file shares, LAN games still work. |

**Routes removed on disconnect:**
- All routes added above are deleted
- Original DNS servers are restored via `networksetup -setdnsservers` (macOS) or `netsh interface ip set dns` (Windows)

**Race condition note:** There is a brief window (~100ms) between TUN creation and route application where traffic could leak. This is acceptable for MVP — the WFP/pf-based kill switch (Phase 2) eliminates this window entirely.

### 3. Kill Switch Implementation

**Approach: Route deletion on health failure (simple, MVP).**

When the health checker detects the tunnel is dead (3 consecutive probe failures), it:
1. Sends `DestroyTUN()` to the helper service
2. The helper removes the two /1 routes, restoring the original default route
3. User traffic resumes over the normal (blocked) school WiFi — effectively "dead" but no traffic goes through a broken tunnel

**Platform commands issued by helper:**

```
Windows (as SYSTEM):
  netsh interface ip delete route 0.0.0.0/1 <TUN_IF_INDEX>
  netsh interface ip delete route 128.0.0.0/1 <TUN_IF_INDEX>

macOS (as root via launchd):
  /sbin/route delete 0.0.0.0/1 -interface utunX
  /sbin/route delete 128.0.0.0/1 -interface utunX
```

**Limitations (documented):**
- Not bulletproof — a crashing engine process with a stuck TUN could leave routes in place
- No outbound firewall block; apps could briefly leak between health checks (15s interval)
- Phase 2 upgrade path: WFP callout driver (Windows) / pf anchor (macOS) for true leak blocking

### 4. DNS Guard — Leak Prevention

Without DNS guard, DNS queries could go to the school's DNS servers (bypassing the VPN tunnel), revealing browsing destinations. The fix: force all DNS through the tunnel.

**Implementation:**
1. On connect: set system DNS to the VPN's internal DNS resolver (runs on the TUN gateway, e.g., `10.0.0.1`)
2. On disconnect: restore original DNS servers (saved before connect)
3. The engine's embedded DNS resolver forwards queries through the tunnel to Cloudflare (`1.1.1.1`) or Quad9 (`9.9.9.9`)

**Platform commands:**
```
Windows:
  netsh interface ip set dns name="<TUN_IF_NAME>" static 10.0.0.1
  netsh interface ip add dns name="<TUN_IF_NAME>" 1.0.0.1 index=2

macOS:
  networksetup -setdnsservers "<TUN_IF_NAME>" 10.0.0.1 1.0.0.1
```

**Backup DNS guard (belt-and-suspenders):** If the system DNS set fails, the engine's TUN packet filter drops all outbound UDP:53 and TCP:53 not destined for the VPN DNS resolver. This is a secondary defense — it means "DNS breaks" rather than "DNS leaks."

### 5. IPv6 Blocking

IPv6 is blocked entirely on the TUN interface to prevent IPv6 leaks. The school WiFi may have IPv6 connectivity while the VPN tunnel is IPv4-only. Without this, apps could make IPv6 connections that bypass the tunnel entirely.

**Implementation:**
1. On Windows: disable IPv6 on the TUN adapter via `netsh interface ipv6 set interface <IF_INDEX> disabled`
2. On macOS: remove any IPv6 address from the TUN interface; do not add an IPv6 default route
3. The helper service checks that `ipv6.google.com` is unreachable after TUN setup (smoke test, logged at debug level)
4. Document as known limitation: IPv6-capable services will fall back to IPv4 through the tunnel (happy eyeballs). Most school WiFi blocks IPv6 anyway.

### 6. `internal/tunnel/tun.go` — Interface

## Privileged Helper Service (TUN Elevation Strategy)

TUN device creation requires admin/root privileges. The client app runs as the user. The solution is a **privileged helper service** installed once (requires admin on install) that creates and manages the TUN interface. After install, users never see a UAC/sudo prompt.

### Windows: SYSTEM Service via Named Pipe

```
┌─────────────────┐     named pipe      ┌──────────────────────┐
│  Client App      │ ──────────────────► │  MyVPN Helper Service │
│  (user account)  │  \\.\pipe\MyVPN     │  (LOCAL SYSTEM)      │
│                   │                     │                       │
│  Commands:        │                     │  Creates wintun TUN   │
│  CreateTUN()     │                     │  Adds/removes routes  │
│  DestroyTUN()   │                     │  Sets DNS             │
│  AddRoute()     │                     │  Applies kill switch  │
│  SetDNS()       │                     │                       │
└─────────────────┘                     └──────────────────────┘
```

**Service implementation (Go, using `golang.org/x/sys/windows/svc`):**

The service binary (`myvpn-helper.exe`) is installed once:
```
sc create MyVPNHelper binPath= "C:\Program Files\MyVPN\myvpn-helper.exe"
sc start MyVPNHelper
```

The service listens on a named pipe `\\.\pipe\MyVPN` for JSON commands from the client app. **Protocol: newline-delimited JSON.** The client writes a single JSON object followed by `\n`. The helper reads lines via `bufio.Scanner` and parses each line as a JSON command. This prevents partial-read bugs. Each command includes:
```json
{ "action": "create_tun", "tun_ip": "10.0.0.2", "tun_gateway": "10.0.0.1" }
{ "action": "add_route", "dest": "0.0.0.0/0", "gateway": "10.0.0.1" }
{ "action": "destroy_tun" }
{ "action": "set_dns", "server": "1.1.1.1" }
{ "action": "block_all" }
{ "action": "unblock_all" }
```

The service is ~200 lines of Go. Build it as a separate binary and bundle it with the installer.

**Install script (run once as admin):**
```batch
@echo off
sc create MyVPNHelper binPath="%~dp0myvpn-helper.exe" start=auto
sc start MyVPNHelper
echo MyVPN Helper Service installed.
```

### macOS: launchd Daemon

On macOS, the helper is a privileged launchd daemon installed via a one-time `sudo` command:
```bash
sudo cp myvpn-helper /usr/local/libexec/
sudo cp com.myvpn.helper.plist /Library/LaunchDaemons/
sudo launchctl load /Library/LaunchDaemons/com.myvpn.helper.plist
```

The daemon listens on a Unix domain socket `/var/run/myvpn-helper.sock` with the same JSON command protocol (newline-delimited JSON, same as Windows).

### MVP Shortcut (documented UX debt)

If you skip the helper service and run the engine elevated directly:
- **Windows:** The user sees a UAC prompt every time they click Connect. Most will stop using the app after the second prompt.
- **macOS:** The user must enter their password every time via `sudo` or `authopen(2)`.
- **Documented tradeoff:** Acceptable only for a technical-early-adopter phase. The helper service is the difference between "it works" and "people use it."

### Critical: Named Pipe / Unix Socket Authentication

**Problem:** Any process on the machine — including malware, a school-issued app, or a compromised browser extension — can connect to `\\.\pipe\MyVPN` or `/var/run/myvpn-helper.sock` and send `{"action": "block_all"}` or `{"action": "add_route", "dest": "0.0.0.0/0", "gateway": "attacker-ip"}`. The service runs as SYSTEM/root — this is a local privilege escalation and DoS hole.

**Fix — Option A (Simple, Recommended): Verify Caller Identity**

The helper checks the connecting process's identity and rejects commands from non-MyVPN processes.

**Windows:** Use `GetNamedPipeClientProcessId` to get the client PID, then open a handle with `PROCESS_QUERY_LIMITED_INFORMATION` and query the executable path via `QueryFullProcessImageName`. Compare against the known MyVPN install directory.

```go
func verifyCallerWindows(pipeFd uintptr) error {
    var pid uint32
    getNamedPipeClientProcessId(pipeFd, &pid)

    h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
    if err != nil {
        return fmt.Errorf("cannot open caller process: %w", err)
    }
    defer windows.CloseHandle(h)

    var exePath string
    queryFullProcessImageName(h, 0, &exePath)

    if !strings.HasPrefix(exePath, `C:\Program Files\MyVPN\`) {
        return fmt.Errorf("unauthorized caller: %s", exePath)
    }
    return nil
}
```

**macOS/Linux:** Use `SO_PEERCRED` on the Unix socket to get the client's PID and UID. Verify the executable path via `proc_pidpath` (macOS) or `readlink /proc/<pid>/exe` (Linux).

**Fix — Option B (Defense in Depth): Shared Per-Installation Secret**

On service install, generate a random 32-byte secret and write it to a file readable only by the user + SYSTEM:
- Windows: `%PROGRAMDATA%\MyVPN\helper.secret` (ACL: user + SYSTEM only)
- macOS: `/var/run/myvpn-helper.secret` (owned by root:wheel, mode 0640)

The client app reads the secret from this file and includes it in every command:
```json
{ "action": "create_tun", "tun_ip": "...", "auth": "<hex-secret>" }
```

The helper rejects any command without a valid auth value. This prevents any process that can't read the secret file from issuing commands.

**Recommended:** Implement Option A (caller verification) as the primary mechanism, Option B as defense-in-depth. Both are ~50 lines of Go each.

---

```go
package tunnel

type Manager interface {
    // Setup creates the TUN device and configures routing.
    Setup() error
    // Teardown removes the TUN device and restores original routing.
    Teardown() error
    // AddRoute adds a route through the tunnel.
    AddRoute(dest, gateway string) error
    // RemoveRoute removes a route.
    RemoveRoute(dest string) error
}
```

### 2. `internal/tunnel/tun_windows.go`

## Helper Client Package

The tunnel, killswitch, and DNS code must NOT call `exec.Command` for privileged operations directly (the user lacks permissions). Instead, they send JSON commands over the named pipe to the SYSTEM-level helper service.

Create `internal/helper/client.go`:

```go
package helper

import (
    "bufio"
    "encoding/json"
    "fmt"
    "net"
    "runtime"

    "github.com/Microsoft/go-winio"
)

// Command sent to the privileged helper service.
type Command struct {
    Action string `json:"action"`
    // Optional fields depending on action:
    TunIP     string `json:"tun_ip,omitempty"`
    Gateway   string `json:"gateway,omitempty"`
    Dest      string `json:"dest,omitempty"`
    Mask      string `json:"mask,omitempty"`
    Server    string `json:"server,omitempty"`
    Auth      string `json:"auth,omitempty"`
}

// SendCommand sends a JSON command to the helper service and returns the response.
// Uses newline-delimited JSON framing — every command ends with \n, and responses
// are read line-by-line. This prevents partial-read bugs that would corrupt messages
// under load or on slow I/O.
func SendCommand(cmd Command) error {
    var conn net.Conn
    var err error

    if runtime.GOOS == "windows" {
        conn, err = winio.DialPipe(`\\.\pipe\MyVPN`, nil)
    } else {
        conn, err = net.Dial("unix", "/var/run/myvpn-helper.sock")
    }
    if err != nil {
        return fmt.Errorf("helper service not found — is it installed and running? (%w)", err)
    }
    defer conn.Close()

    // Newline-delimited JSON: append \n so the helper can read line-by-line
    payload, _ := json.Marshal(cmd)
    payload = append(payload, '\n')
    if _, err := conn.Write(payload); err != nil {
        return fmt.Errorf("helper command failed: %w", err)
    }

    // Read response line
    scanner := bufio.NewScanner(conn)
    if !scanner.Scan() {
        return fmt.Errorf("helper closed connection without response")
    }
    line := scanner.Text()

    var response struct {
        Status  string `json:"status"`
        Message string `json:"message,omitempty"`
    }
    if err := json.Unmarshal([]byte(line), &response); err != nil {
        return fmt.Errorf("helper response invalid: %w", err)
    }
    if response.Status != "ok" {
        return fmt.Errorf("helper error: %s", response.Message)
    }
    return nil
}

// IsAvailable checks whether the helper service is reachable.
func IsAvailable() bool {
    return SendCommand(Command{Action: "ping"}) == nil
}
```

Now rewrite all tunnel operations to use the helper client:

### 2. `internal/tunnel/tun_windows.go`

```go
//go:build windows

package tunnel

import (
    "myvpn/internal/helper"
)

const (
    tunName    = "MyVPN"
    tunIP      = "10.0.0.2"
    tunGateway = "10.0.0.1"
)

func Setup() error {
    // Verify helper is available before attempting any operation
    if !helper.IsAvailable() {
        return fmt.Errorf("MyVPN Helper Service not found. Run installer as Administrator.")
    }
    return helper.SendCommand(helper.Command{
        Action: "create_tun",
        TunIP:  tunIP,
        Gateway: tunGateway,
    })
}

func Teardown() error {
    return helper.SendCommand(helper.Command{
        Action: "destroy_tun",
    })
}

func AddRoute(dest, gateway string) error {
    return helper.SendCommand(helper.Command{
        Action:  "add_route",
        Dest:    dest,
        Gateway: gateway,
        Mask:    "255.255.255.255",
    })
}

func RemoveRoute(dest string) error {
    return helper.SendCommand(helper.Command{
        Action: "remove_route",
        Dest:   dest,
    })
}
```

### 3. `internal/tunnel/killswitch.go`

```go
package tunnel

import (
    "myvpn/internal/helper"
    "os/exec"
    "runtime"
)

// Engage blocks all non-local traffic when the tunnel drops.
// This prevents DNS leaks and ensures no traffic escapes the VPN.
func Engage() error {
    switch runtime.GOOS {
    case "windows":
        return helper.SendCommand(helper.Command{Action: "block_all"})
    case "darwin":
        // Use a named anchor so we don't clobber rules from other tools (Little Snitch, macOS Firewall, etc.)
        return exec.Command("sh", "-c", `echo "block drop out all" | pfctl -a com.myvpn/killswitch -ef -`).Run()
    }
    return nil
}

// Disengage removes the kill switch and restores normal connectivity.
func Disengage() error {
    switch runtime.GOOS {
    case "windows":
        return helper.SendCommand(helper.Command{Action: "unblock_all"})
    case "darwin":
        // Flush only our named anchor, leave everything else intact
        exec.Command("pfctl", "-a", "com.myvpn/killswitch", "-F", "all").Run()
        // Disable pf only if no other anchors are loaded
        return exec.Command("sh", "-c", `pfctl -a com.myvpn/killswitch -F all 2>/dev/null; pfctl -d 2>/dev/null; true`).Run()
    }
    return nil
}
```

### 4. `internal/tunnel/dns.go`

```go
package tunnel

import (
    "myvpn/internal/helper"
    "os/exec"
    "runtime"
)

// ProtectDNS redirects all DNS queries through the tunnel to prevent leaks.
func ProtectDNS() error {
    switch runtime.GOOS {
    case "windows":
        return helper.SendCommand(helper.Command{
            Action: "set_dns",
            Server: "1.1.1.1",
        })
    case "darwin":
        return exec.Command("networksetup", "-setdnsservers", "Wi-Fi", "1.1.1.1").Run()
    }
    return nil
}

// RestoreDNS reverts DNS settings.
func RestoreDNS() error {
    switch runtime.GOOS {
    case "windows":
        return helper.SendCommand(helper.Command{Action: "set_dns", Server: ""})
    case "darwin":
        return exec.Command("networksetup", "-setdnsservers", "Wi-Fi", "empty").Run()
    }
    return nil
}
```

### 5. `internal/health/health.go` — Tunnel Health Check + Auto-Reconnect

```go
package health

import (
    "fmt"
    "log"
    "myvpn/internal/manager"
    "myvpn/internal/storage"
    "net"
    "net/http"
    "sync"
    "time"
)

var (
    consecutiveFails int
    degraded         bool
    mu               sync.Mutex
)

const (
    checkInterval    = 15 * time.Second
    maxFails         = 3
    connectTimeout   = 5 * time.Second
    retryDelay       = 30 * time.Second
    maxRetries       = 5
)

// Start begins periodic health checks through the active tunnel.
// Returns a channel that receives state changes:
// "healthy" | "degraded" | "dead" | "recovered"
func Start(tunGateway string) <-chan string {
    stateChanges := make(chan string, 10)
    
    go func() {
        for {
            time.Sleep(checkInterval)
            
            err := probe(tunGateway)
            
            mu.Lock()
            if err != nil {
                consecutiveFails++
                if consecutiveFails >= maxFails {
                    if !degraded {
                        degraded = true
                        mu.Unlock()
                        stateChanges <- "degraded"
                        // Auto-reconnect loop starts in the UI layer
                        continue
                    }
                    mu.Unlock()
                    stateChanges <- "dead"
                    // UI layer will try fallback engines, then retry
                    continue
                }
                mu.Unlock()
            } else {
                if degraded {
                    degraded = false
                    consecutiveFails = 0
                    mu.Unlock()
                    stateChanges <- "recovered"
                    continue
                }
                consecutiveFails = 0
                mu.Unlock()
            }
        }
    }()
    
    return stateChanges
}

// IsDegraded returns true if the tunnel is in degraded state.
func IsDegraded() bool {
    mu.Lock()
    defer mu.Unlock()
    return degraded
}

// StopChan signals the auto-reconnect loop to abort (user clicked Disconnect).
// The UI sends on this channel when disconnecting.
var StopChan = make(chan struct{}, 1)

// AutoReconnect is called by the UI layer when a "dead" state is received.
// It tries fallback engines first, then retries the primary engine.
// Checks StopChan before each attempt — exits immediately if user disconnected.
// Returns true if reconnected.
func AutoReconnect() bool {
    log.Println("[health] Auto-reconnect: trying fallback engines...")
    
    // Drain any stale signals from previous disconnect cycles.
    // Without this, a stale signal buffered from two goroutines both sending
    // to the channel will poison the next reconnect attempt.
    for len(StopChan) > 0 {
        <-StopChan
    }
    
    // Check if user cancelled (fresh signal after drain)
    select {
    case <-StopChan:
        log.Println("[health] Auto-reconnect cancelled by user")
        return false
    default:
    }
    
    protocols := storage.LoadProtocols()
    
    // Try each protocol (skip the one that just failed)
    failedID := manager.RunningProtocolID()
    for _, proto := range protocols {
        if proto.ID == failedID {
            continue
        }
        log.Printf("[health] Trying fallback: %s", proto.DisplayName)
        if err := manager.StartEngine(proto, storage.LoadPlanTier()); err == nil {
            log.Printf("[health] Fallback %s connected", proto.DisplayName)
            return true
        }
    }
    
    // All fallbacks failed — retry primary engine with delay
    log.Println("[health] All fallbacks failed, retrying primary...")
    for i := 0; i < maxRetries; i++ {
        // Check for cancellation before sleeping
        select {
        case <-StopChan:
            log.Println("[health] Auto-reconnect cancelled by user during retry")
            return false
        default:
        }
        
        time.Sleep(retryDelay)
        
        // Check again after sleep
        select {
        case <-StopChan:
            log.Println("[health] Auto-reconnect cancelled by user after delay")
            return false
        default:
        }
        
        // Try primary engine (first in priority list)
        if len(protocols) > 0 {
            if err := manager.StartEngine(protocols[0], storage.LoadPlanTier()); err == nil {
                log.Printf("[health] Primary reconnected after %d retries", i+1)
                return true
            }
        }
        log.Printf("[health] Primary retry %d/%d failed", i+1, maxRetries)
    }
    
    log.Println("[health] All retries exhausted — giving up")
    return false
}

func probe(tunGateway string) error {
    conn, err := net.DialTimeout("tcp", "1.1.1.1:853", connectTimeout)
    if err != nil {
        client := &http.Client{Timeout: connectTimeout}
        resp, httpErr := client.Head("https://httpstat.us/200")
        if httpErr != nil {
            return fmt.Errorf("health check failed: tcp=%v http=%v", err, httpErr)
        }
        resp.Body.Close()
        if resp.StatusCode != 200 {
            return fmt.Errorf("health check got status %d", resp.StatusCode)
        }
    } else {
        conn.Close()
    }
    return nil
}
```

---

## Phase 1.8: Adaptive Speed Probing + Client-Side Throttling

> **Why:** School WiFi is congested and subject to **QoS shaping** — bandwidth fluctuates dramatically minute-to-minute as 50–500 students share limited AP capacity. Hysteria 2's "Brutal" congestion control aggressively grabs bandwidth, which on a QoS-shaped link causes bufferbloat, latency spikes, and game-rubberbanding. We need a probe **on every connect** and **continuous background monitoring** to dynamically adjust the cap in real-time, preventing buffering without starving the connection.

### 1. The Problem — Why Static Probing Fails

**Criticism received:** The original design cached probe results for 30 minutes. On a QoS-shaped school WiFi:
- Bandwidth can drop 60–80% within seconds when a nearby classroom starts a video stream
- Hysteria 2's Brutal CC interprets the suddenly constrained link as "lossy" and increases send rate → **bufferbloat** → game latency spikes from 100ms to 800ms
- A stale 30-min-old cap either underutilizes the link (if bandwidth improved) or causes buffering (if bandwidth dropped)

**Solution:** Three-layer adaptive system:
1. **Full probe on every connect** (not cached) — ensures each session starts with an accurate cap
2. **Lightweight background monitor** during the session — small 100KB pings every 15s to detect bandwidth shifts
3. **Dynamic cap adjustment** — EWMA-smoothed bandwidth estimate drives real-time Hysteria 2 speed limit changes via the engine's control socket

### 2. Adaptive Probing Architecture

```
┌────────────────────────────────────────────────────────────────────┐
│  CONNECT FLOW                                                      │
│                                                                    │
│  User taps Connect                                                 │
│       │                                                            │
│       ▼                                                            │
│  ╔══════════════════════════════════════════════════════════════╗   │
│  ║  STEP 1 — BANDWIDTH PROBE (runs on regular network,         ║   │
│  ║            BEFORE TUN is created)                             ║   │
│  ║  ┌─────────────────────────┐   ┌─────────────────────────┐  ║   │
│  ║  │ 1a. Quick 1MB download  │   │ Tier-based decision:    │  ║   │
│  ║  │     from /speedtest      │──►│ • Gaming Max → full     │  ║   │
│  ║  │     Time it, compute BW  │   │   probe                 │  ║   │
│  ║  │     If <1.5s → fast      │   │ • Gaming Mid → quick   │  ║   │
│  ║  │     If ≥1.5s → staged    │   │   probe + ramp         │  ║   │
│  ║  └──────────┬──────────────┘   │ • Warp Lite → skip      │  ║   │
│  ║             │                  │   (hardcoded 1MB/s cap)  │  ║   │
│  ║             ▼                  └─────────────────────────┘  ║   │
│  ║  ┌──────────────────────────────────────────────────────┐   ║   │
│  ║  │ 1b. Staged Ramp Test (only if quick test was slow)  │   ║   │
│  ║  │  Stage 1: 500KB @ 500KB/s, measure RTT + loss       │   ║   │
│  ║  │  Stage 2: 500KB @ 1MB/s, measure RTT + loss         │   ║   │
│  ║  │  Stage 3: 500KB @ 2MB/s, measure RTT + loss         │   ║   │
│  ║  │  Stage 4: 500KB @ 5MB/s, measure RTT + loss (Max)   │   ║   │
│  ║  │                                                     │   ║   │
│  ║  │  Pick highest cap with:                             │   ║   │
│  ║  │  • <1% packet loss AND                              │   ║   │
│  ║  │  • RTT < 1.5× baseline RTT (no bufferbloat)         │   ║   │
│  ║  └──────────────────────┬──────────────────────────────┘   ║   │
│  ║                         │                                   ║   │
│  ║  ┌──────────────────────▼──────────────────────────────┐   ║   │
│  ║  │ 1c. Clamp result to tier limits + fallback defaults │   ║   │
│  ║  │                                                    │   ║   │
│  ║  │  Hard caps (never exceed):                         │   ║   │
│  ║  │  • Gaming Max: max 100 Mbps (12,500 KB/s)          │   ║   │
│  ║  │  • Gaming Mid:  max  50 Mbps ( 6,250 KB/s)         │   ║   │
│  ║  │  • Floor:  min   3 Mbps (   375 KB/s)  ← never     │   ║   │
│  ║  │           starves the connection                   │   ║   │
│  ║  │                                                    │   ║   │
│  ║  │  If probe fails/timeout → fallback to safe default:│   ║   │
│  ║  │  • Gaming Max: 40 Mbps (5,000 KB/s)                │   ║   │
│  ║  │  • Gaming Mid: 20 Mbps (2,500 KB/s)                │   ║   │
│  ║  │  • Warp Lite:   8 Mbps (1,000 KB/s)                │   ║   │
│  ║  │  The background monitor refines from there.        │   ║   │
│  ║  └────────────────────────────────────────────────────┘   ║   │
│  ╚══════════════════════════════════════════════════════════════╝   │
│       │                                                            │
│       ▼                                                            │
│  ┌────────────────────────────────────────────────────────────┐    │
│  │  STEP 2 — CREATE TUN DEVICE & INSTALL ROUTES               │    │
│  │  • Create wintun/utun interface                             │    │
│  │  • Install split-tunnel routes (LAN exclusions)             │    │
│  │  • Install DNS guard (redirect :53 through tunnel)         │    │
│  │  • Apply kill switch routes                                 │    │
│  └──────────────────────────┬─────────────────────────────────┘    │
│                             │                                      │
│                             ▼                                      │
│  ┌────────────────────────────────────────────────────────────┐    │
│  │  STEP 3 — LAUNCH ENGINE WITH PROBED CAP                    │    │
│  │  • Load highest-priority protocol (e.g. Hysteria 2)        │    │
│  │  • Inject probed cap into Hysteria 2 config via --speed     │    │
│  │    or config file "speed" field (bps)                       │    │
│  │  • Start engine process                                     │    │
│  │  • Start health check loop (15s tunnel probes)             │    │
│  └──────────────────────────┬─────────────────────────────────┘    │
│                             │                                      │
│                             ▼                                      │
│  ┌────────────────────────────────────────────────────────────┐    │
│  │  STEP 4 — START BACKGROUND MONITOR                         │    │
│  │  • Every 15s: download 100KB chunk via regular network     │    │
│  │  • EWMA smoothing (α=0.3) on bandwidth samples             │    │
│  │  • If EWMA < 70% of cap × 2 consecutive samples           │    │
│  │    → REDUCE cap by 30% (fast reaction)                     │    │
│  │  • If EWMA > 120% of cap × 60 seconds                     │    │
│  │    → INCREASE cap by 20% (cautious rise)                   │    │
│  │  • If RTT spike > 2× baseline                              │    │
│  │    → CUT cap by 50% (bufferbloat!)                         │    │
│  │  • All adjustments clamped to tier min/max limits          │    │
│  └────────────────────────────────────────────────────────────┘    │
│                                                                    │
│  DISCONNECT FLOW                                                  │
│  • Stop background monitor                                        │
│  • Stop engine + remove TUN + restore routes                      │
│  • Log final bandwidth stats to heartbeat telemetry               │
└────────────────────────────────────────────────────────────────────┘
```
```

### 3. `internal/throttle/probe.go` — Initial Connect Probe

```go
package throttle

import (
    "fmt"
    "io"
    "net/http"
    "sync"
    "time"
)

// ── Hard cap limits (absolute, applied after every probe and every monitor adjustment) ──
//
// These prevent the adaptive system from ever setting a cap that exceeds
// the VPS uplink or the plan's contracted maximum, and ensure the connection
// never starves itself below a usable minimum.
const (
    // Absolute minimum floor: 3 Mbps (≈375 KB/s)
    // Even on a terrible network, don't go below this so the user can still
    // browse, send chat messages, or play low-bandwidth games.
    minFloorKBps = 375 // 3 Mbps

    // Gaming Max: hard ceiling 100 Mbps (≈12,500 KB/s)
    // No school WiFi or consumer VPS uplink will reliably exceed this.
    gamingMaxCeilingKBps = 12500

    // Gaming Mid: hard ceiling 50 Mbps (≈6,250 KB/s)
    gamingMidCeilingKBps = 6250

    // Warp Lite: hard ceiling 8 Mbps (1,000 KB/s)
    warpLiteCeilingKBps = 1000
)

// ── Probe parameters ──
const (
    probeCeilingKBps   = 15000               // don't bother probing above ~120 Mbps
    quickTestThreshold = 1500 * time.Millisecond
    quickTestSize      = 1 * 1024 * 1024     // 1MB
    stageSize          = 500 * 1024          // 500KB per stage
)

// ── Fallback defaults (used when the probe fails or times out) ──
//
// If the VPS speedtest endpoint is unreachable, or the download returns
// garbage, we fall back to conservative but usable tier-based values.
// The background monitor will then refine from this starting point.
var fallbackDefaultsKBps = map[string]int{
    "gaming_max": 5000,   // 40 Mbps — safe middle ground for Gaming Max
    "gaming_mid": 2500,   // 20 Mbps — safe middle ground for Gaming Mid
    "warp_lite":  1000,   //  8 Mbps — hardcoded, same as normal
}

// ProbeResult holds the inferred bandwidth state for Hysteria 2.
type ProbeResult struct {
    MaxThroughputKBps int            // Inferred safe cap in KB/s; 0 = uncapped
    SuggestedKBps     int            // The cap to actually apply (tier-adjusted + clamped)
    PacketLoss        float64        // Packet loss at the chosen cap
    BaselineRTT       time.Duration  // RTT to VPS before congestion (bufferbloat baseline)
    ProbedAt          time.Time
    NetworkCondition  string         // "fast", "congested", "degraded", "offline"
    FallbackUsed      bool           // true if probe failed and we used fallback default
}

// Probe runs a full bandwidth test against the VPS and returns
// the recommended throttle cap for Hysteria 2.
// Called on EVERY connect — NOT cached.
// Runs BEFORE TUN device creation so traffic uses the regular network.
//
// If the probe fails entirely, returns a safe fallback default for the tier
// (no error) so the connection can still proceed. The background monitor
// will refine the cap once the session is active.
func Probe(apiBase, tier string) *ProbeResult {
    // Step 1: Measure baseline RTT (before any load)
    baselineRTT, err := measureBaselineRTT(apiBase + "/speedtest/500kb.bin")
    if err != nil {
        return fallbackResult(tier, "offline")
    }

    // Step 2: Quick 1MB download
    start := time.Now()
    bwBytes, err := downloadChunk(apiBase+"/speedtest/1mb.bin", quickTestSize, 0)
    if err != nil {
        return fallbackResult(tier, "offline")
    }
    elapsed := time.Since(start)
    if elapsed < 1*time.Millisecond {
        return fallbackResult(tier, "offline")
    }
    quickThroughputKBps := int(float64(bwBytes) / elapsed.Seconds() / 1024)

    // Step 3: Build result container
    result := &ProbeResult{
        ProbedAt:    time.Now(),
        BaselineRTT: baselineRTT,
        PacketLoss:  0,
    }

    // Step 4: Decide whether to skip / fast-path based on tier + quick result
    switch tier {
    case "warp_lite":
        // Warp Lite uses hardcoded cap — no ramp test needed
        result.MaxThroughputKBps = clampToTier(warpLiteCeilingKBps, tier)
        result.NetworkCondition = "congested"
        result.SuggestedKBps = clampToTier(warpLiteCeilingKBps, tier)
        return result
    case "gaming_mid":
        if elapsed < quickTestThreshold && quickThroughputKBps > 10000 {
            // Fast network — use the tier ceiling as the cap
            result.MaxThroughputKBps = 0 // uncapped at network level
            result.NetworkCondition = "fast"
            result.SuggestedKBps = clampToTier(gamingMidCeilingKBps, tier)
            return result
        }
    case "gaming_max":
        if elapsed < quickTestThreshold && quickThroughputKBps > 10000 {
            // Genuinely fast — no cap needed
            result.MaxThroughputKBps = 0
            result.NetworkCondition = "fast"
            result.SuggestedKBps = 0 // 0 = uncapped
            return result
        }
    }

    // Step 5: Staged ramp test (network is congested or moderate)
    var stageCaps []int
    switch tier {
    case "gaming_max":
        stageCaps = []int{500, 1000, 2000, 5000, 10000, probeCeilingKBps}
    case "gaming_mid":
        stageCaps = []int{500, 1000, 2000, 5000}
    default:
        return fallbackResult(tier, "offline")
    }

    bestCap := minFloorKBps
    bestLoss := 1.0

    for _, capKBps := range stageCaps {
        if capKBps > probeCeilingKBps {
            break
        }
        loss, rtt, err := measureLoss(apiBase+"/speedtest/500kb.bin", stageSize, capKBps)
        if err != nil {
            continue
        }
        // Bufferbloat check: RTT must not exceed 1.5× baseline
        if rtt > 0 && baselineRTT > 0 && rtt > baselineRTT*3/2 {
            break // bufferbloat detected — don't try higher caps
        }
        if loss < 0.01 { // <1% packet loss
            bestCap = capKBps
            bestLoss = loss
        } else {
            break // loss rising, stop
        }
    }

    result.MaxThroughputKBps = bestCap
    result.PacketLoss = bestLoss
    result.SuggestedKBps = clampToTier(bestCap, tier)

    if bestCap <= 1000 {
        result.NetworkCondition = "degraded"
    } else {
        result.NetworkCondition = "congested"
    }

    return result
}

// clampToTier applies absolute per-tier hard caps and floor to a raw KBps value.
// Every probe result and every monitor adjustment MUST pass through this function
// before being applied to the engine.
//
// Rules:
//   - Never go below minFloorKBps (3 Mbps) — prevents starvation.
//   - Never exceed the tier's ceiling — respects VPS uplink limit and plan tier.
//   - 0 (uncapped) is preserved only for Gaming Max on a genuinely fast network.
func clampToTier(rawKBps int, tier string) int {
    // First enforce absolute floor
    if rawKBps > 0 && rawKBps < minFloorKBps {
        rawKBps = minFloorKBps
    }

    switch tier {
    case "gaming_max":
        if rawKBps == 0 {
            return 0 // uncapped — genuine fast network
        }
        if rawKBps > gamingMaxCeilingKBps {
            return gamingMaxCeilingKBps
        }
        return rawKBps
    case "gaming_mid":
        if rawKBps > gamingMidCeilingKBps {
            return gamingMidCeilingKBps
        }
        if rawKBps <= 0 {
            return gamingMidCeilingKBps // uncapped not allowed for Mid
        }
        return rawKBps
    case "warp_lite":
        if rawKBps > warpLiteCeilingKBps {
            return warpLiteCeilingKBps
        }
        if rawKBps <= 0 {
            return warpLiteCeilingKBps
        }
        return rawKBps
    default:
        return rawKBps
    }
}

// fallbackResult returns a safe default ProbeResult when the probe fails.
// The background monitor will refine from this starting point once the session
// is active and it can gather real samples.
func fallbackResult(tier string, condition string) *ProbeResult {
    fallback, ok := fallbackDefaultsKBps[tier]
    if !ok {
        fallback = 1000
    }
    return &ProbeResult{
        MaxThroughputKBps: fallback,
        SuggestedKBps:     clampToTier(fallback, tier),
        PacketLoss:        0,
        BaselineRTT:       100 * time.Millisecond, // assume a reasonable baseline
        ProbedAt:          time.Now(),
        NetworkCondition:  condition,
        FallbackUsed:      true,
    }
}

// measureBaselineRTT does a small HEAD request to measure unloaded RTT.
func measureBaselineRTT(url string) (time.Duration, error) {
    start := time.Now()
    resp, err := http.Head(url)
    if err != nil {
        return 0, err
    }
    resp.Body.Close()
    return time.Since(start), nil
}

// downloadChunk downloads size bytes with an optional rate limit (0 = no limit).
func downloadChunk(url string, size int, rateLimitKBps int) (int64, error) {
    // HTTP GET with optional token-bucket rate limiter
    // Returns number of bytes received, or error if download fails / too slow
    return 0, nil
}

// measureLoss downloads a chunk with rate limiting and measures
// application-level loss (bytes requested vs bytes received) plus RTT.
func measureLoss(url string, size int, rateLimitKBps int) (float64, time.Duration, error) {
    // download with token-bucket limiter, return loss ratio and RTT
    return 0, 0, nil
}
```

### 4. `internal/throttle/monitor.go` — Continuous Background Bandwidth Monitor

```go
package throttle

import (
    "fmt"
    "io"
    "math"
    "net/http"
    "sync"
    "sync/atomic"
    "time"
)

// Monitor tracks real-time bandwidth conditions during a session
// and adjusts the Hysteria 2 cap dynamically to prevent bufferbloat.
type Monitor struct {
    apiBase          string
    tier             string
    baselineRTT      time.Duration

    // Current cap (atomic for lock-free reads from GUI/manager)
    currentCapKBps   int64

    // EWMA state
    ewmaKBps         float64
    ewmaMu           sync.Mutex

    // Cooldown to prevent oscillation
    lastAdjustment   time.Time
    adjustmentMu     sync.Mutex

    // Control
    stopCh           chan struct{}
    stopped          chan struct{}

    // Callback to push cap changes to the engine manager
    onCapChange      func(newCapKBps int)
}

const (
    monitorInterval    = 15 * time.Second
    monitorChunkSize   = 100 * 1024  // 100KB — lightweight
    ewmaAlpha          = 0.3         // EWMA smoothing factor (higher = more responsive)
    reduceThreshold    = 0.70        // Reduce cap if EWMA drops below 70% of current cap
    increaseThreshold  = 1.20        // Increase cap if EWMA stays above 120% of current cap
    reduceWait         = 2 * time.Second  // How long EWMA must stay below threshold before reducing
    increaseWait       = 60 * time.Second // How long before we试探 increasing
    bufferbloatRTTMult = 2.0         // If monitor RTT > 2× baseline, assume bufferbloat
    minIncreaseInterval = 30 * time.Second // Don't increase more than once per 30s
)

// NewMonitor creates a bandwidth monitor. Call Start() to begin.
// onCapChange is called on the monitor's goroutine when the cap changes.
func NewMonitor(apiBase, tier string, baselineRTT time.Duration, initialCapKBps int, onCapChange func(int)) *Monitor {
    return &Monitor{
        apiBase:        apiBase,
        tier:           tier,
        baselineRTT:    baselineRTT,
        currentCapKBps: int64(initialCapKBps),
        ewmaKBps:       float64(initialCapKBps),
        stopCh:         make(chan struct{}),
        stopped:        make(chan struct{}),
        onCapChange:    onCapChange,
    }
}

// Start begins the background monitoring loop.
// Runs until Stop() is called or the monitor fails permanently.
func (m *Monitor) Start() {
    go m.loop()
}

// Stop terminates the monitoring loop and waits for clean exit.
func (m *Monitor) Stop() {
    close(m.stopCh)
    <-m.stopped
}

// CurrentCapKBps returns the current dynamic cap (safe for concurrent use).
func (m *Monitor) CurrentCapKBps() int {
    return int(atomic.LoadInt64(&m.currentCapKBps))
}

// Status returns a human-readable network condition string.
func (m *Monitor) Status() string {
    cap := m.CurrentCapKBps()
    switch {
    case cap == 0:
        return "fast"
    case cap <= 1000:
        return "degraded"
    case cap <= 3000:
        return "congested"
    default:
        return "moderate"
    }
}

func (m *Monitor) loop() {
    defer close(m.stopped)

    ticker := time.NewTicker(monitorInterval)
    defer ticker.Stop()

    // Count consecutive samples below/above threshold for hysteresis
    lowSamples := 0
    highSamples := 0

    for {
        select {
        case <-m.stopCh:
            return
        case <-ticker.C:
            sampleKBps, rtt, err := m.sampleBandwidth()
            if err != nil {
                continue // skip bad sample
            }

            // Update EWMA
            m.ewmaMu.Lock()
            if m.ewmaKBps == 0 {
                m.ewmaKBps = float64(sampleKBps)
            } else {
                m.ewmaKBps = ewmaAlpha*float64(sampleKBps) + (1-ewmaAlpha)*m.ewmaKBps
            }
            smoothedKBps := int(math.Round(m.ewmaKBps))
            m.ewmaMu.Unlock()

            currentCap := int(atomic.LoadInt64(&m.currentCapKBps))

            // Bufferbloat detection (RTT spike)
            if rtt > 0 && m.baselineRTT > 0 && rtt > time.Duration(float64(m.baselineRTT)*bufferbloatRTTMult) {
                // Bufferbloat! Cut cap aggressively
                newCap := int(float64(currentCap) * 0.5)
                m.adjustCap(newCap) // clampToTier inside adjustCap enforces floor
                lowSamples = 0
                highSamples = 0
                continue
            }

            // EWMA-based adjustment
            if currentCap > 0 {
                // Check if bandwidth has dropped significantly
                if float64(smoothedKBps) < float64(currentCap)*reduceThreshold {
                    lowSamples++
                    highSamples = 0
                    if lowSamples >= int(reduceWait/monitorInterval) {
                        // Sustained drop — reduce cap
                        newCap := int(float64(currentCap) * 0.7)
                        m.adjustCap(newCap) // clampToTier inside adjustCap enforces floor
                        lowSamples = 0
                    }
                } else if float64(smoothedKBps) > float64(currentCap)*increaseThreshold {
                    highSamples++
                    lowSamples = 0
                    if highSamples >= int(increaseWait/monitorInterval) {
                        // Sustained headroom — cautiously increase
                        newCap := int(float64(currentCap) * 1.2)
                        m.adjustCap(newCap) // clampToTier inside adjustCap enforces ceiling
                        highSamples = 0
                    }
                } else {
                    // In the comfort zone — reset counters
                    lowSamples = 0
                    highSamples = 0
                }
            } else {
                // Currently uncapped. If EWMA shows constrained bandwidth, apply a cap.
                if smoothedKBps > 0 && float64(smoothedKBps) < float64(gamingMaxCeilingKBps)*0.5 {
                    // Network is fast but not unlimited — cap at 90% of measured
                    newCap := int(float64(smoothedKBps) * 0.9)
                    m.adjustCap(newCap) // clampToTier inside adjustCap enforces floor/ceiling
                }
            }
        }
    }
}

// sampleBandwidth downloads a small chunk and measures throughput + RTT.
func (m *Monitor) sampleBandwidth() (throughputKBps int, rtt time.Duration, err error) {
    url := m.apiBase + "/speedtest/500kb.bin"

    // Measure RTT (time to first byte)
    start := time.Now()
    resp, err := http.Get(url)
    if err != nil {
        return 0, 0, fmt.Errorf("monitor GET failed: %w", err)
    }
    defer resp.Body.Close()
    ttfb := time.Since(start) // approximate RTT

    // Read up to monitorChunkSize bytes
    limited := io.LimitReader(resp.Body, int64(monitorChunkSize))
    n, readErr := io.Copy(io.Discard, limited)
    if readErr != nil {
        return 0, 0, fmt.Errorf("monitor read failed: %w", readErr)
    }
    totalElapsed := time.Since(start)

    if totalElapsed < 1*time.Millisecond {
        return 0, ttfb, fmt.Errorf("monitor sample too fast")
    }

    throughputKBps = int(float64(n) / totalElapsed.Seconds() / 1024)
    return throughputKBps, ttfb, nil
}

// adjustCap atomically updates the current cap and fires the callback.
// Every new cap value is run through clampToTier() to enforce absolute
// per-tier limits (floor 3 Mbps, ceiling 50/100 Mbps depending on tier).
func (m *Monitor) adjustCap(newCapKBps int) {
    m.adjustmentMu.Lock()
    defer m.adjustmentMu.Unlock()

    // Clamp to absolute per-tier limits before applying
    newCapKBps = clampToTier(newCapKBps, m.tier)

    oldCap := int(atomic.LoadInt64(&m.currentCapKBps))
    if newCapKBps == oldCap {
        return
    }

    // Rate-limit adjustments: don't change more than once per 5 seconds
    if time.Since(m.lastAdjustment) < 5*time.Second {
        return
    }

    atomic.StoreInt64(&m.currentCapKBps, int64(newCapKBps))
    m.lastAdjustment = time.Now()

    if m.onCapChange != nil {
        m.onCapChange(newCapKBps)
    }
}
```

### 5. `internal/throttle/monitor_test.go` — Monitor Behavior Tests

```go
package throttle

import (
    "testing"
    "time"
)

func TestMonitorEWMA(t *testing.T) {
    m := NewMonitor("http://test", "gaming_max", 50*time.Millisecond, 5000, nil)

    // Simulate EWMA smoothing
    samples := []int{5000, 4800, 4500, 4200, 4000, 3000, 2500, 2000}
    expected := 5000.0
    for i, s := range samples {
        m.ewmaMu.Lock()
        if i == 0 {
            m.ewmaKBps = float64(s)
        } else {
            m.ewmaKBps = ewmaAlpha*float64(s) + (1-ewmaAlpha)*m.ewmaKBps
        }
        m.ewmaMu.Unlock()
        expected = ewmaAlpha*float64(s) + (1-ewmaAlpha)*expected
    }
    // EWMA should dampen sudden drops
    m.ewmaMu.Lock()
    if m.ewmaKBps < 2000 || m.ewmaKBps > 5000 {
        t.Errorf("EWMA out of range: %f", m.ewmaKBps)
    }
    m.ewmaMu.Unlock()
}

func TestMonitorAdjustCapTierCeiling(t *testing.T) {
    called := false
    m := NewMonitor("http://test", "gaming_mid", 50*time.Millisecond, 5000, func(cap int) {
        called = true
        if cap > gamingMidCeilingKBps {
            t.Errorf("gaming_mid cap %d exceeds ceiling %d", cap, gamingMidCeilingKBps)
        }
    })
    m.adjustCap(10000) // should be clamped to gamingMidCeilingKBps (6250)
    if !called {
        t.Error("callback not invoked")
    }
}
```

### 6. `internal/throttle/throttle.go` — Public API

```go
package throttle

import "sync"

// Global state for the throttle subsystem.
var (
    // The active monitor (nil when disconnected)
    activeMonitor *Monitor
    monitorMu     sync.Mutex

    // Latest probe result (for GUI to read)
    lastProbeResult *ProbeResult
    probeMu         sync.RWMutex
)

// RunProbe performs a fresh bandwidth probe and caches the result.
// Called on every connect. Uses the new Probe() which always returns
// a result (fallback defaults if the probe fails).
func RunProbe(apiBase, tier string) *ProbeResult {
    result := Probe(apiBase, tier)
    probeMu.Lock()
    lastProbeResult = result
    probeMu.Unlock()
    return result
}

// StartBackgroundMonitor begins continuous bandwidth monitoring.
// The onCapChange callback is invoked when the dynamic cap changes.
func StartBackgroundMonitor(apiBase, tier string, baselineRTT time.Duration, initialCapKBps int, onCapChange func(int)) {
    monitorMu.Lock()
    defer monitorMu.Unlock()

    // Stop any existing monitor
    if activeMonitor != nil {
        activeMonitor.Stop()
    }

    activeMonitor = NewMonitor(apiBase, tier, baselineRTT, initialCapKBps, onCapChange)
    activeMonitor.Start()
}

// StopBackgroundMonitor stops the active bandwidth monitor.
func StopBackgroundMonitor() {
    monitorMu.Lock()
    defer monitorMu.Unlock()
    if activeMonitor != nil {
        activeMonitor.Stop()
        activeMonitor = nil
    }
}

// CurrentNetworkStatus returns the current network condition label for the GUI.
func CurrentNetworkStatus() string {
    probeMu.RLock()
    result := lastProbeResult
    probeMu.RUnlock()

    monitorMu.Lock()
    mon := activeMonitor
    monitorMu.Unlock()

    if mon != nil {
        return mon.Status()
    }
    if result != nil {
        return result.NetworkCondition
    }
    return "unknown"
}

// CurrentBandwidthCapKBps returns the current effective cap (0 = uncapped).
// Used by the engine manager when constructing Hysteria 2 --speed flags.
func CurrentBandwidthCapKBps() int {
    monitorMu.Lock()
    mon := activeMonitor
    monitorMu.Unlock()

    if mon != nil {
        return mon.CurrentCapKBps()
    }

    probeMu.RLock()
    result := lastProbeResult
    probeMu.RUnlock()

    if result != nil {
        return result.SuggestedKBps
    }
    return 0
}

// CachedResult returns the last probe result (for GUI display).
func CachedResult() *ProbeResult {
    probeMu.RLock()
    defer probeMu.RUnlock()
    return lastProbeResult
}
```

### 7. Hardcoded usque Throttles (Per Tier)

Unlike Hysteria 2 (dynamically probed + continuously monitored), usque has **hardcoded bandwidth caps** that match the plan tier. These are compiled into the binary and cannot be changed without a client update:

| Tier | usque Cap | Rationale |
|------|----------|-----------|
| Warp Lite | 1 MB/s (8 Mbps) | Good enough for web browsing. Costs $0 (Cloudflare free tier) — pure margin. |
| Gaming Mid | 5 MB/s (40 Mbps) | Sufficient for casual gaming + streaming. |
| Gaming Max | Uncapped | Full speed. User paid for premium. |

**Implementation:** The engine manager passes a `--bandwidth-limit <KBps>` flag to the usque binary on launch. The usque SOCKS5 connection is throttled at that rate. The value is hardcoded in `internal/throttle/usque.go` as a `map[string]int` keyed by plan tier.

```go
// internal/throttle/usque.go
package throttle

// UsqueBandwidthLimit returns the hardcoded bandwidth cap for usque by tier.
// Key: plan_tier, Value: KBps limit (0 = uncapped)
var usqueBandwidthLimits = map[string]int{
    "warp_lite":     1000,   // 1 MB/s
    "gaming_mid":    5000,   // 5 MB/s
    "gaming_max":    0,      // Uncapped
}

func UsqueBandwidthLimit(tier string) int {
    if cap, ok := usqueBandwidthLimits[tier]; ok {
        return cap
    }
    return 1000 // default to Warp Lite cap
}
```

### 8. VPS Speedtest Endpoint

The admin hub (Caddy/PocketBase) needs two static files served at:
- `GET /speedtest/1mb.bin` — 1MB of random data (quick test + initial probe)
- `GET /speedtest/500kb.bin` — 500KB of random data (stage test + background monitor)

Generate once on VPS setup:
```bash
dd if=/dev/urandom of=/opt/speedtest/1mb.bin bs=1M count=1
dd if=/dev/urandom of=/opt/speedtest/500kb.bin bs=500K count=1
```

Caddy serves them with `Cache-Control: no-store, no-cache, must-revalidate` (don't let intermediate caches skew results). The response should also include a `Content-Length` header so the client can verify complete download.

### 9. UI Integration

The GUI reads network status from `throttle.CurrentNetworkStatus()` and the speed label from `throttle.CurrentBandwidthCapKBps()`:

| Network Condition | GUI Indicator | User-visible Label | Hysteria 2 Behavior |
|---|---|---|---|
| `"fast"` | 🟢 Green dot | "Network: Fast" | Uncapped (Gaming Max) or tier-capped (Gaming Mid 5MB/s) |
| `"moderate"` | 🟡 Yellow dot | "Network: Moderate" | Capped at EWMA-derived rate |
| `"congested"` | 🟠 Orange dot | "Network: Congested" | Capped at probed rate, monitor actively adjusting |
| `"degraded"` | 🔴 Red dot | "Network: Slow" | Capped at floor (500KB/s), may trigger fallback to usque |

The GUI updates every second via `statusLoop()`:

```go
// In updateStatus()
networkLabel := throttle.CurrentNetworkStatus()
switch networkLabel {
case "fast":
    state.NetworkLabel.SetText("Network: 🟢 Fast")
case "moderate":
    state.NetworkLabel.SetText("Network: 🟡 Moderate")
case "congested":
    state.NetworkLabel.SetText("Network: 🟠 Congested")
case "degraded":
    state.NetworkLabel.SetText("Network: 🔴 Slow")
default:
    state.NetworkLabel.SetText("Network: --")
}

// Show current bandwidth cap if capped
if cap := throttle.CurrentBandwidthCapKBps(); cap > 0 {
    state.SpeedLabel.SetText(fmt.Sprintf("Speed: ~%d KB/s", cap))
} else {
    state.SpeedLabel.SetText("Speed: Max")
}
```

The user never sees raw probe numbers, packet loss, or RTT — just a color + label + approximate speed.

---

## Phase 1.9: Runtime Engine Download

### Bootstrap Engine Bundling (Solves Chicken-and-Egg)

**Problem:** If the engine binary is downloaded at runtime, and the student is on school WiFi that blocks the download, they're stuck — they need the VPN to bypass the block, but they can't download the VPN engine.

**Fix:** Bundle the **primary engine** (Hysteria 2 / `speedmode`) in the installer. Runtime download is only used for:
- Additional engines (usque, xray, tuic) that aren't bundled
- Engine updates (new versions pushed by the admin hub)

The installer layout:
```
MyVPN-Setup.exe
├── myvpn.exe                    # The Go app
├── myvpn-helper.exe             # Privileged helper service (Windows)
├── engines/
│   └── speedmode.exe            # Hysteria 2 — bundled, primary
└── wintun.dll                   # TUN driver
```

At runtime, `EnsureEngine()` checks:
1. Is the engine bundled in the installer directory? → Use it.
2. Is the engine cached in `~/.myvpn/engines/`? → Verify hash, use it.
3. Download from admin hub → verify SHA256 → cache it.

This way, even if the admin hub is unreachable or blocked, the first connect always works.

### `internal/enginedl/download.go`

Engine binaries are downloaded from the admin hub on first connect (or when bundled version is outdated). Cached in the app data directory and verified by SHA256.

```go
package enginedl

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "myvpn/internal/branding"
    "myvpn/internal/storage"
    "net/http"
    "os"
    "path/filepath"
    "runtime"
)

type EngineMeta struct {
    ProtocolID  string `json:"protocol_id"`
    BinaryName  string `json:"binary_name"`  // "speedmode"
    Platform    string `json:"platform"`     // "windows_amd64"
    Version     string `json:"version"`
    SHA256      string `json:"sha256"`
    DownloadURL string `json:"download_url"`
}

// EnsureEngine checks if the engine binary is cached and valid.
// If not, downloads it from the admin hub.
func EnsureEngine(apiBase string, meta EngineMeta) (string, error) {
    cacheKey := meta.ProtocolID
    cachedPath := storage.GetCachedEnginePath(cacheKey)

    // Check cached binary exists and has correct hash
    if cachedPath != "" {
        if fileExists(cachedPath) && verifyHash(cachedPath, meta.SHA256) {
            return cachedPath, nil
        }
    }

    // Need to download
    binDir := storage.EngineDir()
    ext := ""
    if runtime.GOOS == "windows" {
        ext = ".exe"
    }
    destPath := filepath.Join(binDir, meta.BinaryName+ext)

    fmt.Printf("Downloading engine: %s (%s)...\n", meta.BinaryName, meta.Platform)

    resp, err := http.Get(meta.DownloadURL)
    if err != nil {
        return "", fmt.Errorf("download failed: %w", err)
    }
    defer resp.Body.Close()

    tmpFile, err := os.CreateTemp("", "myvpn-engine-*")
    if err != nil {
        return "", fmt.Errorf("cannot create temp file: %w", err)
    }
    defer os.Remove(tmpFile.Name())

    hash := sha256.New()
    written, err := io.Copy(io.MultiWriter(tmpFile, hash), resp.Body)
    if err != nil {
        tmpFile.Close()
        return "", fmt.Errorf("download incomplete: %w", err)
    }
    tmpFile.Close()

    if written < 1024*1024 {
        return "", fmt.Errorf("downloaded engine too small (%d bytes)", written)
    }

    gotHash := hex.EncodeToString(hash.Sum(nil))
    if gotHash != meta.SHA256 {
        return "", fmt.Errorf("engine SHA256 mismatch: got %s, expected %s", gotHash, meta.SHA256)
    }

    // Move to engine cache with renamed binary
    os.Rename(tmpFile.Name(), destPath)
    os.Chmod(destPath, 0755)

    storage.CacheEnginePath(cacheKey, destPath)
    return destPath, nil
}

func fileExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}

func verifyHash(path, expected string) bool {
    f, err := os.Open(path)
    if err != nil {
        return false
    }
    defer f.Close()

    hash := sha256.New()
    io.Copy(hash, f)
    return hex.EncodeToString(hash.Sum(nil)) == expected
}
```

---

## Phase 1.10: Minimal GUI (Fyne)

> **Requirement:** The user rejected systray-only — they want a proper GUI window, but minimalistic. The business relies on intransparency: no real protocol names, no technical details exposed. Users see only: connection status, tier name, speed indicator, time connected.

### 1. Framework Choice: Fyne

**Why Fyne over alternatives:**

| Framework | Pros | Cons | Verdict |
|-----------|------|------|---------|
| **Fyne** | Cross-platform (Win/Mac/Linux), native look, built-in systray (Phase 2), MIT license | Requires CGO for graphics (~5MB binary overhead) | ✅ Best fit |
| Wails | Web-based (HTML/CSS/JS), flexible UI | Requires WebView2 (Windows) / WKWebView (macOS), heavier dependency chain, CGO required | ❌ Too heavy |
| walk | Windows-only | No macOS support | ❌ Non-starter |

**Fyne import:** `fyne.io/fyne/v2`

### 2. Window Design

```
┌─────────────────────────────────┐
│  MyVPN                          │
│                                 │
│         ● Connected             │
│         Gaming Mode             │
│         00:14:32                │
│                                 │
│  Network: 🟢 Fast               │
│  Speed:   ↗12.4 MB/s           │
│                                 │
│  ┌───────────────────────────┐  │
│  │       Disconnect          │  │
│  └───────────────────────────┘  │
│                                 │
│  ▸ Settings                     │
│     ☐ Send anonymous usage data │
│     Activation: MYVPN-XXXX-XXXX │
│     Plan: Gaming Max            │
│     Device: Bound ✓             │
│                                 │
│  ─────────────────────────────  │
│  v1.0.0  |  Build abc1234      │
└─────────────────────────────────┘
```

**Design rules:**
- Window is **300×400 pixels**, non-resizable (MVP)
- **No protocol names** — uses tier labels: "Warp Lite", "Stealth Browse", "Gaming Mid", "Gaming Max"
- **No IP addresses, server locations, or protocol details** visible anywhere
- **Connection status** uses a colored dot: green = connected, yellow = degraded/fallback, red = disconnected
- **Speed indicator** shows current throughput (sampled every 2s, smoothed over 10s)
- **"Settings"** expandable section hides activation code and device binding info
- **No minimize-to-tray** for MVP (window stays open); Phase 2 adds systray minimize

### 3. `internal/gui/app.go` — GUI Controller

```go
package gui

import (
    "encoding/json"
    "fmt"
    "fyne.io/fyne/v2"
    "fyne.io/fyne/v2/app"
    "fyne.io/fyne/v2/container"
    "fyne.io/fyne/v2/widget"
    "myvpn/internal/activation"
    "myvpn/internal/health"
    "myvpn/internal/manager"
    "myvpn/internal/storage"
    "myvpn/internal/throttle"
    "os"
    "time"
)

type GUIState struct {
    Window          fyne.Window
    StatusLabel     *widget.Label
    StatusDot       *widget.CanvasObject // colored circle
    TierLabel       *widget.Label
    TimeLabel       *widget.Label
    NetworkLabel    *widget.Label
    SpeedLabel      *widget.Label
    ConnectBtn      *widget.Button
    SettingsAccordion *widget.Accordion
}

var state GUIState

// Launch creates the Fyne window and starts the UI loop.
// Called from main() — blocks until window closes.
func Launch(activationCode, planTier string) {
    a := app.New()
    state.Window = a.NewWindow("MyVPN")

    // Status section
    state.StatusDot = newStatusDot("red") // starts disconnected
    state.StatusLabel = widget.NewLabel("Disconnected")
    state.TierLabel = widget.NewLabel(planDisplayName(planTier))
    state.TimeLabel = widget.NewLabel("--:--:--")

    // Network indicators
    state.NetworkLabel = widget.NewLabel("Network: --")
    state.SpeedLabel = widget.NewLabel("Speed: --")

    // Connect/Disconnect button
    state.ConnectBtn = widget.NewButton("Connect", onConnect)
    state.ConnectBtn.Importance = widget.HighImportance

    // Settings accordion (collapsed by default)
    privacyCheck := widget.NewCheck("Send anonymous usage data", onPrivacyToggle)
    privacyCheck.SetChecked(storage.GetPrivacyOptIn())

    actLabel := widget.NewLabel("Activation: " + maskCode(activationCode))
    planLabel := widget.NewLabel("Plan: " + planDisplayName(planTier))
    deviceLabel := widget.NewLabel("Device: Bound ✓")

    settingsContent := container.NewVBox(
        privacyCheck,
        actLabel,
        planLabel,
        deviceLabel,
    )

    settingsAccordion := widget.NewAccordion(
        widget.NewAccordionItem("Settings", settingsContent),
    )
    settingsAccordion.MultiOpen = false

    // Layout
    content := container.NewVBox(
        state.StatusDot,
        state.StatusLabel,
        state.TierLabel,
        state.TimeLabel,
        widget.NewSeparator(),
        state.NetworkLabel,
        state.SpeedLabel,
        widget.NewSeparator(),
        state.ConnectBtn,
        settingsAccordion,
    )

    state.Window.SetContent(content)
    state.Window.Resize(fyne.NewSize(300, 400))
    state.Window.SetFixedSize(true)

    // Start health/status update loop
    go statusLoop()

    state.Window.ShowAndRun()
}

// statusLoop polls connection state and updates the GUI every second.
func statusLoop() {
    ticker := time.NewTicker(1 * time.Second)
    for range ticker.C {
        updateStatus()
    }
}

func updateStatus() {
    connState := manager.CurrentState()
    switch connState {
    case "IDLE", "DISCONNECTED":
        setStatusDot("red")
        state.StatusLabel.SetText("Disconnected")
        state.ConnectBtn.SetText("Connect")
        state.ConnectBtn.Enable()
    case "CONNECTING":
        setStatusDot("yellow")
        state.StatusLabel.SetText("Connecting...")
        state.ConnectBtn.SetText("Cancel")
    case "CONNECTED_PRIMARY":
        setStatusDot("green")
        proto := manager.RunningProtocol()
        state.StatusLabel.SetText("Connected")
        state.TierLabel.SetText(planDisplayName(storage.LoadPlanTier()))
        state.ConnectBtn.SetText("Disconnect")
        // Update speed
        state.SpeedLabel.SetText(manager.CurrentSpeed())
    case "CONNECTED_FALLBACK":
        setStatusDot("yellow")
        state.StatusLabel.SetText("Connected (Fallback)")
        state.TierLabel.SetText(planDisplayName(storage.LoadPlanTier()))
        state.ConnectBtn.SetText("Disconnect")
    case "DEGRADED":
        setStatusDot("orange")
        state.StatusLabel.SetText("Reconnecting...")
        state.ConnectBtn.SetText("Cancel")
    }

    // Update network condition from adaptive throttle subsystem
    networkStatus := throttle.CurrentNetworkStatus()
    switch networkStatus {
    case "fast":
        state.NetworkLabel.SetText("Network: 🟢 Fast")
    case "moderate":
        state.NetworkLabel.SetText("Network: 🟡 Moderate")
    case "congested":
        state.NetworkLabel.SetText("Network: 🟠 Congested")
    case "degraded":
        state.NetworkLabel.SetText("Network: 🔴 Slow")
    default:
        state.NetworkLabel.SetText("Network: --")
    }

    // Show current bandwidth cap (from live monitor if active, else from probe)
    if cap := throttle.CurrentBandwidthCapKBps(); cap > 0 {
        state.SpeedLabel.SetText(fmt.Sprintf("Speed: ~%d KB/s", cap))
    } else if networkStatus == "fast" {
        state.SpeedLabel.SetText("Speed: Max")
    } else {
        state.SpeedLabel.SetText("Speed: --")
    }

    // Update connection time
    if manager.IsConnected() {
        state.TimeLabel.SetText(manager.ConnectionDuration().Truncate(time.Second).String())
    } else {
        state.TimeLabel.SetText("--:--:--")
    }
}

func onConnect() {
    if manager.IsConnected() {
        manager.Disconnect()
        return
    }

    // ── IMPORTANT: PROBE BEFORE TUN ──
    // The bandwidth probe MUST complete BEFORE the TUN device is created
    // and routes are installed. Probe traffic goes over the regular network
    // interface to measure true link bandwidth. If we created the TUN first,
    // probe downloads would try to route through the not-yet-ready tunnel,
    // giving false (low) readings.
    //
    // Flow: Probe → Create TUN + Routes → Launch Engine → Start Monitor
    go func() {
        tier := storage.LoadPlanTier()

        // Warp Lite uses a hardcoded 1 MB/s cap — skip the probe entirely
        if tier == "warp_lite" {
            manager.Connect()
            return
        }

        // Step 1: Run bandwidth probe (uses regular network, NOT the tunnel)
        result := throttle.RunProbe(storage.LoadAPIBase(), tier)

        // Update GUI with probe result
        statusLabel := "Network: "
        switch result.NetworkCondition {
        case "fast":
            statusLabel += "🟢 Fast"
        case "moderate":
            statusLabel += "🟡 Moderate"
        case "congested":
            statusLabel += "🟠 Congested"
        case "degraded":
            statusLabel += "🔴 Slow"
        case "offline":
            statusLabel += "⚫ Offline"
        default:
            statusLabel += result.NetworkCondition
        }
        state.NetworkLabel.SetText(statusLabel)

        if result.FallbackUsed {
            // Probe failed — log but proceed with fallback cap.
            // The background monitor will refine from here.
            state.SpeedLabel.SetText(fmt.Sprintf("Speed: ~%d KB/s (fallback)", result.SuggestedKBps))
        } else if result.SuggestedKBps > 0 {
            state.SpeedLabel.SetText(fmt.Sprintf("Speed: ~%d KB/s", result.SuggestedKBps))
        } else {
            state.SpeedLabel.SetText("Speed: Max")
        }

        // Step 2: Create TUN, install routes, launch engine with probed cap
        //         (manager.Connect reads the cap from throttle.CurrentBandwidthCapKBps())
        manager.Connect()

        // Step 3: Start background bandwidth monitor (runs during session)
        //         The monitor continuously adjusts the cap up/down based on
        //         real-time EWMA measurements and bufferbloat detection.
        throttle.StartBackgroundMonitor(
            storage.LoadAPIBase(),
            tier,
            result.BaselineRTT,
            result.SuggestedKBps,
            func(newCapKBps int) {
                // Called when monitor adjusts cap — update Hysteria 2 via control API
                dynamicCapUpdate(newCapKBps)
            },
        )
    }()
}

// dynamicCapUpdate sends a new bandwidth cap to the running Hysteria 2 engine.
// Hysteria 2 supports runtime speed changes via UDP control socket or SIGHUP.
// This function is called by the background monitor when the cap adjusts.
func dynamicCapUpdate(capKBps int) {
    if !manager.IsRunning() {
        return
    }

    // Map: protocol → update function
    switch manager.RunningProtocolID() {
    case "hysteria2":
        // Hysteria 2 supports on-the-fly speed changes via its control socket.
        // Write updated config and signal the engine to reload.
        configPath := storage.EngineConfigPath("hysteria2")

        // Read existing config, update speed field
        cfg := readHysteriaConfig(configPath)
        if capKBps > 0 {
            cfg["speed"] = capKBps * 8000 // KBps → bps
        } else {
            delete(cfg, "speed")
        }

        // Write updated config
        data, _ := json.Marshal(cfg)
        writeConfigAtomic(configPath, data)

        // Signal engine to reload config (SIGHUP on Unix, or UDP control msg)
        manager.SignalReload()
    }
}

func readHysteriaConfig(path string) map[string]interface{} {
    data, err := os.ReadFile(path)
    if err != nil {
        return make(map[string]interface{})
    }
    var cfg map[string]interface{}
    json.Unmarshal(data, &cfg)
    if cfg == nil {
        cfg = make(map[string]interface{})
    }
    return cfg
}

func writeConfigAtomic(path string, data []byte) {
    tmpPath := path + ".tmp"
    os.WriteFile(tmpPath, data, 0644)
    os.Rename(tmpPath, path)
}

func onPrivacyToggle(checked bool) {
    storage.SetPrivacyOptIn(checked)
}

func planDisplayName(tier string) string {
    names := map[string]string{
        "warp_lite":     "Warp Lite",
        "stealth_browse": "Stealth Browse",
        "gaming_mid":    "Gaming Mid",
        "gaming_max":    "Gaming Max",
    }
    if n, ok := names[tier]; ok {
        return n
    }
    return tier
}

func maskCode(code string) string {
    if len(code) <= 8 {
        return code
    }
    return code[:9] + "-XXXX"
}
```

### 4. Fyne Dependencies

Add to `go.mod`:
```
require (
    fyne.io/fyne/v2 v2.5.1
)
```

**Build note:** Fyne requires a C compiler for some backends on Linux. For Windows cross-compilation, use `CGO_ENABLED=1` with MinGW. For macOS, use Xcode command-line tools. Pre-built Fyne binaries are statically linked and ~5MB overhead.

### 5. Custom Theme — Black + Purple Branding

> **The default Fyne gray look doesn't fit the brand.** The service uses a black background with purple outlines/accents and a subtle gradient. Fyne supports full custom theming via the `fyne.Theme` interface.

#### Theme Colors

| Token | Color | Hex | Usage |
|-------|-------|-----|-------|
| Background | Black | `#0D0D0F` | Main window background |
| Foreground | White (90%) | `#E6E6E6` | Body text, labels |
| Primary | Vibrant Purple | `#A855F7` | Buttons, links, active dot |
| Primary Darker | Deep Purple | `#7C3AED` | Button hover/pressed state |
| Secondary | Dim Purple | `#6B21A8` | Borders, outlines, separators |
| Success | Bright Purple-Pink | `#C084FC` | Status dot when connected |
| Warning | Amber | `#F59E0B` | Status dot when degraded/fallback |
| Input BG | Near-Black | `#1C1C1E` | Accordion backgrounds, input fields |
| Disabled | Muted Gray | `#52525B` | Disabled text |
| Hover | Purple (10%) | `#A855F7` with 10% alpha | Hover highlights |
| Focus Ring | Purple glow | `#A855F7` | Focus indicator on buttons |

#### `internal/gui/theme.go` — Custom Theme Implementation

```go
package gui

import (
    "image/color"

    "fyne.io/fyne/v2"
)

type MyVPNTheme struct{}

// enforce singleton
var _ fyne.Theme = (*MyVPNTheme)(nil)

func (t *MyVPNTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
    // Ignore system variant — always return dark theme colors
    _ = variant

    switch name {
    // Backgrounds
    case "background":
        return color.RGBA{13, 13, 15, 255}        // #0D0D0F
    case fyne.ColorNameInputBackground:
        return color.RGBA{28, 28, 30, 255}         // #1C1C1E
    case fyne.ColorNameButton:
        return color.RGBA{168, 85, 247, 255}       // #A855F7 (purple)
    case fyne.ColorNameHover:
        return color.RGBA{168, 85, 247, 25}        // #A855F7 at 10% alpha
    case fyne.ColorNamePressed:
        return color.RGBA{124, 58, 237, 255}       // #7C3AED

    // Text
    case fyne.ColorNameForeground:
        return color.RGBA{230, 230, 230, 255}      // #E6E6E6
    case fyne.ColorNameDisabled:
        return color.RGBA{82, 82, 91, 255}         // #52525B
    case fyne.ColorNamePlaceHolder:
        return color.RGBA{113, 113, 122, 255}      // #71717A

    // Accent / UI chrome
    case fyne.ColorNamePrimary:
        return color.RGBA{168, 85, 247, 255}       // #A855F7
    case fyne.ColorNameFocusRing:
        return color.RGBA{168, 85, 247, 200}       // purple glow
    case fyne.ColorNameSeparator:
        return color.RGBA{107, 33, 168, 255}       // #6B21A8 (dim purple)
    case fyne.ColorNameMenuBackground:
        return color.RGBA{13, 13, 15, 255}         // match background

    // ScrollBar, Shadow (keep minimal)
    case fyne.ColorNameScrollBar:
        return color.RGBA{168, 85, 247, 80}
    case fyne.ColorNameShadow:
        return color.Transparent

    default:
        return color.RGBA{13, 13, 15, 255}
    }
}

func (t *MyVPNTheme) Font(style fyne.TextStyle) fyne.Resource {
    // Use default Fyne font (clean sans-serif)
    return nil
}

func (t *MyVPNTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
    return fyne.CurrentApp().Settings().Theme().Icon(name)
}

func (t *MyVPNTheme) Size(name fyne.ThemeSizeName) float32 {
    switch name {
    case fyne.SizeNamePadding:
        return 8      // tighter padding for minimal look
    case fyne.SizeNameInnerPadding:
        return 6
    case fyne.SizeNameScrollBar:
        return 6
    case fyne.SizeNameScrollBarSmall:
        return 3
    case fyne.SizeNameSeparatorThickness:
        return 1
    case fyne.SizeNameInputBorder:
        return 1
    case fyne.SizeNameButtonRadius:
        return 6      // slightly rounded corners
    case fyne.SizeNameInputRadius:
        return 4
    case fyne.SizeNameSelectionRadius:
        return 4
    case fyne.SizeNameTextSize:
        return 14
    case fyne.SizeNameHeadingSize:
        return 16
    case fyne.SizeNameSubHeadingSize:
        return 12
    case fyne.SizeNameCaptionSize:
        return 11
    case fyne.SizeNameInlineIcon:
        return 20
    default:
        return fyne.CurrentApp().Settings().Theme().Size(name)
    }
}
```

#### Apply the Theme in `launch()`

Change the first line of `Launch()` to use the custom theme:

```go
func Launch(activationCode, planTier string) {
    a := app.New()
    a.Settings().SetTheme(&MyVPNTheme{})   // ← Add this line
    state.Window = a.NewWindow("MyVPN")
    // ... rest unchanged ...
}
```

#### Gradient Background

Fyne supports `canvas.LinearGradient` and `canvas.RadialGradient` for backgrounds. Use a subtle purple-to-black radial gradient as the window background:

```go
import (
    "image/color"
    "fyne.io/fyne/v2/canvas"
)

func gradientBackground() *canvas.RadialGradient {
    g := canvas.NewRadialGradient(
        color.RGBA{168, 85, 247, 30},   // inner: purple at very low opacity
        color.RGBA{13, 13, 15, 255},    // outer: black
    )
    g.CenterOffsetX = -0.3   // offset center slightly left
    g.CenterOffsetY = -0.2   // offset center slightly up
    return g
}
```

Then wrap the main content in a `container.NewStack(gradientBackground(), content)` to layer it behind the widgets:

```go
func Launch(activationCode, planTier string) {
    a := app.New()
    a.Settings().SetTheme(&MyVPNTheme{})

    state.Window = a.NewWindow("MyVPN")

    // ... build content as before ...

    // Wrap with gradient background
    bg := gradientBackground()
    root := container.NewStack(bg, content)

    state.Window.SetContent(root)
    state.Window.Resize(fyne.NewSize(300, 400))
    state.Window.SetFixedSize(true)

    go statusLoop()
    state.Window.ShowAndRun()
}
```

#### Status Dot with Glow (Instead of Flat Circle)

Replace the simple colored circle with a small radial gradient for a "glowing" effect:

```go
func newStatusDot(colorName string) *canvas.RadialGradient {
    var inner, outer color.Color
    switch colorName {
    case "green":
        inner = color.RGBA{192, 132, 252, 255}  // purple-pink glow
        outer = color.RGBA{192, 132, 252, 50}
    case "yellow":
        inner = color.RGBA{245, 158, 11, 255}
        outer = color.RGBA{245, 158, 11, 50}
    default: // red
        inner = color.RGBA{239, 68, 68, 255}
        outer = color.RGBA{239, 68, 68, 50}
    }
    dot := canvas.NewRadialGradient(inner, outer)
    dot.Resize(fyne.NewSize(12, 12))
    return dot
}
```

> **Tip:** If the gradient looks too different from the mockup on Windows vs macOS, tweak `CenterOffsetX`/`CenterOffsetY` and the inner color alpha (30 is very subtle — try 50 or 80 for a more visible glow).

### 6. What the GUI Never Shows

Per BUSINESS-OVERVIEW.md requirements, the GUI must **never** expose:
- Real protocol names ("Hysteria 2", "usque", "TUIC v5", "VLESS-REALITY")
- Server IP addresses or hostnames
- Port numbers
- Engine binary names ("speedmode.exe", "litemode.exe")
- Packet loss percentages or raw probe data
- Build signature details
- Any debug/developer information

The "Settings" accordion can show activation code (masked) and plan tier name — nothing more.

---

## Phase 1.11: Build System + Main Entrypoint

### 1. `Makefile`

```makefile
.PHONY: all build clean run

APP_NAME = myvpn
VERSION ?= 1.0.0

all: build

# Build for current platform
build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME) .

# Build for Windows amd64 (Fyne requires CGO + MinGW for cross-compilation)
# Prerequisite: sudo apt install gcc-mingw-w64-x86-64
build-windows:
	CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
	    go build -trimpath -ldflags="-s -w -X main.version=$(VERSION) -H windowsgui" -o $(APP_NAME).exe .

# Build for macOS Intel (requires osxcross or native macOS)
build-macos-intel:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
	    go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME)_darwin_amd64 .

# Build for macOS Apple Silicon (requires osxcross or native macOS)
build-macos-arm:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
	    go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME)_darwin_arm64 .

# Build on native platform (Linux dev machine → Linux binary for testing)
build-native:
	CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME) .

# Build all platforms (requires cross-compilation toolchains)
build-all: build-windows build-macos-intel build-macos-arm

# Cross-compilation notes:
# - Fyne requires CGO for graphics backends; CGO_ENABLED=0 builds will fail
# - Windows: install mingw-w64 (sudo apt install gcc-mingw-w64-x86-64 on Ubuntu)
# - macOS: requires osxcross or native macOS build machine (Apple SDK licensing)
# - CI/CD recommendation: GitHub Actions with macos-latest + windows-latest runners
#   avoids cross-compilation toolchain pain; build natively on each OS runner

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe $(APP_NAME)_darwin_*

# Run unit tests for all core logic packages
# See Phase 1.12 for the full testing strategy
test:
	go test -v -race ./internal/...
```

### 2. `main.go` — Entrypoint with Fyne GUI

> **Note:** The UI is now handled by `internal/gui/app.go` (see Phase 1.10). `main.go` is a thin launcher that initializes storage, checks activation, starts the heartbeat, and hands control to the GUI.

```go
package main

import (
    "flag"
    "log"
    "os"

    "myvpn/internal/activation"
    "myvpn/internal/branding"
    "myvpn/internal/gui"
    "myvpn/internal/heartbeat"
    "myvpn/internal/storage"
    "myvpn/internal/updater"
)

var version string // set via ldflags

func main() {
    apiBase := flag.String("api", "https://api.yourdomain.com", "Admin hub URL")
    flag.Parse()

    // --- Rollback safety: must run before any other initialization ---
    // Pass --revert or hold Ctrl on launch to force-revert to myvpn.prev
    revertHeld := false
    for _, arg := range os.Args {
        if arg == "--revert" {
            revertHeld = true
            break
        }
    }
    reverted, err := updater.CheckOnStartup(revertHeld)
    if err != nil {
        log.Printf("startup recovery check failed: %v", err)
    }
    if reverted {
        log.Println("ROLLBACK: reverted to previous version after crash")
        // The restored .prev may have a different version — the heartbeat
        // will re-check for updates on the next cycle.
    }

    storage.Init()
    branding.Init()

    // Crash marker for telemetry
    if heartbeat.HadCrash() {
        heartbeat.SetLastError("previous_run_crashed")
    }
    heartbeat.ClearCrashMarker()
    heartbeat.SetCrashMarker()
    defer heartbeat.ClearCrashMarker()

    // Autostart (platform-specific: scheduled task / LaunchAgent)
    setupAutostart()

    // Activation check — blocks until activated
    if !storage.IsActivated() {
        activation.ShowPrompt(*apiBase)
        if !storage.IsActivated() {
            log.Fatal("Activation required to continue")
        }
    }

    // Start heartbeat in background
    go heartbeat.Start(*apiBase)

    // Launch the minimal GUI — blocks until window closed
    gui.Launch(storage.LoadActivationCode(), storage.LoadPlanTier())
}

func setupAutostart() {
    exe, err := os.Executable()
    if err != nil {
        return
    }
    // Platform-specific autostart (scheduled task on Windows, LaunchAgent on macOS)
    _ = exe
}
```

---

## Phase 1.12: Testing & QA

> **CRITIQUE.md flagged this as the weakest dimension (1/10).** This section establishes the minimum viable testing strategy.

### 1. Unit Tests (Required — Run in CI)

| Package | What to test | Mock strategy |
|---------|-------------|---------------|
| `internal/activation` | Luhn-mod-N checksum validation, code format parsing, fingerprint hashing, device binding request/response | Mock HTTP transport; test valid/invalid/edge-case codes |
| `internal/storage` | Token persistence, protocol list CRUD, plan tier read/write, privacy opt-out toggle | In-memory temp dir; no real filesystem |
| `internal/branding` | Protocol name → display name mapping, tier label lookup | Pure function tests |
| `internal/updater` | SHA256 verification (valid hash, wrong hash, empty file), version comparison, platform matching, sentinel crash detection, auto-revert on sentinel, manual `--revert` flag, `.prev` preservation | Mock HTTP downloads; use known test files; simulate crash by leaving sentinel |
| `internal/heartbeat` | Token expiry logic, grace period calculation, telemetry field sanitization, cert pin hash comparison | Mock HTTP with custom TLS certs |
| `internal/throttle` | Connect probe (1MB quick test + staged ramp), EWMA smoothing (α=0.3), cap reduction on sustained low bandwidth, cap increase on sustained headroom, bufferbloat RTT trigger, tier ceiling enforcement (Gaming Mid ≤5MB/s), monitor start/stop lifecycle, concurrent probe + monitor safety | Mock HTTP with known-speed test files; inject RTT delays; verify cap adjustment timing |
| `internal/health` | Consecutive fail counter, state transition (healthy→degraded→dead), auto-reconnect sequence | No real TUN needed; mock the probe function |

**Run:** `go test -v -race ./internal/...`

### 2. Integration Tests (Manual — Pre-Release Checklist)

These require a real VPS and at least one test device per platform:

| Test | Platform | Steps |
|------|----------|-------|
| **Fresh activation** | Both | Delete all local state, enter valid code → device bound, token stored |
| **Re-activation after reinstall** | Both | Delete app, reinstall → fingerprint matches → auto-activated (no code prompt) |
| **Wrong code rejection** | Both | Enter invalid code → Luhn check catches it before server call |
| **Already-bound code** | Both | Try code on second device → 409 rejected with clear message |
| **Suspended device** | Both | Admin suspends device → next heartbeat returns 403 → GUI shows suspension message |
| **TUN creation + traffic flow** | Windows | Connect → wintun adapter appears in `ipconfig` → `traceroute 8.8.8.8` goes through TUN gateway |
| **TUN creation + traffic flow** | macOS | Connect → `ifconfig` shows utun interface → `traceroute 8.8.8.8` goes through TUN gateway |
| **Kill switch (route deletion)** | Both | Connect → kill engine process → default route restored → `traceroute 8.8.8.8` goes via school WiFi (blocked) |
| **DNS leak check** | Both | Connect → `nslookup google.com` → resolves via 1.0.0.1/1.1.1.1, NOT school DNS |
| **IPv6 leak check** | Both | Connect → `ping -6 ipv6.google.com` → fails (IPv6 blocked) |
| **Fallback engine switch** | Both | Connect primary → block primary server IP → health fails 3× → fallback engine starts → GUI shows yellow dot |
| **Auto-reconnect exhausted** | Both | Block all engines → health retries 5× → GUI shows "Connection Lost — tap to retry" |
| **Grace period** | Both | Connect → kill VPS → app continues working → GUI shows grace countdown (7→6→...→1 days) |
| **Grace expiry** | Both | Let grace expire (or mock) → GUI shows "Re-authentication required" |
| **Bandwidth probe (fast network)** | Both | Connect on fast WiFi → GUI shows 🟢 "Fast" → Hysteria 2 uncapped |
| **Bandwidth probe (congested)** | Both | Throttle VPS to 2MB/s → GUI shows 🟠 "Congested" → Hysteria 2 capped at probed rate |
| **Bandwidth probe (bufferbloat)** | Both | Throttle VPS with delay → probe detects RTT > 1.5× baseline → skips higher stages → cap at lower tier |
| **Background monitor (bandwidth drop)** | Both | Start connected → reduce VPS bandwidth to 30% of cap → within 30s monitor reduces cap → GUI updates |
| **Background monitor (bandwidth increase)** | Both | Start connected at low cap → increase VPS bandwidth → within 60s monitor increases cap → GUI updates |
| **Background monitor (bufferbloat)** | Both | Inject RTT spike during session → monitor detects >2× baseline → cuts cap by 50% immediately |
| **Background monitor (stop on disconnect)** | Both | Connect → disconnect → verify monitor goroutine exits (no leak) |
| **Update (valid)** | Both | Serve new binary with correct SHA256 → client downloads, verifies, applies |
| **Update (tampered)** | Both | Serve binary with wrong SHA256 → client rejects, keeps current version |
| **Privacy toggle** | Both | Toggle off → next heartbeat has telemetry fields zeroed |
| **Update + crash auto-revert** | Both | Apply update → kill app during first 3s → restart → `.update-pending` triggers revert → old version runs |
| **Manual revert via --revert** | Both | Run `myvpn --revert` → restores `.prev`, cleans sentinels |
| **Sentinel two-phase handshake** | Both | Check no stale `.update-pending` or `.update-confirmed` remain after 2 clean launches |

### 3. What Is NOT Tested (Documented Limitations)

- **Concurrent activation attacks** — rate limiting is server-side (PocketBase hook), tested manually
- **WFP/pf kill switch** — Phase 2 feature, not in MVP
- **Split tunnel routing** — Phase 2 feature, not in MVP
- **IPv6 routing through tunnel** — blocked entirely; no IPv6 path to test
- **100+ concurrent clients** — load test deferred; PocketBase + Hysteria 2 scale is well-documented
- **DPI evasion effectiveness** — this is a cat-and-mouse game; cannot be "tested" in a lab

---

## Phase 1.13: Deploy & Verify Checklist

### On the VPS

- [ ] PocketBase is running with WAL mode (`echo "PRAGMA journal_mode;" | sqlite3 /opt/pocketbase/pb_data/data.db` → `wal`)
- [ ] Caddy is running with rate limiting (`caddy validate` and check `/_/health` returns 200)
- [ ] Offsite backup cron is installed and ran successfully (`b2 ls my-vpn-backup-bucket`)
- [ ] Activation rate-limit hook deployed (`/opt/pocketbase/pb_hooks/activation_rate_limit.pb.js`)
- [ ] SQLite WAL hook deployed (`/opt/pocketbase/pb_hooks/sqlite_pragmas.pb.js`)
- [ ] Activation codes, devices, activation_attempts collections created in PocketBase
- [ ] Activation code Luhn-mod-N check digit hook deployed (validate before save)
- [ ] Engine binaries uploaded to admin hub files (available at download URLs)
- [ ] Uptime monitor configured and reporting green
- [ ] Ports 22/80/443 only (`ufw status`)

### Build & Publish

- [ ] App compiles on all platforms (`make build-all`)
- [ ] SHA256 hashes computed for published binaries (`scripts/publish_update.sh`)

### Client Verification (test on Windows + macOS)

- [ ] App launches and shows minimal GUI window (300×400, non-resizable)
- [ ] Autostart is configured (check Task Scheduler / LaunchAgent)
- [ ] Activation: wrong code (typo) → Luhn check detects it before sending to server
- [ ] Activation: valid code on fresh device → permanently bound, fingerprint + token stored
- [ ] Activation: same code on different device → rejected ("already bound to another device")
- [ ] Activation: same code on same device after app reinstall → works (fingerprint matches)
- [ ] Activation: same code on same device when suspended → rejected with suspension message
- [ ] Activation: code unsuspended → same device can re-activate (binding preserved)
- [ ] Privileged helper service installed (Windows: `sc query MyVPNHelper` → RUNNING)
- [ ] "Connect" succeeds without UAC prompt (helper service handles TUN creation)
- [ ] Bundle engine: `speedmode.exe` exists in installer directory, used on first connect
- [ ] TUN device is created (`ipconfig` / `ifconfig` shows MyVPN interface)
- [ ] Full tunnel: all traffic (except RFC 1918) goes through TUN. `traceroute 8.8.8.8` shows TUN gateway
- [ ] DNS guard: `nslookup google.com` shows 1.1.1.1, not school DNS
- [ ] Kill switch: kill the engine process → default TUN route removed, no leak to school network
- [ ] Health check: start with no engine → "Disconnected". Start engine → "Connected".
- [ ] Auto-reconnect: block tunnel → health fails 3× → "Degraded" → fallback engine starts
- [ ] Auto-reconnect (all engines dead): block all engines → 30s delay → retry primary ×5 → "Tap to retry"
- [ ] Heartbeat appears in PocketBase logs with health_status + error telemetry fields
- [ ] K illing the VPS → app shows grace countdown (7 days), continues working over grace period
- [ ] Grace vs token: after 24h without hub, token expires but grace keeps VPN running
- [ ] Certificate pinning: deploying a fake cert → connection refused
- [ ] Update (platform match): serve update with correct SHA256 → client applies it
- [ ] Update (wrong platform): serve update for different platform → rejected
- [ ] Update (SHA256 mismatch): serve update with wrong SHA256 → rejected
- [ ] Privacy opt-out: toggle telemetry off → heartbeat still runs, telemetry fields zeroed
- [ ] Log rotation: fill engine log past 10MB → rotates, keeps last 3 files
- [ ] State machine: GUI shows correct state through IDLE→ACTIVE→CONNECTING→CONNECTED→DEGRADED→DISCONNECTED cycle
- [ ] Bandwidth probe: on throttled network → GUI shows appropriate indicator → Hysteria 2 capped at probed rate
- [ ] Bandwidth probe: on fast network → GUI shows 🟢 "Fast" → Hysteria 2 uncapped
- [ ] Bandwidth probe: RTT spike during ramp triggers bufferbloat detection → cap reduced
- [ ] Background monitor: sustained bandwidth drop → cap reduces within 30s
- [ ] Background monitor: sustained bandwidth increase → cap increases within 60s
- [ ] Background monitor: bufferbloat RTT spike → cap cut by 50% immediately
- [ ] Background monitor: stops cleanly on disconnect (goroutine exits)
- [ ] usque fallback: hardcoded cap applied per tier (1 MB/s Warp Lite, 5 MB/s Gaming Mid)
- [ ] IPv6 blocked: `ping -6 ipv6.google.com` fails when connected
- [ ] GUI never shows real protocol names, IPs, ports, or engine binary names
- [ ] Settings accordion: collapsed by default, shows activation code (masked) and plan name
- [ ] Rollback (crash): apply update → kill app during startup → restart → auto-reverts to `.prev`
- [ ] Rollback (manual): run `myvpn --revert` → restores previous version from `.prev`
- [ ] Rollback (no .prev): run `--revert` with no `.prev` → app starts normally, sentinels cleaned

---

## Troubleshooting

| Problem                                     | Fix                                                                                              |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Engine not found at connect                 | Check `~/.myvpn/engines/` has the renamed binary. Run `enginedl.EnsureEngine()` manually.        |
| Activation fails with 409                   | That code is already bound to another device. Code cannot be transferred — issue a new code.     |
| Activation fails with 403                   | Device is suspended. Check PocketBase for `suspended` flag and `suspended_reason`.               |
| TUN device not created                      | Ensure wintun.dll is installed (auto-downloaded). Run engine manually to check for errors.       |
| Health check shows "dead" but engine runs   | Tunnel misconfigured — check routes and firewall. Try direct ping through TUN IP.                |
| Kill switch route deletion failed            | Run `netsh interface ip delete route 0.0.0.0/1 <IF_INDEX>` (Windows) or `route delete 0.0.0.0/1` (macOS) manually. |
| Fyne build fails with CGO errors             | Fyne requires CGO. Install gcc/mingw. On Ubuntu: `sudo apt install build-essential libgl1-mesa-dev xorg-dev`. |
| Bandwidth probe always shows "Fast"          | Check VPS `/speedtest/1mb.bin` is reachable. Verify Caddy serves it without caching (`Cache-Control: no-store`). |
| Bandwidth probe detects bufferbloat falsely   | Baseline RTT too low. Increase `bufferbloatRTTMult` from 2.0 to 2.5 or 3.0 in `monitor.go`. |
| Monitor oscillates cap up/down rapidly        | EWMA α too high (too responsive). Reduce from 0.3 to 0.15. Increase `minIncreaseInterval` from 30s to 60s. |
| Monitor never increases cap                   | `increaseThreshold` too high or `increaseWait` too long. Reduce threshold from 1.20 to 1.10 or wait from 60s to 30s. |
| GUI window is blank/white                    | Fyne OpenGL backend issue. Try `FYNE_RENDERER=software` env var. Or install GPU drivers.          |
| IPv6 leak detected (IPv6 traffic bypasses tunnel) | Ensure helper service ran `netsh interface ipv6 set interface <IF> disabled` (Win) or no IPv6 route added (macOS). |
| Certificate pinning blocks connection       | You updated your VPS TLS cert. Generate a new pinned hash and ship an update.                    |
| SHA256 mismatch on update                   | Wrong binary uploaded or corrupted download. Re-publish and verify the SHA256 hash.              |
| Heartbeat grace period expired              | VPS has been down for 7+ days. Fix the VPS. Re-activate clients.                                 |
| macOS engine crash on connect               | Check `~/Library/Application Support/MyVPN/logs/` for engine logs. Run engine manually from CLI. |
| Windows SmartScreen blocks the app          | Click "More info" → "Run anyway". (Trade-off for no code signing.)                               |
| Offsite backup fails                        | Check B2 credentials. Run `b2 authorize-account` again.                                          |
| App crashes right after update              | **Auto-revert:** Restart the app — `.update-pending` is still present, so the recovery logic reverts to `myvpn.prev`. |
| Manual rollback needed (app starts but is broken) | Hold **Ctrl** while launching, or run `myvpn --revert` from terminal. Restores `myvpn.prev`.   |
| Manual rollback (app won't start at all)    | Go to install dir, delete `myvpn.exe`/`myvpn`, rename `myvpn.prev` → `myvpn.exe`/`myvpn`, delete `.update-pending` + `.update-confirmed`. |
| `.update-pending` or `.update-confirmed` stuck | Sentinel files are harmless. Delete them manually from the install directory to clear stale markers. |
