// Package gui provides the MyVPN desktop application interface using Fyne v2.
//
// Layout:
//   - Activation screen: code input, tier info, activate button
//   - Main screen: connection status, connect/disconnect, timer, speed
//   - System tray: background operation, quick controls
//   - Diagnostics: support report generation
//
// Hardening: panic recovery in goroutines, async error handling with user notification,
// graceful shutdown on context cancellation, input sanitization, state validation.
package gui

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"myvpn/internal/activation"
	"myvpn/internal/heartbeat"
	"myvpn/internal/manager"
	"myvpn/internal/storage"
	"myvpn/internal/updater"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// Recovery handler for GUI goroutines.
func guiSafe(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("GUI panic recovered: %v\n%s", r, debug.Stack())
		}
	}()
	fn()
}

// App represents the MyVPN desktop application.
type App struct {
	fyneApp    fyne.App
	mainWindow fyne.Window
	ctx        context.Context
	cancel     context.CancelFunc

	// Core components
	store       *storage.Store
	activation  *activation.Client
	tunnel      *manager.Manager
	heartbeat   *heartbeat.Heartbeat
	updater     *updater.Updater
	crashDetect *updater.CrashDetector

	// State
	version   string
	hubURL    string
	connected bool
	activated bool

	// UI elements
	statusIcon   *canvas.Circle
	statusLabel  *widget.Label
	tierLabel    *widget.Label
	codeEntry    *widget.Entry
	connectBtn   *widget.Button
	timerLabel   *widget.Label
	speedLabel   *widget.Label
	mainContent  *fyne.Container
	activateContent *fyne.Container

	// Tray (optional — not all platforms/versions support it)
}

// Run launches the MyVPN desktop application.
func Run(hubURL, version string) {
	RunWithContext(context.Background(), hubURL, version)
}

// RunWithContext launches the desktop application with context support.
func RunWithContext(ctx context.Context, hubURL, version string) {
	guiSafe(func() {
		appCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		a := &App{
			ctx:     appCtx,
			cancel:  cancel,
			hubURL:  hubURL,
			version: version,
		}
		a.run()
	})
}

func (a *App) run() {
	// Initialize storage
	store, err := storage.New("myvpn")
	if err != nil {
		log.Printf("FATAL: Cannot initialize storage: %v", err)
		return
	}
	a.store = store

	// Initialize activation client
	a.activation = activation.NewClient(a.hubURL)

	// Initialize manager
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("FATAL: Cannot determine executable path: %v", err)
		return
	}
	appDir := filepath.Dir(execPath)

	// Find sing-box binary
	singBoxPaths := []string{
		filepath.Join(appDir, "sing-box"),
		filepath.Join(appDir, "sing-box.exe"),
		"/usr/local/bin/sing-box",
	}
	singBoxPath := ""
	for _, p := range singBoxPaths {
		if _, err := os.Stat(p); err == nil {
			singBoxPath = p
			break
		}
	}
	if singBoxPath == "" {
		log.Println("WARNING: sing-box binary not found, tunnel will not work")
	}

	configPath := filepath.Join(appDir, "sing-box-config.json")
	a.tunnel = manager.NewManager(singBoxPath, configPath)

	// Initialize updater (only if we can find app dir)
	if appDir != "" {
		binaryName := filepath.Base(execPath)
		a.updater = updater.New(appDir, binaryName, a.version)
		a.crashDetect = updater.NewCrashDetector(appDir, binaryName)
	}

	// Create the Fyne app
	a.fyneApp = app.NewWithID("com.myvpn.app")
	a.mainWindow = a.fyneApp.NewWindow("MyVPN")

	// Build UI
	a.buildUI()

	// Check for update recovery
	a.checkUpdateRecovery()

	// Restore previous session if activated
	state := a.store.GetData()
	if state.Activated {
		a.activated = true
		a.showMainScreen()
		// Restore connection status
		if a.tunnel != nil && a.tunnel.IsRunning() {
			a.connected = true
			a.updateStatusUI()
		}
	} else {
		a.showActivationScreen()
	}

	// Set up system tray
	a.setupTray()

	// Set close handler
	a.mainWindow.SetCloseIntercept(func() {
		a.shutdown()
	})

	// Listen for context cancellation → graceful shutdown
	go func() {
		<-a.ctx.Done()
		log.Println("Context cancelled, closing application...")
		a.mainWindow.Close()
	}()

	// Run the app (blocking until window closed or context cancelled)
	a.mainWindow.ShowAndRun()
}

