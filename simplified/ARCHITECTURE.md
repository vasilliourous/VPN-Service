# Architecture

## System Overview

```
┌─────────────────────────────────────────────┐
│              STUDENT'S LAPTOP                 │
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │         MyVPN Desktop App (Go+Fyne)    │   │
│  │                                         │   │
│  │  Activation → Get Config → Connect      │   │
│  │                     ↓                   │   │
│  │  ┌─────────────────────────────────┐    │   │
│  │  │  Hysteria 2 Client (--tun)       │    │   │
│  │  │  • QUIC + TLS 1.3               │    │   │
│  │  │  • Built-in TUN device           │    │   │
│  │  │  • Salamander obfuscation        │    │   │
│  │  │  • Brutal/standard CC            │    │   │
│  │  └─────────────────────────────────┘    │   │
│  └───────────────────────────────────────┘   │
│                     │                         │
│              UDP :443 (QUIC)                  │
└─────────────────────┼─────────────────────────┘
                      │
                      ▼
              ┌───────────────┐
              │   INTERNET    │
              │  (school WiFi) │
              └───────┬───────┘
                      │
                      ▼
┌─────────────────────────────────────────────┐
│              YOUR VPS (Ubuntu 22.04)         │
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │   Caddy (reverse proxy, TLS, rate      │   │
│  │   limit, auto LE certificates)         │   │
│  └──────────────┬────────────────────────┘   │
│                 │                              │
│  ┌──────────────┴────────────────────────┐   │
│  │  PocketBase (Go-based backend/SQLite)  │   │
│  │                                       │   │
│  │  Collections:                         │   │
│  │  ┌──────────┐ ┌──────────────┐       │   │
│  │  │ codes    │  │ tier_configs │       │   │
│  │  │ ├ code   │  │ ├ tier       │       │   │
│  │  │ ├ tier   │  │ └ config_json│       │   │
│  │  │ ├ used   │  └──────────────┘       │   │
│  │  │ ├ suspended│                       │   │
│  │  │ └ created │                        │   │
│  │  └──────────┘                         │   │
│  │                                       │   │
│  │  JS Hooks:                            │   │
│  │  • activation.pb.js — validate + bind │   │
│  │  • suspension.pb.js — check on connect│   │
│  └───────────────────────────────────────┘   │
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │  Hysteria 2 Server (multiuser)        │   │
│  │  ├── stealth pool (standard CC)       │   │
│  │  └── strike pool (Brutal CC)          │   │
│  └───────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

## Device Fingerprinting (Educational Infrastructure)

> This feature exists primarily as infrastructure for a parallel educational
> project about endpoint tracking and spyware capabilities. In production,
> it enables permanent device binding and abuse prevention.

### How It Works

On activation, the app collects a hardware fingerprint:

```
fingerprint = SHA256(MAC_address + disk_serial + motherboard_UUID)
```

The fingerprint is sent as an HTTP header alongside the activation code.
The server stores it in the `codes` record, creating a permanent binding.

### What's Collected

| Source | Windows | macOS |
|--------|---------|-------|
| MAC address | `getmac` command output | `ifconfig en0` ether |
| Disk serial | `wmic diskdrive get serialnumber` | IOKit `kIOPropertySerialKey` |
| Motherboard UUID | `wmic csproduct get uuid` | IOKit `kIOPlatformUUIDKey` |

All three are concatenated and SHA256-hashed. The raw values are never
stored or transmitted — only the hash leaves the device.

### Binding Behavior

- **First activation:** Fingerprint is stored alongside the code. One code =
  one device.
- **Reinstall on same device:** Same fingerprint → same code still works
  (server allows if fingerprint matches).
- **Code sharing:** If a different device tries to use the code, the server
  rejects it with "Code already bound to another device."
- **Suspension:** Admin can suspend a code in PocketBase, which blocks
  connections regardless of fingerprint.

### Why Include This for 20 Users?

It's overkill for 20 friends at a school. It's included because:
1. **Educational value** — this is infrastructure for a parallel project
   about how spyware and tracking works.
2. **Future-proofing** — if the service grows beyond 20 users, device
   binding prevents code sharing abuse.
3. **It's ~60 lines of Go** across 3 files (fingerprint.go + 2 platform
   files). Minimal maintenance burden.

---

## Data Flow

### Activation (one-time per app)

```
App: POST /api/collections/codes/records
     Headers: X-Device-Fingerprint: <SHA256 hash>
     Body: { "code": "XXXX-XXXX-XXXX" }

