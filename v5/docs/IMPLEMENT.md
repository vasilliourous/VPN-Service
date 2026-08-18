# MyVPN Client — Implementation Plan

> **⚠️ HISTORICAL PLAN (Fyne era).** This is the original phased implementation
> plan that produced the pre-Wails client. The current client (`v5/client/`) is
> a **Wails v2 + Vue 3** app built from the same `internal/` backend packages —
> see [`WAILS-MIGRATION.md`](WAILS-MIGRATION.md) for the GUI migration and
> [`CLIENT-GUIDE.md`](CLIENT-GUIDE.md) for the current build instructions.
> Kept for reference: the `internal/` package design described here is still
> accurate, but the GUI/helper phases are obsolete.

> **Phased build guide for the desktop client.** Each phase is a milestone that
> can be tested independently. Follow them in order — each phase builds on the
> previous one.
>
> **Target:** ~1,600 lines of Go, 2 binaries (myvpn + sing-box), 3 platforms.
> **Time:** 1–2 weekends for a competent Go developer.

---

## Phase 0: Prerequisites & Setup

### Tools

```bash
# Go 1.22+
go version

# Fyne v2 dependencies (CGO required)
# Linux:
sudo apt install libgl1-mesa-dev xorg-dev

# Windows (cross-compile from Linux):
sudo apt install mingw-w64
```

### Project Scaffold

```bash
mkdir -p v5-client/cmd/myvpn
cd v5-client
go mod init myvpn

# Internal packages
mkdir -p internal/{storage,activation,manager,heartbeat,updater,gui}
```

### Dependencies

```
# go.mod
require (
    fyne.io/fyne/v2 v2.5.0    # GUI toolkit
)
# Note: sing-box is NOT a Go dependency — it's a standalone binary
# that we spawn as a subprocess. We only need its JSON config format.
```

### Directory Structure After Phase 0

```
v5-client/
├── cmd/
│   └── myvpn/
│       └── main.go              # Entry point (thin launcher)
├── internal/
│   ├── storage/                 # Persistent state
│   ├── activation/              # Code validation + fingerprint
│   ├── manager/                 # sing-box lifecycle
│   ├── heartbeat/               # Hub communication
│   ├── updater/                 # Two-phase updates
│   └── gui/                     # Fyne desktop app
├── go.mod
├── Makefile
└── README.md
```

---

## Phase 1: Storage Layer

**Goal:** Working persistent state that survives app restarts.

### What to Build

1. **`internal/storage/storage.go`** — Thread-safe JSON persistence
   - Single JSON file at `~/.config/myvpn/storage.json`
   - Atomic writes: write to `.tmp` → `rename()` over target
   - RWMutex for concurrent access
   - Methods: `GetData()`, `SetActivation()`, `SetHeartbeat()`, `SetUpdatePending()`, `Reset()`

2. **Data structures:**
   - `Data` struct with: `Code`, `Tier`, `DeviceFingerprint`, `ServerConfig`, `UDPRelay`,
     `Activated`, `Version`, `UpdatePending`, `UpdateVersion`, `UpdateSHA256`,
     `LastHeartbeatOK`, `HeartbeatFailures`

### Test

```go
// Verify atomic write works:
// 1. Write some data
// 2. Kill -9 the process mid-write
// 3. Verify old data is intact or new data is complete (never corrupted partial write)
```

### Relevant Docs
- `ARCHITECTURE.md` — Section 2.2 (Storage)
- `v4/internal/storage/storage.go` — Reference implementation (235 lines)

---

## Phase 2: Activation

**Goal:** Validate activation codes and bind the app to a device.

### What to Build

1. **Luhn-mod-N checksum** — `internal/activation/luhn.go`
   - `ValidateCode(code string) error` — client-side check, no server call
   - `GenerateCheckChar(body string) (byte, error)` — for code generation
   - Charset: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (no I/O/0/1)
   - Format: `RQ-XXXX-XXXX-XXXX-C` (15 chars with hyphens)

