// Package main — Locus Wails Desktop App.
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
	"sync/atomic"
	"time"

	"locus/internal/activation"
	"locus/internal/buildinfo"
	"locus/internal/heartbeat"
	"locus/internal/manager"
	"locus/internal/pinned"
	"locus/internal/storage"
	"locus/internal/tray"
	"locus/internal/updater"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	// Stdlib runtime used for GOOS/GOARCH in diagnostics
	goRuntime "runtime"
)

// ──────────────────────────────────────────
//  App struct — exposed to Vue frontend
// ──────────────────────────────────────────

// degradedStreakMax is how many consecutive unrecovered watchdog cycles before
// the app stops auto-churning the engine and drops to "Disconnected" (tap to
// retry). Watchdog probes run roughly every 10s, so ~5 cycles <=> ~50s of a
// stubbornly broken tunnel before we give the control back to the student.
const degradedStreakMax = 5

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

	// build carries the binary's self-knowledge (version, whether that version
	// was injected by the release pipeline, commit, toolchain). Support reports
	// and the update flow both need it: an uninstrumented build must not be
	// mistaken for a release.
	build buildinfo.Info

	// singBoxPath and configPath are the *resolved* engine path and generated
	// config path. Retained so diagnostics can report what the app actually
	// loaded rather than re-deriving it (and possibly disagreeing).
	singBoxPath string
	configPath  string

	// lastUpdateStatus/lastUpdateDetail record the outcome of the most recent
	// update check so "it never offers updates" is answerable from a support
	// report without reproducing it.
	lastUpdateStatus string
	lastUpdateDetail string

	// Cached runtime state (persisted state lives in storage)
	connected bool
	tier      string
	fp        string

	// degradedStreak counts consecutive watchdog cycles where the tunnel failed
	// to recover (the backend watchdog already auto-restarts a few times). When
	// it crosses degradedStreakMax we stop churning and drop to the
	// "Disconnected — tap to retry" state. Reset on each healthy probe.
	degradedStreak int
	autoDisconnect bool

	// lastUpdate is the most recent update signal from the hub (heartbeat or
	// manual check). ApplyUpdate consumes it.
	lastUpdate *updater.UpdateInfo

	// trayCtrl, when non-nil, is the optional system-tray controller started in
	// Startup when LOCUS_TRAY=1 (see internal/tray). It is kept here so lifecycle
	// and state updates can reach it.
	trayCtrl *tray.Controller

	// upMu serializes ApplyUpdate calls (UI double-clicks, heartbeat races).
	upMu     sync.Mutex
	updating bool

	// startupErr is set when Startup fails part-way (e.g. storage cannot be
	// created). Frontend-bound methods check it so a broken startup shows a
	// clear error instead of panicking on nil store/manager.
	startupErr error

	// ready is closed/published once Startup has finished wiring every
	// dependency. Frontend calls and the auto-connect goroutine consult it so a
	// call that arrives early gets an accurate "still starting" answer instead
	// of a misleading one.
	ready atomic.Bool
}

