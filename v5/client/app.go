// Package main — MyVPN Wails Desktop App.
//
// The App struct wraps all internal/ packages and exposes a clean API
// to the Vue frontend via Wails Bind. No business logic lives here —
// it delegates to the internal packages.
//
// See docs/BACKEND-API.md for the complete API reference of each internal package.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"myvpn/internal/activation"
	"myvpn/internal/heartbeat"
	"myvpn/internal/manager"
	"myvpn/internal/storage"
	"myvpn/internal/updater"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	// Stdlib runtime used for GOOS/GOARCH in diagnostics
	goRuntime "runtime"
)

// ──────────────────────────────────────────
//  App struct — exposed to Vue frontend
// ──────────────────────────────────────────

// App is the main application object. Its exported methods are automatically
// bound by Wails and callable from the Vue frontend.
type App struct {
	ctx       context.Context
	store     *storage.Store
	activator *activation.Client
	mgr       *manager.Manager
	hb        *heartbeat.Heartbeat
	up        *updater.Updater
	version   string
	hubURL    string

	// Cached runtime state (persisted state lives in storage)
	connected bool
	tier      string
	fp        string
}

// ──────────────────────────────────────────
//  Types returned to the frontend
// ──────────────────────────────────────────

// ValidateResult is returned by ValidateCode (client-side only, no server call).
type ValidateResult struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message,omitempty"`
}

// ActivateResult is returned by Activate (server call).
type ActivateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Tier    string `json:"tier,omitempty"`
}

// StatusResult is returned by GetStatus.
type StatusResult struct {
	Connected bool   `json:"connected"`
	Tier      string `json:"tier"`
	State     string `json:"state"`     // "running", "stopped", "crashed"
	Failures  int    `json:"failures"`  // heartbeat failures
	GraceDays int    `json:"graceDays"` // remaining grace period in days
}

// OpResult is returned by Connect / Disconnect.
type OpResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// UpdateCheckResult is returned by CheckForUpdate.
type UpdateCheckResult struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// ──────────────────────────────────────────
//  Lifecycle
// ──────────────────────────────────────────

// NewApp creates the App. Called by main.go.
// version comes from the package-level var set via -ldflags "-X main.version=..."
func NewApp() *App {
	return &App{
		version: version,
		hubURL:  "https://networkingguides.duckdns.org",
	}
}

// Startup is called by Wails when the application starts.
// It initializes storage, activates the system tray, and checks for update recovery.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// ── Storage ──
	store, err := storage.New("myvpn")
	if err != nil {
		wailsruntime.LogFatal(a.ctx, "Cannot initialize storage: "+err.Error())
		return
	}
	a.store = store

	// ── Activation client ──
	a.activator = activation.NewClient(a.hubURL)

	// ── Generate fingerprint ──
	a.fp = activation.GenerateFingerprint()

	// ── Find sing-box binary ──
	singBoxPath := findSingBox()
	if singBoxPath == "" {
		wailsruntime.LogWarning(a.ctx, "sing-box binary not found — tunnel will not work")
	}

	// ── Manager (direct mode — no helper binary) ──
	tmpDir := filepath.Join(os.TempDir(), "myvpn")
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		wailsruntime.LogWarning(a.ctx, "Cannot create temp dir: "+err.Error())
	}
	configPath := filepath.Join(tmpDir, "sing-box-config.json")
	a.mgr = manager.NewManager(singBoxPath, configPath, "")
	// IMPORTANT: force direct mode. NewManager defaults to helper mode on
	// Windows, but the helper binary no longer exists — helper mode would
	// fail with "myvpn-helper binary not found".
	a.mgr.SetHelperMode(false)

	// ── Updater (crash recovery) ──
	execPath, err := os.Executable()
	if err == nil {
		appDir := filepath.Dir(execPath)
		binaryName := filepath.Base(execPath)
		a.up = updater.New(appDir, binaryName, a.version)

		// Run update recovery before anything else
		updater.CleanStaleMarkers(appDir, 48*time.Hour)
		if _, err := updater.CheckOnStartup(false); err != nil {
			wailsruntime.LogWarning(a.ctx, "Update recovery warning: "+err.Error())
		}
		if err := updater.ConfirmIfPending(appDir); err != nil {
			wailsruntime.LogWarning(a.ctx, "Update confirm warning: "+err.Error())
		}
	}

	// ── Restore state from storage ──
	state := a.store.GetData()
	a.tier = state.Tier
	if state.Activated && state.ServerConfig != nil {
		wailsruntime.LogInfo(a.ctx, "Device is activated (tier: "+state.Tier+")")
	}

	// ── Window close → hide behaviour ──
	a.setupSystemTray()

	// ── If already activated, start heartbeat ──
	if state.Activated && state.Code != "" {
		a.startHeartbeatLoop(state.Code)
	}

	wailsruntime.LogInfo(a.ctx, "MyVPN started (version "+a.version+")")
}

