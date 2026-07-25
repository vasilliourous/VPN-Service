// Package gui provides the MyVPN desktop application interface using Fyne v2.
//
// Layout:
//   - Activation screen: code input, tier info, activate button
//   - Main screen: connection status, connect/disconnect, timer, speed
//   - System tray: background operation, quick controls
//   - Diagnostics: support report generation
package gui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
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

// App represents the MyVPN desktop application.
type App struct {
	fyneApp    fyne.App
	mainWindow fyne.Window

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

	// Tray
	trayIcon fyne.TrayIcon
}

// Run launches the MyVPN GUI application.
func Run(hubURL, version string) {
	a := &App{
		version:  version,
		hubURL:   hubURL,
		activated: false,
		connected: false,
	}

	// Initialize storage
	var err error
	a.store, err = storage.New("myvpn")
	if err != nil {
		fyne.LogError("Failed to initialize storage", err)
		return
	}

	// Check stored activation state
	data := a.store.GetData()
	a.activated = data.Activated

	// Create Fyne app
	a.fyneApp = app.NewWithID("com.myvpn.desktop")
	a.mainWindow = a.fyneApp.NewWindow("MyVPN")

	// Create tray menu
	a.setupTray()

	// Build UI
	a.buildUI()

	// Handle startup
	if a.activated {
		a.showMainScreen()
		a.startServices(data)
	} else {
		a.showActivationScreen()
	}

	// Check for update recovery
	a.checkUpdateRecovery()

	a.mainWindow.Resize(fyne.NewSize(400, 500))
	a.mainWindow.SetFixedSize(true)
	a.mainWindow.ShowAndRun()
}

