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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

	// lastUpdate is the most recent update signal from the hub (heartbeat or
	// manual check). ApplyUpdate consumes it.
	lastUpdate *updater.UpdateInfo

	// upMu serializes ApplyUpdate calls (UI double-clicks, heartbeat races).
	upMu     sync.Mutex
	updating bool

	// startupErr is set when Startup fails part-way (e.g. storage cannot be
	// created). Frontend-bound methods check it so a broken startup shows a
	// clear error instead of panicking on nil store/manager.
	startupErr error
}

// notReady returns a human-readable failure reason when the app is not fully
// initialized, or "" when it is ready. All frontend-bound methods should bail
// out with this message when it is non-empty.
func (a *App) notReady() string {
	if a.startupErr != nil {
		return a.startupErr.Error()
	}
	if a.store == nil || a.mgr == nil {
		return "application is not ready — restart MyVPN"
	}
	return ""
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
	TunnelOK  bool   `json:"tunnelOk"`  // watchdog: is the tunnel passing traffic?
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

	// Wails runs OnStartup in a goroutine — a panic would kill the whole
	// process with no visible error (GUI builds have no console). Recover,
	// log it to myvpn.log, and keep the window alive for diagnosis.
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := goRuntime.Stack(buf, false)
			log.Printf("PANIC in Startup: %v\n%s", r, buf[:n])
		}
	}()

	// ── Storage ──
	// storage.New is self-healing (corrupt files are moved aside) and falls
	// back to the OS temp dir, so this only fails in catastrophic cases.
	// Never call LogFatal here — it exits the process silently on GUI builds.
	store, err := storage.New("myvpn")
	if err != nil {
		a.startupErr = fmt.Errorf("cannot initialize storage: %w", err)
		wailsruntime.LogError(a.ctx, "Cannot initialize storage: "+err.Error())
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
		// Self-heal legacy installs whose stored code is not in the canonical
		// hyphenated form (older clients stored the raw input). The server
		// looks up codes formatting-sensitively, so a non-canonical code would
		// 404 every heartbeat. Rewriting storage here is a one-time fix; the
		// heartbeat always transmits the canonical form (see startHeartbeatLoop).
		canonical := activation.NormalizeCode(state.Code)
		if canonical != state.Code {
			if err := a.store.SetActivation(canonical, state.Tier, state.DeviceFingerprint, state.ServerConfig, state.UDPRelay); err != nil {
				wailsruntime.LogWarning(a.ctx, "Cannot normalize stored code: "+err.Error())
			} else {
				log.Printf("Stored code normalized to canonical form")
			}
		}
		a.startHeartbeatLoop(canonical)
	}

	// ── Auto-connect after UAC elevation ──
	// When relaunched elevated via the Connect() elevation gate, we pass
	// "--autoconnect". The elevated instance connects on startup so the student
	// doesn't have to click Connect again after accepting the UAC prompt.
	if state.Activated && state.ServerConfig != nil && flagAutoConnect() {
		log.Printf("Auto-connecting (launched with --autoconnect after elevation)")
		go func() {
			// Slight delay so the Wails DOM / event system is ready to receive
			// the status:changed events emitted by Connect().
			time.Sleep(1 * time.Second)
			res := a.Connect()
			if !res.Success {
				log.Printf("Auto-connect after elevation failed: %s", res.Message)
			}
		}()
	}

	wailsruntime.LogInfo(a.ctx, "MyVPN started (version "+a.version+")")
	log.Printf("Startup complete (activated=%v)", state.Activated)
}

