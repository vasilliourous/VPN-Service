// Locus Desktop App — Wails Edition
//
// A single-binary desktop VPN client for school networks.
// All backend logic (activation, heartbeat, storage, updater) lives in
// internal/ packages and is wrapped by this Wails App struct.
//
// Build:
//   wails build                 (recommended — the Wails CLI adds the required
//                                desktop,production build tags automatically)
//   go build -tags "frontend desktop production" .
//                               (manual build — frontend/dist must exist, and
//                                desktop+production are REQUIRED Wails tags:
//                                without them the binary is the stub app that
//                                shows the "correct build tags" error.
//                                On Linux, add webkit2_41 when building against
//                                WebKitGTK 4.1, e.g. Ubuntu 24.04+)
//   go build .                  (compiles without frontend/dist — stub assets, no UI)
//   wails dev                   (hot-reload — uses Vite dev server, stub is fine)
//
// The `assets` var lives in assets_embed.go (build tag `frontend`) and
// assets_stub.go (default). See those files.

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"locus/internal/buildinfo"
	"locus/internal/updater"
)

// version is the runtime version reported to the UI and the updater.
//
// The canonical value lives in <repo>/v5/VERSION (single source of truth).
// Official builds inject it exactly once via -ldflags (see the Makefile, which
// reads v5/VERSION and passes "-X main.version=$(VERSION)" to every target):
//
//	make build
//
// or, for a scratch build / CI:
//
//	go build -ldflags "-X main.version=<VERSION from v5/VERSION>" .
//
// The fallback below is deliberately kept in sync with v5/VERSION so an
// uninstrumented `go build` reports something meaningful — bump v5/VERSION
// (not this literal) before a release.
var version = "2.2.7"

// Windows executables carry an embedded manifest (rsrc_windows_amd64.syso /
// rsrc_windows_arm64.syso) that sets requestedExecutionLevel="requireAdministrator" —
// the app must run elevated because sing-box needs a TUN interface. Regenerate
// them from this module's root with:
//
//	go generate -tags windows
//
// (uses github.com/tc-hib/go-winres, pure Go — no windres/MinGW required).
//
// If the .syso files are missing, Go builds a Windows exe with the default
// asInvoker manifest and Connect() falls back to a runtime UAC relaunch.
//
//go:generate go run github.com/tc-hib/go-winres@latest simply --admin --manifest gui --arch amd64,arm64 --out rsrc --product-name Locus --file-description "Locus secure school VPN" --product-version 2.2.7 --file-version 2.2.7

func main() {
	// Route Go's stderr (panic traces) and the standard logger to a file so
	// failures are never invisible in this GUI app (Windows GUI builds have
	// no console — without this, a crash looks like "nothing happened").
	if logFile, err := openLogFile(); err == nil {
		defer func() { _ = logFile.Close() }()
		os.Stderr = logFile
		log.SetOutput(logFile)
		// Log full build provenance as the FIRST line. Support logs arrive as
		// screenshots of this file, so the very first thing in it must identify
		// the build — including whether the version was actually injected by
		// the release pipeline, which determines whether an update decision
		// based on it means anything.
		log.Println(buildinfo.Detect(version).Describe())
	}

	// ── `--revert`: manual rollback to the pre-update binary ──
	// Only useful immediately after a bad self-update. Performed before the
	// GUI is launched / any engine starts. See updater.CheckOnStartup(true).
	if hasArg("--revert") {
		// CheckOnStartup locates the current executable internally, so no
		// os.Executable() call is needed here.
		reverted, err := updater.CheckOnStartup(true)
		switch {
		case err != nil:
			log.Printf("Revert failed: %v", err)
		case !reverted:
			log.Printf("Revert: no backup to restore (nothing to do)")
		default:
			log.Printf("Revert: reinstated previous Locus binary")
		}
		return // never launch the interactive GUI for a --revert invocation
	}

	// ── Handoff after a self-update ──
	//
	// The updater forks this process to take over from the copy it just
	// replaced, then the OLD process quits. Both are alive for a short window,
	// and because the fork is what happens *first*, the new window used to
	// appear while the old one was still on screen — the user saw two Locus
	// windows, the stale one lingering until the parent finished tearing down.
	//
	// The fork passes --handoff so the new process knows it is a successor
	// rather than a fresh launch: it waits for the predecessor to exit before
	// creating its window, so exactly one window is ever visible. The wait is
	// bounded and the flag is absent on a normal launch, so nothing is added to
	// the ordinary startup path.
	if hasArg(updater.HandoffFlag) {
		waitForPredecessor()
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Locus",
		Width:     480,
		Height:    700,
		MinWidth:  380,
		MinHeight: 500,

		// NOTE: the window is shown on launch. Wails v2.9 has no system tray
		// API and this app has no tray icon, so StartHidden would leave the
		// app permanently invisible ("nothing happened" when launched).

		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.Startup,
		OnShutdown: app.Shutdown,
		OnDomReady: func(ctx context.Context) {
			// Proves the WebView2 loaded the embedded page — if this line is
			// missing from locus.log, the webview never finished loading.
			log.Printf("DOM ready — webview loaded the UI")
		},
		Bind: []interface{}{
			app,
		},

		// ── Platform-specific ──

		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		Linux: &linux.Options{},
	})

	if err != nil {
		// Never die invisibly — the error is written to locus.log (see
		// openLogFile) AND shown in a native message box on Windows.
		showFatalError("Locus failed to start: " + err.Error())
		log.Fatalf("Locus failed to start: %v", err)
	}
}

// hasArg reports whether any os.Arg equals want (exact match, no value).
func hasArg(want string) bool {
	for _, a := range os.Args[1:] {
		if a == want {
			return true
		}
	}
	return false
}

// waitForPredecessor blocks briefly so the process we replaced has exited
// before this one puts its window on screen.
//
// The delay is fixed rather than a real synchronisation primitive on purpose.
// There is no shared handle to wait on: the predecessor is a different process
// that has already forked us and is on its way out, and the thing we are
// avoiding is purely cosmetic (two windows visible for a moment). Anything more
// elaborate — a named mutex, a pipe the parent closes — would add a failure
// mode to the update path in exchange for tightening a pause the user cannot
// perceive. If the predecessor is somehow still up after this, the window still
// appears; nothing is blocked indefinitely.
//
// 1200ms is chosen to sit just inside the parent's own 1500ms quit delay, so
// the child settles immediately after the parent goes.
func waitForPredecessor() {
	const handoffDelay = 1200 * time.Millisecond
	log.Printf("Handoff: waiting %s for the previous process to exit", handoffDelay)
	time.Sleep(handoffDelay)
}

// openLogFile opens (appends to) a log file in the platform config dir so
// panics and fatal errors are captured on GUI builds where there is no console.
// The file is truncated when it exceeds 1MB to avoid unbounded growth.
func openLogFile() (*os.File, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	appDir := filepath.Join(dir, "locus")
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(appDir, "locus.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > 1<<20 {
		_ = os.Remove(logPath) // rotate: keep the log small
	}
	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}
