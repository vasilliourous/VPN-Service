# MyVPN Wails Migration Plan

> **Replacing the Fyne GUI with a Wails (Go + Vue 3) desktop app.**
> All `internal/` packages remain untouched. The Fyne code is retired.
> Only the GUI layer changes.

---

## Why Wails?

| Problem | Fyne (current) | Wails (target) |
|---------|---------------|-----------------|
| **CGO requirement** | Mandatory (OpenGL) | Only for webview embed (tiny surface area) |
| **Cross-compilation** | Needs MinGW/osxcross | Wails handles it |
| **GUI reliability** | Widget set bugs, rendering glitches | HTML/CSS/Vue 3 — bulletproof |
| **Terminal window** (Windows) | Fyne opens conhost | Wails hides it by default |
| **System tray** | Buggy per-platform | Native, reliable |
| **Resizing/scaling** | Poor | CSS handles it perfectly |
| **Question marks** | Font/rune issues in Fyne | Unicode in web = trivial |
| **3 binaries** | myvpn + helper + sing-box | **1 binary** — Wails bundles everything, no helper needed |
| **Elevation** | Separate helper binary + IPC | Wails handles via native dialogs |

---

## Architecture (Target)

```
┌──────────────────────────────────────────────────────┐
│              myvpn (single binary)                      │
│                                                          │
│  ┌────────────────────────────────────────────────┐   │
│  │         Wails Go Backend (app.go)                │   │
│  │                                                  │   │
│  │  ┌──────────────────────────────────────────┐   │   │
│  │  │  Internal Packages (UNCHANGED)            │   │   │
│  │  │  • activation  • heartbeat  • manager     │   │   │
│  │  │  • storage     • updater    • tunnel       │   │   │
│  │  └──────────────────────────────────────────┘   │   │
│  │                                                  │   │
│  │  Exposed Methods (via Wails Bind):               │   │
│  │  Activate(), Connect(), Disconnect(),            │   │
│  │  GetStatus(), GetDiagnostics(), CheckUpdate()    │   │
│  └──────────────────┬─────────────────────────────┘   │
│                     │ Wails IPC                        │
│  ┌──────────────────┴─────────────────────────────┐   │
│  │         Vue 3 + TypeScript Frontend              │   │
│  │                                                  │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────────┐   │   │
│  │  │Activation│  │  Main    │  │ Diagnostics   │   │   │
│  │  │ Screen   │  │  Screen  │  │ Panel         │   │   │
│  │  └──────────┘  └──────────┘  └──────────────┘   │   │
│  │                                                  │   │
│  │  System tray (native, built into Wails)          │   │
│  └──────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────┘
                         │
                         │ sing-box (spawned as subprocess)
                         ▼
              ┌──────────────────────┐
              │  Shadowsocks tunnel   │
              │  to VPS               │
              └──────────────────────┘
```

---

## Project Structure (Target)

```
v5/client/
├── main.go                 ← Wails app entry (replaces old cmd/myvpn/main.go)
├── app.go                  ← App struct with @Bind methods
├── wails.json              ← Wails project configuration
├── go.mod                  ← Updated deps (Wails instead of Fyne)
├── Makefile                ← Updated build targets
│
├── internal/               ← COMPLETELY UNCHANGED
│   ├── activation/         ← Same code, same API
│   ├── heartbeat/          ← Same code, same API
│   ├── manager/            ← Same code, same API
│   ├── storage/            ← Same code, same API
│   ├── tunnel/             ← Same code, same API
│   ├── updater/            ← Same code, same API
│   ├── gui/                ← RETAINED but dead (Fyne reference, not compiled)
│   └── helper/             ← RETAINED but dead (no longer needed)
│
├── frontend/               ← NEW: Vue 3 + Vite + TypeScript
│   ├── index.html
│   ├── src/
│   │   ├── main.ts
│   │   ├── App.vue
│   │   ├── components/
│   │   │   ├── ActivationScreen.vue
│   │   │   ├── MainScreen.vue
│   │   │   ├── StatusIndicator.vue
│   │   │   ├── TierBadge.vue
│   │   │   └── DiagnosticsPanel.vue
│   │   ├── stores/
│   │   │   └── vpn.ts          ← Reactive state (Pinia or composable)
│   │   ├── types/
│   │   │   └── index.ts        ← TypeScript interfaces matching Go types
│   │   ├── assets/
│   │   │   └── styles.css      ← Dark theme, responsive layout
│   │   └── lib/
│   │       └── bridge.ts       ← Typed Wails bindings
│   ├── package.json
│   ├── tsconfig.json
│   └── vite.config.ts
│
└── build/                  ← Wails build assets (icons, etc.)
    └── appicon.png
```