// flagAutoConnect reports whether the process was launched with --autoconnect
// (set by Connect() when it re-launches the app elevated via UAC).
func flagAutoConnect() bool {
	for _, a := range os.Args[1:] {
		if a == "--autoconnect" {
			return true
		}
	}
	return false
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
	if msg := a.notReady(); msg != "" {
		return ActivateResult{Success: false, Message: msg}
	}

	state := a.store.GetData()
	if state.Activated {
		return ActivateResult{
			Success: true,
			Message: "Already activated",
			Tier:    state.Tier,
		}
	}

	// Store and heartbeat the CANONICAL code form ("MYVPN-XXXX-XXXX-XXXX-C").
	// The server's code lookup is formatting-sensitive (codes are seeded in the
	// hyphenated form), so normalizing here — not echoing the raw user input —
	// keeps activation, heartbeat, suspension checks and update signals on the
	// same code string regardless of how the student typed/pasted it.
	code = activation.NormalizeCode(code)

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
			Server:        resp.ServerCfg.Server,
			ServerPort:    resp.ServerCfg.ServerPort,
			Password:      resp.ServerCfg.Password,
			Method:        resp.ServerCfg.Method,
			ServerPortUOT: resp.ServerCfg.ServerPortUOT,
		}
	}

	// The server has already bound this code to this device, so a truncated
	// response (missing server config or tier) would leave the client in a
	// stuck state: the code is bound server-side, but this device cannot
	// persist an activation and re-activation would report "already bound".
	// Surface a clear diagnostic instead of silently failing part-way.
	if resp.ServerCfg == nil {
		return ActivateResult{
			Success: false,
			Message: "server did not return connection settings (code may already be bound) — please re-enter your code or contact support",
		}
	}
	if resp.Tier == "" {
		return ActivateResult{
			Success: false,
			Message: "server did not return a tier for this code — please retry or contact support",
		}
	}
	if resp.ServerCfg.Server == "" || resp.ServerCfg.Password == "" || resp.ServerCfg.Method == "" {
		return ActivateResult{
			Success: false,
			Message: "server returned incomplete connection settings — please retry or contact support",
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
	if a.store == nil {
		return false
	}
	return a.store.IsActivated()
}

// ── Connection ──

// Connect starts the VPN tunnel via sing-box.
func (a *App) Connect() OpResult {
	if msg := a.notReady(); msg != "" {
		return OpResult{Success: false, Message: msg}
	}

	state := a.store.GetData()
	if !state.Activated || state.ServerConfig == nil {
		return OpResult{Success: false, Message: "Not activated"}
	}
	if a.connected {
		return OpResult{Success: true, Message: "Already connected"}
	}

	// TUN interface creation requires administrator privileges on Windows.
	// With the embedded requireAdministrator manifest (see rsrc_windows_*.syso)
	// the app normally ALREADY runs elevated, so this branch is a defense-in-depth
	// fallback for the rare case it launched asInvoker (e.g. an older bundle or
	// a dev run without the .syso). It re-launches the app elevated with
	// --autoconnect, then exits this (non-elevated) instance so the elevated
	// copy takes over and connects — without this a non-admin launch would
	// fail TUN creation with "Access is denied" and the connection would die.
	if !isElevated() {
		// Guard against an elevation loop: if this instance ALREADY came from a
		// UAC relaunch (--autoconnect) and still isn't elevated, relaunching
		// again would only bounce the window forever. Fail clearly instead.
		if flagAutoConnect() {
			log.Printf("Already relaunched for elevation but still not elevated — refusing to loop")
			return OpResult{
				Success: false,
				Message: "MyVPN needs administrator permission to connect, but the elevated copy did not have permission. Close it and relaunch as Administrator, or run it from an administrator account.",
			}
		}
		log.Printf("Not elevated — requesting elevation before connecting")
		if err := relaunchElevated("--autoconnect"); err != nil {
			log.Printf("Elevation request returned: %v", err)
			return OpResult{
				Success: false,
				Message: "Administrator permission was required to connect. The elevation prompt was declined or could not be shown — please relaunch MyVPN and allow the administrator prompt.",
			}
		}
		// The elevated instance is starting; end this one. The original window
		// is closing on purpose as part of the elevation handoff.
		log.Printf("UAC accepted — elevated instance starting; exiting non-elevated process")
		wailsruntime.Quit(a.ctx)
		return OpResult{
			Success: true,
			Message: "Requesting administrator permission — the app will reconnect automatically when allowed.",
		}
	}

	cfg := manager.Config{
		Server:        state.ServerConfig.Server,
		ServerPort:    state.ServerConfig.ServerPort,
		Method:        state.ServerConfig.Method,
		Password:      state.ServerConfig.Password,
		TierName:      state.Tier,
		UDPRelay:      state.UDPRelay,
		ServerPortUOT: state.ServerConfig.ServerPortUOT,
		HubURL:        a.hubURL,
	}

	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()

	if err := a.mgr.Start(ctx, cfg); err != nil {
		return OpResult{Success: false, Message: err.Error()}
	}

	// Wire the tunnel watchdog: it periodically confirms the tunnel is passing
	// traffic and auto-recovers. Its callback keeps the UI's tunnel-health and
	// connected state honest instead of showing "Connected" while the tunnel is
	// silently broken.
	a.mgr.SetProbeCallback(func(healthy bool, stage string, err error) {
		if !healthy && a.connected {
			// Tunnel degraded — reflect it immediately in the UI, then let the
			// watchdog's recovery ladder keep trying before we give up.
			wailsruntime.LogWarning(a.ctx, "Tunnel degraded ("+stage+": "+err.Error()+") — recovering")
			wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())
			return
		}
		if a.connected {
			wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())
		}
	})
	a.mgr.StartWatchdog()

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
	if a.mgr == nil {
		a.connected = false
		return OpResult{Success: true, Message: "Already disconnected"}
	}

	a.mgr.StopWatchdog()
	_ = a.mgr.Stop()
	a.connected = false

	wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())

	return OpResult{Success: true, Message: "Disconnected"}
}

