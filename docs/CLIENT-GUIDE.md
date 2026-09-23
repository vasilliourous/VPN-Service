# Locus Client App — Developer Guide

> **⚠️ STATUS: describes the ARCHIVED client.**
> This document describes `legacy/wails-client/` — the retired Go + Wails +
> sing-box client. **The shipping client is `client/`** (the Tauri fork of Clash
> Verge Rev, tunnelling through mihomo), which has its own docs in
> `client/docs/`. Paths below that read `legacy/wails-client/` were rewritten
> from `legacy/wails-client/` in the 2026-09-23 restructure; the content was not otherwise
> reviewed. Kept because it documents the contract the fork must reproduce —
> see `legacy/wails-client/ARCHIVED.md`.


> This document tells a Go developer exactly how to build, run, and understand
> the current Locus client (Wails v2 + Vue 3, `legacy/wails-client/`).
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
  - Windows: WebView2 (included in Windows 10+); CI builds Windows with
    `CGO_ENABLED=0` (pure Go, WebView2 COM)
- **sing-box binary** in `legacy/wails-client/engines/` or alongside the built app
  (version 1.12.1, Shadowsocks AEAD-256-GCM over TCP)
- **VPS already deployed** (see `DEPLOY.md`) with:
  - `ssserver` × 3 instances running
  - PocketBase with activation/heartbeat hooks
  - Caddy reverse proxy with TLS

---

## 2. Build Pipeline

### Quick Build (Current Platform)