// Shutdown is called by Wails when the application is quitting.
func (a *App) Shutdown(ctx context.Context) {
	log.Println("Shutting down MyVPN...")
	a.disconnect()
	if a.hb != nil {
		a.hb.Stop()
	}
}

// ──────────────────────────────────────────
//  Frontend-bound methods
// ──────────────────────────────────────────

// GetVersion returns the app version string.
func (a *App) GetVersion() string {
	return a.version
}

// GetHubURL returns the configured hub URL.
func (a *App) GetHubURL() string {
	return a.hubURL
}

// GetCodeCharset returns the valid characters for activation codes.
func (a *App) GetCodeCharset() string {
	return activation.CodeCharset
}

// GetCodePrefix returns the activation code prefix.
func (a *App) GetCodePrefix() string {
	return activation.CodePrefix
}

// ── Code Validation (client-side) ──

// ValidateCode checks an activation code format without making a server call.
func (a *App) ValidateCode(code string) ValidateResult {
	if err := activation.ValidateCodeFormat(code); err != nil {
		return ValidateResult{Valid: false, Message: err.Error()}
	}
	return ValidateResult{Valid: true}
}

// ── Activation ──

// Activate sends the code and device fingerprint to the hub server.
// On success, it persists the activation and starts the heartbeat.
func (a *App) Activate(code string) ActivateResult {
	state := a.store.GetData()
	if state.Activated {
		return ActivateResult{
			Success: true,
			Message: "Already activated",
			Tier:    state.Tier,
		}
	}

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	resp, err := a.activator.Activate(ctx, code, a.fp)
	if err != nil {
		return ActivateResult{
			Success: false,
			Message: err.Error(),
		}
	}

	// activation.ServerConfig and storage.ServerConfig are distinct types —
	// map between them explicitly.
	var storageCfg *storage.ServerConfig
	if resp.ServerCfg != nil {
		storageCfg = &storage.ServerConfig{
			Server:     resp.ServerCfg.Server,
			ServerPort: resp.ServerCfg.ServerPort,
			Password:   resp.ServerCfg.Password,
			Method:     resp.ServerCfg.Method,
		}
	}

	// Persist activation
	if err := a.store.SetActivation(code, resp.Tier, resp.DeviceFP, storageCfg, resp.UDPRelay); err != nil {
		return ActivateResult{
			Success: false,
			Message: "Failed to save activation: " + err.Error(),
		}
	}

	a.tier = resp.Tier

	// Start heartbeat loop (code is bound at construction)
	a.startHeartbeatLoop(code)

	wailsruntime.LogInfo(a.ctx, "Activation successful (tier: "+resp.Tier+")")

	return ActivateResult{
		Success: true,
		Message: "Activation successful",
		Tier:    resp.Tier,
	}
}

// IsActivated returns whether the device has been activated.
func (a *App) IsActivated() bool {
	return a.store.IsActivated()
}

// ── Connection ──

// Connect starts the VPN tunnel via sing-box.
func (a *App) Connect() OpResult {
	state := a.store.GetData()
	if !state.Activated || state.ServerConfig == nil {
		return OpResult{Success: false, Message: "Not activated"}
	}
	if a.connected {
		return OpResult{Success: true, Message: "Already connected"}
	}

	cfg := manager.Config{
		Server:     state.ServerConfig.Server,
		ServerPort: state.ServerConfig.ServerPort,
		Method:     state.ServerConfig.Method,
		Password:   state.ServerConfig.Password,
		TierName:   state.Tier,
		UDPRelay:   state.UDPRelay,
		HubURL:     a.hubURL,
	}

	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()

	if err := a.mgr.Start(ctx, cfg); err != nil {
		return OpResult{Success: false, Message: err.Error()}
	}

	a.connected = true

	// Notify frontend
	wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())

	wailsruntime.LogInfo(a.ctx, "Connected to "+state.ServerConfig.Server)
	return OpResult{Success: true, Message: "Connected"}
}

// Disconnect stops the VPN tunnel.
func (a *App) Disconnect() OpResult {
	return a.disconnect()
}

func (a *App) disconnect() OpResult {
	if !a.connected {
		return OpResult{Success: true, Message: "Already disconnected"}
	}

	a.mgr.Stop()
	a.connected = false

	wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())

	return OpResult{Success: true, Message: "Disconnected"}
}

// GetStatus returns the current connection state.
func (a *App) GetStatus() StatusResult {
	return a.buildStatus()
}

func (a *App) buildStatus() StatusResult {
	state := a.store.GetData()
	return StatusResult{
		Connected: a.connected,
		Tier:      a.tier,
		State:     a.mgr.State(),
		Failures:  a.heartbeatFailures(),
		GraceDays: a.graceDays(state.LastHeartbeatOK),
	}
}