// GetStatus returns the current connection state.
func (a *App) GetStatus() StatusResult {
	return a.buildStatus()
}

func (a *App) buildStatus() StatusResult {
	if a.store == nil || a.mgr == nil {
		return StatusResult{}
	}
	state := a.store.GetData()
	return StatusResult{
		Connected: a.connected,
		Tier:      a.tier,
		State:     a.mgr.State(),
		Failures:  a.heartbeatFailures(),
		GraceDays: a.graceDays(state.LastHeartbeatOK),
		TunnelOK:  a.mgr.TunnelHealthy(),
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
	if a.store == nil {
		log.Printf("heartbeat: store not ready — not starting loop")
		return
	}
	if a.hb != nil && a.hb.IsRunning() {
		return
	}

	a.hb = heartbeat.New(a.hubURL, code, a.fp, func(result heartbeat.Result) {
		if result.Success {
			_ = a.store.SetHeartbeat(time.Now().Unix())

			if result.Resp != nil {
				// ── Server config refresh ──
				// The hub can push updated connection parameters (new IP,
				// rotated password, tier change). Apply them so existing
				// devices self-heal without re-activation.
				if result.Resp.ServerConfig != nil {
					state := a.store.GetData()
					tier := result.Resp.Tier
					if tier == "" {
						tier = state.Tier
					}
					cfg := &storage.ServerConfig{
						Server:        result.Resp.ServerConfig.Server,
						ServerPort:    result.Resp.ServerConfig.ServerPort,
						Password:      result.Resp.ServerConfig.Password,
						Method:        result.Resp.ServerConfig.Method,
						ServerPortUOT: result.Resp.ServerConfig.ServerPortUOT,
					}
					// Only rewrite storage when something actually changed.
					cur := state.ServerConfig
					if cur == nil || cur.Server != cfg.Server || cur.ServerPort != cfg.ServerPort ||
						cur.Password != cfg.Password || cur.Method != cfg.Method ||
						cur.ServerPortUOT != cfg.ServerPortUOT ||
						state.UDPRelay != result.Resp.UDPRelay {
						if err := a.store.SetActivation(state.Code, tier, state.DeviceFingerprint, cfg, result.Resp.UDPRelay); err != nil {
							wailsruntime.LogWarning(a.ctx, "Cannot apply server config refresh: "+err.Error())
						} else {
							a.tier = tier
							log.Printf("Server config refreshed from heartbeat (%s:%d)", cfg.Server, cfg.ServerPort)
						}
					}
				}

				// Check for staged-rollout update signal
				if result.Resp.UpdateAvailable != "" {
					a.recordUpdateSignal(result.Resp)
					wailsruntime.EventsEmit(a.ctx, "update:available", map[string]interface{}{
						"version": result.Resp.UpdateAvailable,
						"url":     result.Resp.UpdateURL,
						"sha256":  result.Resp.UpdateSHA256,
					})
				}
			}
		} else {
			_ = a.store.SetHeartbeatFailure(time.Now().Unix())
			wailsruntime.LogWarning(a.ctx, "Heartbeat failed: "+result.Error.Error())
		}

		// Always emit status so the UI reflects grace period changes
		wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())
	})

	a.hb.Start()
}

