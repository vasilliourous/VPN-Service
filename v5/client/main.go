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
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "MyVPN",
		Width:     480,
		Height:    700,
		MinWidth:  380,
		MinHeight: 500,

		// Start hidden so the window only appears when the user opens it
		// from the dock/taskbar/tray.
		StartHidden: true,

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
		log.Fatalf("MyVPN failed to start: %v", err)
	}
}