2. **Device fingerprint** — `internal/activation/fingerprint_linux.go` + `fingerprint_windows.go` (self-contained per platform)
   - `GenerateFingerprint() string` — SHA256 of MAC + disk serial + motherboard UUID
   - Platform sources:
     - Linux: `/sys/class/dmi/id/product_uuid`, `/sys/block/*/device/serial`, `/sys/class/net/*/address`
     - Windows: `wmic csproduct get uuid`, `wmic diskdrive get serialnumber`
   - Fallback: random UUID (persisted in storage)
   - Result cached once per process lifetime

3. **Activation client** — `internal/activation/activation.go`
   - `NewClient(hubURL string) *Client`
   - `Activate(code, fingerprint string) (*ActivateResponse, error)`
   - Flow: strip formatting → Luhn check → POST to `/api/activate`
   - Handle responses: 200 (success + tier + config), 403 (suspended/bound), 429 (rate limit)

### Test

```go
// Unit test Luhn validation:
// - Known-good code → passes
// - Single wrong char → fails
// - Wrong checksum → fails
// - Lowercase input → passes (normalized)
// - Empty string → fails

// Integration test activation:
// - Start PocketBase test instance
// - Insert a known code
// - Activate with matching fingerprint → 200
// - Activate with different fingerprint → 403
```

### Relevant Docs
- `ARCHITECTURE.md` — Section 2.3 (Activation)
- `API.md` — Section 1 (POST /api/activate)
- `v4/internal/activation/` — Reference (4 files, 404 lines)

---

## Phase 3: Manager (Engine)

**Goal:** Connect to the VPN by spawning sing-box with the right config.

### What to Build

1. **Config generator** — `internal/manager/process.go`
   - Takes `ServerConfig` from storage and generates a valid sing-box JSON
   - Config structure:
     ```json
     {
       "log": { "level": "warn" },
       "dns": { "final": "1.1.1.1", ... },
       "inbounds": [{ "type": "tun", "interface_name": "myvpn0", ... }],
       "outbounds": [{ "type": "shadowsocks", "server": "...", ... }],
       "route": { "rules": [...], "final": "proxy" }
     }
     ```

2. **Process lifecycle:**
   - `Start(config)` → write config to temp file → exec `sing-box -c <file>` → confirm running
   - `Stop()` → SIGTERM → wait 5s → SIGKILL if still running
   - `IsRunning()` → check process alive + TUN interface exists

3. **No TUN helper needed** — sing-box creates TUN directly (user has admin rights)

### Locating sing-box

Search paths:
```go
paths := []string{
    "./sing-box",                    // cwd (bundled install)
    "/usr/local/bin/sing-box",
    "/usr/bin/sing-box",
}
```

### Test

```go
// Verify config is valid sing-box JSON:
// - Write config to temp file
// - Run `sing-box check -c <tempfile>` → exit 0

// Verify process lifecycle:
// - Start() → process appears in `ps`
// - Stop() → process disappears
// - Double Stop() → no error
```

### Relevant Docs
- `ARCHITECTURE.md` — Section 2.4 (Manager)
- `v4/internal/manager/process.go` — Reference (455 lines)

---

## Phase 4: Heartbeat

**Goal:** Keep the app connected to the hub for suspension checks & updates.

### What to Build

1. **Heartbeat loop** — `internal/heartbeat/heartbeat.go`
   - Background goroutine with timer
   - Starting interval: 5 minutes
   - On failure: double interval (10min → 20min → ... → 2h)
   - On success: reset to 5 minutes

2. **Request:**
   ```
   GET /api/heartbeat?code=RQ-...&fp=<sha256>
   ```

3. **Response handling:**
   - `status == "ok"` → update storage timestamp, reset failures
   - `status == 403` → show "Account Suspended" to user
   - `update_available` → trigger updater (Phase 5)
   - `server_config` changed → update stored config

4. **Grace period calculation:**
   ```go
   func RemainingGracePeriod(lastHeartbeatOK int64) time.Duration {
       if lastHeartbeatOK == 0 { return 7 * 24 * time.Hour }
       elapsed := time.Since(time.Unix(lastHeartbeatOK, 0))
       remaining := (7 * 24 * time.Hour) - elapsed
       if remaining < 0 { return 0 }
       return remaining
   }
   ```