// buildUI creates the application interface.
func (a *App) buildUI() {
	// Status indicator
	a.statusIcon = canvas.NewCircle(color.RGBA{R: 128, G: 128, B: 128, A: 255})
	a.statusIcon.Resize(fyne.NewSize(16, 16))

	a.statusLabel = widget.NewLabel("Disconnected")
	a.tierLabel = widget.NewLabel("")

	// Header
	header := container.NewVBox(
		widget.NewLabelWithStyle("MyVPN", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Secure School VPN", fyne.TextAlignCenter, fyne.TextStyle{}),
	)

	// Status row
	statusRow := container.NewHBox(
		layout.NewSpacer(),
		a.statusIcon,
		a.statusLabel,
		layout.NewSpacer(),
	)

	// Timer and speed (for main screen)
	a.timerLabel = widget.NewLabel("")
	a.speedLabel = widget.NewLabel("")

	// ── Activation screen ──
	titleLabel := widget.NewLabelWithStyle("Activate MyVPN", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	subtitleLabel := widget.NewLabel("Enter your activation code")
	subtitleLabel.Alignment = fyne.TextAlignCenter

	a.codeEntry = widget.NewEntry()
	a.codeEntry.SetPlaceHolder("MYVPN-XXXX-XXXX-XXXX-C")
	a.codeEntry.Validator = func(s string) error {
		return activation.ValidateCode(s)
	}

	activateBtn := widget.NewButton("Activate", a.onActivate)
	activateBtn.Importance = widget.HighImportance

	a.activateContent = container.NewVBox(
		layout.NewSpacer(),
		titleLabel,
		subtitleLabel,
		widget.NewLabel(""),
		a.codeEntry,
		widget.NewLabel(""),
		activateBtn,
		layout.NewSpacer(),
	)

	// ── Main screen ──
	a.connectBtn = widget.NewButton("Connect", a.onConnect)
	a.connectBtn.Importance = widget.HighImportance

	disconnectBtn := widget.NewButton("Disconnect", a.onDisconnect)

	// Tier badge
	tierRow := container.NewHBox(
		layout.NewSpacer(),
		widget.NewLabel("Tier:"),
		a.tierLabel,
		layout.NewSpacer(),
	)

	a.mainContent = container.NewVBox(
		layout.NewSpacer(),
		tierRow,
		statusRow,
		a.timerLabel,
		a.speedLabel,
		widget.NewLabel(""),
		a.connectBtn,
		disconnectBtn,
		layout.NewSpacer(),
	)

	// Window content
	content := container.NewBorder(
		header,
		nil, nil, nil,
		container.NewStack(a.activateContent, a.mainContent),
	)

	a.mainWindow.SetContent(content)

	// Menu
	settingsItem := fyne.NewMenuItem("Settings", func() {
		a.showSettings()
	})
	diagItem := fyne.NewMenuItem("Export Diagnostics", func() {
		a.exportDiagnostics()
	})
	aboutItem := fyne.NewMenuItem("About", func() {
		dialog.ShowInformation("About MyVPN",
			fmt.Sprintf("MyVPN v%s\n\nSecure VPN for school networks\nBuilt with Go + Fyne", a.version),
			a.mainWindow)
	})
	quitItem := fyne.NewMenuItem("Quit", func() {
		a.shutdown()
	})

	menu := fyne.NewMainMenu(
		fyne.NewMenu("File", settingsItem, diagItem, quitItem),
		fyne.NewMenu("Help", aboutItem),
	)
	a.mainWindow.SetMainMenu(menu)
}

// setupTray creates the system tray integration.
func (a *App) setupTray() {
	toggleLabel := "Show"
	if a.activated {
		toggleLabel = "Show MyVPN"
	}

	showItem := fyne.NewMenuItem(toggleLabel, func() {
		a.mainWindow.Show()
	})

	connectItem := fyne.NewMenuItem("Connect", func() {
		if !a.connected {
			a.onConnect()
		}
	})

	quitItem := fyne.NewMenuItem("Quit", func() {
		a.shutdown()
	})

	menu := fyne.NewMenu("MyVPN", showItem, connectItem, quitItem)
	a.mainWindow.SetCloseIntercept(func() {
		a.mainWindow.Hide()
	})
}

// ── Screens ──

func (a *App) showActivationScreen() {
	a.activateContent.Show()
	a.mainContent.Hide()
	a.mainWindow.SetTitle("MyVPN — Activate")
}

func (a *App) showMainScreen() {
	a.activateContent.Hide()
	a.mainContent.Show()
	a.mainWindow.SetTitle("MyVPN — " + a.statusLabel.Text)
}

// ── Action handlers ──

func (a *App) onActivate() {
	code := a.codeEntry.Text
	fingerprint := activation.GenerateFingerprint()

	// Validate client-side
	if err := activation.ValidateCode(code); err != nil {
		dialog.ShowError(fmt.Errorf("Invalid code: %w", err), a.mainWindow)
		return
	}

	// Send activation request
	client := activation.NewClient(a.hubURL)
	resp, err := client.Activate(code, fingerprint)
	if err != nil {
		dialog.ShowError(fmt.Errorf("Activation failed: %w", err), a.mainWindow)
		return
	}

	// Store activation
	if resp.ServerCfg != nil {
		serverCfg := &storage.ServerConfig{
			Server:     resp.ServerCfg.Server,
			ServerPort: resp.ServerCfg.ServerPort,
			Password:   resp.ServerCfg.Password,
			Method:     resp.ServerCfg.Method,
		}

		if err := a.store.SetActivation(code, resp.Tier, fingerprint, serverCfg, resp.UDPRelay); err != nil {
			dialog.ShowError(fmt.Errorf("Failed to save activation: %w", err), a.mainWindow)
			return
		}
	}

	a.activated = true
	a.tierLabel.SetText(resp.Tier)
	dialog.ShowInformation("Activation Successful",
		fmt.Sprintf("Welcome! You're now activated on the %s tier.", resp.Tier),
		a.mainWindow)

	a.showMainScreen()
}

func (a *App) onConnect() {
	if !a.activated {
		return
	}

	data := a.store.GetData()
	if data.ServerConfig == nil {
		dialog.ShowError(fmt.Errorf("No server configuration found"), a.mainWindow)
		return
	}

	// Start tunnel
	cfg := &manager.EngineConfig{
		ServerAddr: data.ServerConfig.Server,
		ServerPort: data.ServerConfig.ServerPort,
		Password:   data.ServerConfig.Password,
		Method:     data.ServerConfig.Method,
		Tier:       data.Tier,
		UDPRelay:   data.UDPRelay,
		LocalPort:  1080,
	}

	if err := a.tunnel.Start(cfg); err != nil {
		dialog.ShowError(fmt.Errorf("Connection failed: %w", err), a.mainWindow)
		return
	}

	a.connected = true
	a.updateStatusUI()

	// Start heartbeat
	a.heartbeat = heartbeat.New(a.hubURL, data.Code, data.DeviceFingerprint, func(r heartbeat.Result) {
		if r.Success {
			a.store.SetHeartbeat(time.Now().Unix())
			// Check for update signal
			if r.Resp != nil && r.Resp.UpdateAvailable != "" {
				a.onUpdateAvailable(r.Resp)
			}
		} else {
			a.store.SetHeartbeatFailure(time.Now().Unix())
		}
	})
	a.heartbeat.Start()
}

func (a *App) onDisconnect() {
	if !a.connected {
		return
	}

	// Stop heartbeat
	if a.heartbeat != nil {
		a.heartbeat.Stop()
	}

	// Stop tunnel
	if err := a.tunnel.Stop(); err != nil {
		fyne.LogError("Failed to stop tunnel", err)
	}

	a.connected = false
	a.updateStatusUI()
}

func (a *App) onUpdateAvailable(resp *heartbeat.Response) {
	if resp.UpdateAvailable == "" || resp.UpdateURL == "" {
		return
	}

	// Check staged rollout eligibility
	if !updater.ShouldUpdate(100, a.store.GetData().DeviceFingerprint) {
		return // Not in rollout group
	}

	dialog.ShowConfirm("Update Available",
		fmt.Sprintf("Version %s is available. Update now?", resp.UpdateAvailable),
		func(ok bool) {
			if ok {
				a.performUpdate(resp)
			}
		},
		a.mainWindow)
}

func (a *App) performUpdate(resp *heartbeat.Response) {
	execPath, err := fyne.CurrentApp().UniqueID()
	if err != nil {
		dialog.ShowError(err, a.mainWindow)
		return
	}

	// Build platform-aware UpdateInfo from heartbeat response
	info := updater.UpdateInfo{
		Version: resp.UpdateAvailable,
	}
	if resp.UpdateWindows != "" {
		info.Windows = &updater.Asset{
			URL:    resp.UpdateWindows,
			SHA256: resp.UpdateSHA256,
		}
	}
	if resp.UpdateMacOSIntel != "" {
		info.MacOSIntel = &updater.Asset{
			URL:    resp.UpdateMacOSIntel,
			SHA256: resp.UpdateSHA256,
		}
	}
	if resp.UpdateMacOSARM != "" {
		info.MacOSARM = &updater.Asset{
			URL:    resp.UpdateMacOSARM,
			SHA256: resp.UpdateSHA256,
		}
	}
	// Fallback: use generic URL if no platform-specific asset (legacy support)
	if resp.UpdateURL != "" && info.URLForCurrentPlatform() == "" {
		switch runtime.GOOS {
		case "windows":
			info.Windows = &updater.Asset{URL: resp.UpdateURL, SHA256: resp.UpdateSHA256}
		case "darwin":
			if runtime.GOARCH == "arm64" {
				info.MacOSARM = &updater.Asset{URL: resp.UpdateURL, SHA256: resp.UpdateSHA256}
			} else {
				info.MacOSIntel = &updater.Asset{URL: resp.UpdateURL, SHA256: resp.UpdateSHA256}
			}
		}
	}

	newPath, err := a.updater.DownloadAndVerify(info)
	if err != nil {
		dialog.ShowError(fmt.Errorf("Download failed: %w", err), a.mainWindow)
		return
	}

	dialog.ShowInformation("Update Ready",
		fmt.Sprintf("Version %s downloaded. Restarting to apply...", info.Version),
		a.mainWindow)

	// Disconnect before update
	a.onDisconnect()

	if err := a.updater.Apply(newPath, info); err != nil {
		dialog.ShowError(fmt.Errorf("Update failed: %w", err), a.mainWindow)
	}
}

// ── Services lifecycle ──

func (a *App) startServices(data storage.Data) {
	a.tierLabel.SetText(data.Tier)

	// Initialize tunnel manager
	binaryPath := "sing-box" // Expected in PATH or app directory
	tunnel, err := manager.New(binaryPath)
	if err != nil {
		// Sing-box not found — log and continue (user needs to configure)
		fyne.LogError("Tunnel manager init failed (install sing-box)", err)
	}
	a.tunnel = tunnel

	// Initialize updater
	execPath, err := os.Executable()
	if err == nil {
		appDir := filepath.Dir(execPath)
		binaryName := filepath.Base(execPath)
		a.updater = updater.New(appDir, binaryName, a.version)
		a.crashDetect = updater.NewCrashDetector(appDir, binaryName)
	}
}

func (a *App) checkUpdateRecovery() {
	if a.crashDetect == nil {
		return
	}

	rolledBack, err := a.crashDetect.HandleCrashedUpdate()
	if err != nil {
		fyne.LogError("Update recovery check failed", err)
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
		a.connectBtn.SetText("Connected")
		a.connectBtn.Disable()
	} else {
		a.statusIcon.FillColor = color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF} // Grey
		a.statusLabel.SetText("Disconnected")
		a.connectBtn.SetText("Connect")
		a.connectBtn.Enable()
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
	a.fyneApp.Quit()
}
