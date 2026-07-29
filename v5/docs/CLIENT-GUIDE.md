# MyVPN Client App — Developer Guide

> **This document tells a Go developer exactly how to build a MyVPN-compatible client.**  
> The reference implementation is in `v5/client/`.  
> You can use this guide to build from scratch, port to another language, or
> understand the existing code.

---

## 1. Prerequisites

- **Go 1.22+** with CGO (for Fyne v2 GUI)
- **Fyne v2 dependencies:**
  - Linux: `sudo apt install libgl1-mesa-dev xorg-dev`
  - Windows: MinGW-w64 (`x86_64-w64-mingw32-gcc`)
  - macOS: Xcode Command Line Tools
- **VPS already deployed** (see `docs/DEPLOY.md`) with:
  - `ssserver` × 3 instances running
  - PocketBase with activation/heartbeat hooks
  - Caddy reverse proxy with TLS

---

## 2. Build Pipeline

### Quick Build (Current Platform)

```bash
cd v5/client
go mod tidy         # First time only — generates go.sum
make build          # Builds for current OS/arch → dist/myvpn
```

### Cross-Compilation

```bash
make build-linux       # Linux amd64 (CGO)
make build-windows     # Windows amd64 (requires MinGW CC)
make build-macos-intel # macOS Intel (requires osxcross or macOS builder)
make build-macos-arm   # macOS Apple Silicon
make build-all         # All platforms
```

### Full Bundle (Client + Engine)

```bash
make bundle-all        # Build + download sing-box + zip for distribution
```

### TUN Helper (No CGO)

```bash
make build-helper              # Current platform
make build-helper-windows      # Windows
make build-helper-macos-intel  # macOS Intel
make build-helper-macos-arm    # macOS ARM
```

### CI/CD

The `.github/workflows/build.yml` workflow:
1. Lints and vets all code
2. Builds for Linux, macOS (Intel + ARM), Windows
3. Downloads matching sing-box binary for each platform
4. Bundles into platform-specific ZIPs
5. Creates a GitHub Release with checksums

**Trigger:** Push a tag starting with `v` (e.g., `v1.0.0`).

---

## 3. Package Structure

```
cmd/myvpn/main.go           # Entry point: flags → update recovery → GUI
internal/
├── storage/storage.go      # Persistent JSON state (thread-safe, atomic writes)
├── activation/
│   ├── activation.go        # Activation client, server communication
│   ├── fingerprint.go       # SHA256 hardware fingerprint (cross-platform)
│   ├── fingerprint_linux.go # Linux source collection
│   ├── fingerprint_darwin.go# macOS source collection
│   ├── fingerprint_windows.go# Windows source collection
│   └── luhn.go             # Luhn-mod-N checksum validation
├── gui/
│   ├── app.go              # Fyne v2 desktop app (activation + main screens)
│   └── diagnostics.go      # Support report generation
├── heartbeat/heartbeat.go  # Periodic hub communication (5min→2h backoff)
├── manager/process.go      # sing-box config generation + process lifecycle
├── tunnel/tunnel.go        # TUN interface, kill switch, DNS (platform-specific)
├── updater/
│   ├── updater.go          # Two-phase sentinel update system
│   ├── recover.go          # Crash detection and auto-revert
│   ├── update_unix.go      # Unix binary swap + fork
│   └── update_windows.go   # Windows binary swap + fork (.old trick)
└── helper/
    ├── main.go             # Privileged TUN helper service (IPC daemon)
    └── install.go          # Helper installation (systemd/launchd/Windows Service)
```

---

## 4. Core Flows

### 4.1 Startup Sequence

```
main.go:
  1. Parse flags (--hub, --revert, --version)
  2. Run update recovery check (two-phase sentinel)
  3. Initialize storage (~/.config/myvpn/storage.json)
  4. Initialize activation client
  5. Launch Fyne GUI

GUI (app.go):
  1. Check storage.IsActivated()
  2. If not activated → show Activation Screen
  3. If activated → show Main Screen, start heartbeat
```

### 4.2 Activation Flow

```
Collect fingerprint (SHA256 of MAC + disk serial + motherboard UUID)
         │
Validate code client-side (Luhn-mod-N checksum)
         │
POST /api/activate {code, fingerprint}
         │
200 OK → Save to storage (code, tier, serverConfig, fingerprint)
         Start heartbeat loop
```

### 4.3 Connection Flow

```
On connect:
  1. Manager generates sing-box JSON config from saved serverConfig
  2. If helper available → send config via IPC socket
  3. Else → spawn sing-box as subprocess
  4. sing-box creates TUN interface (10.0.0.2/30)
  5. All traffic routed through TUN (except RFC1918)
  6. Health check every 15s through the tunnel
```

### 4.4 Heartbeat Flow