Server: PocketBase validates:
  1. Code exists in 'codes' collection?
  2. Is 'used' = false?  (OR used=true but fingerprint matches → allow reinstall)
  3. Is 'suspended' = false?

If OK → marks code as used → stores fingerprint → returns full Hysteria 2
         config JSON (including auth token, server address, tier params)

App: saves config to local JSON storage → ready to connect
```

### Connection (every session)

```
App: reads saved config from storage
  → if tier == "strike":
      → run bandwidth probe (download 500KB from VPS, measure throughput)
      → set Brutal target to 80% of measured throughput (headroom)
      → update config with measured Brutal target
  → builds command:
    hysteria2 client -c config.json --tun [--speed <cap>]
  → Hysteria 2 handles everything:
     • Creates TUN device (admin prompt on first run)
     • Routes all traffic through tunnel
     • Handles DNS
     • Applies bandwidth cap / Brutal CC as configured
  → App updates GUI: "Connected"

On disconnect:
  → App kills Hysteria 2 process
  → Hysteria 2 cleans up TUN device automatically
  → GUI: "Disconnected"
```

### Suspension Check (every connection attempt)

```
App: POST /api/check-suspension
     { "code": "XXXX-XXXX-XXXX" }

Server: checks if code is suspended in PocketBase
  → returns { "ok": true } or { "ok": false, "reason": "suspended" }

App: if suspended, shows "Account suspended — contact your middleman"
     and blocks connection
```

### Code Reuse (if app is reinstalled)

```
Since there's no hardware fingerprint binding:
  → If a student reinstalls, they just re-enter their code
  → Server sees code is already 'used' = true
  → Server checks: is this code currently suspended? No? → allow
  → Return config again

NOTE: This means codes can be shared. For 20 users at one school,
that's manageable. If abuse happens, suspend the code and generate
a new one for the legitimate user.
```

## Connection State Machine

Simplified to 3 states (no grace, no degraded, no fallback):

```
  ┌──────────┐
  │  IDLE    │ ←── App starts, no activation
  └────┬─────┘
       │ activation successful
       ▼
  ┌──────────┐
  │  READY   │ ←── Code validated, config saved
  └────┬─────┘
       │ user clicks Connect
       ▼
  ┌──────────────┐
  │  CONNECTING  │ ←── Launching Hysteria 2
  └────┬─────────┘
       │ engine reports success
       ▼
  ┌─────────────┐     ┌────────────────┐
  │  CONNECTED  │ ←── │  DISCONNECTED  │ ←── engine exits / user clicks Disconnect
  └─────────────┘     └────────────────┘
       │                     │
       └─── user clicks ─────┘
            Disconnect → goes to READY (can reconnect)