### Test

```go
// Unit test timing:
// - Verify interval doubles on failure (5min → 10min → 20min)
// - Verify cap at 2h (5min → ... → 2h → 2h → 2h)
// - Verify reset on success

// Integration test (with running hub):
// - Send heartbeat with valid code → response has status:"ok"
// - Send heartbeat with suspended code → 403
```

### Relevant Docs
- `ARCHITECTURE.md` — Section 2.5 (Heartbeat)
- `API.md` — Section 2 (GET /api/heartbeat)
- `v4/internal/heartbeat/heartbeat.go` — Reference (245 lines)

---

## Phase 5: Updater

**Goal:** Crash-safe self-update mechanism.

### What to Build

1. **Download & verify** — `internal/updater/updater.go`
   - `DownloadUpdate(url, sha256 string) (tmpPath string, err error)`
   - HTTP download to temp file
   - SHA256 verification (reject if mismatch)

2. **Two-phase sentinel swap:**
   - `ApplyUpdate(tmpPath string) error`
   - Save backup of current binary to `.myvpn-backups/`
   - Create `.update-pending` sentinel file
   - Swap binary (platform-specific):
     - Unix: `os.Rename(newPath, currentPath)`
     - Windows: rename current → `.old`, rename new → current
   - Fork new process with same args
   - Parent exits

3. **Crash recovery** — `internal/updater/recover.go`
   - `CheckOnStartup(revertFlag bool) (rolledBack bool, err error)`
   - If `.update-pending` exists AND `.update-confirmed` missing → auto-revert
   - If `--revert` flag passed → restore from `.myvpn-backups/`

4. **On new process start:**
   - See `.update-pending` → create `.update-confirmed`
   - Remove `.update-pending`
   - Continue normal startup

### Test

```go
// Test auto-revert:
// 1. Place .update-pending (no .update-confirmed)
// 2. Start app
// 3. Verify backup binary was restored
// 4. Verify .reverted sentinel exists

// Test successful update:
// 1. Call ApplyUpdate with valid binary
// 2. New process starts
// 3. Verify .update-confirmed exists
// 4. Verify .update-pending removed
```

### Relevant Docs
- `ARCHITECTURE.md` — Section 2.6 (Updater)
- `v4/internal/updater/` — Reference (3 files, 593 lines)

---

## Phase 6: GUI (Fyne)

**Goal:** Desktop app with activation screen and control panel.

### What to Build

1. **Entry point** — `cmd/myvpn/main.go`
   - Parse flags: `--hub`, `--revert`, `--version`
   - `updater.CheckOnStartup()` before GUI init
   - Call `gui.Run(hubURL, version)`

2. **Fyne app** — `internal/gui/app.go`
   - Activation screen (shown if not activated):
     - Code entry field (centered, monospace)
     - "Activate" button (purple, full width)
     - Error/success message below
   - Main screen (shown if activated):
     - Status indicator (green/grey dot + text)
     - Timer (elapsed connected time)
     - Connect/Disconnect button
     - Speed bar (progress towards tier cap)
     - Settings accordion (tier, version, hub URL)

3. **Theme** (see `UI-AESTHETICS.md` for full spec):
   - Dark background (`#0D0D0F`)
   - Purple accent (`#A855F7`)
   - System font, no custom graphics
   - Tier badges: Eco (grey), Stealth (purple), Strike (gold)

4. **Tier badge:**
   ```go
   func tierBadge(tier string) string {
       switch tier {
       case "strike":  return "Strike ⚡"
       case "stealth": return "Stealth ◉"
       case "eco":     return "Eco ○"
       }
   }
   ```

5. **Window behavior:**
   - Activation: 300×260px, modal
   - Main: 320×440px, hides to tray on close (Fyne v2.5+)
   - System tray: disconnected (grey) / connecting (amber) / connected (green)

### Test

```go
// Manual visual tests:
// - App launches → activation screen (if not activated)
// - Enter invalid code → red error text (no server call)
// - Enter valid code → green success → transitions to main screen
// - Click Connect → button changes to "Disconnecting..." → "Disconnect"
// - Speed bar moves during download
// - Close window → tray icon remains
// - Right-click tray → menu shows options
// - Quit → everything stops cleanly
```