---

## Implementation Phases

### Phase 1: Project Scaffold (this PR)

| Step | File(s) | What |
|------|---------|------|
| 1.1 | `go.mod` | Replace Fyne + helper deps with `github.com/wailsapp/wails/v2` |
| 1.2 | `wails.json` | Project name, version, build config |
| 1.3 | `main.go` | Wails app entry (startup, shutdown, bind App) |
| 1.4 | `app.go` | App struct with all bound methods wrapping internal packages |
| 1.5 | `frontend/` scaffold | `package.json`, `vite.config.ts`, `tsconfig.json`, `index.html` |
| 1.6 | `Makefile` | `wails build`, `wails dev` targets |

### Phase 2: Go Backend Bindings

| Step | Method | Wraps |
|------|--------|-------|
| 2.1 | `App.Activate(code)` | `activation.Client.Activate()` + `storage.SetActivation()` |
| 2.2 | `App.Connect()` | `manager.Start()` with config from `storage` |
| 2.3 | `App.Disconnect()` | `manager.Stop()` + `heartbeat.Stop()` |
| 2.4 | `App.GetStatus()` | `manager.State()` + `heartbeat.Failures()` + `heartbeat.RemainingGracePeriod()` |
| 2.5 | `App.GetDiagnostics()` | Collect system info, storage state, versions |
| 2.6 | `App.CheckUpdate()` | `heartbeat.DoHeartbeat()` → check for update signal |
| 2.7 | `App.ApplyUpdate(info)` | `updater.DownloadAndVerify()` + `updater.ApplyUpdate()` |
| 2.8 | `App.GetCodeFormat()` | Return `CodeCharset`, `CodePrefix` for frontend validation |

### Phase 3: Vue 3 Frontend