// notReady returns a human-readable failure reason when the app is not fully
// initialized, or "" when it is ready. All frontend-bound methods should bail
// out with this message when it is non-empty.
//
// The two failure modes are deliberately worded differently, because they need
// different user actions:
//
//   - Dependencies missing while Startup is still running is a TRANSIENT state.
//     Telling the user to "restart Locus" here was actively bad advice — it
//     names the one action that cannot help, and it is what made the intermittent
//     "application is not ready — restart Locus" look like a crash. The honest
//     answer is "still starting, try again in a moment".
//   - A recorded startupErr is permanent for this process, and only then is
//     restarting the right advice.
func (a *App) notReady() string {
	if a.startupErr != nil {
		return a.startupErr.Error()
	}
	if a.store == nil || a.mgr == nil {
		// No startupErr recorded and the deps are not built yet: this is
		// Startup still in flight.
		if !a.ready.Load() {
			return "Locus is still starting up — try again in a moment."
		}
		// Ready was published but a dependency is still missing, which means
		// Startup took an early-return path. Only here is a restart warranted.
		return "application failed to initialize — restart Locus"
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

// CodeCheckResult is returned by CheckCode — a READ-ONLY pre-check of whether a
// code is recognised by the hub, so the activation screen can answer "is this
// code real?" before the student commits to activating.
type CodeCheckResult struct {
	// Recognised is true only when the hub confirmed the code EXISTS and is
	// usable (unbound, or already bound to this device).
	Recognised bool `json:"recognised"`
	// Status is the raw lookup status: ok | unbound | bound_this_device |
	// bound_other | suspended | expired | not_found | unknown.
	Status string `json:"status"`
	// Known is true when the server gave a definitive answer. When the hub is
	// unreachable (or predates this endpoint) Known is false and the UI must
	// fall back to "we'll confirm when you activate" rather than claiming the
	// code is bad.
	Known   bool   `json:"known"`
	Message string `json:"message,omitempty"`
	Tier    string `json:"tier,omitempty"`
	// ExpiresAt is an ISO-8601 timestamp when the hub reported an expiry.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// StatusResult is returned by GetStatus.
type StatusResult struct {
	Connected bool   `json:"connected"`
	Tier      string `json:"tier"`
	State     string `json:"state"`     // "running", "stopped", "crashed"
	Failures  int    `json:"failures"`  // heartbeat failures
	GraceDays int    `json:"graceDays"` // remaining grace period in days
	TunnelOK  bool   `json:"tunnelOk"`  // watchdog: is the tunnel passing traffic?
	// RepairStage names the current watchdog recovery action when the tunnel is
	// unhealthy but still being recovered: "" | "restart" | "full-reset" |
	// "degraded". Lets the UI distinguish "recovering (retrying engine)" from
	// "tunnel down". See Manager.WatchdogStage().
	RepairStage string `json:"repairStage,omitempty"`
}

// OpResult is returned by Connect / Disconnect.
type OpResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// UpdateCheckResult is returned by CheckForUpdate.
//
// WHY SO MANY FIELDS: a bare `available: false` is useless to both the student
// and to support. "No update" has at least five distinct causes — the hub has
// nothing published, the hub could not be reached, the rollout has not bucketed
// this device yet, the advertised version is not newer than the running one, or
// this is an uninstrumented dev build whose version cannot be trusted. Those
// need different actions, so the result distinguishes them explicitly instead
// of collapsing to a single boolean.
type UpdateCheckResult struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`

	// Status is a stable machine-readable outcome:
	//   available            — an update is ready to apply
	//   up_to_date           — hub advertising a version, but not newer than ours
	//   no_release           — hub has nothing active / rollout 0 for this device
	//   unreachable          — heartbeat failed (offline, blocked, server down)
	//   uninstrumented_build — dev build; version cannot be trusted for comparison
	//   not_activated        — no heartbeat loop running (no code bound)
	Status string `json:"status"`

	// Reason is a one-line human explanation of Status, safe to show.
	Reason string `json:"reason,omitempty"`

	// CurrentVersion is the version this binary is running, and
	// CurrentInstrumented says whether that version came from the release
	// pipeline. Both are here so a support screenshot always shows what the
	// client believed about itself at the moment of the check.
	CurrentVersion      string `json:"currentVersion,omitempty"`
	CurrentInstrumented bool   `json:"currentInstrumented"`

	// Platform is the artifact this client would fetch (e.g. "windows/amd64"),
	// so "the update has no asset for me" is distinguishable from "no update".
	Platform string `json:"platform,omitempty"`

	// AdvertisedVersion is whatever the hub offered, even when we refused it
	// (e.g. it was older than the running build). Empty when nothing was
	// advertised.
	AdvertisedVersion string `json:"advertisedVersion,omitempty"`

	// HeartbeatError carries the transport error when Status is unreachable.
	HeartbeatError string `json:"heartbeatError,omitempty"`
}

// ──────────────────────────────────────────
//  Lifecycle
// ──────────────────────────────────────────

// NewApp creates the App. Called by main.go.
// version comes from the package-level var set via -ldflags "-X main.version=...".
//
// buildinfo.Detect records whether that injection actually happened, so the
// update flow and diagnostics never present a scratch build as a release.
func NewApp() *App {
	return &App{
		version: version,
		hubURL:  "https://networkingguides.duckdns.org",
		build:   buildinfo.Detect(version),
	}
}

// Startup is called by Wails when the application starts.
// It initializes storage, activates the system tray, and checks for update recovery.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// Configure hub TLS pinning before any HTTP client connects. Pins come from
	// LOCUS_HUB_PINS (comma separated base64 SHA-256 SPKI hashes); unset = the
	// additive pin check stays off (fail-open) but normal TLS is still enforced.
	// LOCUS_SKIP_PINNING=1 disables the extra check. See internal/pinned.
	pinned.Load(os.Getenv("LOCUS_HUB_PINS"), os.Getenv("LOCUS_SKIP_PINNING") != "")

	// Wails runs OnStartup in a goroutine — a panic would kill the whole
	// process with no visible error (GUI builds have no console). Recover,
	// log it to locus.log, and keep the window alive for diagnosis.
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
	store, err := storage.New("locus")
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
	a.singBoxPath = singBoxPath
	if singBoxPath == "" {
		wailsruntime.LogWarning(a.ctx, "sing-box binary not found — tunnel will not work")
	} else {
		log.Printf("sing-box engine resolved: %s", singBoxPath)
	}

	// ── Manager (direct mode — no helper binary) ──
	tmpDir := filepath.Join(os.TempDir(), "locus")
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		wailsruntime.LogWarning(a.ctx, "Cannot create temp dir: "+err.Error())
	}
	configPath := filepath.Join(tmpDir, "sing-box-config.json")
	a.configPath = configPath
	a.mgr = manager.NewManager(singBoxPath, configPath, "")
	// IMPORTANT: force direct mode. NewManager defaults to helper mode on
	// Windows, but the helper binary no longer exists — helper mode would
	// fail with "locus-helper binary not found".
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

	// ── Auto-connect after elevation ──
	// When relaunched elevated via the Connect() elevation gate, we pass
	// --autoconnect. The elevated instance connects on startup so the student
	// doesn't have to click Connect again after accepting the UAC prompt.
	//
	// The GUI is ready the moment this line is reached: every field Connect()
	// needs (store, mgr, activator, fp) was assigned above, and the ready flag
	// is published BEFORE the goroutine starts so there is no window in which
	// Connect() could observe a half-built app.
	//
	// This ordering is the fix for "application is not ready — restart Locus":
	// the previous version launched the goroutine and relied on a fixed 1s
	// sleep to outrun Startup. On a cold elevated launch (WebView2
	// initialisation, antivirus scanning the new process, slow disk) Startup
	// could still be running when the sleep expired, so Connect() hit the
	// not-ready guard. Retrying eventually won the race, which is exactly why
	// the error looked intermittent and why "press retry a few times" worked.
	a.ready.Store(true)

	if state.Activated && state.ServerConfig != nil && flagAutoConnect() {
		why := "flag"
		if flagElevatedAttempt() {
			why = "elevated relaunch"
		}
		log.Printf("Auto-connecting on startup (%s, elevated=%v)", why, isElevated())
		go func() {
			// The app is ready; the small delay only exists so the webview has
			// painted and can render the status:changed events rather than
			// missing the first one. It is no longer load-bearing for
			// correctness — readiness is guaranteed by the ready flag.
			time.Sleep(1 * time.Second)
			res := a.Connect()
			if !res.Success {
				log.Printf("Auto-connect on startup failed: %s", res.Message)
			}
		}()
	}

	wailsruntime.LogInfo(a.ctx, "Locus started (version "+a.version+")")
	log.Printf("Startup complete (activated=%v, elevated=%v)", state.Activated, isElevated())
}

// flagAutoConnect reports whether this process should connect as soon as it is
// ready. Set by the elevation handoff, or passed directly on the command line.
func flagAutoConnect() bool {
	return hasFlag(os.Args[1:], flagAutoConnectValue)
}

// flagElevatedAttempt reports whether this process IS the elevated copy that a
// handoff produced.
//
// This must NOT be inferred from --autoconnect: that flag is also a legitimate
// user request, and inferring elevation from it made the loop guard misfire on
// ordinary auto-connect runs (the "elevated copy did not have permission" error
// shown to a user who had approved UAC and WAS elevated).
func flagElevatedAttempt() bool {
	return hasFlag(os.Args[1:], flagElevatedAttemptValue)
}

// Shutdown is called by Wails when the application is quitting.
func (a *App) Shutdown(ctx context.Context) {
	log.Println("Shutting down Locus...")
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

// ValidateCode checks an activation code FORMAT without making a server call.
//
// IMPORTANT: this validates the Luhn-mod-N checksum only. It says the code is
// well-formed, NOT that it exists in the database. Use CheckCode for the latter.
func (a *App) ValidateCode(code string) ValidateResult {
	if err := activation.ValidateCodeFormat(code); err != nil {
		return ValidateResult{Valid: false, Message: err.Error()}
	}
	return ValidateResult{Valid: true}
}

// CheckCode asks the hub whether a code is actually recognised (exists, and is
// not suspended/expired/bound to another device), WITHOUT binding this device.
//
// This is a read-only pre-check for the activation screen. It is deliberately
// best-effort: any transport failure returns Known=false so the UI can say "we
// will confirm when you activate" instead of wrongly telling the student their
// code is invalid. A malformed code never reaches the server.
func (a *App) CheckCode(code string) CodeCheckResult {
	if msg := a.notReady(); msg != "" {
		return CodeCheckResult{Status: "unknown", Known: false, Message: msg}
	}

	// Reject malformed codes locally (and without spending a rate-limit slot).
	if err := activation.ValidateCodeFormat(code); err != nil {
		return CodeCheckResult{
			Status:  string(activation.LookupNotFound),
			Known:   true,
			Message: err.Error(),
		}
	}

	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	resp, err := a.activator.LookupCode(ctx, code, a.fp)
	if err != nil {
		// Unknown, not invalid — the student may still activate successfully.
		return CodeCheckResult{
			Status:  "unknown",
			Known:   false,
			Message: "Could not check the code with the server — it will be verified when you activate.",
		}
	}

	recognised := resp.Status == activation.LookupOK ||
		resp.Status == activation.LookupUnbound ||
		resp.Status == activation.LookupBoundThisDevice

	return CodeCheckResult{
		Recognised: recognised,
		Status:     string(resp.Status),
		Known:      true,
		Message:    resp.Message,
		Tier:       resp.Tier,
		ExpiresAt:  resp.ExpiresAt,
	}
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

	// Store and heartbeat the CANONICAL code form ("RQ-XXXX-XXXX-XXXX-C").
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

	// TUN interface creation requires privilege. Two different situations live
	// behind this one check, and conflating them was a real defect:
	//
	//   Windows — the binary normally runs elevated via the embedded
	//     requireAdministrator manifest. This branch is defense-in-depth for a
	//     bundle built without the .syso (or a dev run): relaunch via UAC with
	//     --autoconnect, then exit this instance so the elevated copy connects.
	//
	//   Unix — there is NO automatic elevation path (see elevate_unix.go). If we
	//     are not root the tunnel cannot work, so fail immediately and say why.
	//     Previously isElevated() returned true unconditionally on Unix, so
	//     Connect() proceeded and the student got an obscure sing-box error
	//     instead of "you need root".
	if !isElevated() {
		// Unix (or Windows without a UAC path): no automatic elevation.
		if reason := elevationUnsupportedReason(); reason != "" {
			log.Printf("Refusing to connect without privilege: %s", reason)
			return OpResult{Success: false, Message: reason}
		}

		// Windows: ask for elevation by relaunching with the "runas" verb.
		//
		// The loop guard uses the DEDICATED --elevated-attempt marker, not
		// --autoconnect. Using --autoconnect was a defect: it is also a
		// user-facing flag, so a normal auto-connect run was misread as a
		// relaunch that had failed to elevate, and Connect() refused with a
		// permission error even when the process WAS elevated.
		if flagElevatedAttempt() {
			// We are the elevated copy and still are not elevated. Elevation
			// genuinely failed (declined, or blocked by policy) — do not loop.
			log.Printf("Elevated relaunch did not gain privilege (elevated=%v) — refusing to loop", isElevated())
			return OpResult{
				Success: false,
				Message: "Windows did not grant administrator permission, so the VPN " +
					"adapter cannot be created. If a permission prompt appeared, " +
					"choose Yes. Otherwise right-click Locus and choose " +
					"\"Run as administrator\".",
			}
		}

		log.Printf("Not elevated — requesting elevation before connecting")
		// Cap the retries: each handoff replaces this process, so a bound is
		// the only way to stop an unbreakable relaunch loop. Three attempts is
		// generous for a prompt that is either approved or not.
		attempt := parseAttempt(os.Args[1:]) + 1
		if attempt > 3 {
			log.Printf("Elevation attempted %d times without success — giving up", attempt-1)
			return OpResult{
				Success: false,
				Message: "Locus could not obtain administrator permission after several " +
					"attempts. Right-click the Locus icon and choose " +
					"\"Run as administrator\", then press Connect.",
			}
		}

		if err := relaunchElevated(
			flagElevatedAttemptValue,
			flagAutoConnectValue,
			flagAttemptValue+strconv.Itoa(attempt),
		); err != nil {
			log.Printf("Elevation request returned: %v", err)
			return OpResult{
				Success: false,
				Message: "Administrator permission was required to connect. The elevation " +
					"prompt was declined or could not be shown — please relaunch Locus " +
					"and allow the administrator prompt.",
			}
		}
		// The elevated instance is starting; end this one. The original window
		// is closing on purpose as part of the elevation handoff.
		log.Printf("UAC accepted — elevated instance starting (attempt %d); exiting this process", attempt)
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
		// healthy probes reset the degraded streak.
		if !a.connected {
			return
		}
		if healthy {
			if a.degradedStreak > 0 {
				a.degradedStreak = 0
			}
			wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())
			return
		}
		// Degraded — reflect it in the UI, then keep counting. If it stays
		// unrecovered past the cap, stop auto-churning and surface
		// "Disconnected" so the student taps to retry when the network allows.
		a.degradedStreak++
		wailsruntime.LogWarning(a.ctx, "Tunnel degraded ("+stage+": "+err.Error()+") — recovering")
		wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())
		if a.degradedStreak >= degradedStreakMax && !a.autoDisconnect {
			a.autoDisconnect = true
			log.Printf("watchdog: tunnel unrecovered after %d tries — disconnecting; tap Connect to retry", a.degradedStreak)
			go a.disconnectNow()
		}
	})
	a.mgr.StartWatchdog()

	a.connected = true
	a.degradedStreak = 0
	a.autoDisconnect = false

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
	if a.mgr == nil {
		a.connected = false
		return OpResult{Success: true, Message: "Already disconnected"}
	}

	// The engine is stopped UNCONDITIONALLY, even when we already believe we are
	// disconnected.
	//
	// This used to early-return on `!a.connected`, which was the direct cause of
	// a two-bug chain that could only be escaped by restarting the app:
	//
	//   1. The watchdog's auto-disconnect calls this after the UI has already
	//      flipped to Disconnected (or the student tapped Disconnect while a
	//      repair was in flight). `a.connected` was therefore already false, so
	//      this returned "Already disconnected" WITHOUT calling mgr.Stop().
	//      sing-box kept running, untracked.
	//   2. The next Connect hit Start()'s "tunnel is already running" guard,
	//      because a real engine was alive while the app believed it was
	//      disconnected. No UI action could clear that state.
	//
	// `a.connected` is a UI/state flag; it is not evidence about the process.
	// Only the manager can answer that, and calling Stop() when nothing is
	// running is already a safe no-op (it cancels the context and cleans up the
	// config file), so there is nothing to save by skipping it.
	wasConnected := a.connected

	// Emit an OPTIMISTIC disconnected status before the (up to ~2s) shutdown
	// wait, so the UI reflects the tap immediately instead of sitting on a
	// spinner until Stop() returns. The authoritative status is emitted again
	// below once the engine has actually gone.
	a.connected = false
	a.autoDisconnect = false
	wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())

	a.mgr.StopWatchdog()
	// Stop() is idempotent and safe when no engine is tracked; always call it so
	// an orphan cannot survive a disconnect.
	if err := a.mgr.Stop(); err != nil {
		// A failure here is worth surfacing rather than swallowing: it means the
		// engine may still be alive, which is exactly the condition that makes
		// the next Connect fail. The manager has already force-killed and
		// cleared tracking by this point, so report it but do not re-set
		// a.connected.
		log.Printf("disconnect: stopping engine returned an error: %v", err)
		wailsruntime.LogWarning(a.ctx, "Tunnel stop reported an error: "+err.Error())
	}

	wailsruntime.EventsEmit(a.ctx, "status:changed", a.buildStatus())

	if !wasConnected {
		return OpResult{Success: true, Message: "Disconnected"}
	}
	return OpResult{Success: true, Message: "Disconnected"}
}

// disconnectNow is invoked (guarded, on its own goroutine) by the watchdog once
// the tunnel has stayed unrecovered past degradedStreakMax probes. It stops the
// watchdog cleanly and drops to the Disconnected state, so the app stops
// churning the engine and the student can tap Connect when the network allows.
func (a *App) disconnectNow() {
	result := a.disconnect()
	if !result.Success {
		log.Printf("watchdog: auto-disconnect failed: %s", result.Message)
	}
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
	res := StatusResult{
		Connected: a.connected,
		Tier:      a.tier,
		State:     a.mgr.State(),
		Failures:  a.heartbeatFailures(),
		GraceDays: a.graceDays(state.LastHeartbeatOK),
		TunnelOK:  a.mgr.TunnelHealthy(),
		// Expose the live watchdog recovery stage only while we're still
		// attempting to recover a degraded tunnel, so the UI can show an honest
		// "recovering (restarting engine)" vs "tunnel down" distinction.
		RepairStage: repairStageWhen(a.mgr),
	}
	// Keep the optional tray's status line in sync with app state.
	if a.trayCtrl != nil {
		a.trayCtrl.SetTier(a.tier)
		a.trayCtrl.SetConnected(a.connected)
	}
	return res
}

// repairStageWhen returns the current watchdog recovery stage when the tunnel is
// unhealthy (and we are therefore recovering); "" when the tunnel is healthy or
// no watchdog recovery is in progress. Centralizing this keeps the JSON shape
// stable (empty/'healthy' never set while healthy).
func repairStageWhen(m *manager.Manager) string {
	if m == nil {
		return ""
	}
	if m.TunnelHealthy() {
		return ""
	}
	return m.WatchdogStage()
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

	// Only store a signal we would be willing to act on. The hub gates which
	// clients SEE an update, but it cannot be trusted to only ever advertise
	// versions that are actually newer — a mis-set update_config row would
	// otherwise show an "Update available" prompt for a downgrade. Ignoring it
	// here keeps the UI honest and stops ApplyUpdate from being reachable.
	if !updater.IsNewer(resp.UpdateAvailable, a.version) {
		if resp.UpdateAvailable != "" {
			log.Printf("Ignoring update signal for %s (running %s) — not newer",
				resp.UpdateAvailable, a.version)
		}
		a.lastUpdate = nil
		return
	}

	a.lastUpdate = &updater.UpdateInfo{
		Version:               resp.UpdateAvailable,
		SHA256:                resp.UpdateSHA256,
		DownloadURL:           resp.UpdateURL,
		DownloadURLLinux:      resp.UpdateLinux,
		DownloadURLWindows:    resp.UpdateWindows,
		DownloadURLMacOSIntel: resp.UpdateMacOSIntel,
		DownloadURLMacOSARM:   resp.UpdateMacOSARM,
		SHA256Linux:           resp.UpdateSHA256Linux,
		SHA256Windows:         resp.UpdateSHA256Windows,
		SHA256MacOSIntel:      resp.UpdateSHA256MacOSIntel,
		SHA256MacOSARM:        resp.UpdateSHA256MacOSARM,
	}
}

// CheckForUpdate performs a manual heartbeat to check for available updates.
func (a *App) CheckForUpdate() UpdateCheckResult {
	// Always report what the client is and what it would fetch, even when the
	// check cannot complete — this is the information a support report needs
	// most, and it is the part that used to be missing entirely.
	res := UpdateCheckResult{
		CurrentVersion:      a.version,
		CurrentInstrumented: a.build.Instrumented,
		Platform:            a.build.Platform,
	}
	// Remember the outcome so GetDiagnostics can report it without re-checking.
	defer func() {
		a.lastUpdateStatus = res.Status
		a.lastUpdateDetail = res.Reason
	}()
	if a.hb == nil {
		// No code is bound, so the heartbeat path cannot run. Fall back to the
		// public release manifest, which needs no credentials — otherwise an
		// unactivated (or unactivatable) client could never be told that a
		// fixed build exists.
		return a.checkUpdateViaPublicManifest(res, UpdateStatusNotActivated,
			"No activation code is bound to this device yet, so this checked the public release list.")
	}

	result := a.hb.DoBeat()
	if !result.Success || result.Resp == nil {
		// The heartbeat failed. That used to be the end of the road: the client
		// could not learn about a new build at the exact moment it most needed
		// one. Try the credential-free manifest before giving up.
		if result.Error != nil {
			res.HeartbeatError = result.Error.Error()
		}
		return a.checkUpdateViaPublicManifest(res, UpdateStatusUnreachable,
			"Could not reach the update server through your activation. "+
				"This checked the public release list instead.")
	}

	advertised := result.Resp.UpdateAvailable
	res.AdvertisedVersion = advertised

	if advertised == "" {
		// The hub answered but is advertising nothing: either no release is
		// published, or this device has not been bucketed into the rollout yet.
		// Both are correct, expected states — say so instead of leaving the
		// student with a dead button.
		res.Status = UpdateStatusNoRelease
		res.Reason = "You are running the newest published version."
		return res
	}

	a.recordUpdateSignal(result.Resp)

	// recordUpdateSignal drops anything that is not strictly newer, so a stale
	// update_config row cannot be reported as an available update. Reflect that
	// decision back to the UI rather than trusting the raw response.
	if a.lastUpdate == nil {
		if !a.build.Instrumented {
			// The refusal may be an artefact of an uninstrumented build whose
			// fallback version is newer than anything the hub publishes. Say
			// that plainly rather than claiming "up to date".
			res.Status = UpdateStatusUninstrumentedBuild
			res.Reason = "This is a development build, so version comparison is unreliable. Install a release build to receive updates."
			return res
		}
		res.Status = UpdateStatusUpToDate
		res.Reason = "The server is offering " + advertised + ", which is not newer than the version you are running (" + a.version + ")."
		return res
	}

	// An update is offerable. Verify we actually have an artifact for THIS
	// platform before telling the student one is available — the hub only
	// includes the download URLs it has, and a missing asset would otherwise
	// surface as a download failure after the student pressed Update.
	url := a.lastUpdate.PlatformDownloadURL()
	if url == "" {
		res.Status = UpdateStatusNoAsset
		res.Reason = "Update " + a.lastUpdate.Version + " is available, but no build is published for this platform (" + a.build.Platform + ")."
		return res
	}
	if a.lastUpdate.PlatformSHA256() == "" {
		res.Status = UpdateStatusNoAsset
		res.Reason = "Update " + a.lastUpdate.Version + " is available, but the server did not publish a checksum for this platform, so it cannot be verified safely."
		return res
	}

	res.Available = true
	res.Status = UpdateStatusAvailable
	res.Version = a.lastUpdate.Version
	res.URL = url
	res.SHA256 = a.lastUpdate.PlatformSHA256()
	res.Reason = "Update " + a.lastUpdate.Version + " is ready to install."
	return res
}

// checkUpdateViaPublicManifest is the fallback update path.
//
// It exists because the primary path (heartbeat) needs a bound activation code,
// so a client whose build is broken badly enough to prevent activation — or
// whose code is suspended — could never learn that a fix had been published.
// The failure prevented escaping the failure.
//
// fallbackStatus/why describe WHY we ended up here, so the UI can still explain
// the situation honestly. If the manifest yields a newer version, the result is
// marked available and the reason says it came from the public list; otherwise
// the original status is preserved (with the fallback appended to the reason)
// so the user is not told "up to date" when we never actually reached the hub.
func (a *App) checkUpdateViaPublicManifest(res UpdateCheckResult, fallbackStatus, why string) UpdateCheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	info, err := updater.FetchPublicRelease(ctx, a.hubURL, nil)
	if err != nil {
		// The public path failed too. Report the original condition — it is the
		// more truthful description of why no update could be confirmed.
		res.Status = fallbackStatus
		res.Reason = why + " That also failed (" + err.Error() + ")."
		return res
	}
	if info == nil {
		res.Status = fallbackStatus
		res.Reason = why + " No release is published."
		return res
	}

	res.AdvertisedVersion = info.Version

	if !updater.IsNewer(info.Version, a.version) {
		// The public list agrees we are current. Keep the fallback status (the
		// activation problem is still real and still worth reporting) but say
		// the version itself is fine.
		res.Status = fallbackStatus
		res.Reason = why + " No newer release is published."
		return res
	}

	url := info.PlatformDownloadURL()
	sha := info.PlatformSHA256()
	if url == "" || sha == "" {
		res.Status = UpdateStatusNoAsset
		res.Reason = "Version " + info.Version + " is published, but no build with a checksum exists for this platform (" + a.build.Platform + ")."
		return res
	}

	// Adopt it so ApplyUpdate can act on it.
	a.upMu.Lock()
	a.lastUpdate = info
	a.upMu.Unlock()

	res.Available = true
	res.Status = UpdateStatusAvailable
	res.Version = info.Version
	res.URL = url
	res.SHA256 = sha
	res.Reason = "Update " + info.Version + " is available. " + why
	return res
}

// Update check outcome codes. Stable strings — the UI and support tooling
// match on these, so they must not be reworded casually.
const (
	UpdateStatusAvailable           = "available"
	UpdateStatusUpToDate            = "up_to_date"
	UpdateStatusNoRelease           = "no_release"
	UpdateStatusUnreachable         = "unreachable"
	UpdateStatusUninstrumentedBuild = "uninstrumented_build"
	UpdateStatusNotActivated        = "not_activated"
	UpdateStatusNoAsset             = "no_asset"
)

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
		return OpResult{Success: false, Message: "Already running version " + a.version + " — nothing to do"}
	}
	// Refuse anything that is not STRICTLY newer. A string-equality check is not
	// enough: if update_config is ever left pointing at an older release (a
	// rollback, a typo, a stale row), we must not silently downgrade the client.
	// There is no server-driven downgrade in this system, so guarding here
	// makes that class of mistake harmless.
	if !updater.IsNewer(a.lastUpdate.Version, a.version) {
		advertised := a.lastUpdate.Version
		a.lastUpdate = nil
		log.Printf("ApplyUpdate: ignoring non-newer version %s (running %s)", advertised, a.version)
		return OpResult{
			Success: false,
			Message: "The server is advertising " + advertised + ", which is not newer than the version you are running (" + a.version + "). No update applied.",
		}
	}
	info := *a.lastUpdate // copy — the heartbeat may replace the pointer
	a.updating = true

	// An update we cannot fetch for this platform must fail with an explanation
	// rather than starting a download that is guaranteed to 404.
	if a.lastUpdate.PlatformDownloadURL() == "" {
		a.lastUpdate = nil
		a.updating = false
		return OpResult{Success: false, Message: "Update " + info.Version + " has no build published for this platform (" + a.build.Platform + ")."}
	}
	if a.lastUpdate.PlatformSHA256() == "" {
		a.lastUpdate = nil
		a.updating = false
		return OpResult{Success: false, Message: "Update " + info.Version + " has no checksum published for this platform (" + a.build.Platform + "), so it cannot be verified."}
	}

	go func() {
		emit := func(phase, message string) {
			wailsruntime.EventsEmit(a.ctx, "update:status", map[string]interface{}{
				"phase":   phase,
				"message": message,
				// Carry the version on every progress event so the UI (and any
				// log a student screenshots) always names what is being
				// installed, instead of showing bare phase text.
				"from": a.version,
				"to":   info.Version,
			})
		}

		// Downloads can take minutes on school WiFi — never block a 10s RPC
		// timeout. The UI stays responsive and is driven by update:status.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		log.Printf("ApplyUpdate: %s -> %s (%s)", a.version, info.Version, a.build.Platform)
		emit("downloading", "Downloading "+info.Version+"…")
		if err := a.up.PerformUpdate(ctx, info); err != nil {
			a.upMu.Lock()
			a.updating = false
			a.upMu.Unlock()
			log.Printf("ApplyUpdate: %s -> %s failed: %v", a.version, info.Version, err)
			emit("failed", "Update to "+info.Version+" failed: "+err.Error())
			return
		}

		log.Printf("ApplyUpdate: applied %s -> %s — restarting", a.version, info.Version)
		emit("applied", "Updated to "+info.Version+" — restarting…")

		// Give the webview a moment to paint the "Restarting…" state, then quit
		// so the forked new binary takes over (see updater.PerformUpdate).
		time.Sleep(1500 * time.Millisecond)
		wailsruntime.Quit(a.ctx)
	}()

	return OpResult{Success: true, Message: "Updating " + a.version + " → " + info.Version}
}

// ── Diagnostics ──

// GetDiagnostics returns a plain-text support report with no PII.
//
// This is the primary debugging artefact in the field: a student copies it into
// a message. It therefore has to answer "which build is this, is that build
// trustworthy, and what did it actually load" — questions the previous version
// could not answer, because it omitted build provenance, the sing-box binary it
// resolved, the log location, and the last update outcome.
func (a *App) GetDiagnostics() string {
	if msg := a.notReady(); msg != "" {
		return fmt.Sprintf("Locus Diagnostics\n===================\n%s\n\nApp not ready: %s\n", a.build.Describe(), msg)
	}
	state := a.store.GetData()
	mgrState := a.mgr.State()

	// Build provenance. An uninstrumented build must be obvious here: the
	// version string alone cannot be trusted for support decisions, and the
	// whole point of the report is to be trustworthy.
	buildLine := "Build:       " + a.build.Describe()
	if w := a.build.Warning(); w != "" {
		buildLine += "\n             ⚠ " + w
	}
	if a.build.Commit != "" {
		buildLine += "\nCommit:      " + a.build.Commit
	}
	if a.build.BuiltAt != "" {
		buildLine += "\nCommit time: " + a.build.BuiltAt
	}

	// Which engine binary did we actually resolve, and does it still exist?
	// "sing-box not found" is a top-3 field failure and was previously
	// invisible unless the student happened to read the log.
	singBox := a.singBoxPath
	if singBox == "" {
		singBox = "(NOT FOUND — the tunnel cannot start)"
	} else if _, err := os.Stat(singBox); err != nil {
		singBox += " (MISSING: " + err.Error() + ")"
	}

	// The last update outcome, so "it never updates" is diagnosable from the
	// report alone.
	updateLine := "not checked yet"
	if a.lastUpdateStatus != "" {
		updateLine = a.lastUpdateStatus
		if a.lastUpdateDetail != "" {
			updateLine += " — " + a.lastUpdateDetail
		}
	}

	report := fmt.Sprintf(`Locus Diagnostics
===================
%s

OS:          %s
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
sing-box:     %s
Config:       %s

Last update check: %s

Leftover engines:
%s

Log file:     %s
Reported:     %s
`,
		buildLine,
		goRuntime.GOOS+"/"+goRuntime.GOARCH,
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
		singBox,
		a.configPath,
		updateLine,
		leftoverLines(a.mgr.ForeignEngines()),
		logFilePath(),
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

// logFilePath returns the path of the log file main.go writes to, or a short
// explanation when it cannot be determined. Support asks students for this file
// constantly, and until now nothing in the app ever named it.
func logFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "(unknown — could not resolve the config directory)"
	}
	return filepath.Join(dir, "locus", "locus.log")
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

// setupSystemTray configures window behaviour and starts the optional tray.
//
// NOTE: Wails v2 has NO system tray API and cannot intercept "close -> hide",
// so closing the window still quits the app (no minimize-to-tray). A real tray
// icon (live status + Connect/Disconnect/Open/Quit) is provided by internal/tray
// but is OPT-IN via LOCUS_TRAY=1 and OFF by default (see internal/tray for why).
func (a *App) setupSystemTray() {
	// Dark background matches the UI theme (#06130C)
	wailsruntime.WindowSetBackgroundColour(a.ctx, 6, 19, 12, 255)

	// Dormant hooks — frontend/bridge can emit these to show/quit the window.
	wailsruntime.EventsOn(a.ctx, "tray:show", func(data ...interface{}) {
		wailsruntime.WindowShow(a.ctx)
	})
	wailsruntime.EventsOn(a.ctx, "tray:quit", func(data ...interface{}) {
		wailsruntime.Quit(a.ctx)
	})

	wailsruntime.LogInfo(a.ctx, "Window background set; tray hooks registered")

	// Optional system tray: only when the operator opts in. Off by default so an
	// unvalidated native tray can never regress a normal release. Runs its own
	// goroutine; any panic there is recovered inside internal/tray.
	if loc, ok := os.LookupEnv("LOCUS_TRAY"); ok && loc != "" && loc != "0" && loc != "false" {
		a.trayCtrl = tray.Start(tray.Actions{
			Toggle: func() string {
				if a.connected {
					return a.disconnect().Message
				}
				res := a.Connect()
				return res.Message
			},
			OpenWindow: func() {
				wailsruntime.WindowShow(a.ctx)
			},
			Quit: func() {
				wailsruntime.Quit(a.ctx)
			},
		})
		if a.trayCtrl != nil {
			a.trayCtrl.SetTier(a.tier)
			a.trayCtrl.SetConnected(a.connected)
		}
		wailsruntime.LogInfo(a.ctx, "System tray enabled (LOCUS_TRAY). Validate on this OS before shipping.")
	}
}