```bash
cd legacy/wails-client
go mod tidy          # First time only — generates go.sum
make build           # wails build -tags frontend → dist/locus
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

### Versioning (single source of truth)

The runtime client version comes from **`the root VERSION file`** (one line, e.g. `2.0.1`).

- `make build` reads `the root VERSION file` and injects it via
  `-ldflags "-X main.version=$(VERSION)"`.
- CI (`.github/workflows/build.yml`) injects the same file's value, or — on a
  `v*` tag push — the tag with its leading `v` stripped (so `git tag v2.0.1`
  produces a binary reporting `2.0.1`).
- To release: **bump `the root VERSION file`**, commit, then `git tag v<same>` + push.
  `wails.json` and `package.json` "version" fields are build metadata and are
  not the runtime source.

### Development (Hot-Reload)

```bash
cd legacy/wails-client
make dev             # Builds frontend, then wails dev (Vite dev server)
```

### Cross-Compilation

```bash
make build-linux        # Linux amd64
make build-windows      # Windows amd64 (CGO_ENABLED=0 in CI)
make build-macos-intel  # macOS Intel (needs osxcross or Mac builder)
make build-macos-arm    # macOS Apple Silicon
make build-all          # All four targets
```

### CI/CD

The `.github/workflows/build.yml` workflow (repo root):
1. Lints (`golangci-lint`) and vets all Go code
2. Builds the Vue frontend, then the admin console, then the client with
   `-tags frontend`
3. Builds for Linux, macOS (Intel + ARM), and Windows in parallel
4. Downloads the matching sing-box binary (**1.12.1**) for each platform
5. Bundles 2 binaries (`locus` + `sing-box`) into **4** platform ZIPs — the
   portable artefact, which remains supported
6. Builds the **installers**: a Windows Inno Setup `.exe` and macOS `.dmg`
   disk images (per architecture). These install into a directory the
   application owns, which is what makes in-place self-update reliable — see
   §3.1. Linux ships portable only, by design.
7. Publishes the **raw per-platform executables** + `manifest.json` +
   `checksums.sha256` — the raw binaries are what the auto-updater consumes
   (it cannot unpack a zip, and must never be handed an installer)

**Trigger:** Push a tag starting with `v` (e.g., `v2.0.0`).

> The hub's `fetch-release.py` resolves assets **by name** from an allowlist of
> the four raw binaries plus `manifest.json`. The installer and DMG assets on the
> release are therefore ignored by the update pipeline rather than breaking its
> all-or-nothing check — an installer is not a valid update payload.

---

## 3. Package Structure

```
legacy/wails-client/
├── main.go               # Wails entry point: NewApp() → wails.Run() (binds App)
├── app.go                # App struct — wraps internal/ for the Vue UI, events
├── wails.json            # Wails project config (name, frontend build; version = build metadata only)
├── assets_embed.go       # //go:embed all:frontend/dist (build tag: frontend)
├── assets_stub.go        # Empty asset FS without the frontend tag
├── internal/
│   ├── storage/storage.go      # Persistent JSON state (thread-safe, atomic writes)
│   ├── activation/
│   │   ├── activation.go        # Activation client, server communication
│   │   ├── fingerprint_linux.go # Self-contained fingerprint (shared logic + Linux collector)
│   │   ├── fingerprint_windows.go# Self-contained fingerprint (shared logic + Windows collector)
│   │   ├── fingerprint_darwin.go # Self-contained fingerprint (shared logic + macOS collector)
│   │   └── luhn.go             # Luhn-mod-N checksum validation
│   ├── heartbeat/heartbeat.go  # Periodic hub communication (5min→2h backoff)
│   ├── manager/                # sing-box config generation + process lifecycle
│   │   ├── process.go           #   config generation, spawn/stop, health loop
│   │   ├── watchdog.go          #   10s tunnel probe + recovery escalation ladder
│   │   ├── process_{unix,windows}.go # process-group detach, platform specifics
│   │   └── selfheal_{unix,windows}.go # kill foreign engines, drop stale locus0 TUN
│   ├── pinned/pinned.go        # Hub TLS SPKI pinning (fail-closed once configured)
│   ├── tray/                   # OPT-IN system tray (LOCUS_TRAY=1); no-op on darwin
│   ├── tunnel/tunnel.go        # Fallback TUN, kill switch, DNS (platform-specific)
│   └── updater/
│       ├── updater.go          # Two-phase sentinel update system
│       ├── recover.go          # Crash detection and auto-revert
│       ├── version.go          # Numeric version compare — refuses downgrades
│       ├── update_linux.go      # Linux binary swap + fork
│       ├── update_windows.go   # Windows binary swap + fork (.old trick)
│       └── update_darwin.go    # macOS binary swap + fork
├── frontend/              # Vue 3 + Vite + TypeScript UI (embedded into binary)
│   └── src/
│       ├── App.vue             # Activation ↔ Main screen switch
│       ├── components/         # ActivationScreen, MainScreen, StatusIndicator, TierBadge
│       ├── stores/vpn.ts       # Reactive state + actions
│       ├── lib/bridge.ts       # Typed wrapper around window.runtime.Call
│       └── types/index.ts      # TypeScript mirrors of the Go API types
├── engines/               # sing-box binary placeholder (for local dev)
├── rsrc_windows_*.syso    # requireAdministrator manifest (Windows)
├── go.mod                 # module locus, go 1.22, wails v2.12.0
└── Makefile               # dev / build / build-all / test / vet targets
```

There is no `cmd/locus/`, `internal/gui/`, or `internal/helper/` — those were
removed in the Wails migration. The pre-migration Fyne client lives in `v4/`
(reference only, outside the Go module).

---

## 4. Core Flows

### 4.1 Startup Sequence

```
Wails App.Startup():
  1. storage.New("locus") — load or create state
  2. activation.NewClient(hubURL) — hub = https://networkingguides.duckdns.org
  3. GenerateFingerprint()
  4. findSingBox() — alongside the executable, then system paths
  5. manager.NewManager(singBoxPath, tmpConfigPath, "") + SetHelperMode(false)
  6. updater.CleanStaleMarkers(48h) + CheckOnStartup(false) + ConfirmIfPending()
   7. setupSystemTray() — dark background; if `LOCUS_TRAY=1`, starts the optional systray (live status + Connect/Disconnect/Open/Quit). OFF by default because Wails v2 has no tray API and it must be validated per OS. Closing the window still quits (no close-to-hide).
  8. If already activated → startHeartbeatLoop(code)

Frontend:
  1. App.vue checks vpn.state.activated
  2. Not activated → ActivationScreen; activated → MainScreen