| Step | Component | What |
|------|-----------|------|
| 3.1 | `types/index.ts` | TS interfaces matching Go structs |
| 3.2 | `lib/bridge.ts` | Typed wrapper around `window.runtime` and Wails Bind calls |
| 3.3 | `stores/vpn.ts` | Reactive state: connection status, tier, timer, diagnostics |
| 3.4 | `ActivationScreen.vue` | Code input, client-side Luhn check, "Activate" button, error display |
| 3.5 | `MainScreen.vue` | Connect/Disconnect button, status dot, timer, tier badge, speed |
| 3.6 | `StatusIndicator.vue` | Green/grey/red dot + label (Connected/Disconnected/Error) |
| 3.7 | `TierBadge.vue` | Eco (grey), Stealth (purple), Strike (gold) with icon |
| 3.8 | `DiagnosticsPanel.vue` | Copy-to-clipboard support report |
| 3.9 | `App.vue` | Root: routing between activation and main screens, system tray setup |
| 3.10 | `styles.css` | Dark theme (#0D0D0F background, #A855F7 accent), responsive layout |

### Phase 4: Polish & Testing

| Step | What |
|------|------|
| 4.1 | System tray: minimize on close, context menu (Show/Connect/Quit) |
| 4.2 | Update UX: progress bar during download, "Update available" notification |
| 4.3 | Grace period: countdown display, "tap to retry" when expired |
| 4.4 | Error handling: toast notifications for heartbeat failures, connection errors |
| 4.5 | Windows: no terminal window, proper taskbar icon |
| 4.6 | macOS: menu bar integration, template icon |
| 4.7 | Linux: `.desktop` file, system tray indicator |

---

## Go Backend API (Wails Bind Methods)

These are the methods exposed to the Vue frontend via `wails.Run()` `Bind` option:

```go
type App struct {
    store     *storage.Store
    activator *activation.Client
    mgr       *manager.Manager
    hb        *heartbeat.Heartbeat
    up        *updater.Updater
    version   string
    hubURL    string
    ctx       context.Context
}

// ── Lifecycle ──
func (a *App) Startup(ctx context.Context)        // Wails lifecycle hook
func (a *App) Shutdown(ctx context.Context)         // Wails lifecycle hook

// ── Activation ──
func (a *App) ValidateCodeLocally(code string) *ValidationResult  // Luhn-mod-N, no server call
func (a *App) Activate(code string) *ActivationResult             // Server activation
func (a *App) IsActivated() bool                                   // Check stored state

// ── Connection ──
func (a *App) Connect() *OpResult       // Start sing-box
func (a *App) Disconnect() *OpResult    // Stop sing-box
func (a *App) GetStatus() *Status       // Current connection state

// ── Heartbeat ──
func (a *App) StartHeartbeat()           // Begin periodic heartbeats
func (a *App) StopHeartbeat()            // Stop heartbeats
func (a *App) GetRemainingGraceDays() int

// ── Updates ──
func (a *App) CheckForUpdate() *UpdateCheckResult
func (a *App) ApplyUpdate(info UpdateInfo) *OpResult

// ── Diagnostics ──
func (a *App) GetDiagnostics() string    // Text report for support

// ── Codec ──
func (a *App) GetCodeCharset() string    // "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
func (a *App) GetCodePrefix() string     // "MYVPN"
```

All methods use Go native types (not Wails-specific wrappers). Wails automatically
serializes them to JSON for the frontend.

---

## Build & Run

### Prerequisites

```bash
# 1. Install Go 1.22+
# 2. Install Node.js 18+ (for the Vue frontend)
# 3. Install Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# 4. Linux only: GTK + WebKit dev headers
sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev

# 5. Install frontend dependencies
cd v5/client/frontend && npm install
```

### Development (hot-reload)

```bash
cd v5/client
wails dev
```

This starts the Go backend and the Vite dev server. Changes to Go or Vue files
auto-reload. The backend binds `App` methods and the Vue frontend calls them.
`wails dev` compiles the stub assets (no `frontend/dist` needed) and loads the
UI from the Vite dev server — so it works on a fresh checkout.

### Production Build

```bash
cd v5/client
wails build -tags frontend
```

Produces a single native binary (`build/bin/myvpn`). The binary bundles the Vue
frontend as embedded assets. Ship with `sing-box` alongside it.

### Cross-Compilation

```bash
cd v5/client
GOOS=linux GOARCH=amd64 wails build -tags frontend -o dist/myvpn-linux
GOOS=windows GOARCH=amd64 wails build -tags frontend -o dist/myvpn.exe
GOOS=darwin GOARCH=arm64 wails build -tags frontend -o dist/myvpn-darwin-arm64
```

Wails handles the cross-compilation. Ensure the target platform's webview is
available (WebView2 on Windows, WebKit on macOS/Linux).

### Build Tags (embed vs stub)

The UI is embedded via the `frontend` build tag (see `assets_embed.go` /
`assets_stub.go` in `v5/client/`):

| Command | What it produces |
|---------|------------------|
| `go build -tags frontend .` | Full binary WITH embedded UI (requires `frontend/dist` to exist) |
| `wails build -tags frontend` | Same, via Wails CLI (also builds the frontend itself) |
| `go build .` | Compiles WITHOUT UI assets (stub FS — useful for headless checks) |
| `wails dev` | Hot-reload — loads from the Vite dev server (stub is fine) |

If you add a new job that compiles Go, build the frontend first and pass
`-tags frontend`, otherwise the app compiles with an empty asset FS.

---

## Current Project State (post-migration)

```
v5/client/
├── main.go                 ← Wails entry (replaces old cmd/myvpn/main.go)
├── app.go                  ← App struct with bound methods for Vue frontend
├── wails.json              ← Wails project configuration
├── go.mod                  ← Wails v2 dep (Fyne removed)
├── Makefile                ← Build targets for dev/build/cross-compile
│
├── internal/               ← UNCHANGED — stable, tested, documented
│   ├── activation/         ← Luhn codes, fingerprinting, server API
│   ├── heartbeat/          ← Periodic health checks, grace period
│   ├── manager/            ← sing-box process + config generation
│   ├── storage/            ← JSON persistence, atomic writes
│   ├── tunnel/             ← TUN fallback (retained for reference)
│   └── updater/            ← Two-phase crash-safe updates
│
├── _legacy note: the old Fyne code was moved OUT of the module to v5/legacy/
│   (gui/, helper/, cmd/myvpn/) so go vet / go mod tidy / go build ./... never see it
│
├── frontend/               ← Vue 3 + TypeScript + Vite
│   ├── index.html
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   └── src/
│       ├── main.ts                 ← Vue app entry
│       ├── App.vue                 ← Root component (activation/main routing)
│       ├── env.d.ts                ← Wails runtime type declarations
│       ├── types/index.ts          ← TypeScript interfaces matching Go types
│       ├── lib/bridge.ts           ← Typed Wails bridge wrappers
│       ├── stores/vpn.ts           ← Reactive state (composable)
│       ├── assets/icon.svg         ← App icon source
│       ├── assets/styles.css       ← Dark theme CSS
│       └── components/
│           ├── ActivationScreen.vue  ← Code entry + validation + activation
│           ├── MainScreen.vue        ← Connect/disconnect + status + diagnostics
│           ├── StatusIndicator.vue   ← Green/grey/red dot
│           └── TierBadge.vue         ← Eco/Stealth/Strike badge
│
├── build/
│   └── README.md           ← Icon build instructions
│
└── engines/README.md       ← sing-box binary placeholder
```

### Key Changes

| Before (Fyne) | After (Wails) |
|---------------|---------------|
| `cmd/myvpn/main.go` | `main.go` (package root) |
| Fyne GUI in `internal/gui/` | Vue 3 frontend in `frontend/` |
| 3 binaries + helper IPC | 1 binary (Wails bundles frontend) |
| Fyne v2 dependency | Wails v2 dependency |
| CGO required for OpenGL | Webview embed (minimal CGO) |
| Terminal popup on Windows | No terminal window |
| Manual cross-compile complexity | `GOOS= GOARCH= wails build` |

---

## What Goes Away

| Component | Reason |
|-----------|--------|
| `internal/gui/` (Fyne) | Moved to `v5/legacy/gui/` — outside the Go module so builds stay Fyne-free |
| `internal/helper/` (separate binary) | Moved to `v5/legacy/helper/` — Wails handles elevation; no more helper IPC |
| `cmd/myvpn/main.go` (old) | Moved to `v5/legacy/cmd/myvpn/` — replaced by `main.go` at root |
| `myvpn-helper` binary | No longer needed |
| Fyne dependency | Removed from `go.mod` — `go mod tidy` never re-adds it (legacy code is outside the module) |
| 3-binary bundle | Down to 1 binary (`myvpn`) + `sing-box` (still separate but bundled in zip) |

> **sing-box remains a separate binary** downloaded alongside the app. The manager
> package spawns it as a subprocess. This is unchanged.

---

## Rollback Plan

If the Wails migration breaks:

1. **Go backend code** — totally unchanged. The `internal/` packages are untouched.
2. **To revert:** Restore `main.go` from the old `v5/legacy/cmd/myvpn/main.go`, revert `go.mod` to Fyne deps, and move the Fyne code back from `v5/legacy/`. The `internal/` packages work identically with either GUI.
3. **Storage format** — unchanged JSON schema. The Wails version reads/writes the same `storage.json` as the Fyne version.