Engine crash → goes to DISCONNECTED → user taps "Retry"
```

## The Three Config Profiles

### Eco (standard CC, throttled)

```json
{
  "server": "vps.yourdomain.com:443",
  "auth": "eco-pool-auth-token",
  "tls": {
    "sni": "www.bing.com",
    "insecure": false
  },
  "obfs": "salamander",
  "obfs-password": "eco-obfs-key",
  "recv_window_conn": 1048576,
  "recv_window": 4194304,
  "disable_mtu_discovery": true,
  "speed": 5000000
}
```

- Standard CC (no brutald) → bufferbloat under load
- Small recv_window → limits throughput further, causes buffering on video
- 5 Mbps cap → enough for text messaging, basic web, 360p YouTube
- Experience: intentionally frustrating. If they pay $2 more for Stealth, it feels 10× faster.

### Stealth (standard CC)

```json
{
  "server": "vps.yourdomain.com:443",
  "auth": "stealth-pool-auth-token",
  "tls": {
    "sni": "www.bing.com",
    "insecure": false
  },
  "obfs": "salamander",
  "obfs-password": "stealth-obfs-key",
  "recv_window_conn": 4194304,
  "recv_window": 33554432,
  "disable_mtu_discovery": true,
  "speed": 48000000
}
```

- Standard CC (no brutald configured) → bufferbloat under load, higher latency
- Large recv_window → good throughput for streaming
- 48 Mbps cap → fast enough for 4K streaming, slow enough to feel "capped" vs Strike

### Strike (Brutal CC)

```json
{
  "server": "vps.yourdomain.com:443",
  "auth": "strike-pool-auth-token",
  "tls": {
    "sni": "www.bing.com",
    "insecure": false
  },
  "obfs": "salamander",
  "obfs-password": "strike-obfs-key",
  "brutal": {
    "enabled": true,
    "up_mbps": 50,
    "down_mbps": 50
  },
  "recv_window_conn": 16777216,
  "recv_window": 67108864,
  "disable_mtu_discovery": true,
  "hop-interval": 10
}
```

- Brutal CC enabled → stable low latency under congestion
- Large recv_window + hop-interval 10s → optimal for gaming traffic patterns
- 50 Mbps target → Brutal aims for this, adapts down if congested

### Why Only a Single Probe (No Ongoing Adaptation)

V2 had an adaptive loop that monitored health-check RTT every 15s and
adjusted Brutal target mid-session. That's gone. Here's what's left:

- **One probe at connect time** — download 500KB from VPS, measure
  throughput, set Brutal target to 80% of that. Takes ~1 second.
- **No ongoing adaptation** — once Brutal target is set, Hysteria 2's
  Brutal CC handles momentary congestion changes on its own.

**Why this works for 20 users:**

| Scenario | How Brutal Handles It |
|----------|----------------------|
| WiFi is 50 Mbps at lunch start | Probe measures ~50 Mbps, target = 40 Mbps. Smooth gaming. |
| WiFi drops to 10 Mbps mid-session | Brutal CC automatically reduces send rate to avoid packet loss. Gaming stays playable. |
| Lunch ends, WiFi back to 50 Mbps | Brutal CC ramps back up to target. |
| User moves to a different part of school | Brutal CC adapts within seconds. No probe needed again. |

**What the probe prevents:** Starting with Brutal target = 50 Mbps on a
10 Mbps link would cause bufferbloat immediately. The probe sets a
realistic starting target so Brutal doesn't fight a losing battle.

**Code footprint:** ~30 lines in a single `internal/probe/probe.go` file.


## Why Hysteria 2 Only (No usque/Warp)

| Reason | Detail |
|--------|--------|
| Complexity | usque/Warp is a whole second engine to manage, download, configure |
| Reliability | Hysteria 2 works on all three platforms and is actively maintained |
| Blocking risk | If Hysteria 2 gets blocked, change obfs/SNI/port — no need for a fallback |
| Bundle size | One engine binary instead of two |
| For 20 users | If it's blocked for a day, you push a new config. No one dies. |

## Storage (Local)

Single JSON file at standard app data path:

```json
{
  "version": 1,
  "code": "XXXX-XXXX-XXXX",
  "tier": "strike",
  "device_fingerprint": "sha256hashhere",
  "config": { ... },
  "activated": true
}
```

Path per platform:
- Windows: `%APPDATA%/MyVPN/data.json`
- macOS: `~/Library/Application Support/MyVPN/data.json`
- Linux: `~/.local/share/MyVPN/data.json`

No encryption. No engine cache. No protocol lists. No telemetry opt-out. 6 fields.

## GUI Layout

### Activation Window

```
┌─────────────────────────────┐
│       Activate MyVPN         │
│                               │
│  Enter your activation code   │
│  ┌───────────────────────┐   │
│  │ XXXX-XXXX-XXXX        │   │
│  └───────────────────────┘   │
│                               │
│  [Status: ready]              │
│                               │
│  [     Activate      ]        │
└─────────────────────────────┘
```

### Main Window (after activation)

```
┌─────────────────────────────┐
│  ●●●●●○○○○                    │
│                               │
│           STRIKE              │
│        Gaming Plan            │
│                               │
│          00:12:34             │
│                               │
│  ═══════════════════════════  │
│                               │
│  Speed: 42 Mbps               │
│                               │
│  [    Disconnect    ]         │
│                               │
│  ═══════════════════════════  │
│                               │
│  Code: XXXX-****-XXXX         │
│                               │
│  Settings ▸                   │
│    └ Send usage data [✓]     │
└─────────────────────────────┘
```

## API Endpoints

### `POST /api/collections/codes/records` (PocketBase auto-generated)
Activation. Request body: `{ "code": "XXXX-XXXX-XXXX" }`.
JS hook intercepts, validates, marks used, returns config.

### `POST /api/check-suspension` (custom)
Suspension check. Request body: `{ "code": "XXXX-XXXX-XXXX" }`.
Response: `{ "ok": true }` or `{ "ok": false, "reason": "..." }`.

That's all the API you need. Two endpoints.

## PocketBase Collections

### `codes`

| Field | Type | Notes |
|-------|------|-------|
| `code` | text (unique) | e.g. `A3X9-K7M2-R4P1` |
| `tier` | text | `stealth` or `strike` |
| `used` | bool | Marked true on first activation |
| `suspended` | bool | Admin sets to block a user |
| `middleman` | text (optional) | Who sold this code |
| `created` | auto date | |

### `tier_configs`

| Field | Type | Notes |
|-------|------|-------|
| `tier` | text (unique) | `stealth` or `strike` |
| `config_json` | json (raw) | Full Hysteria 2 client config |
| `active` | bool | Only active configs are returned |
| `updated` | auto date | |

## Update Distribution (Binaries + update.json)

The app binaries and `update.json` are served as static files from the VPS
via Caddy. This is not a full website — just files on disk.

```
/var/www/html/
├── myvpn-windows.zip       ← pushed by agent on update
├── myvpn-macos-intel.zip
├── myvpn-macos-arm.zip
└── update.json             ← checked by the auto-updater
```

### `update.json` Format

```json
{
  "version": "1.1.0",
  "windows": {
    "url": "https://networkingguides.duckdns.org/myvpn-windows.zip",
    "sha256": "abc123def456..."
  },
  "macos_intel": {
    "url": "https://networkingguides.duckdns.org/myvpn-macos-intel.zip",
    "sha256": "789ghi..."
  },
  "macos_arm": {
    "url": "https://networkingguides.duckdns.org/myvpn-macos-arm.zip",
    "sha256": "..."
  }
}
```

Updated by the agent whenever a new version is built. The desktop app checks
this endpoint every 6 hours and applies updates silently.

---

## Auto-Updater (Background)

Re-added but simplified from V1. No sentinel handshake, no rollback, no
two-phase commit. Just check, download, verify, replace, restart.

### Flow

```
Every 6 hours + on app launch:
  GET /update.json
  Compare version string to current version
  If newer:
    Download zip → extract binary → verify SHA256
    Rename current binary to myvpn.prev
    Move new binary into place
    Restart app (user sees a brief flash)

