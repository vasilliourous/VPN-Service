# MyVPN Client App — Developer Guide

> This document tells a Go developer exactly how to build, run, and understand
> the current MyVPN client (Wails v2 + Vue 3, `v5/client/`).
> The old Fyne-based guide was superseded by the Wails migration — see
> [`WAILS-MIGRATION.md`](WAILS-MIGRATION.md) for the migration history and
> rollback plan, and [`BACKEND-API.md`](BACKEND-API.md) for the exact
> `internal/` package API.

---

## 1. Prerequisites

- **Go 1.22+**
- **Node.js 18+** (for the Vue 3 frontend)
- **Wails CLI:** `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- **WebView build deps (CGO):**
  - Linux: `sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev` (Ubuntu 22.04)
    or `libwebkit2gtk-4.1-dev` (Ubuntu 24.04+)
  - macOS: Xcode Command Line Tools
  - Windows: WebView2 (included in Windows 10+); CI builds Windows with
    `CGO_ENABLED=0` (pure Go, WebView2 COM)
- **sing-box binary** in `v5/client/engines/` or alongside the built app
  (version 1.10.0, Shadowsocks AEAD-256-GCM over TCP)
- **VPS already deployed** (see `docs/DEPLOY.md`) with:
  - `ssserver` × 3 instances running
  - PocketBase with activation/heartbeat hooks
  - Caddy reverse proxy with TLS

---

## 2. Build Pipeline

### Quick Build (Current Platform)

```bash
cd v5/client
go mod tidy          # First time only — generates go.sum
make build           # wails build -tags frontend → dist/myvpn
```

The frontend must be built **before** Go compiles — `//go:embed all:frontend/dist`
in `assets_embed.go` (build tag `frontend`) requires `frontend/dist` to exist.
`wails build` does this automatically via `wails.json`; `make dev` builds it too.

For manual `go build`, pass the **full Wails tag set**:

```bash
go build -tags "frontend desktop production" .
```

`desktop` and `production` are required Wails build tags — they select the real
desktop implementation. Without them the binary compiles but is the stub app
that shows the "Wails applications will not build without the correct build
tags" error dialog at runtime.

### Development (Hot-Reload)

```bash
cd v5/client
make dev             # Builds frontend, then wails dev (Vite dev server)
```

### Cross-Compilation

```bash
make build-linux        # Linux amd64
make build-windows      # Windows amd64 (CGO_ENABLED=0 in CI)
make build-macos-intel  # macOS Intel
make build-macos-arm    # macOS Apple Silicon
make build-all          # All four targets
```

### CI/CD

The `.github/workflows/build.yml` workflow (repo root):
1. Lints and vets all Go code
2. Builds the Vue frontend, then the client with `-tags frontend`
3. Builds for Linux, macOS (Intel + ARM), Windows in parallel
4. Downloads the matching sing-box binary (1.10.0) for each platform
5. Bundles 2 binaries (`myvpn` + `sing-box`) into platform ZIPs
6. Creates a GitHub Release with a `checksums.sha256` file

**Trigger:** Push a tag starting with `v` (e.g., `v2.0.0`).

---

## 3. Package Structure

```
v5/client/
├── main.go               # Wails entry point: NewApp() → wails.Run() (binds App)
├── app.go                # App struct — wraps internal/ for the Vue UI, events
├── wails.json            # Wails project config (name, frontend build, version 2.0.0)
├── assets_embed.go       # //go:embed all:frontend/dist (build tag: frontend)
├── assets_stub.go        # Empty asset FS without the frontend tag
├── internal/
│   ├── storage/storage.go      # Persistent JSON state (thread-safe, atomic writes)
│   ├── activation/
│   │   ├── activation.go        # Activation client, server communication
│   │   ├── fingerprint.go       # SHA256 hardware fingerprint (cross-platform)
│   │   ├── fingerprint_linux.go # Linux source collection (sysfs)
│   │   ├── fingerprint_darwin.go# macOS source collection (networksetup/ioreg)
│   │   ├── fingerprint_windows.go# Windows source collection (PowerShell/WMI)
│   │   └── luhn.go             # Luhn-mod-N checksum validation
│   ├── heartbeat/heartbeat.go  # Periodic hub communication (5min→2h backoff)
│   ├── manager/process.go      # sing-box config generation + process lifecycle
│   ├── tunnel/tunnel.go        # Fallback TUN, kill switch, DNS (platform-specific)
│   └── updater/
│       ├── updater.go          # Two-phase sentinel update system
│       ├── recover.go          # Crash detection and auto-revert
│       ├── update_unix.go      # Unix binary swap + fork
│       └── update_windows.go   # Windows binary swap + fork (.old trick)
├── frontend/              # Vue 3 + Vite + TypeScript UI (embedded into binary)
│   └── src/
│       ├── App.vue             # Activation ↔ Main screen switch
│       ├── components/         # ActivationScreen, MainScreen, StatusIndicator, TierBadge
│       ├── stores/vpn.ts       # Reactive state + actions
│       ├── lib/bridge.ts       # Typed wrapper around window.runtime.Call
│       └── types/index.ts      # TypeScript mirrors of the Go API types
├── engines/               # sing-box binary placeholder (for local dev)
├── go.mod                 # module myvpn, go 1.22, wails v2.9.1
└── Makefile               # dev / build / build-all / test / vet targets
```