```
Loop:
  Every N minutes (N starts at 5, doubles on failure up to 2h):
    GET /api/heartbeat?code=X&fp=Y
    Success → reset interval to 5min, check for:
      - Suspension signal (403) → show "account suspended"
      - Update available → trigger staged rollout download
      - Tier config changes → apply new server config
    Failure → increment failure counter
              double interval (up to 2h max)
              7-day grace period counts down
              on grace expiry → "tap to retry" screen
```

### 4.5 Update Flow

```
Heartbeat says update_available for my bucket:
  1. Download binary to temp file
  2. Verify SHA256 checksum
  3. Save backup of current binary
  4. Create .update-pending sentinel
  5. Swap binary (platform-specific)
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
GET /api/heartbeat?code=MYVPN-...&fp=<sha256>

→ 200: { "status": "ok", "tier": "eco", "server_config": {...},
         "update_available": "1.1.0", "update_url": "...",
         "update_sha256": "..." }
→ 403: Code suspended
→ 404: Code not found
```

---

## 6. Storage Format

**File:** `~/.config/myvpn/storage.json` (Linux)  
Or platform-appropriate app data directory.

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
  "version": "1.0.0",
  "update_pending": false,
  "last_heartbeat_ok": 1700000000,
  "heartbeat_failures": 0
}
```

All writes are atomic (write to `.tmp`, then `rename`).

---

## 7. sing-box Configuration

The manager generates a config like this:

```json
{
  "log": { "level": "warn" },
  "dns": {
    "final": "1.1.1.1",
    "servers": { "default": { "address": "1.1.1.1", "detour": "proxy" } }
  },
  "inbounds": [
    { "type": "tun", "tag": "tun-in",
      "interface_name": "myvpn0",
      "address": "10.0.0.2/30",
      "mtu": 1500,
      "auto_route": true,
      "strict_route": true,
      "sniff": true }
  ],
  "outbounds": [
    { "type": "shadowsocks", "tag": "proxy",
      "server": "networkingguides.duckdns.org",
      "server_port": 8443,
      "method": "aes-256-gcm",
      "password": "..." }
  ],
  "route": {
    "rules": [
      { "rule": "geoip:private", "outbound_tag": "direct" }
    ],
    "auto_detect_interface": true,
    "final": "proxy"
  }
}
```

---

## 8. Platform-Specific Notes

### Linux
- **TUN:** `ip tuntap add dev myvpn0 mode tun` (requires root)
- **Helper:** systemd service at `/etc/systemd/system/myvpn-helper.service`
- **Fingerprint:** Reads `/sys/class/dmi/id/product_uuid`, `/sys/block/*/device/serial`
- **CGO deps:** `libgl1-mesa-dev`, `xorg-dev`

### macOS
- **TUN:** `ifconfig utunX ...` (via helper service)
- **Helper:** launchd daemon at `/Library/LaunchDaemons/com.myvpn.helper.plist`
- **Fingerprint:** IOKit calls for disk serial and platform UUID
- **CGO:** Xcode Command Line Tools (clang)
- **Notarization:** Requires Apple Developer account for distribution

### Windows
- **TUN:** Requires `myvpn-helper.exe` running as SYSTEM service
- **Helper:** Windows Service, communicates via named pipe `\\.\pipe\MyVPNHelper`
- **Fingerprint:** `wmic` commands for disk and motherboard serials
- **CGO:** MinGW-w64 (`x86_64-w64-mingw32-gcc`)
- **GUI:** Fyne uses DirectX on Windows (no extra deps)

---

## 9. Security Considerations

| Concern | Mitigation |
|---------|------------|
| Code brute force | Client-side Luhn check first, server rate limits (5/10min) |
| Device cloning | Multi-source fingerprint (MAC + disk + mobo) |
| Traffic fingerprinting | Shadowsocks AEAD (no TLS, no JA3 fingerprint) |
| Update subversion | SHA256 checksum verification before install |
| Crash on update | Two-phase sentinel with auto-revert |
| Hub compromise | Certificate pinning (planned, not implemented in v4) |
| Data exposure | All storage is at-rest encrypted by OS only |
| Reverse engineering | Binary renaming, no protocol names in UI |

---

## 10. Testing Checklist

Before releasing a new client build:

- [ ] `go vet ./...` — no warnings
- [ ] Builds for all 3 target platforms
- [ ] Activation: valid code → success
- [ ] Activation: invalid code → client-side fail (no server call)
- [ ] Activation: used code on same device → success
- [ ] Activation: used code on different device → 403
- [ ] Heartbeat: interval doubles on failure, resets on success
- [ ] Heartbeat: 7-day grace period counted correctly
- [ ] Heartbeat: update signal triggers download
- [ ] Update: SHA256 mismatch → download rejected
- [ ] Update: successful update → new binary runs, confirmed sentinel created
- [ ] Update: crash new binary → auto-revert on next start
- [ ] Connection: TUN interface created with correct IP
- [ ] Connection: kill switch blocks non-VPN traffic on disconnect
- [ ] Diagnostics: report includes all fields without PII leaks
- [ ] Grace period: heartbeat fails for 7 days → "tap to retry"