// ── Updates ──

// recordUpdateSignal stores the hub's latest update signal so ApplyUpdate can
// consume it later (the heartbeat fires every 5 min, but the user may click
// "Update" minutes after the signal arrived).
func (a *App) recordUpdateSignal(resp *heartbeat.Response) {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	a.lastUpdate = &updater.UpdateInfo{
		Version:               resp.UpdateAvailable,
		SHA256:                resp.UpdateSHA256,
		DownloadURL:           resp.UpdateURL,
		DownloadURLLinux:      resp.UpdateLinux,
		DownloadURLWindows:    resp.UpdateWindows,
		DownloadURLMacOSIntel: resp.UpdateMacOSIntel,
		DownloadURLMacOSARM:   resp.UpdateMacOSARM,
	}
}

// CheckForUpdate performs a manual heartbeat to check for available updates.
func (a *App) CheckForUpdate() UpdateCheckResult {
	if a.hb == nil {
		return UpdateCheckResult{Available: false}
	}

	result := a.hb.DoBeat()
	if !result.Success || result.Resp == nil || result.Resp.UpdateAvailable == "" {
		return UpdateCheckResult{Available: false}
	}

	a.recordUpdateSignal(result.Resp)

	return UpdateCheckResult{
		Available: true,
		Version:   result.Resp.UpdateAvailable,
		URL:       result.Resp.UpdateURL,
		SHA256:    result.Resp.UpdateSHA256,
	}
}

// ApplyUpdate downloads, verifies and applies the update signalled by the hub,
// then quits so the forked new binary takes over. It runs in the background and
// emits update:status events so the UI can show progress:
//
//	{phase: "downloading"} → {phase: "verifying"} → {phase: "applying"}
//	→ {phase: "applied"} (app quits ~1.5s later) | {phase: "failed", message}
//
// Returns immediately; failures are surfaced through the update:status event
// (phase "failed") and shown in the UI toast.
func (a *App) ApplyUpdate() OpResult {
	a.upMu.Lock()
	defer a.upMu.Unlock()

	if a.updating {
		return OpResult{Success: false, Message: "An update is already being applied"}
	}
	if a.up == nil {
		return OpResult{Success: false, Message: "Updater not initialized"}
	}
	if a.lastUpdate == nil {
		return OpResult{Success: false, Message: "No update available — check for updates first"}
	}
	if a.lastUpdate.Version == a.version {
		// Staged rollouts re-advertise the same version every heartbeat —
		// don't re-download and re-apply a build that is already running.
		a.lastUpdate = nil
		return OpResult{Success: false, Message: "Already running the latest version"}
	}
	info := *a.lastUpdate // copy — the heartbeat may replace the pointer
	a.updating = true

	go func() {
		emit := func(phase, message string) {
			wailsruntime.EventsEmit(a.ctx, "update:status", map[string]interface{}{
				"phase":   phase,
				"message": message,
			})
		}

		// Downloads can take minutes on school WiFi — never block a 10s RPC
		// timeout. The UI stays responsive and is driven by update:status.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		emit("downloading", "Downloading update…")
		log.Printf("ApplyUpdate: downloading %s (v%s)", info.Version, info.Version)
		if err := a.up.PerformUpdate(ctx, info); err != nil {
			a.upMu.Lock()
			a.updating = false
			a.upMu.Unlock()
			log.Printf("ApplyUpdate: failed: %v", err)
			emit("failed", "Update failed: "+err.Error())
			return
		}

		log.Printf("ApplyUpdate: applied v%s — restarting", info.Version)
		emit("applied", "Update applied — restarting…")

		// Give the webview a moment to paint the "Restarting…" state, then quit
		// so the forked new binary takes over (see updater.PerformUpdate).
		time.Sleep(1500 * time.Millisecond)
		wailsruntime.Quit(a.ctx)
	}()

	return OpResult{Success: true, Message: "Update started"}
}