There is no `cmd/myvpn/`, `internal/gui/`, or `internal/helper/` — those were
moved to `v5/legacy/` (reference only, outside the Go module).

---

## 4. Core Flows

### 4.1 Startup Sequence

```
Wails App.Startup():
  1. storage.New("myvpn") — load or create state
  2. activation.NewClient(hubURL) — hub = https://networkingguides.duckdns.org
  3. GenerateFingerprint()
  4. findSingBox() — alongside the executable, then system paths
  5. manager.NewManager(singBoxPath, tmpConfigPath, "") + SetHelperMode(false)
  6. updater.CleanStaleMarkers(48h) + CheckOnStartup(false) + ConfirmIfPending()
   7. setupSystemTray() — dark background + dormant tray hooks (Wails v2.9 has no tray API; window is shown at launch)
  8. If already activated → startHeartbeatLoop(code)

Frontend:
  1. App.vue checks vpn.state.activated
  2. Not activated → ActivationScreen; activated → MainScreen
```

### 4.2 Activation Flow

```
Collect fingerprint (SHA256 of MAC + disk serial + motherboard UUID)
         │
Validate code client-side (Luhn-mod-N checksum, MYVPN-XXXX-XXXX-XXXX-C)
         │
POST /api/activate {code, fingerprint}
         │
200 OK → Save to storage (code, tier, serverConfig, fingerprint)
         Start heartbeat loop (5min → 2h backoff)
```

### 4.3 Connection Flow

```
On connect:
  1. Manager generates sing-box JSON config from saved serverConfig
  2. Manager.Start() spawns sing-box as a subprocess (direct mode)
  3. sing-box creates TUN interface (myvpn0, 10.0.0.1/30)
  4. All traffic routed through TUN → Shadowsocks → VPS
  5. Health loop: signal-0 check every 10s, auto-restart up to
     3 times within a 5-minute window
```

### 4.4 Heartbeat Flow

```
Loop:
  Every N minutes (N starts at 5, doubles on failure up to 2h):
    POST /api/heartbeat {code, fingerprint}
    Success → reset interval to 5min, check for:
      - Suspension signal → UI warning
      - Update available → trigger staged rollout download
      - Server config refresh → apply new server config
    Failure → increment failure counter
              double interval (up to 2h max)
              7-day grace period counts down (shown in UI)
```

### 4.5 Update Flow

```
Heartbeat says update_available for this device:
  1. Download binary to temp file (.new)
  2. Verify SHA256 checksum
  3. Save backup of current binary (.myvpn-backups/)
  4. Create .update-pending sentinel
  5. Swap binary (platform-specific: rename / .old trick)
  6. Fork new process with same args
  7. Parent exits

New process starts:
  Sees .update-pending → creates .update-confirmed
  Removes .update-pending
  Continues normal startup

On crash:
  Next start: .update-pending exists, .update-confirmed missing
  → Auto-revert to backup binary
  → Place .reverted sentinel
```

---

## 5. Server API Contracts

See `docs/API.md` for complete request/response schemas.

### Activation Endpoint

```
POST /api/activate
Content-Type: application/json

{
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
  "fingerprint": "<sha256 hash>"
}

→ 200: { "code": 200, "tier": "eco", "server_config": {...}, "udp_relay": false }
→ 400: Invalid code or missing fields
→ 403: Code already bound / suspended
→ 404: Code not found
→ 410: Code expired
→ 429: Rate limited
```

### Heartbeat Endpoint

```
POST /api/heartbeat
Content-Type: application/json

{
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
  "fingerprint": "<sha256 hash>"
}

→ 200: { "status": "ok", "tier": "eco", "server_config": {...},
         "update_available": "2.1.0", "update_url": "...",
         "update_sha256": "..." }
→ 403: Code suspended
→ 404: Code not found
```

---

## 6. Storage Format