### Relevant Docs
- `UI-AESTHETICS.md` — Full design spec (colors, layout, icons)
- `ARCHITECTURE.md` — Section 2.7 (GUI)
- `v4/internal/gui/` — Reference (2 files, 636 lines)

---

## Phase 7: Build System

**Goal:** Cross-platform builds + CI/CD.

### Makefile Targets

```makefile
build              # Current platform
build-linux        # Linux amd64 (CGO)
build-windows      # Windows amd64 (needs MinGW CC)
build-macos-intel  # macOS Intel (needs osxcross or Mac builder)
build-macos-arm    # macOS Apple Silicon
build-all          # All 4 targets
clean              # Remove dist/
```

### Bundling sing-box

Each platform bundle needs:
1. `myvpn` (built by Makefile)
2. `sing-box` (downloaded from GitHub releases)

```bash
SING_BOX_VERSION="1.10.0"
curl -sLO "https://github.com/SagerNet/sing-box/releases/download/v${SING_BOX_VERSION}/..."
```

### CI/CD (GitHub Actions)

Jobs:
1. **Lint** — `go vet`, `golangci-lint`
2. **Build** — Build matrix for 3 OSes × 2 archs (where applicable)
3. **Bundle** — Download sing-box, zip the pair
4. **Release** — On tag push `v*`, create GitHub Release with all ZIPs + checksums

### Relevant Docs
- `CLIENT-GUIDE.md` — Build commands, CI config
- `v4/Makefile` — Reference (162 lines)
- `.github/workflows/build.yml` — Reference

---

## Phase 8: Polish & Edge Cases

**Goal:** Hardening before production release.

### What to Address

1. **Network errors during activation:** Show "Cannot reach server. Check your connection."
   Don't crash. Don't hang indefinitely.
2. **sing-box crashes:** Manager detects process exit → update heartbeat → reconnect attempt.
   After 3 attempts → "Tap to Retry".
3. **Storage corruption:** If JSON is unparseable, start fresh (not crash).
4. **Clock skew:** If system clock is wrong, grace period calculations break.
   Document as known limitation.
5. **Multiple instances:** Prevent second myvpn process from starting
   (lock file in app data directory).
6. **Uninstall:** Remove storage file, sentinel files, backups directory.

### Test Checklist

```
[ ] Activation: valid code → success
[ ] Activation: invalid code → client-side fail (no server call)
[ ] Activation: used code on same device → success (reinstall scenario)
[ ] Activation: used code on different device → 403
[ ] Heartbeat: interval doubles on failure
[ ] Heartbeat: 7-day grace period counted correctly
[ ] Heartbeat: update signal triggers download
[ ] Update: SHA256 mismatch → download rejected
[ ] Update: successful update → new binary runs
[ ] Update: crash on new binary → auto-revert on next start
[ ] Connection: TUN interface created
[ ] Connection: kill switch blocks non-VPN traffic on disconnect
[ ] Diagnostics: report includes all fields without PII
[ ] Grace period: heartbeat fails 7 days → "tap to retry"
[ ] Cross-platform: builds and runs on Windows, macOS, and Linux
```

---

## Phase Dependency Graph

```
Phase 0 (Prerequisites)
    │
    ▼
Phase 1 (Storage) ◄─────── All phases need storage
    │
    ▼
Phase 2 (Activation) ────► Phase 6 (GUI: activation screen)
    │
    ▼
Phase 3 (Manager) ───────► Phase 6 (GUI: connect/disconnect)
    │
    ▼
Phase 4 (Heartbeat) ─────► Phase 5 (Updater: detects updates)
                              │
                              ▼
                          Phase 6 (GUI: update progress)
    │
    ▼
Phase 7 (Build System) ──► Packaging all of the above
    │
    ▼
Phase 8 (Polish) ──────── Edge cases, hardening, final testing
```

Start with Phase 1. Each phase produces a testable artifact. Don't move to
the next phase until the current one works.