On next launch:
  If myvpn.prev exists and current binary is same as last check:
    Delete myvpn.prev (update was successful)
```

### Design Decisions

- **No sentinel, no rollback:** If the update fails, the app crashes and the
  user re-downloads from the website. For 20 users this is acceptable.
- **SHA256 only:** No Ed25519 signing keys. The update.json is served over
  HTTPS from the same VPS — if an attacker can modify update.json, they
  already control the VPS and can do worse.
- **Zip bundle:** The app download includes the Hysteria 2 engine binary.
  The updater replaces the whole bundle, not just the app binary.
- **Portable:** No installers, no registry keys. Replace files in place.

### Code Footprint

```go
// internal/updater/updater.go  ~80 lines
type UpdateCheck struct {
    Version string
    Windows  *PlatformUpdate `json:"windows"`
    MacOSIntel *PlatformUpdate `json:"macos_intel"`
    MacOSARM *PlatformUpdate `json:"macos_arm"`
}
type PlatformUpdate struct {
    URL    string `json:"url"`
    SHA256 string `json:"sha256"`
}

func Check(baseURL string, currentVersion string) (*UpdateCheck, error)
func DownloadAndApply(update *PlatformUpdate) error
```

---

## Background Mode (Systray)

Instead of a foreground window, the app runs in the system tray.

### Behavior

| Action | Result |
|--------|--------|
| App launches | Starts minimized to system tray |
| Left-click tray icon | Opens mini control panel (same as current main window) |
| Right-click tray icon | Menu: Connect/Disconnect, Show Window, Settings, Quit |
| Close control panel | Hides to tray, connection stays alive |
| Quit from tray menu | Kills engine, exits app |
| Auto-start on boot | Windows: registry `Run` key. macOS: `LaunchAgent` plist. |

### Connection Indicator

The tray icon itself shows connection status:
- **Grey dot** — Disconnected / starting up
- **Green dot** — Connected and healthy
- **Red dot** — Engine crashed / error

### GUI Changes (Fyne)

```go
// In gui/app.go, replacing current Run():
//   1. Create the app normally
//   2. Set up system tray via desktop.App interface
//   3. Create control panel window (same as current main window)
//   4. Start in background, show activation prompt if needed
//   5. Launch background goroutines:
//      - Auto-update check (every 6h)
//      - (Optional) connection health ping every 60s
```

Fyne's `desktop.App` interface provides:
- `SetSystemTrayIcon(resource)` — set the tray icon
- `SetSystemTrayMenu(fyne.Menu)` — set right-click menu
- The window `Show()` / `Hide()` controls visibility

This replaces the current "launch window, block on ShowAndRun" pattern.

---

---

## UI/UX Aesthetics Plan (Post-Backend)

> **Status:** Design reference only. Implement AFTER all backend logic is working.
> The current 320-line GUI (`gui/app.go`) works functionally — this section
> describes how to make it look good.

### Design Philosophy

```
Professional ≠ complicated. Minimal ≠ boring. Dark ≠ edgy.
The app should feel like a tool, not a toy.
```

Three visual goals:
1. **One glance, one action** — the user opens the window and instantly knows
   what to do (connect/disconnect). No hunting for buttons.
2. **Status without reading** — connection state is obvious from color, icon,
   and animation. You don't need to read "Connected" to know you're connected.
3. **Tier sells itself** — the Strike tier should *look* premium compared to
   Eco, through subtle visual cues, even though the UI is identical.

### Color Palette

```
Background        #0D0D0F  (near-black, slightly warm)
Surface           #1A1A1E  (cards, panels)
Surface hover     #252529  (button hover, interactive)
Border            #2E2E35  (subtle dividers)
Text primary      #F5F5F7  (near-white)
Text secondary    #8E8E96  (labels, hints)
Accent (purple)   #A855F7  (buttons, active indicators)
Accent hover      #C084FC  (button hover)
Success (green)   #22C55E  (connected dot)
Error (red)       #EF4444  (disconnected dot, errors)
Warning (amber)   #F59E0B  (connecting, degraded)
```

### Typography

- Font: system default (SF Pro on macOS, Segoe UI on Windows, Noto on Linux)
- Sizes: 11px (labels), 13px (body), 15px (status), 20px (tier name), 28px (timer)
- No custom font loading — system fonts are faster and look native
- All text left-aligned except the timer (center) and tier badge (right)

### System Tray Icons

Three tiny PNGs (16×16 or 22×22):

| State | Icon | Visual |
|-------|------|--------|
| Disconnected | `tray_disconnected.png` | Grey circle with slash |
| Connecting | `tray_connecting.png` | Amber circle with animated spinner |
| Connected | `tray_connected.png` | Green circle with checkmark |

On macOS, the menu bar icon should be a template image (macOS tints it
automatically). On Windows, a colored icon works.

### Control Panel Window (320×440px, fixed size)

```
┌──────────────────────────────────┐
│  [icon] MyVPN           — □ ×   │
│  ──────────────────────────────── │
│                                  │
│         ◉ Connected              │  ← status row
│           00:12:34               │  ← timer, centered
│                                  │
│         ┌──────────────┐         │
│         │              │         │
│         │  DISCONNECT  │         │  ← large pill button
│         │              │         │
│         └──────────────┘         │
│                                  │
│    Speed  42 Mbps  ████████░░    │  ← subtle progress bar
│                                  │
│  ──────────────────────────────── │
│  ⚙ Settings  ▸                   │  ← expandable panel
│  ──────────────────────────────── │
│  Code: ABC1-****-GHI3            │
│  Tier: Strike ● Gaming           │
│  Version: 1.0.0                  │
└──────────────────────────────────┘
```

### Activation Window (300×260px, modal)

```
┌──────────────────────────────┐
│         [logo mark]           │  ← simple geometric logo (purple)
│         MyVPN                 │
│                               │
│  Enter your activation code   │
│  ┌────────────────────────┐   │
│  │ ABC1-DEF2-GHI3         │   │  ← clean input, centered text
│  └────────────────────────┘   │
│                               │
│  ┌────────────────────────┐   │
│  │       Activate         │   │  ← purple button, full width
│  └────────────────────────┘   │
│                               │
│  Invalid code — check & retry │  ← error in red, success in green
└──────────────────────────────┘
```

### Connect/Disconnect Button States

| State | Appearance | Text |
|-------|-----------|------|
| Idle (disconnected) | Purple pill, slight glow | Connect |
| Connecting | Amber pill, pulsing animation | Connecting... |
| Connected | Green pill, steady | Disconnect |
| Disconnecting | Amber pill, fading | Disconnecting... |
| Error | Red pill | Tap to Retry |
| Suspended | Grey pill, disabled | Contact Support |

### Animations (Keep Minimal)

Fyne doesn't have great animation APIs, so keep it simple:

| Element | Animation |
|---------|-----------|
| Status dot | No animation. Instant color change. |
| Connect button | Text changes immediately. No slide transitions. |
| Timer | Updates every second. Smooth. |
| Speed bar | Smooth width transition using `canvas.NewLinearGradient` |
| Activation → Main | Fyne's window transition. `Close()` + `Show()`. |

**Do not add:** spinning loaders, slide-in panels, particle effects, animated
logos. These look bad in Fyne and add dev time. A fast, clean UI that responds
instantly is more professional than a slow, animated one.

### The "Premium Tier" Touch

For the Strike tier, add a subtle gold accent to the tier badge:

- Strike: gold accent text (#EAB308) with a small "⚡" icon
- Stealth: purple accent text (#A855F7) with a "◉" icon  
- Eco: grey accent text (#8E8E96) with a "○" icon

No other differences between tiers. Same window, same buttons, same layout.
The only visual difference is the badge color + icon. This reinforces the
value ladder without any code complexity.

### Implementation Order

```
1. Get the backend working (activation, connection, engine, updater)
2. Apply the dark theme colors (existing branding.go, expand it)
3. Set up system tray icons (3 PNGs, SetSystemTrayIcon)
4. Layout the control panel window (widgets, spacing, order)
5. Add the speed progress bar (canvas.LinearGradient)
6. Add tier badge styling (color + icon based on tier string)
7. Polish: hover states, focus rings, window sizing
8. Test on all three platforms (Windows, macOS Intel, macOS ARM)
```

### Fyne-Specific Tips

```go
// Rounded corners on buttons
btn := widget.NewButton("Connect", nil)
btn.Importance = widget.HighImportance  // filled purple

