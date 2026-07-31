// MyVPN Desktop App — Wails Edition
//
// A single-binary desktop VPN client for school networks.
// All backend logic (activation, heartbeat, storage, updater) lives in
// internal/ packages and is wrapped by this Wails App struct.
//
// Build:
//   wails build   (produces a single native binary)
//   wails dev     (hot-reload development)
//   go build .    (works too — frontend/dist must exist first, see CI workflow)

package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

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
