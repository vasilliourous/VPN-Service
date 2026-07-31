// MyVPN Desktop App — Wails Edition
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
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// version is set at build time via:
//
//	go build -ldflags "-X main.version=2.0.0"
var version = "2.0.0"

func main() {
	// Route Go's stderr (panic traces) and the standard logger to a file so
	// failures are never invisible in this GUI app (Windows GUI builds have
	// no console — without this, a crash looks like "nothing happened").
	if logFile, err := openLogFile(); err == nil {
		defer func() { _ = logFile.Close() }()
		os.Stderr = logFile
		log.SetOutput(logFile)
		log.Printf("MyVPN starting (version %s)", version)
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "MyVPN",
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
		// Never die invisibly — the error is also written to myvpn.log
		// (see openLogFile) so GUI builds without a console still produce
		// a diagnosis.
		log.Fatalf("MyVPN failed to start: %v", err)
	}
}

// openLogFile opens (appends to) a log file in the platform config dir so
// panics and fatal errors are captured on GUI builds where there is no console.
// The file is truncated when it exceeds 1MB to avoid unbounded growth.
func openLogFile() (*os.File, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	appDir := filepath.Join(dir, "myvpn")
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(appDir, "myvpn.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > 1<<20 {
		_ = os.Remove(logPath) // rotate: keep the log small
	}
	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}