// Custom background with gradient
bg := canvas.NewRadialGradient(
    color.RGBA{168, 85, 247, 30},   // purple at 30 alpha
    color.RGBA{13, 13, 15, 255},    // near-black
)
bg.CenterOffsetX = -0.3
bg.CenterOffsetY = -0.2

// System tray setup
if desk, ok := a.(desktop.App); ok {
    desk.SetSystemTrayIcon(resourceTrayConnectedPng)
    desk.SetSystemTrayMenu(fyne.NewMenu("MyVPN", menuItems...))
}

// Speed bar using progress bar (simple approach)
speedBar := widget.NewProgressBar()
speedBar.Min = 0
speedBar.Max = 100  // percentage of tier cap
```

For the full visual spec including mockups, pixel measurements, and all widget
configurations, see `UI-AESTHETICS.md`.

---

## File-by-File Breakdown (Target)

| File | Purpose | Est. Lines |
|------|---------|------------|
| `cmd/myvpn/main.go` | Entry, flags, init, launch GUI | 40 |
| `internal/storage/storage.go` | Read/write JSON to app data dir | 80 |
| `internal/activation/activation.go` | POST code + fingerprint to server, parse response | 60 |
| `internal/activation/fingerprint.go` | Collect hardware fingerprint (SHA256) | 15 |
| `internal/activation/fingerprint_windows.go` | Windows fingerprint sources | 50 |
| `internal/activation/fingerprint_darwin.go` | macOS fingerprint sources | 55 |
| `internal/probe/probe.go` | Download 500KB, measure throughput | 30 |
| `internal/manager/process.go` | Find engine, start/stop, inject Brutal target | 80 |
| `internal/updater/updater.go` | Check version, download, verify, replace | 80 |
| `internal/tunnel/tunnel.go` | Thin wrapper — just calls engine --tun (or fallback) | 30 |
| `internal/storage/storage.go` | Read/write JSON to app data dir | 80 |
| `internal/activation/activation.go` | POST code + fingerprint to server, parse response | 60 |
| `internal/activation/fingerprint.go` | Collect hardware fingerprint (SHA256) | 15 |
| `internal/activation/fingerprint_windows.go` | Windows fingerprint sources | 50 |
| `internal/activation/fingerprint_darwin.go` | macOS fingerprint sources | 55 |
| `internal/probe/probe.go` | Download 500KB, measure throughput | 30 |
| `internal/manager/process.go` | Start/stop engine, inject Brutal target | 80 |
| `internal/updater/updater.go` | Version check, download, SHA256 verify, replace | 80 |
| `internal/tunnel/tunnel.go` | Engine --tun wrapper (no-op preferred path) | 10 |
| `internal/tunnel/tun_linux.go` | Fallback: ip tuntap | 30 |
| `internal/tunnel/tun_windows.go` | Fallback: wintun helper | 40 |
| `internal/tunnel/tun_darwin.go` | Fallback: utun helper | 40 |
| `internal/tunnel/killswitch.go` | Block non-VPN traffic on disconnect | 25 |
| `internal/tunnel/dns.go` | Redirect DNS through tunnel | 20 |
| `internal/gui/app.go` | Systray app + activation + control panel | 320 |
| `server/Caddyfile` | Reverse proxy + static site | 40 |
| `server/pb_hooks/activation.pb.js` | Code validate + bind + return config | 60 |
| `server/pb_hooks/suspension.pb.js` | Check code status | 25 |
| `server/pb_hooks/middleman_codes.pb.js` | Self-service code generation | 40 |
| `scripts/setup_vps.sh` | One-command VPS setup | 100 |
| `scripts/generate_codes.sh` | Batch code generator via PocketBase API | 40 |
| **Total** | | **~1,260** |

## What to Delete From V1/V2 Code

```
RE-ADDED (simplified from V1):
  internal/updater/                  # Background auto-update (80 lines, no sentinel)
  internal/probe/                    # Single-download probe for Strike tier

