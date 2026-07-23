# Implementation Plan

> Target: ~1,260 lines of Go across 22 files, plus server configs and scripts.
> Time: One focused weekend for a competent Go developer.

## Prerequisites

- Go 1.22+ installed
- Fyne v2 dependencies (CGO):
  - Windows: `mingw-w64`
  - macOS: Xcode Command Line Tools
  - Linux: `sudo apt install libgl1-mesa-dev xorg-dev`
- A VPS with Ubuntu 22.04 ($6–12/month from Hetzner, DigitalOcean, or Vultr)
- A domain pointing to your VPS (for Let's Encrypt TLS)
- Hysteria 2 binary downloaded for your target platforms

## Phase 1: Project Scaffolding

### Step 1.1 — Initialize Go Module

```bash
mkdir -p myvpn-simplified/cmd/myvpn
cd myvpn-simplified
go mod init myvpn
```

### Step 1.2 — Directory Structure

```bash
mkdir -p internal/{storage,activation,manager,tunnel,gui}
mkdir -p server/pb_hooks
mkdir -p scripts
```

## Phase 2: Server (PocketBase + Caddy)

Do this first so you can test the client against a real API.

### Step 2.1 — VPS Setup

Deploy using `scripts/setup_vps.sh` (see Phase 7).

### Step 2.2 — PocketBase Collections

Create in PocketBase admin UI (`https://api.yourdomain.com/_/`):

**Collection: `codes`**
- `code` (text, required, unique) — format `XXXX-XXXX-XXXX`
- `tier` (select, required, options: `stealth`, `strike`)
- `used` (bool, default: false)
- `suspended` (bool, default: false)
- `middleman` (text, optional)
- Auto-created: `created`, `updated`

**Collection: `tier_configs`**
- `tier` (text, required, unique, options: `stealth`, `strike`)
- `config_json` (json, required) — full Hysteria 2 client config
- `active` (bool, default: true)
- Auto-created: `created`, `updated`

### Step 2.3 — JS Hook: activation.pb.js

**File:** `server/pb_hooks/activation.pb.js`

Purpose: Intercept `POST /api/collections/codes/records` to:
1. Look up the code in the `codes` collection
2. Validate it exists, is not already used, is not suspended
3. Mark it as used (first time only; allow if same code re-activates)
4. Fetch the matching `tier_configs` entry
5. Return the config JSON + tier to the client

```javascript
// server/pb_hooks/activation.pb.js
// Intercepts code validation requests

routerAdd("POST", "/api/collections/codes/records", (c) => {
  const data = $apis.requestInfo(c).data;
  const code = data.code;

  if (!code || typeof code !== 'string') {
    return c.json(400, { message: "Code is required" });
  }

  // Normalize
  const clean = code.trim().toUpperCase();

  // Find the code
  const record = $app.dao().findFirstRecordByData("codes", "code", clean);
  if (!record) {
    return c.json(404, { message: "Invalid activation code" });
  }

  if (record.getBool("suspended")) {
    return c.json(403, { message: "Account suspended — contact your middleman" });
  }

  // If already used, still allow (reinstall scenario) unless suspended
  // Mark as used on first activation
  if (!record.getBool("used")) {
    record.set("used", true);
    $app.dao().saveRecord(record);
  }

  // Fetch tier config
  const tier = record.getString("tier");
  const tierConfigs = $app.dao().findRecordsByFilter("tier_configs", `tier='${tier}' && active=true`);
  if (tierConfigs.length === 0) {
    return c.json(500, { message: "No configuration available for your tier" });
  }

  const tc = tierConfigs[0];
  const rawConfig = tc.get("config_json");

  return c.json(200, {
    tier: tier,
    config: rawConfig
  });
});
```

### Step 2.4 — JS Hook: suspension.pb.js

**File:** `server/pb_hooks/suspension.pb.js`

Purpose: Simple endpoint that checks if a code is suspended.

```javascript
// server/pb_hooks/suspension.pb.js
// Checks if a code is currently suspended

routerAdd("POST", "/api/check-suspension", (c) => {
  const data = $apis.requestInfo(c).data;
  const code = data.code;

  if (!code || typeof code !== 'string') {
    return c.json(400, { message: "Code is required" });
  }

  const clean = code.trim().toUpperCase();
  const record = $app.dao().findFirstRecordByData("codes", "code", clean);

  if (!record) {
    return c.json(404, { ok: false, reason: "Code not found" });
  }

  if (record.getBool("suspended")) {
    return c.json(200, { ok: false, reason: "suspended" });
  }

  return c.json(200, { ok: true });
});
```

### Step 2.5 — JS Hook: middleman_codes.pb.js (Optional)

**File:** `server/pb_hooks/middleman_codes.pb.js`

Purpose: Lets middlemen self-service generate codes without bothering the admin.
Middlemen hit this endpoint with a shared secret token, get fresh codes returned.

```javascript
// server/pb_hooks/middleman_codes.pb.js
// POST /api/middleman/request-codes
// Middlemen can request fresh codes using their secret token.
// Body: { "token": "middleman-secret", "tier": "stealth", "count": 5 }
// Response: { "codes": ["ABCD-EFGH-IJKL", ...] }

routerAdd("POST", "/api/middleman/request-codes", (c) => {
  const data = $apis.requestInfo(c).data;
  const token = data.token;
  const tier = data.tier || "eco";
  const count = Math.min(data.count || 5, 20);

  if (token !== "your-middleman-secret-token") {
    return c.json(403, { message: "Invalid token" });
  }

  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const codes = [];

  for (let i = 0; i < count; i++) {
    let code = "";
    for (let j = 0; j < 12; j++) {
      code += chars[Math.floor(Math.random() * chars.length)];
      if (j === 3 || j === 7) code += "-";
    }
    const record = new Record($app.dao().findCollectionByNameOrId("codes"));
    record.set("code", code);
    record.set("tier", tier);
    record.set("used", false);
    record.set("suspended", false);
    $app.dao().saveRecord(record);
    codes.push(code);
  }

  return c.json(200, { codes: codes });
});
```

### Step 2.6 — Caddy Config

**File:** `server/Caddyfile`

```caddy
api.yourdomain.com {
    # Static files (probe file for bandwidth detection)
    root * /var/www/html

    # PocketBase reverse proxy
    handle_path /api/* {
        reverse_proxy 127.0.0.1:8090
    }

    handle_path /_/* {
        reverse_proxy 127.0.0.1:8090
    }

    # Static files served directly (probe-file, etc.)
    handle {
        file_server
    }

    rate_limit {
        zone activation {
            key {remote_host}
            events 5
            window 10m
        }
        zone general {
            key {remote_host}
            events 100
            window 10s
        }
    }

    @activation path /api/collections/codes/records
    route @activation {
        rate_limit zone activation
    }

    @suspension path /api/check-suspension
    route @suspension {
        rate_limit zone general
    }
}
```

### Step 2.6 — Seed Data

Create at least one entry in `tier_configs` for each tier with the full Hysteria 2 client JSON (see ARCHITECTURE.md for examples).

Generate some test codes in the `codes` collection.

## Phase 3: Client — Core Packages

### Step 3.1 — Storage (`internal/storage/storage.go`)

Simplest possible persistent state. Single JSON file, no encryption, no rotation.

```go
package storage

// Types:
//   - Config struct: Code, Tier, ConfigJSON, Activated bool
//
// Functions:
//   - Init()        — create app data dir, load or init empty config
//   - SaveActivation(code, tier string, config []byte)
//   - LoadConfig()  — returns saved config or nil
//   - IsActivated() — bool
//   - Clear()       — delete saved state (for debugging)
//
// App data dir:
//   Windows: %APPDATA%/MyVPN
//   macOS:   ~/Library/Application Support/MyVPN
//   Linux:   ~/.local/share/MyVPN
//
// File: data.json
//   { "code": "...", "tier": "...", "config": {...}, "activated": true }
//
// No mutex needed for single-GUI-thread access.
// Use os.MkdirAll, os.ReadFile, os.WriteFile with 0600 perms.
```

### Step 3.2 — Activation (`internal/activation/activation.go`)

```go
package activation

// Types:
//   - ActivationResponse struct: Tier string, Config json.RawMessage
//
// Functions:
//   - Validate(apiBase, code string) (*ActivationResponse, error)
//
// Flow:
//   POST json {"code": code} to apiBase + "/api/collections/codes/records"
//   Headers: Content-Type: application/json
//   Parse response:
//     200 → return ActivationResponse
//     403 → return error "Account suspended — contact your middleman"
//     404 → return error "Invalid activation code"
//     429 → return error "Too many attempts — wait a few minutes"
//     else → return error with status code
//
// Device fingerprint (X-Device-Fingerprint header):
//   SHA256(MAC + disk serial + motherboard UUID)
//   Collected by fingerprint.go + platform-specific files.
//   Sent with every activation request.
//   Server stores on first activation, validates on subsequent.//
// Return: ActivationResponse with Tier + Config.
// Errors: "Account suspended", "Code already bound", etc.
```

### Step 3.3 — Fingerprint (`internal/activation/fingerprint.go` + platform files)

```go
// internal/activation/fingerprint.go
//
// CollectFingerprint() returns SHA256(MAC + diskSerial + moboUUID).
// Empty string if collection fails (app still works, but no binding).
//
// Platform-specific files implement the three collectors:
//
//   fingerprint_windows.go (build tag: windows)
//     getMAC()     → exec "getmac" → parse first active adapter
//     getDisk()    → exec "wmic diskdrive get serialnumber" → first non-empty
//     getMobo()    → exec "wmic csproduct get uuid" → first non-empty
//
//   fingerprint_darwin.go (build tag: darwin)
//     getMAC()     → ioctl on en0 (AF_LINK, LLADDR)
//     getDisk()    → IOKit IOService for "IOService" → kIOPropertySerialKey
//     getMobo()    → IOKit IOService for "IOPlatformExpertDevice" → kIOPlatformUUIDKey
//
//   fingerprint_linux.go (build tag: linux)
//     getMAC()     → read /sys/class/net/*/address for first non-loopback
//     getDisk()    → read /sys/block/*/device/serial (partial, may be empty)
//     getMobo()    → read /sys/devices/virtual/dmi/id/product_uuid
//                   (fallback: /etc/machine-id)
//
// Usage in activation.go:
//   fingerprint := CollectFingerprint()
//   req.Header.Set("X-Device-Fingerprint", fingerprint)
//
// The three raw values are concatenated with "|" separator before hashing:
//   hash := SHA256(mac + "|" + serial + "|" + uuid)
```

### Step 3.4 — Probe (`internal/probe/probe.go`)

```go
package probe

// Types:
//   - ProbeResult struct: BandwidthBps int, BaselineRTT time.Duration
//
// Functions:
//   - Run(apiBase string, tier string) (*ProbeResult, error)
//
// Flow:
//   Only runs for "strike" tier (stealth uses hardcoded cap).
//   GET <apiBase>/probe-file (a small file on the VPS, ~500KB, served by Caddy static root).
//   Measure time to download fully.
//   bandwidthBps = (fileSizeBytes * 8) / durationSeconds
//   targetBps = int(float64(bandwidthBps) * 0.8)  // 80% for headroom
//   baselineRTT = time to first byte
//
//   Returns ProbeResult (or nil if tier != "strike").
//
// VPS setup: Place a 500KB file at /var/www/html/probe-file on the VPS
// (or serve through Caddy from PocketBase). Content doesn't matter —
// /dev/urandom or a static file.

//   The result is passed to StartEngine() which sets Brutal target.
//   Eco and Stealth tiers: returns nil (no probe needed, speed cap in config JSON).
//   Strike tier: returns ProbeResult. Manager injects into Brutal target before launch.
```

### Step 3.5 — Manager (`internal/manager/process.go`)

```go
package manager

// Types:
//   - EngineStatus struct { Running bool, Tier string }
//
// Functions:
//   - StartEngine(configPath, tier string, enginePath string, probeResult *probe.ProbeResult) error
//   - StopEngine() error
//   - Status() EngineStatus
//
// Flow:
//   StartEngine:
//     If tier == "strike" and probeResult != nil:
//       Before launching, read the config JSON from configPath,
//       update brutal.up_mbps and brutal.down_mbps to
//       probeResult.TargetBps / 1_000_000 (convert bps to Mbps),
//       write config back.
//     Build command:
//       enginePath + "client" + "-c" + configPath + "--tun"
//       No --speed flag — all tiers get speed limits from the config JSON itself.
//       Eco has "speed": 5000000, Stealth has "speed": 48000000, Strike has brutald.
//     Set Stdout/Stderr to log file in app data dir
//     cmd.Start()
//     Store *exec.Cmd
//
//   StopEngine:
//     Kill process (SIGINT on Unix, TerminateProcess on Windows)
//     Wait for exit
//
//   The process wrapper handles:
//     - Finding the engine binary (next to app binary or in engines/ subdir)
//     - Writing engine logs to app data dir
//     - Reporting status
//
//   No process disguise, no sandboxing, no priority tweaking.
```

### Step 3.6 — Updater (`internal/updater/updater.go`)

```go
package updater

// Types:
//   UpdateCheck struct: Version string, Windows/MacOSIntel/MacOSARM *PlatformUpdate
//   PlatformUpdate struct: URL string, SHA256 string
//
// Functions:
//   - Check(apiBase, currentVersion string) (*UpdateCheck, error)
//       GET <apiBase>/update.json
//       Compare version strings.
//       If same version → return nil (no update).
//       If newer → return UpdateCheck with download info.
//
//   - DownloadAndApply(update *PlatformUpdate) error
//       Download zip from update.URL to a temp file.
//       Verify SHA256 of the downloaded zip.
//       Find the binary inside the zip (first entry, or named myvpn*).
//       Extract to a temp location.
//       Rename current executable to <myvpn>.prev
//       Move extracted binary to current executable path.
//       Return nil → caller should restart the app.
//
//   - Cleanup()
//       Delete <myvpn>.prev if current binary is running fine.
//       Called on startup.
//
// Flow:
//   On app launch:
//     updater.Cleanup()  // remove .prev from previous update
//     go func() {
//       time.Sleep(30s)  // wait for app to stabilize
//       updater.Check(apiBase, version)
//       if update available → updater.DownloadAndApply(update)
//       if success → os.Exit(0)  // app restarts via OS/batch script
//     }()
//
//   Every 6 hours:
//     Same check loop (goroutine in GUI).
//
//   No sentinel, no rollback, no signing keys.
//   If download or verify fails → log error, retry next check.
//   If app crashes after update → user re-downloads from website.
```

### Step 3.7 — Tunnel (`internal/tunnel/`)

```go
package tunnel

// This package exists as a FALLBACK in case Hysteria 2's --tun flag
// doesn't work on a particular platform or version.
//
// Preferred approach: engine --tun (no Go tunnel code needed)
//
// Fallback per platform (only if --tun fails):
//
// Linux:   ip tuntap add dev tun0 mode tun
//          ip link set dev tun0 up
//          ip addr add 10.0.0.2/24 dev tun0
//          ip route add 0.0.0.0/1 via 10.0.0.1 dev tun0
//          ip route add 128.0.0.0/1 via 10.0.0.1 dev tun0
//
// Windows: wintun API via cgo or netsh commands
//          (simplest: use wireguard-windows TUN driver)
//
// macOS:   utun interface via syscall
//          ifconfig utunX up
//          route add default -interface utunX
//
// Functions:
//   Setup() error      — create TUN if --tun not available
//   Teardown() error   — destroy TUN
//   Engage() error     — add default routes
//   Disengage() error  — remove routes
//   Guard() error      — redirect DNS (53) through tunnel
//   UnGuard() error    — restore DNS
//
// Implement and test only if --tun doesn't work on a target platform.
```

### Step 3.8 — GUI (`internal/gui/app.go`)

This is the biggest file (~320 lines). A Fyne v2 application that runs in
the system tray, with a control panel window.

**Architecture:**

```
main() → storage.Init() → updater.Cleanup() → gui.Run(apiBase, version)
  │
  gui.Run:
  ├── Create Fyne app (app.New())
  ├── Check storage.IsActivated()
  │   ├── false → show activation screen (modal window)
  │   └── true  → proceed
  ├── Create control panel window (hidden)
  ├── Set up system tray icon + menu
  ├── Start background goroutines:
  │   ├── Auto-update check (30s delay, then every 6h)
  │   ├── Connection status refresh (every 2s when connected)
  │   └── (Optional) Auto-start on boot
  └── app.Run()  // main loop (doesn't block, tray keeps running)
```

**System Tray (Fyne desktop.App):**

```go
import "fyne.io/fyne/v2/driver/desktop"

if desk, ok := a.(desktop.App); ok {
    // Set the tray icon (green/grey/red dot based on connection)
    desk.SetSystemTrayIcon(resourceIconPng)
    
    // Right-click menu
    desk.SetSystemTrayMenu(fyne.NewMenu("MyVPN",
        fyne.NewMenuItem("Show Window", func() { showWindow() }),
        fyne.NewMenuItem("Connect/Disconnect", func() { toggleConnect() }),
        fyne.NewMenuItem("Settings", func() { showSettings() }),
        fyne.NewMenuItem("Quit", func() { cleanupAndExit() }),
    ))
}
```

**Control Panel Window:**

- Window title: "MyVPN" (hidden on close, not destroyed)
- Top: connection indicator (colored dot + text)
- Tier name (e.g. "Strike · Gaming")
- Connection timer (HH:MM:SS)
- Speed display (optional, from engine log parsing)
- Connect/Disconnect button
- Settings section (expandable):
  - Code display (masked)
  - Theme: dark background (#0D0D0F), purple accent (#A855F7)

**Activation Screen:**

- Modal window (blocks until activated or quit)
- Code entry field
- Activate button
- Status messages
- On success: saves config + fingerprint, closes, creates tray

**Background Goroutines:**

```go
// 1. Auto-update checker
go func() {
    time.Sleep(30 * time.Second)  // wait for app to stabilize
    ticker := time.NewTicker(6 * time.Hour)
    for {
        update, err := updater.Check(apiBase, version)
        if err == nil && update != nil {
            // Download and apply
            err = updater.DownloadAndApply(getPlatformUpdate(update))
            if err == nil {
                // Update applied — restart app
                os.Exit(0)
            }
        }
        <-ticker.C
    }
}()

// 2. Connection status ticker (updates timer + speed every 2s)
go func() {
    for range time.NewTicker(2 * time.Second).C {
        if connected {
            updateTimer()
            updateSpeed()
        }
    }
}()
```

**Connect/Disconnect Flow:**

```
connect() — goroutine:
  1. Check suspension via /api/check-suspension
  2. Find engine binary (next to app or in engines/ subdir)
  3. Write config JSON to temp file in app data dir
  4. If tier == "strike":
       a. Run probe.Run(apiBase, "strike")
       b. If probe succeeds → update config's Brutal target
       c. If probe fails → use default 50 Mbps
  5. StartEngine(configPath, tier, enginePath, probeResult)
  6. If OK → update tray icon to green, update status
  7. Start connection stats reader (goroutine)

disconnect() — goroutine:
  1. StopEngine()
  2. Update tray icon to grey, update status
```

## Phase 4: Main Entrypoint

### Step 4.1 — `cmd/myvpn/main.go`

```go
package main

import (
    "flag"
    "myvpn/internal/gui"
    "myvpn/internal/storage"
)

var (
    version     = "0.1.0"
    adminHubURL = flag.String("hub", "https://api.yourdomain.com", "Admin hub URL")
)

func main() {
    flag.Parse()

    // Initialize storage (creates app data dir)
    storage.Init()

    // GUI handles everything from here
    gui.Run(*adminHubURL)
}
```

## Phase 5: Go Module Dependencies

### `go.mod`

```
module myvpn

go 1.22

require fyne.io/fyne/v2 v2.5.0
```

Run `go mod tidy` after writing all Go files to resolve dependencies.

## Phase 6: Build System

### Step 6.1 — Makefile

```makefile
APP_NAME := myvpn
VERSION := 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build-native build-windows build-macos-intel build-macos-arm clean

build-native:
	CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o $(APP_NAME) ./cmd/$(APP_NAME)

build-windows:
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		go build -ldflags="$(LDFLAGS) -H windowsgui" -o $(APP_NAME).exe ./cmd/$(APP_NAME)

build-macos-intel:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" -o $(APP_NAME)-intel ./cmd/$(APP_NAME)

build-macos-arm:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
		go build -ldflags="$(LDFLAGS)" -o $(APP_NAME)-arm ./cmd/$(APP_NAME)

clean:
	rm -f $(APP_NAME) $(APP_NAME).exe $(APP_NAME)-intel $(APP_NAME)-arm

test:
	go test ./...
```

## Phase 7: Scripts

### Step 7.1 — `scripts/setup_vps.sh`

One-command VPS bootstrap:

```bash
#!/bin/bash
# setup_vps.sh — Deploy admin hub to a fresh VPS
# Usage: ssh root@your-vps 'bash -s' < setup_vps.sh
#
# WARNING: Run this on a fresh Ubuntu 22.04 VPS only.
# It installs Caddy, PocketBase, and Hysteria 2 server.

set -euo pipefail

DOMAIN="${DOMAIN:-api.yourdomain.com}"
HYSTERIA_VERSION="${HYSTERIA_VERSION:-v2.4.0}"

# 1. System update + firewall
apt update && apt upgrade -y
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 22/tcp
ufw --force enable

# 2. Install Caddy
apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy

# 3. Install PocketBase
wget -qO- https://github.com/pocketbase/pocketbase/releases/download/v0.22.22/pocketbase_0.22.22_linux_amd64.zip -O /tmp/pb.zip
unzip -o /tmp/pb.zip -d /opt/pocketbase
rm /tmp/pb.zip
adduser --system --group --no-create-home pocketbase
chown -R pocketbase:pocketbase /opt/pocketbase

cat > /etc/systemd/system/pocketbase.service <<'EOF'
[Unit]
Description=PocketBase
After=network.target

[Service]
User=pocketbase
Type=simple
ExecStart=/opt/pocketbase/pocketbase serve --dir=/opt/pocketbase/data --http=127.0.0.1:8090
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable pocketbase
systemctl start pocketbase

# Wait for PocketBase to be ready
sleep 3

# 4. Configure Caddy
# 4a. Create probe file for bandwidth detection
mkdir -p /var/www/html
dd if=/dev/urandom of=/var/www/html/probe-file bs=1024 count=500 2>/dev/null
chmod 644 /var/www/html/probe-file

# 4b. Configure Caddy
cat > /etc/caddy/Caddyfile <<'EOF'
api.yourdomain.com {
    root * /var/www/html

    handle_path /api/* {
        reverse_proxy 127.0.0.1:8090
    }

    handle_path /_/* {
        reverse_proxy 127.0.0.1:8090
    }

    handle {
        file_server
    }
}
EOF

# Note: Replace api.yourdomain.com with your actual domain
# Run: caddy fmt --overwrite /etc/caddy/Caddyfile
# Run: systemctl reload caddy

# 5. Install Hysteria 2
wget -qO- "https://github.com/apernet/hysteria/releases/download/app%2F${HYSTERIA_VERSION}/hysteria-linux-amd64" -O /usr/local/bin/hysteria2
chmod +x /usr/local/bin/hysteria2

# Create single Hysteria 2 server config (three auth pools in one)
mkdir -p /etc/hysteria

cat > /etc/hysteria/server.yaml <<'EOF'
listen: :443
tls:
  cert: /etc/letsencrypt/live/api.yourdomain.com/fullchain.pem
  key: /etc/letsencrypt/live/api.yourdomain.com/privkey.pem
quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520
auth:
  type: password
  password:
    - "eco-pool-auth-token"       # CHANGE THIS
    - "stealth-pool-auth-token"   # CHANGE THIS
    - "strike-pool-auth-token"    # CHANGE THIS
EOF

# Install as systemd service
cat > /etc/systemd/system/hysteria.service <<'EOF'
[Unit]
Description=Hysteria 2 Server (all tiers)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/hysteria2 server -c /etc/hysteria/server.yaml
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable hysteria

# 6. Install auto-refill cron job (keeps code pool full)
cat > /usr/local/bin/refill-codes.sh <<'REFILL'
#!/bin/bash
# Auto-refill codes when fewer than 10 unused remain
# Change PB_TOKEN + MIDDLEMAN_TOKEN after first PocketBase admin setup
PB_API='https://api.yourdomain.com'
PB_TOKEN='CHANGE_ME_TO_YOUR_PB_ADMIN_TOKEN'
MIDDLEMAN_TOKEN='your-middleman-secret'

UNUSED=$(curl -s "$PB_API/api/collections/codes/records?filter=(used=false)&count=1" \
  -H "Authorization: Bearer $PB_TOKEN" | grep -o '"totalItems":[0-9]*' | cut -d: -f2)

if [ -z "$UNUSED" ] || [ "$UNUSED" -lt 10 ]; then
  echo "$(date): Refilling codes ($UNUSED remaining)" >> /var/log/refill-codes.log
  curl -s -X POST "$PB_API/api/middleman/request-codes" \
    -H "Content-Type: application/json" \
    -d '{"token":"'"$MIDDLEMAN_TOKEN"'","tier":"eco","count":10}' > /dev/null
  curl -s -X POST "$PB_API/api/middleman/request-codes" \
    -H "Content-Type: application/json" \
    -d '{"token":"'"$MIDDLEMAN_TOKEN"'","tier":"stealth","count":6}' > /dev/null
  curl -s -X POST "$PB_API/api/middleman/request-codes" \
    -H "Content-Type: application/json" \
    -d '{"token":"'"$MIDDLEMAN_TOKEN"'","tier":"strike","count":4}' > /dev/null
fi
REFILL
chmod +x /usr/local/bin/refill-codes.sh
echo "0 * * * * root /usr/local/bin/refill-codes.sh" > /etc/cron.d/refill-codes

echo "=== Setup complete ==="
echo ""
echo "Next steps:"
echo "1. Set your DNS A record to this server's IP"
echo "2. Open https://api.yourdomain.com/_/ and create admin account"
echo "3. Create 'codes' and 'tier_configs' collections"
echo "4. Copy pb_hooks/ to /opt/pocketbase/pb_hooks/"
echo "5. Update auth passwords in /etc/hysteria/server.yaml + PB_TOKEN in /usr/local/bin/refill-codes.sh"
echo "6. systemctl start hysteria"
echo "7. Generate initial codes via admin UI or ./scripts/generate_codes.sh"
```

### Step 7.2 — `scripts/generate_codes.sh`

```bash
#!/bin/bash
# generate_codes.sh — Batch-create activation codes via PocketBase API
# Usage: ./generate_codes.sh <api_base> <admin_token> <tier> <count> [middleman]
#
# Example:
#   ./generate_codes.sh https://api.yourdomain.com myadmintoken strike 10

API_BASE="${1}"
TOKEN="${2}"
TIER="${3}"
COUNT="${4}"
MIDDLEMAN="${5:-}"

if [ -z "$API_BASE" ] || [ -z "$TOKEN" ] || [ -z "$TIER" ] || [ -z "$COUNT" ]; then
    echo "Usage: $0 <api_base> <admin_token> <tier> <count> [middleman]"
    exit 1
fi

generate_code() {
    # Simple 12-char alphanumeric: XXXX-XXXX-XXXX
    # Avoid 0, O, I, 1 for readability
    CHARS="ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
    local code=""
    for i in $(seq 1 12); do
        idx=$((RANDOM % ${#CHARS}))
        code="${code}${CHARS:$idx:1}"
        if [ "$i" -eq 4 ] || [ "$i" -eq 8 ]; then
            code="${code}-"
        fi
    done
    echo "$code"
}

for i in $(seq 1 "$COUNT"); do
    CODE=$(generate_code)
    cat > /tmp/code_payload.json <<EOF
{
    "code": "${CODE}",
    "tier": "${TIER}",
    "used": false,
    "suspended": false
}
EOF

    if [ -n "$MIDDLEMAN" ]; then
        cat > /tmp/code_payload.json <<EOF
{
    "code": "${CODE}",
    "tier": "${TIER}",
    "used": false,
    "suspended": false,
    "middleman": "${MIDDLEMAN}"
}
EOF
    fi

    curl -s -X POST "${API_BASE}/api/collections/codes/records" \
        -H "Authorization: Bearer ${TOKEN}" \
        -H "Content-Type: application/json" \
        -d @/tmp/code_payload.json

    echo " — $CODE"
done
```

### Step 7.3 — `scripts/print_codes.sh`

Generates codes and outputs them in a printable format suitable for cutting
into card strips.

```bash
#!/bin/bash
# print_codes.sh — Generate codes and output in printable card format
# Usage: ./print_codes.sh <api_base> <admin_token> <tier> <count> [output_file]
#
# Example:
#   ./print_codes.sh https://api.yourdomain.com myadmintoken eco 50 cards.txt
#   ./print_codes.sh https://api.yourdomain.com myadmintoken strike 30

API_BASE="${1}"
TOKEN="${2}"
TIER="${3}"
COUNT="${4}"
OUTPUT="${5:-/dev/stdout}"

if [ -z "$API_BASE" ] || [ -z "$TOKEN" ] || [ -z "$TIER" ] || [ -z "$COUNT" ]; then
    echo "Usage: $0 <api_base> <admin_token> <tier> <count> [output_file]"
    exit 1
fi

# Generate and format for printing
{
    echo "=============================================="
    echo "  MyVPN - ${TIER} Codes"
    echo "  Generated: $(date)"
    echo "=============================================="
    echo ""

    for i in $(seq 1 "$COUNT"); do
        # Generate a random code
        CHARS="ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
        CODE=""
        for j in $(seq 1 12); do
            idx=$((RANDOM % ${#CHARS}))
            CODE="${CODE}${CHARS:$idx:1}"
            if [ "$j" -eq 4 ] || [ "$j" -eq 8 ]; then
                CODE="${CODE}-"
            fi
        done

        # Create the code in PocketBase
        curl -s -X POST "${API_BASE}/api/collections/codes/records" \
            -H "Authorization: Bearer ${TOKEN}" \
            -H "Content-Type: application/json" \
            -d "{\"code\":\"${CODE}\",\"tier\":\"${TIER}\",\"used\":false,\"suspended\":false}" > /dev/null

        # Output in card format (cut along the dashes)
        echo "┌─────────────────────────────┐"
        echo "│                             │"
        echo "│     Activation Code         │"
        echo "│                             │"
        echo "│       ${CODE}        │"
        echo "│                             │"
        echo "│  Tier: ${TIER}                    │"
        echo "│  Download: api.yourdomain.com  │"
        echo "│                             │"
        echo "└─────────────────────────────┘"
        echo ""
    done

    echo "Generated ${COUNT} codes."
} > "$OUTPUT"

# If output to file, show path and hint
if [ "$OUTPUT" != "/dev/stdout" ]; then
    echo "Codes saved to: $OUTPUT"
    echo "Print and cut along the borders."
fi
```

This script produces ASCII art cards that you can print, cut, and staple to
laminated instruction sheets. Each card has:
- The activation code in large text
- The tier name
- The download URL

## Phase 9: Testing Checklist

### Manual Tests (do these before distributing)

| Test | Expected |
|------|----------|
| Enter valid code | App shows tier name, Connect button becomes active |
| Enter invalid code | App shows "Invalid activation code" |
| Enter suspended code | App shows "Account suspended — contact your middleman" |
| Click Connect | Status changes to "Connecting", then "Connected" |
| Browse while connected | Traffic routes through tunnel, school blocks bypassed |
| Game while connected | Strike tier: stable ping (< 100ms). Stealth: higher ping, bufferbloat. |
| Click Disconnect | Status changes to "Disconnected", tunnel drops |
| Close app while connected | Next launch shows "Connected" → prompt to reconnect? Or just start fresh. |
| Reinstall app | Same code works again (code marked used but not suspended) |
| Suspend code in admin | Next connect attempt shows suspension message |
| Wrong API URL | App shows connection error gracefully |

### Cross-Platform Tests

| Platform | Test |
|----------|------|
| Windows 10/11 | Build with mingw, test on real hardware or VM |
| macOS Intel | Build with osxcross or native Mac |
| macOS Apple Silicon | Build native, test on M1/M2 Mac |
| Linux (optional) | Build-native, test with `ip tuntap` |

## Phase 8: Update Distribution Files

The app binaries and `update.json` are served as static files from the VPS.
No website — just files on disk that the auto-updater checks.

### Step 8.1 — `update.json`

```json
{
  "version": "0.1.0",
  "windows": {
    "url": "https://api.yourdomain.com/myvpn-windows.zip",
    "sha256": ""
  },
  "macos_intel": {
    "url": "https://api.yourdomain.com/myvpn-macos-intel.zip",
    "sha256": ""
  },
  "macos_arm": {
    "url": "https://api.yourdomain.com/myvpn-macos-arm.zip",
    "sha256": ""
  }
}
```

The agent updates this file whenever a new version is built. SHA256 is
computed from the zip file and inserted here. The desktop app checks
this endpoint every 6 hours and applies updates automatically.

Place this file at `/var/www/html/update.json` on the VPS (the setup script
already creates this directory and configures Caddy to serve it).

## Phase 10: Distribution (Physical Cards)

### For 20 Users

1. **Build for each platform:**
   ```bash
   make build-windows     # myvpn.exe
   make build-macos-intel # myvpn-intel
   make build-macos-arm   # myvpn-arm
   ```

2. **Bundle Hysteria 2 engine** alongside the binary:
   ```
   MyVPN/
   ├── myvpn.exe          (or myvpn-intel / myvpn-arm)
   └── engines/
       └── hysteria2.exe  (or hysteria2-darwin-amd64, etc.)
   ```

3. **Zip and upload** to Google Drive or Discord channel.

4. **Write short instructions:**
   - Windows: "Download, unzip, run myvpn.exe. If Windows Defender blocks it, click 'More info' → 'Run anyway'."
   - macOS: "Download, unzip. Ctrl+click myvpn → Open. Enter your code."

5. **Share with middlemen** — they give codes to students, students download the app.

## What a Future Agent Needs to Know

If you're an AI agent picking this up later, here's what matters:

1. **ARCHITECTURE.md** — system design. Read this first.
2. **BUSINESS.md** — why certain choices were made (tiers, pricing, decoy effect).
3. **IMPLEMENT.md** (this file) — step-by-step build guide for the developer.
4. **DEPLOY.md** — how to operate the server day-to-day.
5. **OPS.md** — **the agent operations manual.** Every API call, SSH command, and
   workflow you'll need to manage the service without writing code.
6. **UI-AESTHETICS.md** — visual design spec for the GUI. Do this **after** the
   backend is fully working.

### Key Implementation Notes

- The GUI uses **Fyne v2**. Familiarize yourself with `fyne.io/fyne/v2` widgets.
- The engine is **Hysteria 2**. Download the binary for each platform; the app executes it as a subprocess.
- The server is **PocketBase** with JS hooks. PocketBase auto-generates REST APIs from collections.
- **No CGO on the server side** (PocketBase is Go, but we interact via HTTP).
- **CGO is required on the client** because Fyne needs it.
- The most complex part is the GUI (`internal/gui/app.go` at ~250 lines). Everything else is straightforward HTTP calls and process management.

### Agent Management Model

The human **never writes code or SSH commands directly.** They tell the agent what
to do, and the agent runs the documented API calls and SSH commands from OPS.md.

| Human Says | Agent Does |
|-----------|-----------|
| "Suspend user ABC1" | Runs the suspend API call (OPS.md §1) |
| "Generate codes" | Runs generate_codes.sh or middleman API (OPS.md §2) |
| "Change the SNI/port" | Updates tier_configs + server.yaml (OPS.md §3) |
| "Push an update" | Builds binaries, updates configs (OPS.md §6) |
| "Server is down" | SSH in, checks logs, fixes (OPS.md §1) |