// ── Diagnostics ──

// GetDiagnostics returns a plain-text support report with no PII.
func (a *App) GetDiagnostics() string {
	if msg := a.notReady(); msg != "" {
		return fmt.Sprintf("MyVPN Diagnostics\n===================\nVersion: %s\n\nApp not ready: %s\n", a.version, msg)
	}
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
	Tunnel OK:   %v

	Heartbeat OK:     %d
	Heartbeat Fail:   %d
	Grace Remaining:  %d days

Server:       %s

Leftover engines:
%s
Reported: %s
`,
		a.version,
		goRuntime.GOOS, goRuntime.GOARCH,
		goRuntime.Version(),
		state.Activated,
		a.connected,
		a.tier,
		mgrState,
		a.mgr.TunnelHealthy(),
		state.LastHeartbeatOK,
		a.heartbeatFailures(),
		a.graceDays(state.LastHeartbeatOK),
		a.serverReachability(),
		leftoverLines(a.mgr.ForeignEngines()),
		time.Now().UTC().Format(time.RFC3339),
	)

	return report
}

// leftoverLines formats a foreign-engine summary for the diagnostics report,
// defaulting to "(none)" when empty.
func leftoverLines(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// serverReachability tests TCP connectivity to the configured VPN server.
// It distinguishes network blocks from tunnel-state problems.
//
// IMPORTANT: when the VPN tunnel is UP, auto_route captures the host's OWN
// sockets — including this probe's dial — and routes them through the TUN. A
// raw host-level dial is then no longer an out-of-band test: it fails whenever
// the tunnel isn't passing traffic, which previously produced a confusing
// "server UNREACHABLE" in diagnostics even though the server itself was fine
// and the real problem was the tunnel. The label is therefore qualified by the
// connection state so support reports read correctly.
func (a *App) serverReachability() string {
	state := a.store.GetData()
	if state.ServerConfig == nil {
		return "no server config"
	}
	addr := net.JoinHostPort(state.ServerConfig.Server, strconv.Itoa(state.ServerConfig.ServerPort))
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err == nil {
		_ = conn.Close()
		return fmt.Sprintf("%s reachable", addr)
	}
	if a.connected {
		// Tunnel up but this host-level probe fails: traffic is being routed
		// through the (now non-functional) tunnel, so "unreachable" is not a
		// statement about the server — it means the tunnel is not passing TCP.
		return fmt.Sprintf("%s via tunnel UNREACHABLE — tunnel not passing TCP (server out-of-band not tested)", addr)
	}
	return fmt.Sprintf("%s UNREACHABLE (%v)", addr, err)
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

// setupSystemTray configures window behaviour and (future) system tray hooks.
//
// NOTE: Wails v2.9 has NO system tray API and this app does not create a tray
// icon — the "tray:show" / "tray:quit" listeners below are dormant hooks for a
// future tray implementation. The window is shown on launch (StartHidden is
// off) and closing the window quits the app (Wails v2.9 has no close-to-hide
// interception either).
func (a *App) setupSystemTray() {
	// Dark background matches the UI theme (#0D0D0F)
	wailsruntime.WindowSetBackgroundColour(a.ctx, 13, 13, 15, 255)

	// Dormant hooks — nothing emits these events yet (no tray icon exists).
	// Listen for "show" event triggered from the tray or dock
	wailsruntime.EventsOn(a.ctx, "tray:show", func(optionalData ...interface{}) {
		wailsruntime.WindowShow(a.ctx)
	})

	// Listen for "quit" event from tray menu
	wailsruntime.EventsOn(a.ctx, "tray:quit", func(optionalData ...interface{}) {
		wailsruntime.Quit(a.ctx)
	})

	wailsruntime.LogInfo(a.ctx, "Window background set; tray hooks registered (no tray icon yet)")
}
