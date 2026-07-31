# MyVPN Backend API Reference

> **Complete public API surface of all `internal/` packages.**
> If the GUI layer is rewritten or replaced, this document tells you exactly how to
> interface with each backend package. The packages themselves should NOT be modified
> — they are stable, tested, and platform-independent Go code.

---

## Table of Contents

1. [Package: `storage`](#1-package-storage)
2. [Package: `activation`](#2-package-activation)
3. [Package: `manager`](#3-package-manager)
4. [Package: `heartbeat`](#4-package-heartbeat)
5. [Package: `updater`](#5-package-updater)
6. [Package: `tunnel`](#6-package-tunnel)
7. [Dependency Graph](#7-dependency-graph)
8. [Platform-Specific Files](#8-platform-specific-files)

---

## 1. Package: `storage`

**File:** `internal/storage/storage.go`
**Import:** `"myvpn/internal/storage"`
**Purpose:** Persistent JSON state management. Thread-safe, atomic writes, backup rotation.

### Types

```go
// Data represents the persisted state of the MyVPN client.
type Data struct {
    Code              string        `json:"code,omitempty"`
    Tier              string        `json:"tier,omitempty"`
    DeviceFingerprint string        `json:"device_fingerprint,omitempty"`
    ServerConfig      *ServerConfig `json:"server_config,omitempty"`
    UDPRelay          bool          `json:"udp_relay,omitempty"`
    Activated         bool          `json:"activated"`
    Version           string        `json:"version,omitempty"`
    UpdatePending     bool          `json:"update_pending,omitempty"`
    UpdateVersion     string        `json:"update_version,omitempty"`
    UpdateSHA256      string        `json:"update_sha256,omitempty"`
    UpdateTimestamp   int64         `json:"update_timestamp,omitempty"`
    LastHeartbeatOK   int64         `json:"last_heartbeat_ok,omitempty"`
    HeartbeatFailures int           `json:"heartbeat_failures,omitempty"`
    CrashedOnUpdate   bool          `json:"crashed_on_update,omitempty"`
}

type ServerConfig struct {
    Server     string `json:"server"`
    ServerPort int    `json:"server_port"`
    Password   string `json:"password"`
    Method     string `json:"method"`
}
```

### Constructor

```go
// New creates or loads a Store in the platform-specific app data directory.
// appName is used as the subdirectory name (e.g. "myvpn").
// File locations:
//   Linux:   ~/.config/<appName>/storage.json
//   macOS:   ~/Library/Application Support/<appName>/storage.json
//   Windows: %APPDATA%\<appName>\storage.json
func New(appName string) (*Store, error)
```

### Methods

```go
// GetData returns a copy of the current persisted state (thread-safe).
func (s *Store) GetData() Data

// SetActivation persists the activation result.
func (s *Store) SetActivation(code, tier, fingerprint string, config *ServerConfig, udpRelay bool) error

// ClearActivation resets all activation fields.
func (s *Store) ClearActivation() error

// SetVersion saves the current app version.
func (s *Store) SetVersion(version string) error

// SetHeartbeat updates the last successful heartbeat timestamp.
func (s *Store) SetHeartbeat(timestamp int64) error

// SetHeartbeatFailure increments the failure counter and sets the failure timestamp.
func (s *Store) SetHeartbeatFailure(timestamp int64) error

// ResetHeartbeatFailures clears the failure counter.
func (s *Store) ResetHeartbeatFailures() error

// SetUpdatePending marks that an update is in progress.
func (s *Store) SetUpdatePending(version, sha256 string, timestamp int64) error

// ClearUpdatePending removes the update-pending flag.
func (s *Store) ClearUpdatePending() error

// SetCrashedOnUpdate marks that the update may have crashed.
func (s *Store) SetCrashedOnUpdate(crashed bool) error

// SetConnected persists the connection state.
func (s *Store) SetConnected(connected bool) error

// RestoreFromBackup attempts to restore the most recent valid backup file.
// Backups are named storage.json.bak.{0,1,2} (rotating, 3-deep).
func (s *Store) RestoreFromBackup() error

// Path returns the full filesystem path of the storage file.
func (s *Store) Path() string
```

### Safety Guarantees

- All writes are atomic: write to `.tmp` file, then `rename()` over target
- Backups kept at `storage.json.bak.{0,1,2}` (rotate on every write)
- Thread-safe via `sync.RWMutex`
- File permissions: `0600`, directory: `0700`

---

## 2. Package: `activation`

**Files:** `internal/activation/activation.go`, `luhn.go`, `fingerprint.go`, `fingerprint_{darwin,linux,windows}.go`
**Import:** `"myvpn/internal/activation"`
**Purpose:** Code validation (Luhn-mod-N), device fingerprinting, server activation API.

### Types

```go
// ActivateResponse is the response from the activation server.
type ActivateResponse struct {
    Code       int           `json:"code"`
    Message    string        `json:"message"`
    Tier       string        `json:"tier,omitempty"`
    DeviceFP   string        `json:"device_fingerprint,omitempty"`
    ServerCfg  *ServerConfig `json:"server_config,omitempty"`
    UDPRelay   bool          `json:"udp_relay,omitempty"`
}

// ServerConfig mirrors storage.ServerConfig.
type ServerConfig struct {
    Server     string `json:"server"`
    ServerPort int    `json:"server_port"`
    Password   string `json:"password"`
    Method     string `json:"method"`
}
```

### Common Errors

```go
var ErrInvalidCharacter    = errors.New("code contains invalid character")
var ErrInvalidCode         = errors.New("invalid activation code format")
var ErrChecksumFailed      = errors.New("activation code checksum failed")
var ErrServerError         = errors.New("server returned an error")
var ErrCodeBound           = errors.New("code already bound to another device")
var ErrCodeSuspended       = errors.New("code is suspended")
var ErrCodeExpired         = errors.New("code has expired")
var ErrRateLimited         = errors.New("too many activation attempts")
var ErrTimeout             = errors.New("activation request timed out")
var ErrServerUnreachable   = errors.New("server is unreachable")
```

### Functions

```go
// ── Client (HTTP) ──

// NewClient creates an activation client targeting the given hub URL.
// e.g. NewClient("https://networkingguides.duckdns.org")
func NewClient(hubURL string) *Client

// Activate sends the code + fingerprint to the server.
// Returns the parsed response or one of the Err* errors above.
// Handles: rate limiting (429→ErrRateLimited), suspension (403→ErrCodeSuspended),
// binding conflicts (403→ErrCodeBound), expiry (410→ErrCodeExpired).
func (c *Client) Activate(ctx context.Context, code, fingerprint string) (*ActivateResponse, error)

// ValidateCodeFormat validates a complete activation code including Luhn-mod-N checksum.
// Returns nil if valid, or an error describing the issue (wrong length, bad charset, checksum mismatch).
// This is a client-side check — no server call is made.
func ValidateCodeFormat(code string) error

// ValidateHubURL checks that a hub URL is well-formed (valid URL, https, non-empty host).
func ValidateHubURL(hubURL string) error

// ── Luhn-mod-N Validation (client-side) ──

// CodeCharset is the valid character set: ABCDEFGHJKLMNPQRSTUVWXYZ23456789
const CodeCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const CodePrefix = "MYVPN"
const CodeSegments = 3
const CodeSegmentLen = 4
const CodeTotalLen = 18  // prefix(5) + segments(12) + checksum(1)

// ValidateCharset checks that a character belongs to CodeCharset.
func ValidateCharset(c byte) error

// FormatCode inserts hyphens: "MYVPNXXXX...C" → "MYVPN-XXXX-XXXX-XXXX-C"
func FormatCode(raw string) string

// GenerateCheckChar computes the Luhn-mod-N checksum for a code body.
func GenerateCheckChar(body string) (byte, error)

// ── Device Fingerprinting ──

// GenerateFingerprint creates a SHA256 device fingerprint from hardware sources.
// Sources (by priority):
//   1. MAC + disk serial + motherboard UUID (strongest)
//   2. MAC + motherboard UUID (medium)
//   3. MAC + hostname + machine_id (weak)
//   4. Random UUID (fallback, persisted so it's stable per install)
// The result is cached — subsequent calls return the same value.
func GenerateFingerprint() string

// ResetFingerprint clears the cached fingerprint. Used for testing.
func ResetFingerprint()

// ValidateFingerprint checks that a fingerprint is non-empty and ≥16 chars.
func ValidateFingerprint(fp string) bool
```

### Platform-Specific Files

| File | Build Tag | Source |
|------|-----------|--------|
| `fingerprint_darwin.go` | `darwin` | MAC via `en0/en1`, motherboard via IOKit `IOPlatformSerial` |
| `fingerprint_linux.go` | `linux` | MAC via `/sys/class/net/*/address`, disk via `/sys/block/*/serial`, motherboard via `/sys/devices/virtual/dmi/id/product_uuid` |
| `fingerprint_windows.go` | `windows` | MAC via `GetAdaptersAddresses`, disk via `Win32_DiskDrive`, motherboard via `Win32_ComputerSystemProduct` |

---

## 3. Package: `manager`

**Files:** `internal/manager/process.go`, `process_{unix,windows}.go`
**Import:** `"myvpn/internal/manager"`
**Purpose:** Sing-box process lifecycle, JSON config generation, health monitoring.

### Types

```go
// Config holds the parameters for generating a sing-box configuration.
type Config struct {
    Server     string
    ServerPort int
    Method     string
    Password   string
    Tier       string   // "eco", "stealth", or "strike"
    UDPRelay   bool
}
```

### Constructor

```go
// NewManager creates a new Manager.
//   singBoxPath: path to the sing-box binary
//   configPath:  where to write the generated JSON config (temp file recommended)
//   helperPath:  path to myvpn-helper binary (empty = direct mode, no helper)
func NewManager(singBoxPath, configPath, helperPath string) *Manager
```

### Methods

```go
// Start launches sing-box. If a helper path was provided, it sends the config
// to the helper via IPC; otherwise it spawns sing-box directly.
// Returns ErrProcessNotRunning if sing-box exits immediately (config error / permissions).
func (m *Manager) Start(ctx context.Context, cfg Config) error

// Stop terminates sing-box gracefully (SIGTERM → 5s timeout → SIGKILL on Unix,
// or process kill on Windows). Cleans up the config file.
func (m *Manager) Stop() error

// IsRunning checks if sing-box is still alive.
func (m *Manager) IsRunning() bool

// State returns a status string: "running", "stopped", or "crashed".
func (m *Manager) State() string

// StartHelper launches the privileged TUN helper (pkexec/sudo/UAC).
// Only needed if helperPath was provided to NewManager.
// In the new Wails architecture, this is NOT used — elevation is handled by Wails.
func (m *Manager) StartHelper() error
```

### Configuration Generation (internal, used by Start)

```go
// generateConfig creates a sing-box JSON config with:
//   - TUN inbound (myvpn0, 10.0.0.1/30, auto_route, strict_route)
//   - Shadowsocks outbound to the VPS
//   - DNS via 1.1.1.1 through tunnel
//   - Direct outbound for private IPs
//   - Tier-appropriate settings (UDP relay for Strike)
func generateConfig(cfg Config) ([]byte, error)
```

### Process Lifecycle

```
Start() →
  1. Generate config (TUN inbound + Shadowsocks outbound)
  2. Write config to configPath
  3. Spawn: sing-box run -c <configPath>
  4. Wait 500ms probe → if exited, return ErrProcessNotRunning
  5. Start health check loop (10s interval, 3 failures = stop)

Stop() →
  1. SIGTERM (Unix) / Kill (Windows)
  2. Wait up to 10s
  3. If still alive → SIGKILL (Unix) / Kill again (Windows)
  4. Remove config file
```

### Platform Files

| File | Build Tag | Detail |
|------|-----------|--------|
| `process_unix.go` | `!windows` | `Setpgid: true` for process group detachment |
| `process_windows.go` | `windows` | `CREATE_NEW_PROCESS_GROUP` for detachment |

---

## 4. Package: `heartbeat`

**File:** `internal/heartbeat/heartbeat.go`
**Import:** `"myvpn/internal/heartbeat"`
**Purpose:** Periodic communication with the hub server for suspension checks, staged rollouts, and grace period tracking.

### Constants

```go
const MinInterval     = 5 * time.Minute    // Initial heartbeat interval
const MaxInterval     = 2 * time.Hour      // Max interval after failures (doubles each time)
const GracePeriod     = 7 * 24 * time.Hour // 7 days without heartbeat → require reactivation
const CallbackTimeout = 5 * time.Second
const JitterPercent   = 0.1                // ±10% jitter to prevent thundering herd
```

### Types

```go
type Response struct {
    Status         string `json:"status"`                     // "ok" or error
    ServerTime     string `json:"server_time,omitempty"`
    Tier           string `json:"tier,omitempty"`
    ServerConfig   *ServerConfig `json:"server_config,omitempty"`
    UDPRelay       bool   `json:"udp_relay,omitempty"`
    UpdateAvailable string `json:"update_available,omitempty"`
    UpdateURL      string `json:"update_url,omitempty"`
    UpdateSHA256   string `json:"update_sha256,omitempty"`
    UpdateLinux    string `json:"update_linux,omitempty"`
    UpdateWindows  string `json:"update_windows,omitempty"`
    UpdateMacOSIntel string `json:"update_macos_intel,omitempty"`
    UpdateMacOSARM string `json:"update_macos_arm,omitempty"`
}

type Result struct {
    Success bool
    Resp    *Response
    Error   error
}

type HeartbeatError struct {
    Code    int
    Message string
    Wrapped error
}
```

### Constructor

```go
// New creates a Heartbeat. hubURL is the base URL of the server.
func New(hubURL string) *Heartbeat
```

### Methods

```go
// Start begins the heartbeat loop in a goroutine.
//   code, fingerprint: sent with each heartbeat for server validation
//   callback: invoked on each heartbeat result (use this to update UI / check for updates)
// The interval starts at MinInterval, doubles on failure, resets on success.
func (h *Heartbeat) Start(ctx context.Context, code, fingerprint string, callback func(Result))

// Stop terminates the heartbeat loop.
func (h *Heartbeat) Stop()

// DoHeartbeat performs a single heartbeat request synchronously and returns the result.
// Useful for manual/on-demand heartbeats outside the loop.
func (h *Heartbeat) DoHeartbeat(ctx context.Context, code, fingerprint string) Result

// Failures returns the consecutive heartbeat failure count.
func (h *Heartbeat) Failures() int

// RemainingGracePeriod calculates remaining time before the code requires reactivation.
//   lastHeartbeatOK: Unix timestamp of the last successful heartbeat (from storage)
// Returns 0 if the grace period has expired.
func (h *Heartbeat) RemainingGracePeriod(lastHeartbeatOK int64) time.Duration
```

### Behavior

```
On success:
  - interval resets to 5min
  - callback(Result{Success: true, Resp: ...})

On failure:
  - interval doubles (5min → 10min → 20min → ... → 2h max)
  - callback(Result{Success: false, Error: ...})

Grace period:
  - Client continues working for 7 days after last successful heartbeat
  - After 7 days → "tap to retry" / require re-activation
  - The server can suspend a code at any time (returns 403)
```

---

## 5. Package: `updater`

**Files:** `internal/updater/updater.go`, `recover.go`, `update_{unix,windows}.go`
**Import:** `"myvpn/internal/updater"`
**Purpose:** Two-phase crash-safe update system with staged rollout support.

### Sentinel Files

```go
const SentinelPending   = ".update-pending"    // Created before swap → indicates update in progress
const SentinelConfirmed = ".update-confirmed"  // Created by new binary → confirms update succeeded
const SentinelReverted  = ".reverted"          // Created after auto-revert → marks rollback
const BackupDir         = ".myvpn-backups"     // Directory containing previous binary backups
```

### Types

```go
type UpdateInfo struct {
    Version  string
    URL      string
    SHA256   string
    Platform string
}

type UpdateProgress struct {
    BytesDone  int64
    BytesTotal int64
    Phase      string // "downloading", "verifying", "swapping", "done", "failed"
    Error      string
}

type RecoveryState struct {
    RolledBack      bool
    PreviousVersion string
    AttemptedVersion string
    Reason          string
}
```

### Constructor

```go
// New creates an Updater.
//   appDir:      directory containing the myvpn binary
//   binaryName:  "myvpn" (or "myvpn.exe" on Windows)
//   currentVersion: e.g. "2.0.0"
func New(appDir, binaryName, currentVersion string) *Updater
```

### Methods

```go
// CheckAndUpdate checks if an update is available and applies it.
// If the update is for a different platform (e.g. update_linux when on Windows),
// it selects the correct platform URL automatically.
// Returns true if an update was applied (caller should re-launch).
func (u *Updater) CheckAndUpdate(ctx context.Context, updateInfo UpdateInfo, progress func(UpdateProgress)) (bool, error)

// DownloadAndVerify downloads the new binary and checks its SHA256.
// Returns the path to the downloaded file.
func (u *Updater) DownloadAndVerify(ctx context.Context, url, expectedSHA256 string, progress func(UpdateProgress)) (string, error)

// ApplyUpdate swaps the current binary with the downloaded one and forks the new process.
// This is the two-phase commit:
//   1. Create .update-pending sentinel
//   2. Backup current binary to BackupDir
//   3. Swap binary (platform-specific)
//   4. Fork new process
//   5. Parent exits (new process creates .update-confirmed on success)
func (u *Updater) ApplyUpdate(downloadedPath string) error
```

### Standalone Functions

```go
// ── Crash Recovery (used at startup, before GUI) ──

// CheckOnStartup handles the two-phase update handshake at app startup.
//   revertFlag: true if --revert was passed (manual revert)
// Returns:
//   rolledBack: true if a revert was performed
//   err:        non-nil if recovery failed
// Called from main() before anything else.
func CheckOnStartup(revertFlag bool) (rolledBack bool, err error)

// ConfirmIfPending checks for .update-pending and creates .update-confirmed.
// Called from main() after successful startup to confirm the update.
func ConfirmIfPending(appDir string) error

// CleanStaleMarkers removes update markers older than maxAge.
func CleanStaleMarkers(appDir string, maxAge time.Duration)

// ── CrashDetector (secondary crash detection) ──

type CrashDetector struct { /* ... */ }

func NewCrashDetector(appDir, binaryName string) *CrashDetector
func (cd *CrashDetector) WasCrashedUpdate() bool
func (cd *CrashDetector) HandleCrashedUpdate() (bool, error)

// ── Recovery Diagnosis ──

func DiagnoseRecovery(appDir string) *RecoveryState
```

### Platform-Specific Files

| File | Build Tag | Swap Strategy |
|------|-----------|---------------|
| `update_unix.go` | `!windows` | `os.Rename` (atomic), `exec.Command` with `Setpgid` |
| `update_windows.go` | `windows` | Rename to `.old`, move new in place, cleanup `.old`; `CREATE_NEW_PROCESS_GROUP` |

### Update Flow

```
Heartbeat detects update available →
  1. DownloadAndVerify() → download URL, check SHA256
  2. ApplyUpdate():
     a. Create .update-pending sentinel
     b. Backup current binary → .myvpn-backups/
     c. Swap new binary in place (platform-specific)
     d. Fork new process (inherits args, stdin/stdout/stderr)
     e. Parent exits
  3. New binary starts:
     a. CheckOnStartup() → sees .update-pending, no .update-confirmed yet
     b. ConfirmIfPending() → creates .update-confirmed
     c. Cleanup → removes both sentinels
  4. If new binary crashes before step 3b:
     a. Next start → sees .update-pending, no .update-confirmed
     b. CheckOnStartup() → auto-reverts to backup
     c. Creates .reverted sentinel
```

---

## 6. Package: `tunnel`

**File:** `internal/tunnel/tunnel.go`
**Import:** `"myvpn/internal/tunnel"`
**Purpose:** Direct TUN interface management (fallback — primary tunnel is via sing-box through the manager).

### Types

```go
type Config struct {
    Name       string   // Interface name, default "myvpn0"
    MTU        int      // Default 1500
    VirtualIP  string   // Default "10.0.0.2"
    DNSServers []string // Default ["1.1.1.1", "1.0.0.1"]
    KillSwitch bool     // Default true
}

// Interface represents a TUN device.
type Interface interface {
    Name() string
    Start() error
    Stop() error
    IsUp() bool
}
```

### Functions

```go
// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config

// New creates a TUN interface for the current platform.
// Returns linuxTUN, darwinTUN, or windowsTUN (all implement Interface).
func New(cfg Config) (Interface, error)

// KillSwitch enables or disables the kill switch.
// On Linux: iptables/nftables rules to block non-TUN traffic.
// On macOS: pfctl rules.
// On Windows: (not implemented).
func KillSwitch(enable bool) error
```

### Platform Implementations

| Platform | Type | TUN Creation | Routes |
|----------|------|-------------|--------|
| Linux | `linuxTUN` | `ip tuntap add` + `ip link set up` | `ip route add 0.0.0.0/1 dev myvpn0`, `ip route add 128.0.0.0/1 dev myvpn0` |
| macOS | `darwinTUN` | `ifconfig myvpn0 inet 10.0.0.2 10.0.0.2 up` | `route add -net 0.0.0.0/1 -interface myvpn0`, `route add -net 128.0.0.0/1 -interface myvpn0` |
| Windows | `windowsTUN` | Requires myvpn-helper service | Not directly implemented |

> **Note:** In normal operation, TUN is managed by sing-box (via the manager package).
> This package is a fallback for edge cases where sing-box TUN doesn't work.

---

## 7. Dependency Graph

```
main.go (entry point)
  │
  ├── updater.CheckOnStartup()          ← pure filesystem, no deps
  ├── updater.ConfirmIfPending()        ← pure filesystem, no deps
  │
  └── App (GUI layer — currently Fyne, target Wails)
        │
        ├── storage.New()               ← no internal deps
        ├── activation.NewClient()      ← no internal deps
        ├── activation.GenerateFingerprint() ← no internal deps
        ├── manager.NewManager()        ← no internal deps
        ├── heartbeat.New()             ← no internal deps
        │
        ├── activation.ValidateCharset() ← pure algorithm
        ├── activation.Client.Activate() ← HTTP only (no internal deps)
        │
        ├── manager.Start(cfg)          ← uses cfg from storage.ServerConfig
        ├── manager.Stop()
        ├── manager.IsRunning()
        │
        ├── heartbeat.Start(code, fp, callback) ← callback for UI updates
        ├── heartbeat.Stop()
        ├── heartbeat.RemainingGracePeriod()
        │
        └── updater.CheckAndUpdate()    ← depends on heartbeat.Response
```

**Key rule:** No internal package imports any other internal package. They are all
independent libraries used by the GUI layer. This makes them safe to reuse in any
rewrite.

---

## 8. Platform-Specific Files

These files use build tags and must be included in any build:

| Package | File | Tag | Purpose |
|---------|------|-----|---------|
| `activation` | `fingerprint_darwin.go` | `darwin` | MAC + motherboard collection |
| `activation` | `fingerprint_linux.go` | `linux` | MAC + disk + motherboard collection |
| `activation` | `fingerprint_windows.go` | `windows` | MAC + disk + motherboard collection |
| `manager` | `process_unix.go` | `!windows` | `Setpgid: true` for process detachment |
| `manager` | `process_windows.go` | `windows` | `CREATE_NEW_PROCESS_GROUP` |
| `updater` | `update_unix.go` | `!windows` | Atomic rename + fork with `Setpgid` |
| `updater` | `update_windows.go` | `windows` | `.old` rename trick + fork with `CREATE_NEW_PROCESS_GROUP` |

> **Note:** The old Fyne GUI (`internal/gui/`) and the separate TUN helper binary
> (`internal/helper/`) were moved OUT of the Go module to `v5/legacy/`
> (`v5/legacy/gui/`, `v5/legacy/helper/`, `v5/legacy/cmd/myvpn/`) so they are not
> compiled, scanned by `go vet`, or re-added to `go.mod` by `go mod tidy`.
> They remain in the repo for reference and rollback only.
| `tunnel` | (platform code inline) | — | Runtime `GOOS` check, not build tags |
