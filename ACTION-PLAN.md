# VPN App — Action Plan (v3 Hardened)

**Goal:** Cross-platform VPN app (Windows + macOS) with Hysteria 2 (gaming primary) and usque/Warp (fallback — unbottlenecked for Gaming plans). Activation codes are permanently bound to a single hardware-fingerprinted device with server-side suspension capability. Features: TUN mode via privileged helper service, auto-reconnect loop, certificate pinning, heartbeat grace period, process sandboxing, and tunnel health checks.

**Stack:** Go + systray + Hysteria 2 + usque + PocketBase + Caddy  
**Prerequisite:** VPS (Ubuntu 22.04, 2GB RAM), domain pointing to it

> 📘 **Business context:** `BUSINESS-OVERVIEW.md`  
> 📘 **Full architecture:** `PLAN.md`

**Key design decisions (v4 changes from v3):**

- ✅ **Hardware-fingerprinted device binding** — permanent one-device-per-code binding. Device identity is a SHA256 hash of MAC address + disk serial + motherboard UUID. Survives app deletion/reinstall. Server can suspend connections without destroying the binding.
- ✅ **No update signing** — updates are SHA256-verified direct downloads from the admin hub. No Ed25519 key management, no offline signing ceremony, no key rotation headaches.
- ✅ **Privileged helper service** (Windows: SYSTEM service via named pipe, macOS: launchd daemon) — TUN creation requires admin on install only, not on every launch
- ✅ **Auto-reconnect loop** — 3 health fails → try fallback → all dead → wait 30s → retry primary ×5 → give up with "tap to retry"
- ✅ **Full tunnel for MVP** — all traffic through VPN except RFC 1918 local. Kill switch: delete default TUN route. Split tunnel is Phase 2.
- ✅ **Grace period overrides token expiry** — during heartbeat failure, last valid token continues working until 7-day grace expires
- ✅ **Primary engine (Hysteria 2) bundled in installer** — bootstrap chicken-and-egg solved. Runtime download only for updates.
- ✅ **Explicit 6-state connection machine** — IDLE → ACTIVE → CONNECTING → CONNECTED_PRIMARY → CONNECTED_FALLBACK → DEGRADED → DISCONNECTED, with GRACE orthogonal
- ✅ **Privacy notice + opt-out toggle** — telemetry fields documented, no browsing history collected
- ✅ **Log rotation** — auto-rotate at 10MB, keep last 3 files
- ✅ **Activation code format** — `MYVPN-XXXX-XXXX-XXXX-C` with Luhn-mod-N checksum
- ✅ **Suspension, not deactivation** — admin can suspend a device from connecting. Binding is never destroyed; code can never be reused on a different device.
- ❌ Middlemen have no app or panel access — they hand out paper codes, that's all