// heartbeatFailures returns the heartbeat failure count (0 if heartbeat not running).
func (a *App) heartbeatFailures() int {
	if a.hb == nil {
		return 0
	}
	return a.hb.Failures()
}

// graceDays returns the remaining grace period in days (7 if never heartbeated).
func (a *App) graceDays(lastHeartbeatOK int64) int {
	if a.hb == nil {
		return 7
	}
	return int(a.hb.RemainingGracePeriod(lastHeartbeatOK).Hours() / 24)
}

// ── Heartbeat ──

// startHeartbeatLoop creates (or re-creates) the heartbeat with the given code
// and starts the periodic loop. Safe to call multiple times — a running
// heartbeat is left untouched.
func (a *App) startHeartbeatLoop(code string) {
	if a.hb != nil && a.hb.IsRunning() {
		return
	}

	a.hb = heartbeat.New(a.hubURL, code, a.fp, func(result heartbeat.Result) {
		if result.Success {
			a.store.SetHeartbeat(time.Now().Unix())

			// Check for staged-rollout update signal
			if result.Resp != nil && result.Resp.UpdateAvailable != "" {
				wailsruntime.EventsEmit(a.ctx, "update:available", map[string]interface{}{
					"version": result.Resp.UpdateAvailable,
					"url":     result.Resp.UpdateURL,
					"sha256":  result.Resp.UpdateSHA256,
				})
			}
		} else {
			a.store.SetHeartbeatFailure(time.Now().Unix())
			wailsruntime.LogWarning(a.ctx, "Heartbeat failed: "+result.Error.Error())
		}

		// Always emit status so the UI reflects grace period changes
		wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())
	})

	a.hb.Start()
}

// ── Updates ──

// CheckForUpdate performs a manual heartbeat to check for available updates.
func (a *App) CheckForUpdate() UpdateCheckResult {
	if a.hb == nil {
		return UpdateCheckResult{Available: false}
	}

	result := a.hb.DoBeat()
	if !result.Success || result.Resp == nil || result.Resp.UpdateAvailable == "" {
		return UpdateCheckResult{Available: false}
	}

	return UpdateCheckResult{
		Available: true,
		Version:   result.Resp.UpdateAvailable,
		URL:       result.Resp.UpdateURL,
		SHA256:    result.Resp.UpdateSHA256,
	}
}

// ── Diagnostics ──

// GetDiagnostics returns a plain-text support report with no PII.
func (a *App) GetDiagnostics() string {
	state := a.store.GetData()
	mgrState := a.mgr.State()

	report := fmt.Sprintf(`MyVPN Diagnostics
===================
Version:     %s
OS:          %s/%s
Go:          %s

Activated:   %v
Connected:   %v
Tier:        %s
Engine:      %s

Heartbeat OK:     %d
Heartbeat Fail:   %d
Grace Remaining:  %d days

Reported: %s
`,
		a.version,
		goRuntime.GOOS, goRuntime.GOARCH,
		goRuntime.Version(),
		state.Activated,
		a.connected,
		a.tier,
		mgrState,
		state.LastHeartbeatOK,
		a.heartbeatFailures(),
		a.graceDays(state.LastHeartbeatOK),
		time.Now().UTC().Format(time.RFC3339),
	)

	return report
}

// ──────────────────────────────────────────
//  Internal helpers
// ──────────────────────────────────────────

// findSingBox searches common paths for the sing-box binary.
func findSingBox() string {
	// First, check alongside our own executable
	execPath, err := os.Executable()
	if err == nil {
		appDir := filepath.Dir(execPath)
		candidates := []string{
			filepath.Join(appDir, "sing-box"),
			filepath.Join(appDir, "sing-box.exe"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}

	// Then check system paths
	systemPaths := []string{
		"/usr/local/bin/sing-box",
		"/usr/bin/sing-box",
		"/opt/homebrew/bin/sing-box",
	}
	for _, p := range systemPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

// setupSystemTray configures window close behaviour and system tray.
// On close, the window hides to the system tray instead of quitting.
// The app exits via the tray menu (Quit) or system Quit command.
func (a *App) setupSystemTray() {
	// Dark background matches the UI theme (#0D0D0F)
	wailsruntime.WindowSetBackgroundColour(a.ctx, 13, 13, 15, 255)

	// Listen for "show" event triggered from the tray or dock
	wailsruntime.EventsOn(a.ctx, "tray:show", func(optionalData ...interface{}) {
		wailsruntime.WindowShow(a.ctx)
	})

	// Listen for "quit" event from tray menu
	wailsruntime.EventsOn(a.ctx, "tray:quit", func(optionalData ...interface{}) {
		wailsruntime.Quit(a.ctx)
	})

	wailsruntime.LogInfo(a.ctx, "Window close → hide behaviour set")
}