```

### 4.2 Activation Flow

```
Collect fingerprint (SHA256 of MAC + disk serial + motherboard UUID)
         │
Validate code client-side (Luhn-mod-N checksum, RQ-XXXX-XXXX-XXXX-C)
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
  3. sing-box creates TUN interface (locus0, 10.0.0.1/30)
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
  1. Resolve the install location (internal/install) and ensure the private
     staging directory exists inside it
  2. Download binary to <staging>/.new
  3. Verify SHA256 checksum (before anything is replaced)
  4. Save backup of current binary (.locus-backups/)
  5. Create .update-pending sentinel
  6. Swap binary (platform-specific: rename / .old trick)
  7. Fork new process with same args
  8. Parent exits

New process starts:
  Sees .update-pending → creates .update-confirmed
  Removes .update-pending
  Continues normal startup

On crash:
  Next start: .update-pending exists, .update-confirmed missing
  → Auto-revert to backup binary
  → Place .reverted sentinel
```

**The download is never staged in the directory the app was launched from.**
It goes into a private subdirectory of the *resolved install location* (see
§3.1), the file handle is closed before the rename, and a transient blocker is
retried a bounded number of times. A transient blocker is an antivirus scanner,
the search indexer or a sync client holding a handle on a freshly written
executable — which is what happens to any `.exe` that appears in Downloads, and
is why the portable build used to fail to update itself there.

---

## 3.1 Install Locations & Deployment Modes

`internal/install` resolves where this copy of Locus lives and how it was
deployed. The distinction decides whether an in-place self-update is safe, and it
is reported in Diagnostics.

| Platform | Install location | Mode |
|---|---|---|
| Windows | `%ProgramFiles%\Locus`, else `%LOCALAPPDATA%\Programs\Locus` | `installed` |
| macOS | `/Applications/Locus.app` (or `~/Applications`) | `installed` |
| Linux | `~/.local/bin`, else `~/.local/share/locus/bin` | `installed` |
| Anywhere else | the launched directory | `portable` |

Additional modes:

- `portable` — an extracted zip or a USB stick. **Supported, not a failure.**
  Updates are staged privately inside the launched directory.
- `unwritable` — the directory cannot be written to; self-update needs a
  writable location and the client says so rather than failing mid-download.
- `immutable` — a read-only mounted bundle (an AppImage). No write can succeed,
  so the remedy is replacing the bundle, not elevation.

Detection is a real write probe rather than a permissions check, because mode
bits are close to meaningless on Windows. The launch directory is only adopted
as an install location when it already *exists* — a location is never created
speculatively, so a portable user is not silently relocated.

**Linux is deliberately the least intrusive of the three**: there is no
system-wide target and nothing is written to `/usr` or `/opt`.

---

## 5. Server API Contracts

See `API.md` for complete request/response schemas.

### Activation Endpoint

```
POST /api/activate
Content-Type: application/json