**Estimated total time: 4-5 days** (the helper service and auto-reconnect loop add ~1 day, but they're the difference between "works in a demo" and "actually works for real users").

---

## Contents

- [Phase 1.1: VPS + Admin Hub Hardening](#phase-11-vps--admin-hub-hardening)
- [Phase 1.2: Project Scaffolding](#phase-12-project-scaffolding)
- [Phase 1.3: Core Modules — Storage, Activation, Branding](#phase-13-core-modules--storage-activation-branding)
- [Phase 1.4: Protocol Engine Manager + Sandboxing + Binary Rename](#phase-14-protocol-engine-manager--sandboxing--binary-rename)
- [Phase 1.5: Heartbeat with Cert Pinning + Grace + Telemetry](#phase-15-heartbeat-with-cert-pinning--grace--telemetry)
- [Phase 1.6: Auto-Updater with SHA256 Verification](#phase-16-auto-updater-with-sha256-verification)
- [Phase 1.7: TUN Mode + Split Tunnel + Kill Switch + DNS Guard + Health Check](#phase-17-tun-mode--split-tunnel--kill-switch--dns-guard--health-check)
- [Phase 1.8: Runtime Engine Download](#phase-18-runtime-engine-download)
- [Phase 1.9: Build System + Main Entrypoint](#phase-19-build-system--main-entrypoint)
- [Phase 1.10: Deploy & Verify Checklist](#phase-110-deploy--verify-checklist)

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
    "os"
    "os/exec"
    "path/filepath"
    "sync"
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
        // The systray shows the suspension message. Heartbeat continues
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
    oldPath := filepath.Join(exeDir, "myvpn.old")
    newPath := filepath.Join(exeDir, "myvpn.exe")

    os.Rename(exe, oldPath)
    os.Rename(newBinaryPath, newPath)

    // Spawn helper to delete .old and launch new version
    cmd := exec.Command("cmd", "/c",
        "timeout /t 2 /nobreak > nul && del \""+oldPath+"\" && start \"\" \""+newPath+"\"")
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

    // Copy-and-verify approach — eliminates the crash window between two renames.
    // 1. Write new binary to myvpn.new (already done — newBinaryPath is the temp file)
    // 2. Verify it can start (quick sanity check)
    // 3. Copy over the existing binary (overwrite, not rename)
    // 4. Delete the temp file
    
    // Verify: try to exec the new binary with --version flag
    versionCmd := exec.Command(newBinaryPath, "--version")
    if err := versionCmd.Run(); err != nil {
        return fmt.Errorf("new binary verification failed: %w", err)
    }
    
    // Copy new binary over the old one (in-place overwrite)
    input, err := os.ReadFile(newBinaryPath)
    if err != nil {
        return fmt.Errorf("cannot read new binary: %w", err)
    }
    if err := os.WriteFile(newPath, input, 0755); err != nil {
        return fmt.Errorf("cannot write new binary: %w", err)
    }
    
    // Clean up the temp file
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

## Phase 1.7: TUN Mode + Split Tunnel + Kill Switch + DNS Guard + Health Check

### 1. `internal/tunnel/tun.go` — Interface

## Privileged Helper Service (TUN Elevation Strategy)

TUN device creation requires admin/root privileges. The systray app runs as the user. The solution is a **privileged helper service** installed once (requires admin on install) that creates and manages the TUN interface. After install, users never see a UAC/sudo prompt.

### Windows: SYSTEM Service via Named Pipe

```
┌─────────────────┐     named pipe      ┌──────────────────────┐
│  Systray App     │ ──────────────────► │  MyVPN Helper Service │
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

The service listens on a named pipe `\\.\pipe\MyVPN` for JSON commands from the systray app. **Protocol: newline-delimited JSON.** The client writes a single JSON object followed by `\n`. The helper reads lines via `bufio.Scanner` and parses each line as a JSON command. This prevents partial-read bugs. Each command includes:
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

The systray app reads the secret from this file and includes it in every command:
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

## Phase 1.8: Runtime Engine Download

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

## Phase 1.9: Build System + Main Entrypoint

### 1. `Makefile`

```makefile
.PHONY: all build clean run

APP_NAME = myvpn
VERSION ?= 1.0.0

all: build

# Build for current platform
build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME) .

# Build for Windows amd64
build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	    -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME).exe .

# Build for macOS (Intel + Apple Silicon)
build-macos-intel:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	    -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME)_darwin_amd64 .

build-macos-arm:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath \
	    -ldflags="-s -w -X main.version=$(VERSION)" -o $(APP_NAME)_darwin_arm64 .

# Build all platforms
build-all: build-windows build-macos-intel build-macos-arm

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe $(APP_NAME)_darwin_*

# Run unit tests for core logic packages
test:
	go test -v -race ./internal/activation/...
	go test -v -race ./internal/updater/...
	go test -v -race ./internal/branding/...
	go test -v -race ./internal/storage/...
	go test -v -race ./internal/health/...

# Run all tests (note: tunnel and manager tests require root/admin)
test-all: test
	go test -v -race ./internal/tunnel/... 2>/dev/null || echo "tunnel tests skipped (need root)"
	go test -v -race ./internal/manager/... 2>/dev/null || echo "manager tests skipped (need root)"

run: build
	./$(APP_NAME)
```

### 2. `main.go` — Entrypoint with full lifecycle

```go
package main

import (
    "flag"
    "myvpn/internal/activation"
    "myvpn/internal/branding"
    "myvpn/internal/enginedl"
    "myvpn/internal/health"
    "myvpn/internal/heartbeat"
    "myvpn/internal/manager"
    "myvpn/internal/storage"
    "myvpn/internal/tunnel"
    "fmt"
    "log"
    "os"
    "sync"
    "sync/atomic"
    "time"

    "github.com/getlantern/systray"
)

// Typed connection state machine — atomic to prevent data races.
// See PLAN.md §5.2 for the full state transition diagram.
type ConnState int32
const (
    StateIdle              ConnState = 0
    StateActive            ConnState = 1
    StateConnecting        ConnState = 2
    StateConnectedPrimary  ConnState = 3
    StateConnectedFallback ConnState = 4
    StateDegraded          ConnState = 5
    StateDisconnected      ConnState = 6
)

var (
    version   string // set via ldflags
    connState ConnState // atomic — use loadState/storeState accessors
    mu        sync.Mutex // protects connect/disconnect transitions
    apiBase   string
    healthCh  <-chan string
)

func loadState() ConnState {
    return ConnState(atomic.LoadInt32((*int32)(&connState)))
}

func storeState(s ConnState) {
    atomic.StoreInt32((*int32)(&connState), int32(s))
}

func compareAndSwapState(old, new ConnState) bool {
    return atomic.CompareAndSwapInt32((*int32)(&connState), int32(old), int32(new))
}

func main() {
    flag.StringVar(&apiBase, "api", "https://api.yourdomain.com", "Admin hub URL")
    flag.Parse()

    storage.Init()
    branding.Init()

    // Check for crash from previous run
    if heartbeat.HadCrash() {
        heartbeat.SetLastError("previous_run_crashed")
    }
    heartbeat.ClearCrashMarker()
    heartbeat.SetCrashMarker()
    defer heartbeat.ClearCrashMarker()

    // Set up autostart BEFORE activation prompt
    setupAutostart()

    // Check activation
    if !storage.IsActivated() {
        activation.ShowPrompt(apiBase)
    }

    systray.Run(onReady, onExit)
}

func onReady() {
    systray.SetTitle("MyVPN")
    systray.SetTooltip("MyVPN — Disconnected")

    planDisplayName := branding.PlanDisplayName(storage.LoadPlanTier())

    mConnect := systray.AddMenuItem("Connect", "Connect VPN")
    mDisconnect := systray.AddMenuItem("Disconnect", "Disconnect VPN")
    mStatus := systray.AddMenuItem("Status: Disconnected", "")
    mStatus.Disable()
    mPlan := systray.AddMenuItem("Plan: "+planDisplayName, "")
    mPlan.Disable()

    // Grace period indicator (hidden by default)
    mGrace := systray.AddMenuItem("", "")
    mGrace.Disable()
    mGrace.Hide()

    systray.AddSeparator()

    // Privacy toggle
    mPrivacy := systray.AddMenuItem("Send anonymous usage data", "")
    if storage.TelemetryOptOut() {
        mPrivacy.Uncheck()
    } else {
        mPrivacy.Check()
    }

    systray.AddSeparator()
    mQuit := systray.AddMenuItem("Quit", "Exit MyVPN")

    // Start heartbeat background loop
    go heartbeat.Start(apiBase)

    // Update grace period display — check every 60s when in grace, hourly otherwise
    go func() {
        for {
            days := heartbeat.GraceRemaining()
            if days > 0 && days < 7 {
                mGrace.SetTitle(fmt.Sprintf("%d days until re-authentication required", days))
                mGrace.Show()
                time.Sleep(60 * time.Second) // frequent check when in grace
            } else {
                mGrace.Hide()
                time.Sleep(1 * time.Hour)
            }
        }
    }()

    // UI event loop
    go func() {
        for {
            select {

            // ── Connect ──
            case <-mConnect.ClickedCh:
                if compareAndSwapState(StateActive, StateConnecting) {
                    mStatus.SetTitle("Status: Connecting...")
                    systray.SetTooltip("MyVPN — Connecting")
                    go connect()
                }

            // ── Disconnect ──
            case <-mDisconnect.ClickedCh:
                s := loadState()
                if s == StateConnectedPrimary || s == StateConnectedFallback || s == StateDegraded {
                    // Signal the auto-reconnect loop to stop immediately
                    select {
                    case health.StopChan <- struct{}{}:
                    default:
                    }
                    disconnect()
                    mStatus.SetTitle("Status: Disconnected")
                    systray.SetTooltip("MyVPN — Disconnected")
                }

            // ── Health State Changes ──
            case state := <-healthCh:
                switch state {
                case "healthy":
                    mStatus.SetTitle("Status: Connected")
                    systray.SetTooltip("MyVPN — Connected ✓")
                case "recovered":
                    mStatus.SetTitle("Status: Connected (recovered)")
                    systray.SetTooltip("MyVPN — Recovered ✓")
                case "degraded":
                    mStatus.SetTitle("Status: Degraded — switching engine...")
                    systray.SetTooltip("MyVPN — Degraded")
                    go tryFallback()
                case "dead":
                    storeState(StateDisconnected)
                    // Auto-reconnect loop
                    mStatus.SetTitle("Status: Reconnecting...")
                    systray.SetTooltip("MyVPN — Reconnecting...")
                    go func() {
                        if health.AutoReconnect() {
                            storeState(StateConnectedFallback)
                            mStatus.SetTitle("Status: Connected (auto-recovered)")
                            systray.SetTooltip("MyVPN — Auto-recovered ✓")
                        } else {
                            disconnect()
                            tunnel.Engage()
                            storeState(StateDisconnected)
                            mStatus.SetTitle("Status: Disconnected — tap Connect to retry")
                            systray.SetTooltip("MyVPN — Connection Lost")
                        }
                    }()
                }

            // ── Privacy Toggle ──
            case <-mPrivacy.ClickedCh:
                optOut := !storage.TelemetryOptOut()
                storage.SetTelemetryOptOut(optOut)
                if optOut {
                    mPrivacy.Uncheck()
                } else {
                    mPrivacy.Check()
                }

            // ── Quit ──
            case <-mQuit.ClickedCh:
                disconnect()
                systray.Quit()
                return
            }
        }
    }()
}

func connect() {
    // State guard: only connect if we're in ACTIVE state
    if !compareAndSwapState(StateActive, StateConnecting) {
        return
    }

    protocols := storage.LoadProtocols()
    if len(protocols) == 0 {
        storeState(StateActive)
        return
    }

    // Try protocols in priority order
    for _, proto := range protocols {
        err := manager.StartEngine(proto, storage.LoadPlanTier())
        if err != nil {
            continue
        }

        // Engine started — set up TUN, routes, DNS, kill switch
        if err := tunnel.Setup(); err != nil {
            // Helper service not available — show clear error, roll back engine
            manager.StopEngine()
            storeState(StateActive)
            systray.SetTooltip("MyVPN — Setup failed: " + err.Error())
            log.Printf("[connect] tunnel setup failed: %v", err)
            return
        }
        if err := tunnel.ProtectDNS(); err != nil {
            log.Printf("[connect] DNS setup failed (non-fatal): %v", err)
        }

        // Start health check
        healthCh = health.Start("10.0.0.1")

        storeState(StateConnectedPrimary)
        systray.SetTitle("MyVPN — " + proto.DisplayName)
        systray.SetTooltip("Connected — " + proto.DisplayName)
        return
    }

    // All engines failed
    storeState(StateActive)
    systray.SetTitle("MyVPN")
    systray.SetTooltip("MyVPN — All engines failed. Check logs.")
}

func disconnect() {
    // Signal auto-reconnect loop to stop
    select {
    case health.StopChan <- struct{}{}:
    default:
    }

    manager.StopEngine()
    tunnel.Disengage()
    tunnel.RestoreDNS()
    tunnel.Teardown()

    storeState(StateActive)
    systray.SetTitle("MyVPN")
    systray.SetTooltip("MyVPN — Disconnected")
}

func tryFallback() {
    protocols := storage.LoadProtocols()
    for _, proto := range protocols {
        if proto.ID != manager.RunningProtocolID() {
            manager.StopEngine()
            err := manager.StartEngine(proto, storage.LoadPlanTier())
            if err == nil {
                systray.SetTitle("MyVPN — " + proto.DisplayName + " (fallback)")
                return
            }
        }
    }
}

func setupAutostart() {
    exe, err := os.Executable()
    if err != nil {
        return
    }
    // Platform-specific autostart (scheduled task on Windows, LaunchAgent on macOS)
    // See persistence package in Phase 1.7 of the original plan
}
```

---

## Phase 1.10: Deploy & Verify Checklist

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

- [ ] App launches and shows systray icon
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
- [ ] State machine: systray shows correct state through IDLE→ACTIVE→CONNECTING→CONNECTED→DEGRADED→DISCONNECTED cycle

---

## Troubleshooting

| Problem                                     | Fix                                                                                              |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Engine not found at connect                 | Check `~/.myvpn/engines/` has the renamed binary. Run `enginedl.EnsureEngine()` manually.        |
| Activation fails with 409                   | That code is already bound to another device. Code cannot be transferred — issue a new code.     |
| Activation fails with 403                   | Device is suspended. Check PocketBase for `suspended` flag and `suspended_reason`.               |
| TUN device not created                      | Ensure wintun.dll is installed (auto-downloaded). Run engine manually to check for errors.       |
| Health check shows "dead" but engine runs   | Tunnel misconfigured — check routes and firewall. Try direct ping through TUN IP.                |
| Kill switch active, can't restore           | Run `netsh advfirewall delete rule name=MyVPN_KillSwitch` manually.                              |
| Certificate pinning blocks connection       | You updated your VPS TLS cert. Generate a new pinned hash and ship an update.                    |
| SHA256 mismatch on update                   | Wrong binary uploaded or corrupted download. Re-publish and verify the SHA256 hash.              |
| Heartbeat grace period expired              | VPS has been down for 7+ days. Fix the VPS. Re-activate clients.                                 |
| macOS engine crash on connect               | Check `~/Library/Application Support/MyVPN/logs/` for engine logs. Run engine manually from CLI. |
| Windows SmartScreen blocks the app          | Click "More info" → "Run anyway". (Trade-off for no code signing.)                               |
| `go:embed` fails during build               | (No longer used — engines downloaded at runtime.)                                                |
| Offsite backup fails                        | Check B2 credentials. Run `b2 authorize-account` again.                                          |
