# MyVPN — Complete Agent Handoff Document

> **Purpose:** This single file captures everything a future agent (AI or human) needs to know to understand, build, run, extend, or maintain this project. Read this first before making any changes.
>
> **Generated:** 2026-07-21  
> **Total project:** 40 files, ~2,376 lines  
> **Language:** Go 1.22 + Fyne v2 GUI + JavaScript (PocketBase hooks)  
> **License:** Unlicense (Public Domain)

---

## Table of Contents

1. [What Is This Project?](#1-what-is-this-project)
2. [Project Structure](#2-project-structure)
3. [Architecture Overview](#3-architecture-overview)
4. [Lifecycle & Data Flow](#4-lifecycle--data-flow)
5. [Package-by-Package Reference](#5-package-by-package-reference)
   - [5.1 main.go](#51-maingo)
   - [5.2 storage](#52-storage)
   - [5.3 activation](#53-activation)
   - [5.4 branding](#54-branding)
   - [5.5 manager](#55-manager)
   - [5.6 heartbeat](#56-heartbeat)
   - [5.7 tunnel](#57-tunnel)
   - [5.8 probe](#58-probe)
   - [5.9 monitor](#59-monitor)
   - [5.10 health](#510-health)
   - [5.11 helper](#511-helper)
   - [5.12 updater](#512-updater)
   - [5.13 gui](#513-gui)
6. [Server-Side Components](#6-server-side-components)
   - [6.1 Caddy Reverse Proxy](#61-caddy-reverse-proxy)
   - [6.2 PocketBase Hooks](#62-pocketbase-hooks)
7. [API Contracts](#7-api-contracts)
   - [7.1 Activation API](#71-activation-api)
   - [7.2 Heartbeat API](#72-heartbeat-api)
8. [Connection State Machine](#8-connection-state-machine)
9. [Activation & Device Binding Details](#9-activation--device-binding-details)
10. [Bandwidth Probe & Monitor Algorithms](#10-bandwidth-probe--monitor-algorithms)
11. [Update System & Rollback](#11-update-system--rollback)
12. [Tier Limits & Pricing](#12-tier-limits--pricing)
13. [Build System & CI/CD](#13-build-system--cicd)
14. [Server Deployment](#14-server-deployment)
15. [Security Model](#15-security-model)
16. [What's Implemented vs What's Stubbed](#16-whats-implemented-vs-whats-stubbed)
17. [Known Issues & TODOs](#17-known-issues--todos)
18. [Business Context Quick Reference](#18-business-context-quick-reference)

---

## 1. What Is This Project?

MyVPN is a **cross-platform desktop VPN app** designed for school/college students who want to:
- Bypass school WiFi restrictions (DPI, port blocking, domain filtering)
- Play online games on congested WiFi (low-latency QUIC protocol)
- Access blocked websites
- Pay with cash via middlemen (no credit card needed)

**Key differentiators from free VPNs:**
- Hysteria 2 over QUIC — thrives on lossy/congested WiFi where TCP-based VPNs collapse
- Permanent hardware-bound activation — one code = one device forever
- Middleman distribution model — physical codes sold for cash
- Anti-detection: renamed binaries, no real protocol names in UI, cert-pinned comms

**Stack:** Go + Fyne GUI (desktop) + Hysteria 2 (engine) + usque/Warp (fallback) + PocketBase + Caddy (server)

---

## 2. Project Structure

```
myvpn/
│
├── main.go                              # Entry point: parse flags, init storage, launch GUI
├── go.mod                               # Go module definition (needs go mod tidy)
├── Makefile                             # Cross-compile targets
├── README.md                            # Quick-start documentation
├── .gitignore
│
├── .github/workflows/
│   └── build.yml                        # GitHub Actions CI for Win/Mac/Linux
│
├── internal/
│   ├── storage/
│   │   └── storage.go                   # Persistent JSON state, log rotation
│   │
│   ├── activation/
│   │   ├── activation.go               # Luhn-mod-N validation, Validate() API call
│   │   ├── activation_test.go          # Tests for Luhn check
│   │   ├── fingerprint.go              # CollectFingerprint() — SHA256(MAC+disk+mobo)
│   │   ├── fingerprint_windows.go      # getMAC/getDiskSerial/getMoboSerial (WMIC)
│   │   ├── fingerprint_darwin.go       # getMAC/getDiskSerial/getMoboSerial (system_profiler)
│   │   └── fingerprint_linux.go        # Stubs for Linux testing
│   │
│   ├── branding/
│   │   ├── branding.go                 # Fake protocol/plan name mappings
│   │   └── branding_test.go            # Tests for name mappings
│   │
│   ├── manager/
│   │   ├── process.go                  # Engine lifecycle (Start/Stop), sandbox stubs
│   │   └── disguise_windows.go         # HideWindow=true for Windows engine process
│   │
│   ├── heartbeat/
│   │   └── heartbeat.go                # 60s loop, cert pinning, grace period, token rotation
│   │
│   ├── tunnel/
│   │   ├── tunnel.go                   # Manager interface definition
│   │   ├── tun_windows.go              # TUN via helper IPC (wintun)
│   │   ├── tun_darwin.go               # TUN via helper IPC (utun)
│   │   ├── tun_linux.go               # TUN via ip tuntap (for testing)
│   │   ├── killswitch.go              # Block all non-VPN traffic on disconnect
│   │   └── dns.go                     # Redirect DNS through tunnel
│   │
│   ├── probe/
│   │   └── probe.go                    # Bandwidth probe (1MB download) on connect
│   │
│   ├── monitor/
│   │   └── monitor.go                  # Background EWMA bandwidth monitor (15s intervals)
│   │
│   ├── health/
│   │   └── health.go                   # 15s tunnel health check with degraded/dead callbacks
│   │
│   ├── helper/
│   │   ├── helper.go                   # IPC client (send commands to privileged service)
│   │   ├── service_windows.go          # Named pipe listener stub (Windows helper service)
│   │   └── service_darwin.go           # Unix socket listener stub (macOS helper daemon)
│   │
│   ├── updater/
│   │   ├── updater.go                  # SHA256 download/verify, binary swap
│   │   └── recover.go                  # Two-phase sentinel rollback
│   │
│   └── gui/
│       └── app.go                      # Fyne window, theme, connect/disconnect UI
│
├── server/
│   ├── Caddyfile                       # Reverse proxy + rate limiting config
│   └── pb_hooks/
│       ├── sqlite_pragmas.pb.js        # WAL mode + busy_timeout
│       ├── activation_rate_limit.pb.js # 5 attempts/10min per IP
│       ├── device_binding.pb.js        # Permanent one-code-one-device binding
│       └── heartbeat.pb.js             # Token verify/rotate, suspension check
│
└── scripts/
    ├── setup_server.sh                 # One-command VPS deployment (Ubuntu)
    └── publish_update.sh               # SHA256 + JSON update payload generator
```

---

## 3. Architecture Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                    MYVPN DESKTOP APP (Go + Fyne)                  │
│                                                                    │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐  │
│  │  GUI     │  │ Heartbeat│  │ Manager  │  │  Updater          │  │
│  │ (Fyne)   │  │ (60s)    │  │ (Process)│  │  (SHA256+Rollback)│  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └──────────────────┘  │
│       │              │            │                               │
│  ┌────┴──────────────┴────────────┴──────────────────────────┐   │
│  │                    Core Services                            │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐  │   │
│  │  │ Storage  │ │Activation│ │ Branding  │ │ Helper IPC   │  │   │
│  │  │ (JSON)   │ │(Fingerpr.)│ │(FakeNames)│ │(Named Pipe)  │  │   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────┬───────┘  │   │
│  └───────────────────────────────────────────────────┼────────┘   │
│                                                       │           │
│  ┌───────────────────────────────────────────────────┐│           │
│  │  Tunnel Layer                                      ││           │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────┐  ││           │
│  │  │ TUN Dev  │ │Killswitch│ │ DNS Guard│ │Health│  ││           │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────┘  ││           │
│  └────────────────────────────────────────────────────┘│           │
│  ┌─────────────────────────────────────────────────────┘           │
│  │  Protocol Engines (external binaries, downloaded at runtime)    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐      │
│  │  │ speedmode.exe │  │ litemode.exe │  │ stealthmode.exe  │      │
│  │  │ (Hysteria 2)  │  │ (usque/Warp) │  │ (Xray — Phase 2) │      │
│  │  └──────────────┘  └──────────────┘  └──────────────────┘      │
│  └──────────────────────────────────────────────────────────────────┘
                              │
                              ▼  HTTPS (TLS 1.3, cert-pinned)
┌──────────────────────────────────────────────────────────────────┐
│                      ADMIN HUB (your VPS)                         │
│                                                                   │
│  ┌───────────────────────────────────────────────────────────┐   │
│  │                    Caddy (reverse proxy)                    │   │
│  │  • TLS termination (Let's Encrypt)                          │   │
│  │  • Rate limiting: heartbeat (1/10s), activation (5/10m)    │   │
│  │  • Static file serving: /speedtest/, /updates/              │   │
│  └──────────────────────┬────────────────────────────────────┘   │
│                         │ localhost:8090                          │
│  ┌──────────────────────┴────────────────────────────────────┐   │
│  │                  PocketBase                                 │   │
│  │  • SQLite (WAL mode, busy_timeout=5000)                     │   │
│  │  • Collections: activation_codes, activation_attempts       │   │
│  │  • JS Hooks: device binding, heartbeat, rate limiting        │   │
│  │  • Admin UI at /_/                                          │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                   │
│  /var/www/speedtest/     ← 1mb.bin, 100kb.bin (bandwidth probe)   │
│  /var/www/updates/       ← Binary downloads for update system      │
└──────────────────────────────────────────────────────────────────┘
```

---

## 4. Lifecycle & Data Flow

### 4.1 Startup Sequence
```
main.go
  │
  ├── storage.Init()
  │     └── Creates directories, loads data.json (or creates default)
  │     └── Starts log rotation goroutine (10MB rotate, keep 3)
  │
  ├── updater.CheckOnStartup(revertFlag)
  │     └── Checks .update-pending / .update-confirmed sentinels
  │     └── Auto-reverts if previous update crashed
  │
  └── gui.Run(adminHubURL)
        │
        ├── (Not activated?)
        │     └── showActivationPrompt()
        │           ├── User enters code
        │           ├── activation.Validate() → POST to hub
        │           ├── storage.SaveActivation(token, plan)
        │           └── launchMain()
        │
        └── launchMain()
              ├── go heartbeat.Start()   ← background goroutine
              ├── Build Fyne window
              └── state.Window.ShowAndRun()   ← blocks until close
```

### 4.2 Connect Sequence (user taps "Connect")
```
connect()
  │
  ├── STEP 1: Bandwidth Probe
  │     └── probe.Run(hubURL, tier)
  │           ├── Download 1MB from /speedtest/1mb.bin
  │           ├── Measure throughput (bps) + RTT
  │           ├── Clamp to tier limits (floor 3 Mbps)
  │           └── Return Result{ BandwidthBps, BaselineRTT, IsConstrained }
  │
  ├── STEP 2: TUN Setup
  │     ├── tunnel.Setup()         ← Create wintun/utun/tun0
  │     ├── tunnel.Engage()        ← Kill switch (block non-VPN traffic)
  │     └── tunnel.Guard()         ← DNS redirect through tunnel
  │
  ├── STEP 3: Engine Launch
  │     └── manager.StartEngine(protocolConfig, planTier, capBps)
  │           ├── Locate cached engine binary (e.g., speedmode.exe)
  │           ├── Build command with bandwidth cap injected
  │           ├── disguiseProcess() → HideWindow=true (Windows)
  │           └── cmd.Start() → process runs in background
  │
  ├── STEP 4: Health Checker
  │     └── health.New(hubURL).Start()
  │           ├── Every 15s: HTTP GET /_/health
  │           ├── 3 failures → disconnected state
  │           └── Callbacks: OnDegraded, OnDead, OnRecovered
  │
  └── STEP 5: Bandwidth Monitor
        └── monitor.New(hubURL, tier, initialCap, baselineRTT).Start()
              ├── Every 15s: download 100KB sample
              ├── EWMA smoothing (α=0.3)
              ├── Dynamic cap adjustment (bufferbloat detection)
              └── Continues until disconnect
```

### 4.3 Disconnect Sequence
```
disconnect()
  ├── monitor.Stop()
  ├── health.Stop()
  ├── manager.StopEngine()
  ├── tunnel.UnGuard()
  ├── tunnel.Disengage()
  └── tunnel.Teardown()
```

### 4.4 Heartbeat Loop (background, every 60s)
```
doHeartbeat()
  ├── Load token from storage
  ├── Build HeartbeatRequest (device ID, status, uptime, errors)
  ├── POST /api/heartbeat with cert-pinned TLS
  │     ├── Success:
  │     │     ├── Reset fail counter + grace timer
  │     │     ├── Save rotated token
  │     │     ├── Check suspension status
  │     │     ├── Update protocol list
  │     │     └── Process remote commands
  │     └── Failure:
  │           ├── Increment consecutiveFails
  │           ├── < 3 → retry next interval
  │           └── ≥ 3 → grace mode (continue with last token for 7 days)
  │
  └── After 7 days grace expiry:
        └── MustReauthenticate() = true
              └── User must re-activate
```

---

## 5. Package-by-Package Reference

### 5.1 main.go

**File:** `myvpn/main.go` (32 lines)

**Purpose:** Entry point. Parses flags, initializes storage, checks updates, launches GUI.

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--hub` | `https://api.yourdomain.com` | Admin hub URL |
| `--revert` | `false` | Force-revert to previous binary |

**Control Flow:**
1. `storage.Init()` — creates directories, loads data.json
2. `updater.CheckOnStartup()` — checks for pending update rollback
3. `gui.Run()` — handles activation if needed, then main window

**Notes:**
- Heartbeat is started from `gui.launchMain()` not from main.go (starts after activation)
- Device fingerprint is collected/saved inside the GUI's activation handler

---

### 5.2 storage

**File:** `internal/storage/storage.go` (235 lines)

**Purpose:** Persistent state management. All app data stored as JSON in the platform-appropriate app data directory.

**Data Model:**
```go
type AppData struct {
    Token             string            // JWT returned from activation
    DeviceFingerprint string            // SHA256 of MAC+disk+mobo
    PlanTier          string            // warp_lite, stealth_browse, gaming_mid, gaming_max
    Protocols         []ProtocolConfig  // Protocol list from heartbeat
    ActiveConn        string            // Currently active protocol ID
    TelemetryOptOut   bool              // Privacy setting
    EngineCache       map[string]string // protocolID → cached binary path
}

type ProtocolConfig struct {
    ID          string          // e.g. "hysteria2"
    DisplayName string          // e.g. "Speed Mode"
    BinaryName  string          // e.g. "speedmode"
    ConfigJSON  json.RawMessage // Engine-specific config
    Weight      int             // Priority (higher = more preferred)
}
```

**Data Directory (platform-specific):**
| Platform | Path |
|----------|------|
| Windows | `%APPDATA%/MyVPN/` |
| macOS | `~/Library/Application Support/MyVPN/` |
| Linux | `~/.local/share/MyVPN/` |

**Subdirectories:** `logs/`, `engines/`

**Log Rotation:** Background goroutine checks every 5 minutes. Rotates any file prefixed `engine-` when it exceeds 10MB. Keeps last 3 rotated copies (`.log.1`, `.log.2`, `.log.3`).

**Key Functions:**
```go
func Init()                                    // Singleton init
func IsActivated() bool                        // Token != ""
func SaveActivation(token, planTier string)    // Store activation result
func LoadToken() string
func SaveToken(token string)                   // For heartbeat token rotation
func LoadPlanTier() string
func SaveDeviceFingerprint(fp string)
func LoadDeviceFingerprint() string
func GetProtocols() []ProtocolConfig
func SetProtocols(protos []ProtocolConfig)     // From heartbeat response
func GetCachedEnginePath(protocolID) string
func SetCachedEnginePath(protocolID, path string)
func GetTelemetryOptOut() bool
func SetTelemetryOptOut(bool)
func DataDir() string
func LogDir() string
func EngineDir() string
```

---

### 5.3 activation

**Files:** `internal/activation/` (6 files, 280 lines total)

**Purpose:** Hardware fingerprint collection and activation code validation.

**Fingerprint Collection:**
```go
func CollectFingerprint() string {
    raw := fmt.Sprintf("%s|%s|%s", getMAC(), getDiskSerial(), getMoboSerial())
    hash := sha256.Sum256([]byte(raw))
    return hex.EncodeToString(hash[:])
}
```

| Platform | getMAC() | getDiskSerial() | getMoboSerial() |
|----------|----------|-----------------|-----------------|
| Windows | `getmac /fo csv /nh` | `wmic diskdrive get SerialNumber` | `wmic baseboard get SerialNumber` |
| macOS | `ifconfig en0` → `ether` | `system_profiler SPStorageDataType` | `system_profiler SPHardwareDataType` |
| Linux | Returns stub `"linux-test-mac"` | Returns stub `"linux-test-disk"` | Returns stub `"linux-test-mobo"` |

**Activation Code Format:**
`MYVPN-XXXX-XXXX-XXXX-C`
- Prefix: `MYVPN-`
- Character set: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (32 chars, excludes I,O,0,1 for readability)
- 16 data characters + 1 Luhn-mod-N check digit
- Case-insensitive on input

**Luhn-mod-N Algorithm:**
The check digit is computed over the 16 data characters. Starting from the rightmost data character, every second character's charset index is doubled. If doubling exceeds the charset length, the value is decomposed (floor(index/BASE) + index%BASE). Sum all values, compute `(BASE - (sum % BASE)) % BASE` to get the expected check digit index.

**Key Functions:**
```go
func Validate(apiBase, code string) (*ActivationResponse, error)
// - Client-side Luhn check
// - POST to /api/collections/activations/records with X-Device-Fingerprint header
// - Returns {Token, Plan} on success
// - Handles 403 (suspended/bound), 404 (invalid), 429 (rate limit)

func CollectFingerprint() string
```

**Test coverage:** `activation_test.go` tests Luhn validation with valid, invalid check digit, wrong length, excluded chars, and lowercase input.

---

### 5.4 branding

**File:** `internal/branding/branding.go` (71 lines)  
**Test:** `internal/branding/branding_test.go` (54 lines)

**Purpose:** Obfuscation layer — maps real protocol/plan IDs to fake user-visible names.

**Mappings:**
```go
Protocol Names:         Binary Names:         Plan Names:
hysteria2 → "Speed Mode"  → "speedmode"     warp_lite → "Warp Lite"
usque     → "Lite Mode"   → "litemode"      stealth_browse → "Stealth Browse"
xray      → "Stealth Mode" → "stealthmode"  gaming_mid → "Gaming Mid"
tuic      → "Game Mode"   → "gamemode"      gaming_max → "Gaming Max"

Log Codes:
hysteria2 → "[01]"
usque     → "[02]"
xray      → "[03]"
tuic      → "[04]"
```

**Key Functions:**
```go
func ProtocolDisplayName(protocolID string) string
func BinaryName(protocolID string) string
func PlanDisplayName(planID string) string
func LogCode(protocolID string) string
```

**Critical rule:** The GUI MUST NEVER display real protocol names, IP addresses, port numbers, engine binary names, or any technical detail. All user-facing strings go through this package.

---

### 5.5 manager

**Files:** `internal/manager/process.go` (159 lines), `disguise_windows.go` (12 lines)

**Purpose:** Lifecycle management for VPN engine processes.

**Key Functions:**
```go
func IsRunning() bool
func LastExitCode() int
func StartEngine(cfg ProtocolConfig, planTier string, bandwidthBps int) error
func StopEngine() error
func ActiveProtocolID() string
```

**StartEngine Flow:**
1. Acquire mutex, stop any existing engine
2. Locate cached binary (from EngineCache or default path)
3. Build command with engine-specific flags and bandwidth cap
4. `disguiseProcess()` → HideWindow=true on Windows
5. `sandbox()` → stub for platform-specific process sandboxing
6. Redirect stdout/stderr to rotating log file
7. `cmd.Start()` → launch as child process
8. Spawn goroutine to `cmd.Wait()` and capture exit code

**Engine Config Mapping:**
```go
"hysteria2": binaryPath + "client -c <config> --speed <bandwidthBps>"
"usque":     binaryPath + "-c <config>"
default:     binaryPath + "-c <config>"
```

**disguise_windows.go** (`//go:build windows`):
```go
func disguiseProcess(cmd *exec.Cmd, _ string) {
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
```

**Notes:**
- The `sandbox()` function is a stub. Implement per-platform process sandboxing (job objects on Windows, seatbelt on macOS) for production.
- The generic `disguiseProcess` in process.go is a no-op `func(_ *exec.Cmd, _ string) {}` using blank identifiers to avoid unused-parameter errors.

---

### 5.6 heartbeat

**File:** `internal/heartbeat/heartbeat.go` (271 lines)

**Purpose:** Background keepalive loop that maintains server connection, handles token rotation, grace period, and remote commands.

**Constants:**
```go
heartbeatInterval   = 60 seconds
maxConsecutiveFails = 3
graceDuration       = 7 days
```

**Key Types:**
```go
type HeartbeatRequest struct {
    Token, DeviceID, AppVersion, OSPlatform, ProtocolID string
    Connected, HealthStatus                              string  // "healthy"|"degraded"|"dead"
    EngineExitCode                                       int
    LastError                                            string
    UptimeSec                                            int
    BytesUp, BytesDown                                   int64
}

type HeartbeatResponse struct {
    Status    string              // "active"|"suspended"|"disabled"
    Plan      string
    Token     string             // Rotated JWT
    Protocols []ProtocolConfig
    Commands  []RemoteCommand    // "update", "disable", "reconfigure"
    Message   string
}
```

**Key Functions:**
```go
func SetCertPin(certSHA256 string)     // Set expected hub certificate hash
func Start(baseURL string)             // Start 60s heartbeat loop (blocking)
func SetLastError(errMsg string)       // Report engine errors to next heartbeat
func GraceRemaining() int              // Days of grace left
func MustReauthenticate() bool         // Grace expired?
```

**Certificate Pinning:**
```go
func pinnedHTTPClient() *http.Client {
    // TLS config with InsecureSkipVerify=false when certHash is set
    // VerifyPeerCertificate checks SHA256 of peer's public key
    // against hubCertHash
}
```
Set `hubCertHash` to the SHA256 of your server's TLS certificate public key. If left empty (dev mode), certificate verification is skipped. In production, always set this to prevent MITM by school SSL inspection proxies.

**Grace Period Logic:**
```
Normal operation:
  heartbeat succeeds → reset fail counter, reset grace timer, rotate token
  
Heartbeat failures:
  1-2 failures → retry at next 60s interval
  3+ failures → enter grace mode
    - Last valid token continues working
    - Token expiry (24h) is OVERRIDDEN by grace
    - Grace counts down from 7 days
  
Grace expired:
  MustReauthenticate() = true
  User must re-activate through activation flow
```

**Remote Commands:**
```go
"disable"     → manager.StopEngine(), show "Account disabled"
"update"      → Print available update info (actual update handled by updater)
"reconfigure" → Print reconfigure notice
```

---

### 5.7 tunnel

**Files:** `internal/tunnel/` (6 files, 189 lines total)

**Purpose:** Network tunnel management — create/destroy TUN device, install routes, engage/disengage kill switch, configure DNS.

**Interface:**
```go
type Manager interface {
    Setup() error
    Teardown() error
    AddRoute(dest, gateway string) error
    RemoveRoute(dest string) error
}
```

**Platform Implementations:**

| File | Build Tag | TUN Creation | Route Management |
|------|-----------|-------------|-----------------|
| `tun_windows.go` | `windows` | Via helper IPC (`create_tun` command) | Via helper IPC |
| `tun_darwin.go` | `darwin` | Via helper IPC (`create_tun` command) | Via helper IPC |
| `tun_linux.go` | `linux` | Direct `ip tuntap` + `ip addr` + `ip link` | Direct `ip route` |

**Kill Switch:**
```go
func Engage() error     // Block all non-VPN traffic
func Disengage() error  // Restore normal routing
```
| Platform | Engage | Disengage |
|----------|--------|-----------|
| Windows | `netsh interface ip delete route 0.0.0.0/0` | Routes restored on TUN teardown |
| macOS | `pfctl -a com.myvpn/killswitch -ef -` with `block drop out all` | `pfctl -a com.myvpn/killswitch -F all` |
| Linux | `iptables -A OUTPUT -j DROP` | `iptables -F OUTPUT` |

**DNS Guard:**
```go
func Guard() error   // Redirect DNS :53 through tunnel
func UnGuard() error // Restore normal DNS
```
Sets DNS server to tunnel IP (10.0.0.1) via platform-specific commands.

---

### 5.8 probe

**File:** `internal/probe/probe.go` (82 lines)

**Purpose:** Bandwidth probe run on the regular network BEFORE TUN creation. Determines initial bandwidth cap for the engine.

**Types:**
```go
type Result struct {
    BandwidthBps  int           // 0 = uncapped (unconstrained network)
    BaselineRTT   time.Duration // Time to first byte
    IsConstrained bool          // true if download took >1.5s
}
```

**Algorithm:**
```
1. If tier == "warp_lite" → return hardcoded 8 Mbps (skip probe)
2. Download 1MB from /speedtest/1mb.bin via HTTP GET
3. Measure time to first byte (BaselineRTT)
4. Measure total throughput: bytes*8 / elapsed_seconds
5. If elapsed > 1.5s → network is constrained → apply cap
6. Clamp to tier limits (floor 3 Mbps, ceiling varies by tier)
7. If unconstrained → return BandwidthBps=0 (no cap needed)
```

**Tier Clamping:**
| Tier | Ceiling | Floor |
|------|---------|-------|
| Gaming Max | 100 Mbps | 3 Mbps |
| Gaming Mid | 50 Mbps | 3 Mbps |
| Stealth Browse | 48 Mbps | 3 Mbps |
| Warp Lite | 8 Mbps (hardcoded, no probe) | N/A |

---

### 5.9 monitor

**File:** `internal/monitor/monitor.go` (210 lines)

**Purpose:** Background bandwidth monitor that dynamically adjusts the engine's speed cap during a session. Runs every 15 seconds.

**Key Fields:**
```go
type Monitor struct {
    currentCapKBps int64           // Atomic, current cap in KB/s
    ewmaKBps       float64         // Exponentially weighted moving average
    tier           string
    baselineRTT    time.Duration
    probeURL       string           // /speedtest/100kb.bin
    lastAdjustment time.Time        // Rate-limit adjustments
}
```

**Constants:**
```go
monitorInterval     = 15 * time.Second
monitorChunkSize    = 100 * 1024  // 100KB
ewmaAlpha           = 0.3         // Weight for new samples
decreaseThreshold   = 0.7         // Reduce cap if EWMA < 70% of current
decreaseSamples     = 2           // Need 2 consecutive low samples
increaseThreshold   = 1.2         // Increase cap if EWMA > 120% of current
increaseWait        = 60 * time.Second  // Need 60s of headroom
bufferbloatRTTMult  = 2.0         // RTT spike > 2x baseline = bufferbloat
bufferbloatCapCut   = 0.5         // Cut cap by 50% on bufferbloat
capDecreaseFactor   = 0.7         // Reduce cap by 30%
capIncreaseFactor   = 1.2         // Increase cap by 20%
minAdjustmentGap    = 5 * time.Second  // Don't adjust more often than 5s
```

**Algorithm:**
```
Every 15 seconds:
  1. Download 100KB chunk, measure throughput & RTT
  2. Update EWMA: ewma = 0.3*sample + 0.7*ewma
  3. Check for bufferbloat:
       if RTT > baselineRTT * 2 → cut cap by 50%, reset counters
  4. If capped:
       if EWMA < cap * 0.7 for 2 consecutive samples → reduce cap by 30%
       if EWMA > cap * 1.2 for 60 seconds → increase cap by 20%
       else → in comfort zone, reset counters
  5. If uncapped but EWMA shows constraint → apply cap at 90% of EWMA
  6. All adjustments clamped to tier min/max
```

**Clamp Boundaries (converted from bps to KB/s):**
```go
floor = 375 KB/s (~3 Mbps)
ceiling:
  gaming_max:     ≈ 12,207 KB/s (100 Mbps)
  gaming_mid:     ≈  6,104 KB/s (50 Mbps)
  stealth_browse: ≈  5,859 KB/s (48 Mbps)
  warp_lite:      ≈    977 KB/s (8 Mbps)
```

---

### 5.10 health

**File:** `internal/health/health.go` (108 lines)

**Purpose:** Tunnel health check that detects connectivity loss. Probes the admin hub through the tunnel every 15 seconds.

**Key Fields:**
```go
type Checker struct {
    probeURL   string     // /_/health endpoint on admin hub
    interval   time.Duration  // 15s
    maxFails   int            // 3
    fails      int
    degraded   bool
    // Callbacks
    onDegraded  func()
    onRecovered func()
    onDead      func()
}
```

**States:**
- **Healthy:** HTTP 200 response → reset fail count
- **Degraded:** 1+ failures → fire `onDegraded` callback
- **Dead:** 3+ failures → fire `onDead` callback

**Integration with GUI:**
- `OnDegraded` → show yellow status dot, log "DEGRADED"
- `OnDead` → trigger `disconnect()` sequence
- `OnRecovered` → restore green status dot (automatic when healthy again)

---

### 5.11 helper

**Files:** `internal/helper/` (3 files, 183 lines total)

**Purpose:** IPC client and service skeleton for the privileged helper (Windows SYSTEM service / macOS launchd daemon). The helper runs as root/administrator and performs privileged operations (TUN creation, firewall rules) on behalf of the unprivileged GUI app.

**Command Protocol (JSON over named pipe / Unix socket):**
```go
type Command struct {
    Action  string // "create_tun", "destroy_tun", "block_all", "unblock_all", "add_route", "remove_route"
    TUNIP   string
    Dest    string
    Gateway string
    Mask    string
    Servers string
    Auth    string // Shared secret for authentication
}
```

**Client Side (`helper.go`):**
```go
func SendCommand(cmd Command) error
// 1. Connect to socket (Unix) or pipe (Windows)
// 2. Read shared secret from helper.secret file
// 3. Set cmd.Auth to secret
// 4. JSON marshal and send
```

**Service Skeleton (`service_windows.go`, `service_darwin.go`):**
- `RunService()` — listens on named pipe / Unix socket, accepts connections, verifies auth secret, dispatches to action handlers
- `handleConnection()` — decodes JSON command, checks auth, routes to action
- Action handlers are **stubs** — they parse the command but don't implement the actual TUN/firewall operations

**Security Model:**
- Shared secret generated at service install time, written to file readable only by user + SYSTEM (root)
- Service rejects commands with invalid auth
- Secret file locations: `%PROGRAMDATA%/MyVPN/helper.secret` (Windows), `/var/run/myvpn-helper.secret` (macOS)

**Important:** The helper service action handlers (create_tun, destroy_tun, block_all, etc.) are NOT fully implemented. They need actual wintun/utun driver calls and pf/netsh command execution filled in.

---

### 5.12 updater

**Files:** `internal/updater/` (2 files, 168 lines total)

**Purpose:** Self-update system with SHA256 integrity verification and crash-rollback protection.

**Update Flow (`updater.go`):**
```go
func DownloadAndVerify(url, expectedSHA256, destPath string) error
// 1. HTTP GET from admin hub (cert-pinned TLS)
// 2. Compute SHA256 during download (io.MultiWriter)
// 3. Compare computed hash vs expected
// 4. On match: write to destPath
// 5. Set executable bit on non-Windows

func SwapBinary(newBinaryPath string) error
// 1. Copy current binary to myvpn.prev
// 2. Rename new binary to current binary path
```

**Rollback System (`recover.go`):**
```go
func CheckOnStartup(holdRevertKey bool) (bool, error)
func MarkUpdatePending() error
```

**Two-Phase Sentinel Protocol:**
```
Normal update flow:
  1. Download new binary, compute SHA256
  2. Copy current → myvpn.prev
  3. Write new binary in place of current
  4. Create .update-pending sentinel
  5. Restart app

First launch after update:
  .update-pending EXISTS + .prev EXISTS
  → This is the first launch after update
  → Rename .update-pending → .update-confirmed (Phase 2)
  → .prev preserved as safety net

Second launch (successful):
  .update-confirmed EXISTS + .prev EXISTS
  → Previous launch worked fine
  → Delete .update-confirmed + .prev (cleanup)

Crash on first launch:
  .update-pending EXISTS + .prev EXISTS
  → App crashed before Phase 2 transition
  → AUTO-REVERT: restore .prev → delete .update-pending
  
Manual revert:
  User holds Ctrl at launch OR passes --revert flag
  → Restore .prev immediately, regardless of sentinel state
```

---

### 5.13 gui

**File:** `internal/gui/app.go` (322 lines)

**Purpose:** Fyne-based desktop GUI. Single app instance handles both activation prompt and main window.

**Architecture:**
```go
var (
    state GUIState   // Package-level state (single window)
    a     fyne.App   // Single Fyne app instance
)

type GUIState struct {
    Window           fyne.Window
    StatusLabel      *widget.Label
    StatusDot        *canvas.Circle     // Green/yellow/red dot
    TierLabel        *widget.Label
    TimeLabel        *widget.Label      // Connection duration
    SpeedLabel       *widget.Label
    ConnectBtn       *widget.Button
    adminHubURL      string
    connected        bool
    startTime        time.Time
    healthChecker    *health.Checker
    bandwidthMonitor *monitor.Monitor
}
```

**Entry Points:**
```go
func Run(adminHubURL string)
// 1. Create Fyne app with custom black+purple theme
// 2. If not activated → showActivationPrompt()
// 3. If activated → launchMain()
// Blocks until window closes

func launchMain(adminHubURL, planTier string)
// 1. Start heartbeat loop
// 2. Build 300×400 Fyne window
// 3. Green/yellow/red status dot (canvas.Circle)
// 4. Connect/Disconnect button
// 5. Collapsible Settings accordion (privacy toggle, code, plan)
// 6. Black+purple gradient background
// 7. Start statusLoop() goroutine

func showActivationPrompt(adminHubURL string)
// 1. Show entry field + Activate button
// 2. On success: save activation, call launchMain()
```

**UI Elements:**
- **300×400px** non-resizable window
- Status dot: green (connected), yellow (degraded/connecting), red (disconnected)
- Status label: "Connected", "Disconnected", "Connecting", "Degraded", or error message
- Tier label: plan display name from branding
- Time label: connection duration (HH:MM:SS)
- Speed label: current cap or "Fast"
- Connect/Disconnect button
- Settings accordion (collapsed): privacy check, masked activation code, plan name

**Custom Theme (myVPNTheme):**
```go
Background:  #0D0D0F (near-black)
Primary:     #A855F7 (purple)
Foreground:  #E6E6EB (light gray)
Button:      #A855F7 (purple)
```

**statusLoop (background goroutine):**
```
Every 2 seconds (only when connected):
  - Update time label with elapsed duration
  - Update speed label with current cap from monitor
```

**Threading Note:** Fyne requires all UI operations on the main goroutine. The `statusLoop` and `connect()`/`disconnect()` goroutines call `widget.SetText()` and `canvas.Refresh()` from background goroutines. This works in practice for simple apps but may cause warnings in Fyne's debug mode. For production, use `canvas.Refresh()` properly or route UI updates through channels.

---

## 6. Server-Side Components

### 6.1 Caddy Reverse Proxy

**File:** `server/Caddyfile`

**Configuration:**
```
api.yourdomain.com {
    reverse_proxy 127.0.0.1:8090

    rate_limit {
        zone heartbeat { key {remote_host} match_path /api/heartbeat rate 1/10s burst 3 }
        zone activation { key {remote_host} match_path /api/collections/activations/* rate 5/10m burst 5 }
        zone general { key {remote_host} rate 100/10s burst 50 }
    }

    handle /speedtest/* {
        root * /var/www/speedtest
        file_server
        header Cache-Control "no-store"
    }

    handle /updates/* {
        root * /var/www/updates
        file_server
    }
}
```

**Rate Limits:**
| Zone | Path | Rate | Burst | Purpose |
|------|------|------|-------|---------|
| heartbeat | `/api/heartbeat` | 1 per 10s | 3 | Prevent heartbeat spam |
| activation | `/api/collections/activations/*` | 5 per 10m | 5 | Brute-force protection |
| general | all other | 100 per 10s | 50 | General abuse prevention |

**Static File Serving:**
- `/speedtest/` — bandwidth probe payloads (1mb.bin, 100kb.bin)
- `/updates/` — SHA256-verified update binaries

### 6.2 PocketBase Hooks

#### sqlite_pragmas.pb.js
```javascript
onBeforeServe(() => {
    const db = $app.dao().db();
    db.exec("PRAGMA journal_mode=WAL;");
    db.exec("PRAGMA busy_timeout=5000;");
});
```
Runs on server startup. Enables WAL mode for concurrent read/write and sets a 5-second busy timeout to prevent SQLITE_BUSY errors.

#### activation_rate_limit.pb.js
```javascript
routerAdd("POST", "/api/collections/activations/records", (c) => {
    const ip = c.requestHeader("X-Forwarded-For") || c.remoteIP();
    const recent = $app.dao().findRecordsByFilter("activation_attempts",
        `ip="${ip}" && created > "${...}"`);
    if (recent.length >= 5) {
        return c.json(429, { message: "Too many attempts" });
    }
    $app.dao().saveRecord($app.dao().createRecord("activation_attempts", { ip, created: new Date() }));
    return c.next();
});
```
Intercepts activation POST requests. Tracks attempts per IP in the `activation_attempts` collection. Blocks after 5 attempts in 10 minutes.

#### device_binding.pb.js
The most critical hook. Handles permanent one-code-one-device binding.

**Logic:**
1. Read `code` from body, `X-Device-Fingerprint` from header
2. Server-side Luhn-mod-N validation
3. Look up `activation_codes` collection by code
4. Check expiry
5. If `bound_device_id` is null: bind fingerprint, save, return token
6. If `bound_device_id` matches fingerprint: check suspension status
   - Suspended → return 403 with reason
   - Not suspended → return new token
7. If `bound_device_id` differs: return 403 "already bound"
8. Token generation: base64(JSON payload) + "." + HMAC-SHA256(payload, TOKEN_SECRET)

**Token Format:** `base64({"fingerprint":"...","code":"...","exp":...}) + "." + HMAC-SHA256`

**Important:** Set `TOKEN_SECRET` environment variable to a random 64-char hex string in PocketBase's environment.

#### heartbeat.pb.js
Handles the 60s heartbeat endpoint.

**Logic:**
1. Extract Bearer token and X-Device-Fingerprint header
2. Verify HMAC signature
3. Decode payload, check expiry
4. Look up activation code by device fingerprint
5. Check suspension status → return "suspended" status if suspended
6. Generate new rotated token (24h expiry)
7. Return active status, plan, protocols, commands

---

## 7. API Contracts

### 7.1 Activation API

**Endpoint:** `POST /api/collections/activations/records`

**Headers:**
| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `X-Device-Fingerprint` | SHA256 hardware fingerprint |

**Request Body:**
```json
{
    "code": "MYVPN-A3X9-K7M2-Q5P1-C"
}
```

**Success Response (200):**
```json
{
    "token": "base64payload.HMAC",
    "plan": "gaming_max"
}
```

**Error Responses:**
| Status | Meaning |
|--------|---------|
| 400 | Missing code or fingerprint, or invalid format |
| 403 | Already bound to different device, or device is suspended |
| 404 | Invalid or expired code |
| 410 | Code expired |
| 429 | Too many attempts |

### 7.2 Heartbeat API

**Endpoint:** `POST /api/heartbeat`

**Headers:**
| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <token>` |
| `Content-Type` | `application/json` |
| `X-Device-Fingerprint` | SHA256 hardware fingerprint |

**Request Body:**
```json
{
    "device_fingerprint": "sha256hex...",
    "app_version": "1.0.0",
    "os_platform": "windows_amd64",
    "protocol_id": "hysteria2",
    "connected": true,
    "health_status": "healthy",
    "engine_exit_code": 0,
    "last_error": "",
    "uptime_seconds": 3600,
    "bytes_up": 1234,
    "bytes_down": 5678
}
```

**Response Body:**
```json
{
    "status": "active",
    "plan": "gaming_max",
    "token": "new-jwt...",
    "protocols": [
        {
            "id": "hysteria2",
            "display_name": "Speed Mode",
            "binary_name": "speedmode",
            "config": { "server": "...", "auth": "..." },
            "weight": 1
        }
    ],
    "commands": [],
    "message": ""
}
```

**Status Values:**
| Status | Meaning | Client Action |
|--------|---------|---------------|
| `active` | Everything OK | Rotate token, process commands |
| `suspended` | Device blocked by admin | Stop engine, show message |
| `disabled` | Account disabled | Stop engine, prompt re-activation |

**Commands:**
| Action | Payload | Effect |
|--------|---------|--------|
| `update` | `{url, sha256, version, platform}` | Download and apply update |
| `disable` | `{}` | Stop engine permanently |
| `reconfigure` | `{}` | Re-read config |

---

## 8. Connection State Machine

```
                    ┌──────────┐
                    │  IDLE    │  App launched, storage loaded
                    └────┬─────┘
                         │
                         ▼
                  ┌──────────────┐
                  │   ACTIVE     │  Ready to connect. User sees "Connect" button.
                  └──────┬───────┘
                         │ user taps Connect
                         ▼
                  ┌──────────────┐
                  │  CONNECTING  │  Probe running, TUN being created, engine starting
                  └──────┬───────┘
                         │
               ┌─────────┴──────────┐
               ▼                    ▼
    ┌──────────────────┐  ┌──────────────────┐
    │CONNECTED_PRIMARY  │  │CONNECTED_FALLBACK│  Green dot
    │(Hysteria 2)       │  │(usque/Warp)      │
    └────────┬─────────┘  └────────┬─────────┘
             │                     │
             │  health degraded    │  health degraded
             ▼                     ▼
    ┌──────────────────────────────────────┐
    │             DEGRADED                  │  Yellow dot, engine stuttering,
    │  (trying other engine or waiting)     │  probing for recovery
    └────────────────┬─────────────────────┘
                     │ all engines failed
                     ▼
             ┌──────────────┐
             │ DISCONNECTED │  Red dot, kill switch active
             └──────┬───────┘
                    │ "tap to retry"
                    ▼
               ┌──────────┐
               │  ACTIVE  │
               └──────────┘

GRACE state (orthogonal — active alongside any state):
  Heartbeat has failed ≥3 times but 7-day window hasn't expired.
  Token expiry is overridden — last valid token continues working.
  GUI shows remaining grace days.
```

**Transitions:**
| From | Event | To |
|------|-------|----|
| IDLE | App ready | ACTIVE |
| ACTIVE | Tap Connect | CONNECTING |
| CONNECTING | Engine started + health OK | CONNECTED_PRIMARY |
| CONNECTING | Fallback engine started | CONNECTED_FALLBACK |
| CONNECTED_PRIMARY | 3 health fails, fallback available | CONNECTED_FALLBACK |
| CONNECTED_PRIMARY | all fails+fallback also fails | DEGRADED |
| CONNECTED_FALLBACK | 3 health fails | DEGRADED |
| DEGRADED | all engines dead | DISCONNECTED |
| DISCONNECTED | Tap retry | ACTIVE |
| Any state | Heartbeat grace expired | Must re-activate |

---

## 9. Activation & Device Binding Details

### Code Generation
Codes are generated via PocketBase admin UI (or a script) and printed on paper for middlemen.

**Format:** `MYVPN-XXXX-XXXX-XXXX-C`
- Prefix: `MYVPN-` (branded, distinguishes from competitor codes)
- Character set: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (32 chars)
- Excluded: `I`, `O`, `0`, `1` (readability)
- 16 data chars + 1 check digit
- Case-insensitive on input (normalized to uppercase)
- Optional `expires` date in PocketBase (default 90 days)

### Binding Rules
1. **Fresh code** (bound_device_id is null): Bind fingerprint, save record, return token
2. **Same device** (bound_device_id === fingerprint): Return new token (unless suspended)
3. **Different device** (bound_device_id !== fingerprint): **Permanent rejection.** Code can never be used on a different device.
4. **No deactivation.** Codes cannot be unbound. Admin sets `suspended=true` to block connections. Unsuspending immediately allows reconnection.

### Why This Design
- Survives app deletion/reinstall (same hardware = same fingerprint)
- Prevents code sharing among students
- Middlemen sell "one code = one device" — clear value proposition
- Suspension preserves binding (admin can unsuspend without re-binding)
- No multi-device plans (simplicity)

### Luhn-mod-N Server-Side Validation (JavaScript)
```javascript
const CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const BASE = CHARSET.length;

function luhnModN(code) {
    let sum = 0, double = false;
    for (let i = code.length - 1; i >= 0; i--) {
        let val = CHARSET.indexOf(code[i].toUpperCase());
        if (val === -1) return -1;
        if (double) { val *= 2; if (val >= BASE) val = Math.floor(val/BASE) + (val%BASE); }
        sum += val;
        double = !double;
    }
    return (BASE - (sum % BASE)) % BASE;
}
```

---

## 10. Bandwidth Probe & Monitor Algorithms

### Initial Probe (runs ONCE per connect, before TUN)
```
1. For Warp Lite: return 8 Mbps hardcoded (skip probe)
2. HTTP GET /speedtest/1mb.bin (1 MB file)
3. Measure:
   - Time To First Byte (TTFB) → baseline RTT
   - Total throughput = (bytes*8) / elapsed_seconds
4. If elapsed < 1.5s → unconstrained → return cap=0 (no cap)
5. If elapsed >= 1.5s → constrained → return measured cap
6. Clamp: min 3 Mbps, max varies by tier
```

### Background Monitor (runs every 15s during connection)
```
EWMA = 0.3 × sample + 0.7 × EWMA (initialized on first sample)

Bufferbloat detection:
  if RTT > baselineRTT × 2:
    cap = cap × 0.5           ← immediate 50% cut
    reset low/high counters

If currently capped:
  if EWMA < cap × 0.7 for 2 consecutive samples:
    cap = cap × 0.7           ← 30% reduction
  if EWMA > cap × 1.2 for 60 seconds:
    cap = cap × 1.2           ← 20% cautious increase
  else: in comfort zone, reset counters

If currently uncapped (cap=0) but EWMA shows constraint:
  cap = EWMA × 0.9            ← apply cap at 90% of measured

All adjustments clamped to [tier_floor, tier_ceiling]
Minimum 5 seconds between adjustments
```

**Why this design:**
- Bufferbloat detection prevents the "buffer then spike" pattern on congested WiFi
- Fast reduction (2 samples = 30s) protects against sudden congestion
- Slow increase (60s sustained headroom) prevents oscillation
- EWMA smoothing filters out transient spikes

---

## 11. Update System & Rollback

### Update Flow
1. Server sends `{"action": "update", "payload": {"url": "...", "sha256": "...", "version": "1.0.1"}}` in heartbeat response
2. Client downloads binary from admin hub over cert-pinned TLS
3. Computes SHA256 during download, compares with expected
4. **On match:** copies current binary to `myvpn.prev`, writes new binary, creates `.update-pending`, restarts
5. **On mismatch:** deletes downloaded file, reports error in next heartbeat

### Two-Phase Sentinel Rollback
```
Phase 0: Normal operation
  Binary: myvpn.exe (or myvpn)
  Backup: (none)
  Sentinels: (none)

Phase 1: After update write, before first launch
  Binary: new version
  Backup: myvpn.prev (old version)
  Sentinel: .update-pending

Phase 2: First launch after update (successful)
  .update-pending → .update-confirmed (rename)
  Backup preserved

Phase 3: Second launch (confirms update is stable)
  Delete .update-confirmed
  Delete myvpn.prev
  Clean state

Crash recovery:
  If .update-pending exists but app crashed before Phase 2:
    → Auto-restore myvpn.prev → delete .update-pending
    → Previous version runs clean

Manual revert:
  --revert flag OR hold Ctrl at startup
  → Immediately restore myvpn.prev regardless of sentinel state
```

---

## 12. Tier Limits & Pricing

| Tier | Code Prefix | Primary Engine | Bandwidth Cap | usque Fallback Cap | Gaming Tuning | Price Range |
|------|-------------|---------------|---------------|-------------------|---------------|-------------|
| Warp Lite | `MYVPN-W...` | usque/Warp only | 8 Mbps | N/A | N/A | $2-4/mo |
| Stealth Browse | `MYVPN-S...` | Hysteria 2 | 48 Mbps | 5 MB/s | Standard CC (throughput) | $5-7/mo |
| Gaming Mid | `MYVPN-G...` | Hysteria 2 | 50 Mbps | 5 MB/s | Brutal CC (low latency) | $8-10/mo |
| Gaming Max | `MYVPN-X...` | Hysteria 2 | 100 Mbps | 5 MB/s | Brutal CC (low latency) | $11-13/mo |

**Bandwidth notes:**
- All caps are per-connection soft limits enforced client-side
- Hysteria 2 Brutal CC mode trades throughput for latency stability (ideal for gaming)
- Stealth Browse uses standard Hysteria 2 congestion control for max throughput
- Warp Lite is intentionally throttled to push users toward paid tiers

---

## 13. Build System & CI/CD

### Makefile Targets
```makefile
make build-windows      # Windows amd64 (requires mingw-w64)
make build-macos-intel  # macOS Intel (requires Xcode)
make build-macos-arm    # macOS Apple Silicon
make build-native       # Current platform
make clean              # Remove binaries
make test               # go test -v -race ./internal/...
```

### GitHub Actions (`.github/workflows/build.yml`)
- **On push** to main/master, **on tag** v*, and **on PR**
- **Matrix builds:**
  - `windows-latest` → myvpn_windows_amd64.exe
  - `macos-latest` with arch matrix (amd64, arm64) → myvpn_darwin_*
- **Test job** on `ubuntu-latest` with Fyne deps
- **Artifacts** uploaded per platform

### Fyne/CGO Requirements
Fyne requires CGO for graphics backends. This means:
- **Linux build:** `libgl1-mesa-dev`, `xorg-dev`
- **Windows cross-compile:** `mingw-w64` (CC=x86_64-w64-mingw32-gcc)
- **macOS build:** Native macOS runner with Xcode CLT
- **GitHub Actions handles this** — each OS runner has native toolchains

### go.mod
The current `go.mod` has only the direct Fyne dependency. Run `go mod tidy` on a machine with internet access to resolve all transitive dependencies and generate `go.sum`.

---

## 14. Server Deployment

### Automated Deployment
```bash
# On a fresh Ubuntu 22.04 VPS (2GB RAM min):
bash scripts/setup_server.sh api.yourdomain.com
```

**What the script does:**
1. System update + upgrade
2. Install Caddy with rate_limit module
3. Install PocketBase v0.22.0 as systemd service
4. Configure Caddy reverse proxy with rate limiting zones
5. Create /var/www/speedtest/ and /var/www/updates/ directories
6. Generate 1MB and 100KB random files for bandwidth probes
7. Configure UFW (allow 80, 443, 22)
8. Create pb_hooks directory
9. Print post-deployment instructions

### Post-Deployment Steps
1. **Set TOKEN_SECRET env var** for PocketBase:
   ```bash
   echo 'TOKEN_SECRET=your-random-64-char-hex' >> /opt/pocketbase/.env
   systemctl restart pocketbase
   ```
2. **Create collections** in PocketBase admin UI at `https://api.yourdomain.com/_/`:
   - **activation_codes:** code (text, unique), plan (text), bound_device_id (text, unique, nullable), suspended (bool), suspended_reason (text), created_by (text), expires (datetime), active (bool)
   - **activation_attempts:** ip (text), created (autodate)
3. **Copy hooks:**
   ```bash
   cp server/pb_hooks/*.pb.js /opt/pocketbase/pb_hooks/
   chown pocketbase:pocketbase /opt/pocketbase/pb_hooks/
   systemctl restart pocketbase
   ```
4. **Generate activation codes** via PocketBase admin or script

### Offsite Backups
The setup script includes a Backblaze B2 backup system note. Hourly SQLite backup + daily compressed SQL dump uploaded to B2. Configure credentials manually.

### Uptime Monitoring
Set up UptimeRobot, BetterUptime, or PingPong pointing at `https://api.yourdomain.com/_/health`.

---

## 15. Security Model

### Certificate Pinning
```go
heartbeat.SetCertPin("sha256_of_server_tls_public_key")
```
- Client verifies hub's TLS certificate by hashing the server's public key
- On mismatch: connection rejected, heartbeat fails, grace period begins
- Defeats school SSL inspection proxies that present their own CA-signed certs
- In dev mode: leave empty to skip verification

### Hardware-Bound Activation
- Device identity is SHA256(MAC|disk_serial|motherboard_serial)
- Survives app reinstallation (same hardware = same identity)
- Cannot be transferred to another device
- Server-side suspension preserves binding

### Helper IPC Authentication
- Shared 32-byte random secret generated at service install
- Written to file readable only by user + SYSTEM (root)
- Every IPC command includes the secret
- Service rejects commands with invalid auth
- Defense-in-depth: caller PID verification (future)

### Process Disguise
- Engine binaries renamed: `hysteria.exe` → `speedmode.exe`
- argv[0] set to innocuous name
- Windows: `HideWindow=true` to suppress console window
- No UPX packing (triggers AV)
- Submit renamed binaries to AV vendors

### Telemetry Privacy
- Only operational data: uptime, health status, version, protocol ID
- No browsing history, destination IPs, DNS queries recorded
- Opt-out toggle in Settings
- Documented in privacy notice

### Rate Limiting
| Layer | Mechanism | Limit |
|-------|-----------|-------|
| Caddy | rate_limit module | 5/10min activation, 1/10s heartbeat |
| PocketBase JS hook | activation_attempts collection | 5/10min per IP |

---

## 16. What's Implemented vs What's Stubbed

### Fully Implemented
- ✅ Activation code validation (Luhn-mod-N client + server)
- ✅ Hardware fingerprint collection (Windows + macOS)
- ✅ Storage persistence with JSON
- ✅ Log rotation (10MB rotate, keep 3)
- ✅ Branding/obfuscation layer
- ✅ Engine process lifecycle management
- ✅ Heartbeat loop with cert pinning
- ✅ Grace period (7 days, overrides token expiry)
- ✅ Token rotation (new token each heartbeat)
- ✅ Bandwidth probe (1MB download + tier clamping)
- ✅ Background bandwidth monitor (EWMA + bufferbloat detection)
- ✅ Health checker (15s tunnel probes)
- ✅ TUN device setup (Linux via ip tuntap)
- ✅ Kill switch (iptables/pf/netsh)
- ✅ DNS guard
- ✅ SHA256 update verification
- ✅ Two-phase sentinel rollback
- ✅ Fyne GUI (activation + main window)
- ✅ Black+purple custom theme
- ✅ All server-side PocketBase hooks
- ✅ Caddy configuration with rate limiting
- ✅ GitHub Actions CI/CD
- ✅ Makefile cross-compile targets
- ✅ Server deployment script
- ✅ Tests (activation Luhn + branding)

### Stubbed / Needs Work
- ⚠️ **Helper service actions** (`service_windows.go`, `service_darwin.go`): The IPC protocol and auth are implemented, but the actual TUN creation/destruction and firewall rules in the helper service are stubs. Need real wintun/utun driver calls.
- ⚠️ **Process sandboxing** (`sandbox()` in process.go): No-op currently. Needs Windows Job Objects and macOS Seatbelt profiles.
- ⚠️ **Binary disguise on non-Windows**: `disguiseProcess` on non-Windows is a no-op. macOS/Linux may need argv[0] manipulation.
- ⚠️ **macOS TUN** (`tun_darwin.go`): Uses helper IPC but helper is stubbed. Falls back to Linux-style if needed.
- ⚠️ **Engine bundling**: Hysteria 2 binary is not bundled in the installer yet. Needs a script or build step.
- ⚠️ **Installer**: No Windows installer (MSI) or macOS DMG. Currently distributes raw binary.
- ⚠️ **usque/Warp engine support**: `buildCommand` in manager has a usque case but it's untested.
- ⚠️ **Fyne threading**: UI updates from goroutines may cause warnings. Production version should use channels.

---

## 17. Known Issues & TODOs

### Issues
1. **Fyne UI from goroutines**: `widget.SetText()` and `canvas.Refresh()` called from background goroutines in `statusLoop()`, `connect()`, `disconnect()`. Works in practice but violates Fyne's single-goroutine model. Fix: use `canvas.Refresh()` properly or route all UI updates through a channel.

2. **Double app.New() in activation prompt**: The old `ShowActivationPrompt` created a separate `app.New()`. This was fixed in the current code (single `app.New()` in `Run()`), but the old approach is in git history. Always use `gui.Run()` as the single entry point.

3. **go.sum missing**: Must run `go mod tidy` before first build. This resolves Fyne's ~20 transitive dependencies.

4. **CGO cross-compilation**: Building Windows binaries on Linux requires `mingw-w64`. Building macOS on Linux requires `osxcross`. The Makefile assumes native builds or GitHub Actions runners.

### TODOs
- [ ] **Fill in helper service actions**: Implement `create_tun`, `destroy_tun`, `block_all`, `unblock_all` in `service_windows.go` and `service_darwin.go`
- [ ] **Bundle Hysteria 2 binary**: Add a script to download the Hysteria 2 release binary and include it in the installer
- [ ] **Create Windows installer**: Use NSIS or WiX to create an MSI that installs the helper service, bundles Hysteria 2, and creates shortcuts
- [ ] **Create macOS DMG**: Package the app as a .app bundle with launchd plist for the helper daemon
- [ ] **Add sandboxing**: Windows Job Objects, macOS Seatbelt profiles
- [ ] **Submit to AV vendors**: Submit renamed binaries to Microsoft Defender, etc. to reduce false positives
- [ ] **Build version into binary**: The `-X main.version=` ldflag is set in Makefile but needs the version injected by CI
- [ ] **Add grace days display in GUI**: Show remaining grace days when in grace mode
- [ ] **Implement split tunnel** (Phase 2): RFC 1918 exclusions, user-configurable routes
- [ ] **Add VLESS-REALITY support** (Phase 2): Additional protocol engine for deeper obfuscation
- [ ] **Systray minimize** (Phase 2): Minimize to system tray instead of closing

---

## 18. Business Context Quick Reference

### Market
- School/college students bypassing WiFi restrictions
- 50-500 users per school
- $2-13/month per user
- Breakeven at 10-20 paying users

### Distribution
- **Middleman network:** Middlemen hand out physical activation codes for cash
- Middlemen have NO app access, NO admin panel, NO dashboard
- Middlemen take ~20-30% commission
- Codes generated via PocketBase admin, printed on paper

### Competition
- **X-VPN (xVPN):** Currently most-used at the school, Everest protocol becoming detectable
- **Free VPNs:** Psiphon, Lantern, Hotspot Shield — blocked by school IT, useless for gaming
- **Differentiator:** Hysteria 2 over QUIC (works on lossy WiFi), TUN mode (games work), kill switch, hardware binding

### Ethical Notes
- The product is intentionally closed-source
- Real protocol names are hidden from users (deceptive naming)
- The business model acknowledges it operates in a gray area
- Blocking research (comprehensive-vpn-blocklist.md) exists for competitive intelligence

---

> **Last words for future agents:**
> 1. Read this file FIRST before making any changes.
> 2. The memory system has `vpn-project-features` (high-level) and `vpn-core-logic` (deep architecture) for quick recall.
> 3. When in Plan mode, explore and propose before editing.
> 4. The most likely next work items are filling the helper service stubs and creating installers.
> 5. Test on Linux first (TUN works via `ip tuntap`), then deploy to Windows build via GitHub Actions.
> 6. Always keep the branding layer in mind — never expose real protocol names in the UI.
> 7. The 7-day grace period is the most important reliability feature — protect it.
> 8. If something seems missing or broken, check the `internal/storage/storage.go` data model first — most features depend on it.