{
  "code": "RQ-ABCD-EFGH-JKMN-T",
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
  "code": "RQ-ABCD-EFGH-JKMN-T",
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

**File:** `os.UserConfigDir()/locus/storage.json`
(`~/.config/locus/storage.json` on Linux, `%APPDATA%\locus\` on Windows).

```json
{
  "code": "RQ-ABCD-EFGH-JKMN-T",
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
    "final": "dns-tunnel",
    "servers": [
      { "type": "https", "tag": "dns-tunnel", "server": "1.1.1.1", "server_port": 443, "detour": "proxy" },
      { "type": "https", "tag": "dns-direct", "server": "1.1.1.1", "server_port": 443, "detour": "direct" }
    ]
  },
  "inbounds": [
    { "type": "tun", "tag": "tun-in",
      "interface_name": "locus0",
      "address": ["10.0.0.1/30"],
      "mtu": 1500,
      "auto_route": true,
      "strict_route": false,
      "sniff": true }
  ],
  "outbounds": [
    { "type": "shadowsocks", "tag": "proxy",
      "server": "networkingguides.duckdns.org",
      "server_port": 8443,
      "method": "aes-256-gcm",
      "password": "..." },
    { "type": "direct", "tag": "direct" },
  ],
  "route": {
    "rules": [ { "protocol": "dns", "action": { "type": "dns" } } ],
    "auto_detect_interface": true,
    "final": "proxy"
  }
}
```

Set `LOCUS_DEBUG=1` to switch the log level to `debug`. UDP is sent raw
(standard ss UDP): sing-box's `udp_over_tcp` is proprietary to sing-box and
shadowsocks-rust rejects it with RST (see FIXES.md Follow-up 9), so it is
never emitted for any tier.

---

## 8. Platform-Specific Notes

### Linux
- **TUN:** sing-box creates `locus0` directly (user has admin rights on BYOD)
- **Fingerprint:** Reads `/sys/class/net/*/address`, `/sys/block/*/device/serial`,
  `/sys/class/dmi/id/product_uuid`, `/etc/machine-id`
- **WebView deps:** `libgtk-3-dev` + `libwebkit2gtk-4.0-dev` (22.04) or `4.1` (24.04+)
- **Build tag:** add `webkit2_41` when building against WebKitGTK 4.1 (Ubuntu 24.04+):
  `go build -tags "frontend desktop production webkit2_41" .`

### Windows
- **TUN:** sing-box creates the TUN interface (Wintun driver) directly
- **Fingerprint:** PowerShell `Get-NetAdapter` (MAC), WMI `Win32_DiskDrive`
  (disk serial), WMI `Win32_ComputerSystemProduct` (motherboard UUID)
- **Build:** CI compiles with `CGO_ENABLED=0` (`-H windowsgui` — no console window);
  local `wails build` may need MinGW-w64 on Linux
- **WebView:** WebView2 (included in Windows 10+)

### macOS
- **TUN:** sing-box creates the TUN interface directly
- **Fingerprint:** `networksetup -getmacaddress en0/en1`, `ioreg IOPlatformSerialNumber`,
  `ioreg IOPlatformUUID`
- **WebView:** WKWebView; needs `darwin_link.go` (the `UTType` cgo shim) — see FIXES.md
- **Signing:** the build is **UNSIGNED** (no Apple Developer account). Gatekeeper will
  block it on first launch. Workaround: right-click the app → **Open**, or run
  `xattr -cr /Applications/Locus.app` (or `chmod +x` is not needed). Full user-facing
  instructions are planned for the download site (built separately).

---

## 9. Security Considerations

| Concern | Mitigation |
|---------|------------|
| Code brute force | Client-side Luhn check first, server rate limits (5/10min) |
| Device cloning | Multi-source fingerprint (MAC + disk + mobo) |
| Traffic fingerprinting | Shadowsocks AEAD (no TLS, no JA3 fingerprint) |
| Update subversion | SHA256 checksum verification before install |
| Crash on update | Two-phase sentinel with auto-revert |
| Hub compromise | HTTPS to the hub + **SPKI pinning** of the hub TLS cert (additive; `internal/pinned`). Set the operator-provided pin via `LOCUS_HUB_PINS`; `LOCUS_SKIP_PINNING=1` disables it. See capture command in `internal/pinned/pinned.go`. |
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
- [ ] Update: clicking the "Update vX available" button downloads, shows progress (spinner + "Downloading…"), applies, and restarts into the new version
- [ ] Update: failed download/checksum surfaces a toast and leaves the old binary running
- [ ] Update: SHA256 mismatch → download rejected
- [ ] Update: successful update → new binary runs, confirmed sentinel created
- [ ] Update: crash new binary → auto-revert on next start
- [ ] Connection: TUN interface created (`locus0`, 10.0.0.1/30)
- [ ] Connection: disconnect stops sing-box and removes the config file
- [ ] Diagnostics: report includes all fields without PII leaks
- [ ] Window: appears on launch; closing it quits the app (no minimize-to-tray in Wails v2)
- [ ] System tray (Linux/Windows/macOS): run with `LOCUS_TRAY=1` and verify the icon, Connect/Disconnect, Open, and Quit menu items (this MUST be validated per OS; it is off by default) — note closing the window still quits (no close-to-hide until a Wails v3 migration)