// buildUI constructs all UI elements.
func (a *App) buildUI() {
	// Status icon
	a.statusIcon = canvas.NewCircle(color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF})
	a.statusIcon.Resize(fyne.NewSize(16, 16))

	// Status label
	a.statusLabel = widget.NewLabel("Disconnected")

	// Tier label (hidden until activated)
	a.tierLabel = widget.NewLabel("")

	// Connect button
	a.connectBtn = widget.NewButton("Connect", func() {
		guiSafe(func() {
			a.onConnect()
		})
	})

	// Timer label
	a.timerLabel = widget.NewLabel("")
	a.timerLabel.Hide()

	// Speed label
	a.speedLabel = widget.NewLabel("")
	a.speedLabel.Hide()
}

func (a *App) showActivationScreen() {
	// Welcome message
	welcome := widget.NewLabelWithStyle(
		"Welcome to MyVPN\nEnter your activation code to get started.",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	// Code entry
	a.codeEntry = widget.NewEntry()
	a.codeEntry.SetPlaceHolder("MYVPN-XXXX-XXXX-XXXX-C")

	// Activate button
	activateBtn := widget.NewButton("Activate", func() {
		guiSafe(func() {
			a.onActivate()
		})
	})

	// Build activation layout
	content := container.NewVBox(
		welcome,
		layout.NewSpacer(),
		a.codeEntry,
		activateBtn,
		layout.NewSpacer(),
	)

	a.mainWindow.SetContent(content)
	a.mainWindow.Resize(fyne.NewSize(400, 300))
}

func (a *App) showMainScreen() {
	// Status row
	statusRow := container.NewHBox(
		a.statusIcon,
		a.statusLabel,
		a.tierLabel,
	)

	// Controls
	controls := container.NewVBox(
		a.connectBtn,
		a.timerLabel,
		a.speedLabel,
	)

	// Settings/Diagnostics buttons
	settingsBtn := widget.NewButton("Settings", func() {
		guiSafe(func() {
			a.showSettings()
		})
	})
	diagBtn := widget.NewButton("Diagnostics", func() {
		guiSafe(func() {
			a.exportDiagnostics()
		})
	})
	quitBtn := widget.NewButton("Quit", func() {
		guiSafe(func() {
			a.shutdown()
		})
	})

	btnRow := container.NewHBox(settingsBtn, diagBtn, quitBtn)

	// Main layout
	content := container.NewVBox(
		statusRow,
		layout.NewSpacer(),
		controls,
		layout.NewSpacer(),
		btnRow,
	)

	a.mainWindow.SetContent(content)
	a.mainWindow.Resize(fyne.NewSize(400, 350))
}

func (a *App) setupTray() {
	// System tray setup — available on most desktop platforms
	// Use type assertion to check if the app supports system tray
	menu := fyne.NewMenu("MyVPN",
		fyne.NewMenuItem("Show", func() {
			a.mainWindow.Show()
		}),
		fyne.NewMenuItem("Disconnect", func() {
			guiSafe(func() {
				a.onDisconnect()
			})
		}),
		fyne.NewMenuItem("Quit", func() {
			a.shutdown()
		}),
	)

	// Attempt to set the system tray menu (silent if unavailable)
	if appWithTray, ok := interface{}(a.fyneApp).(interface{ SetSystemTrayMenu(*fyne.Menu) }); ok {
		appWithTray.SetSystemTrayMenu(menu)
	}
}

// ── Actions ──

func (a *App) onActivate() {
	code := a.codeEntry.Text
	if code == "" {
		dialog.ShowError(fmt.Errorf("Please enter an activation code"), a.mainWindow)
		return
	}

	// Client-side validation first
	if err := activation.ValidateCode(code); err != nil {
		dialog.ShowError(err, a.mainWindow)
		return
	}

	// Show progress
	progress := dialog.NewProgress("Activating", "Contacting server...", a.mainWindow)
	progress.Show()

	// Perform activation asynchronously
	go guiSafe(func() {
		fingerprint := activation.GenerateFingerprint()

		resp, err := a.activation.Activate(a.ctx, code, fingerprint)
		if err != nil {
			progress.Hide()
			dialog.ShowError(fmt.Errorf("Activation failed: %w", err), a.mainWindow)
			return
		}

		// Save activation
		var sCfg *storage.ServerConfig
		if resp.ServerCfg != nil {
			sCfg = &storage.ServerConfig{
				Server:     resp.ServerCfg.Server,
				ServerPort: resp.ServerCfg.ServerPort,
				Password:   resp.ServerCfg.Password,
				Method:     resp.ServerCfg.Method,
			}
		}

		if err := a.store.SetActivation(code, resp.Tier, fingerprint, sCfg, resp.UDPRelay); err != nil {
			progress.Hide()
			dialog.ShowError(fmt.Errorf("Cannot save activation: %w", err), a.mainWindow)
			return
		}

		progress.Hide()

		a.activated = true
		a.tierLabel.SetText("· " + a.capitalize(resp.Tier))

		dialog.ShowInformation("Activated",
			fmt.Sprintf("Welcome! You're on the %s plan.", resp.Tier),
			a.mainWindow)

		a.showMainScreen()
	})
}

func (a *App) onConnect() {
	if a.connected {
		return
	}

	state := a.store.GetData()
	if !state.Activated || state.ServerConfig == nil {
		dialog.ShowError(fmt.Errorf("Not activated. Please enter an activation code."), a.mainWindow)
		return
	}

	// Start tunnel
	cfg := manager.Config{
		Server:     state.ServerConfig.Server,
		ServerPort: state.ServerConfig.ServerPort,
		Password:   state.ServerConfig.Password,
		Method:     state.ServerConfig.Method,
		TierName:   state.Tier,
		UDPRelay:   state.UDPRelay,
		HubURL:     a.hubURL,
	}

	if err := a.tunnel.Start(a.ctx, cfg); err != nil {
		dialog.ShowError(fmt.Errorf("Connection failed: %w", err), a.mainWindow)
		return
	}

	a.connected = true
	a.updateStatusUI()

	// Start heartbeat
	a.startHeartbeat()
}

func (a *App) onDisconnect() {
	if !a.connected {
		return
	}

	if a.heartbeat != nil {
		a.heartbeat.Stop()
	}

	if err := a.tunnel.Stop(); err != nil {
		log.Printf("Disconnect error: %v", err)
	}

	a.connected = false
	a.updateStatusUI()
}

func (a *App) startHeartbeat() {
	state := a.store.GetData()
	if !state.Activated {
		return
	}

	a.heartbeat = heartbeat.New(a.hubURL, state.Code, state.DeviceFingerprint, func(result heartbeat.Result) {
		guiSafe(func() {
			if result.Success {
				// Update stored heartbeat
				if err := a.store.SetHeartbeat(time.Now().Unix()); err != nil {
					log.Printf("Failed to save heartbeat: %v", err)
				}

				if result.Resp == nil {
					return
				}

				// ── Check for server-driven config refresh ──
				// The server can push an updated ServerConfig at any time
				// (e.g., password rotation, server migration). If the config
				// changed, restart the tunnel with the new parameters.
				if result.Resp.ServerConfig != nil {
					curState := a.store.GetData()
					cfgChanged := curState.ServerConfig == nil ||
						curState.ServerConfig.Server != result.Resp.ServerConfig.Server ||
						curState.ServerConfig.ServerPort != result.Resp.ServerConfig.ServerPort ||
						curState.ServerConfig.Password != result.Resp.ServerConfig.Password ||
						curState.ServerConfig.Method != result.Resp.ServerConfig.Method ||
						curState.UDPRelay != result.Resp.UDPRelay

					if cfgChanged {
						log.Println("Server config changed via heartbeat — reconnecting with new config")
						newCfg := &storage.ServerConfig{
							Server:     result.Resp.ServerConfig.Server,
							ServerPort: result.Resp.ServerConfig.ServerPort,
							Password:   result.Resp.ServerConfig.Password,
							Method:     result.Resp.ServerConfig.Method,
						}
						// Persist new config
						if err := a.store.SetActivation(curState.Code, result.Resp.Tier,
							curState.DeviceFingerprint, newCfg, result.Resp.UDPRelay); err != nil {
							log.Printf("Failed to save updated config: %v", err)
							return
						}
					}
				}

				// Update tier label if changed
				if result.Resp.Tier != "" {
					curState := a.store.GetData()
					if curState.Tier != result.Resp.Tier {
						a.tierLabel.SetText("· " + a.capitalize(result.Resp.Tier))
					}
				}

				// ── Check for staged updates ──
				if result.Resp.UpdateAvailable != "" &&
					result.Resp.UpdateURL != "" && result.Resp.UpdateSHA256 != "" {

					updateInfo := updater.UpdateInfo{
						Version:              result.Resp.UpdateAvailable,
						SHA256:               result.Resp.UpdateSHA256,
						DownloadURL:          result.Resp.UpdateURL,
						DownloadURLLinux:     result.Resp.UpdateLinux,
						DownloadURLWindows:   result.Resp.UpdateWindows,
						DownloadURLMacOSIntel: result.Resp.UpdateMacOSIntel,
						DownloadURLMacOSARM:  result.Resp.UpdateMacOSARM,
					}

					a.promptUpdate(updateInfo)
				}
			} else {
				// Heartbeat failed — increment failures
				if err := a.store.SetHeartbeatFailure(time.Now().Unix()); err != nil {
					log.Printf("Failed to save heartbeat failure: %v", err)
				}

				// Check grace period
				state := a.store.GetData()
				remaining := a.heartbeat.RemainingGracePeriod(state.LastHeartbeatOK)
				if remaining <= 0 {
					log.Println("Grace period expired — disconnecting")
					a.onDisconnect()
				}
			}
		})
	})

	a.heartbeat.Start()
}

func (a *App) promptUpdate(info updater.UpdateInfo) {
	// Only prompt once per version
	state := a.store.GetData()
	if state.UpdateVersion == info.Version {
		return
	}

	confirm := dialog.NewConfirm(
		"Update Available",
		fmt.Sprintf("Version %s is available. Would you like to update?", info.Version),
		func(ok bool) {
			if !ok {
				return
			}

			// Perform update asynchronously
			go guiSafe(func() {
				if err := a.updater.PerformUpdate(a.ctx, info); err != nil {
					dialog.ShowError(fmt.Errorf("Update failed: %w", err), a.mainWindow)
					return
				}
				// If we reach here, the update succeeded and new binary was forked
				// This process should exit
				os.Exit(0)
			})
		},
		a.mainWindow,
	)
	confirm.Show()
}

func (a *App) checkUpdateRecovery() {
	if a.crashDetect == nil {
		return
	}

	rolledBack, err := a.crashDetect.HandleCrashedUpdate()
	if err != nil {
		log.Printf("Update recovery check failed: %v", err)
		return
	}

	if rolledBack {
		dialog.ShowInformation("Update Reverted",
			"The previous update did not complete successfully and has been reverted. "+
				"You can try updating again later.",
			a.mainWindow)
	}
}

// ── UI updates ──

func (a *App) updateStatusUI() {
	if a.connected {
		a.statusIcon.FillColor = color.RGBA{G: 0x22, A: 0xFF} // Green
		a.statusLabel.SetText("Connected")
		a.connectBtn.SetText("Disconnect")
		a.connectBtn.OnTapped = func() {
			guiSafe(func() { a.onDisconnect() })
		}
		a.timerLabel.Show()
		a.speedLabel.Show()
	} else {
		a.statusIcon.FillColor = color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF} // Grey
		a.statusLabel.SetText("Disconnected")
		a.connectBtn.SetText("Connect")
		a.connectBtn.OnTapped = func() {
			guiSafe(func() { a.onConnect() })
		}
		a.timerLabel.Hide()
		a.speedLabel.Hide()
	}
	a.statusIcon.Refresh()
}

// ── Dialogs ──

func (a *App) showSettings() {
	dialog.ShowInformation("Settings",
		"Hub URL: "+a.hubURL+"\nVersion: "+a.version,
		a.mainWindow)
}

func (a *App) exportDiagnostics() {
	diag := collectDiagnostics(a.store, a.version, a.connected)
	dialog.ShowInformation("Diagnostics Exported",
		"Diagnostics information:\n\n"+diag,
		a.mainWindow)
}

func (a *App) shutdown() {
	a.onDisconnect()

	if a.store != nil {
		if err := a.store.SetVersion(a.version); err != nil {
			log.Printf("Failed to save version: %v", err)
		}
	}

	if a.cancel != nil {
		a.cancel()
	}

	a.fyneApp.Quit()
}

// capitalize returns a string with the first letter uppercased.
func (a *App) capitalize(s string) string {
	if s == "" {
		return ""
	}
	// Use strings.ToUpper for the first rune (safe for all valid tier names)
	if len(s) == 1 {
		return strings.ToUpper(s)
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