**File:** `os.UserConfigDir()/myvpn/storage.json`
(`~/.config/myvpn/storage.json` on Linux, `~/Library/Application Support/myvpn/`
on macOS, `%APPDATA%\myvpn\` on Windows).

```json
{
  "code": "MYVPN-ABCD-EFGH-IJKL-M",
  "tier": "eco",
  "device_fingerprint": "<sha256>",
  "server_config": {
    "server": "networkingguides.duckdns.org",
    "server_port": 8443,
    "password": "...",
    "method": "aes-256-gcm"
  },
  "udp_relay": false,
  "activated": true,
  "version": "2.0.0",
  "update_pending": false,
  "update_version": "",
  "update_sha256": "",
  "update_timestamp": 0,
  "last_heartbeat_ok": 1700000000,
  "heartbeat_failures": 0,
  "crashed_on_update": false,
  "crash_timestamp": 0
}
```

All writes are atomic (write to `.tmp`, fsync, then `rename`). File perms
`0600`, directory `0700`, 3 rotating backups (`storage.json.bak.{0,1,2}`).

---

## 7. sing-box Configuration

The manager generates a config like this (`generateConfig` in
`internal/manager/process.go`):

```json
{
  "log": { "level": "warn" },
  "dns": {
    "final": "dns-direct",
    "servers": [
      { "tag": "dns-direct", "address": "https://1.1.1.1/dns-query", "detour": "direct" },
      { "tag": "dns-tunnel", "address": "https://1.1.1.1/dns-query" }
    ]
  },
  "inbounds": [
    { "type": "tun", "tag": "tun-in",
      "interface_name": "myvpn0",
      "address": ["10.0.0.1/30"],
      "mtu": 1500,
      "auto_route": true,
      "strict_route": true }
  ],
  "outbounds": [
    { "type": "shadowsocks", "tag": "proxy",
      "server": "networkingguides.duckdns.org",
      "server_port": 8443,
      "method": "aes-256-gcm",
      "password": "..." },
    { "type": "direct", "tag": "direct" },
    { "type": "dns", "tag": "dns-out" }
  ],
  "route": {
    "rules": [ { "protocol": "dns", "outbound": "dns-out" } ],
    "auto_detect_interface": true,
    "final": "proxy"
  }
}
```

Set `MYVPN_DEBUG=1` to switch the log level to `debug`. The Strike tier (or any
activation with `udp_relay`) adds `"udp_over_tcp": { "enabled": true, "version": 2 }`
to the shadowsocks outbound.

---

## 8. Platform-Specific Notes

### Linux
- **TUN:** sing-box creates `myvpn0` directly (user has admin rights on BYOD)
- **Fingerprint:** Reads `/sys/class/net/*/address`, `/sys/block/*/device/serial`,
  `/sys/class/dmi/id/product_uuid`, `/etc/machine-id`
- **WebView deps:** `libgtk-3-dev` + `libwebkit2gtk-4.0-dev` (22.04) or `4.1` (24.04+)
- **Build tag:** add `webkit2_41` when building against WebKitGTK 4.1 (Ubuntu 24.04+):
  `go build -tags "frontend desktop production webkit2_41" .`

### macOS
- **TUN:** sing-box creates `myvpn0` directly
- **Fingerprint:** `networksetup -getmacaddress en0/en1`, `ioreg IOPlatformSerialNumber`,
  `ioreg IOPlatformUUID`
- **WebView:** Xcode Command Line Tools (clang)
- **Notarization:** Requires an Apple Developer account for distribution

### Windows
- **TUN:** sing-box creates the TUN interface (Wintun driver) directly
- **Fingerprint:** PowerShell `Get-NetAdapter` (MAC), WMI `Win32_DiskDrive`
  (disk serial), WMI `Win32_ComputerSystemProduct` (motherboard UUID)
- **Build:** CI compiles with `CGO_ENABLED=0` (`-H windowsgui` — no console window);
  local `wails build` may need MinGW-w64 on Linux
- **WebView:** WebView2 (included in Windows 10+)

---

## 9. Security Considerations

| Concern | Mitigation |
|---------|------------|
| Code brute force | Client-side Luhn check first, server rate limits (5/10min) |
| Device cloning | Multi-source fingerprint (MAC + disk + mobo) |
| Traffic fingerprinting | Shadowsocks AEAD (no TLS, no JA3 fingerprint) |
| Update subversion | SHA256 checksum verification before install |
| Crash on update | Two-phase sentinel with auto-revert |
| Hub compromise | HTTPS to the hub; no client-side certificate pinning in the current build |
| Data exposure | Storage is at-rest encrypted by the OS only |
| Reverse engineering | No protocol names in the UI; engine is a separate binary |

---

## 10. Testing Checklist

Before releasing a new client build:

- [ ] `go vet ./...` — no warnings
- [ ] `go test ./...` — unit tests pass
- [ ] Frontend builds: `cd frontend && npm install && npm run build`
- [ ] `go build -tags "frontend desktop production" .` — embeds the real UI
- [ ] Builds for all 3 target platforms (CI does this automatically)
- [ ] Activation: valid code → success
- [ ] Activation: invalid code → client-side fail (no server call)
- [ ] Activation: used code on same device → success
- [ ] Activation: used code on different device → 403
- [ ] Heartbeat: interval doubles on failure, resets on success
- [ ] Heartbeat: 7-day grace period counted correctly in the UI
- [ ] Heartbeat: update signal triggers update notification
- [ ] Update: SHA256 mismatch → download rejected
- [ ] Update: successful update → new binary runs, confirmed sentinel created
- [ ] Update: crash new binary → auto-revert on next start
- [ ] Connection: TUN interface created (`myvpn0`, 10.0.0.1/30)
- [ ] Connection: disconnect stops sing-box and removes the config file
- [ ] Diagnostics: report includes all fields without PII leaks
- [ ] Window: appears on launch; closing it quits the app (no tray icon yet)