KEPT:
  internal/activation/fingerprint.go # + platform files for device binding

DELETED:
  internal/monitor/                  # EWMA bandwidth monitor
  internal/heartbeat/                # Heartbeat + grace + telemetry
  internal/health/                   # Health checks (engine handles)
  internal/helper/                   # Privileged helper service
  internal/branding/                 # Fake names (inline if needed in manager)
  internal/manager/disguise*.go      # Process disguise
  internal/manager/sandbox*          # Sandboxing
```

## Hysteria 2 Server Setup

Run a single Hysteria 2 server with three auth pools:

```yaml
# /etc/hysteria/server.yaml
listen: :443
tls:
  cert: /etc/letsencrypt/live/networkingguides.duckdns.org/fullchain.pem
  key: /etc/letsencrypt/live/networkingguides.duckdns.org/privkey.pem
auth:
  type: password
  password:
    - eco-pool-auth-token       # ← Eco tier
    - stealth-pool-auth-token   # ← Stealth tier
    - strike-pool-auth-token    # ← Strike tier
quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520
```

All three tiers share the same server process. The `auth` field in each client
config selects the pool. Speed limits and Brutal CC are enforced **client-side**
via the config JSON — Eco's config has `"speed": 5000000`, Stealth's has
`"speed": 48000000`, and Strike's has `"brutal"` settings instead.

This means **one server process, one port, one TLS certificate** for all tiers.
The client configs delivered by PocketBase determine each user's experience.

