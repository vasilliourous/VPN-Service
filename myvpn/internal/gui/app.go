package gui

import (
	"fmt"
	"image/color"
	"myvpn/internal/activation"
	"myvpn/internal/branding"
	"myvpn/internal/health"
	"myvpn/internal/heartbeat"
	"myvpn/internal/manager"
	"myvpn/internal/monitor"
	"myvpn/internal/probe"
	"myvpn/internal/storage"
	"myvpn/internal/tunnel"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type GUIState struct {
	Window           fyne.Window
	StatusLabel      *widget.Label
	StatusDot        *canvas.Circle
	TierLabel        *widget.Label
	TimeLabel        *widget.Label
	SpeedLabel       *widget.Label
	ConnectBtn       *widget.Button
	adminHubURL      string
	connected        bool
	startTime        time.Time
	healthChecker    *health.Checker
	bandwidthMonitor *monitor.Monitor
}

var (
	state GUIState
	a     fyne.App
)

func Run(adminHubURL string) {
	a = app.New()
	a.Settings().SetTheme(&myVPNTheme{})

	if !storage.IsActivated() {
		showActivationPrompt(adminHubURL)
		return
	}

	launchMain(adminHubURL, storage.LoadPlanTier())
}

func launchMain(adminHubURL, planTier string) {
	state.adminHubURL = adminHubURL

	// Start heartbeat loop in background
	go heartbeat.Start(adminHubURL)

	state.Window = a.NewWindow("MyVPN")

	dotSize := float32(16)
	state.StatusDot = canvas.NewCircle(color.RGBA{239, 68, 68, 255})
	state.StatusDot.Resize(fyne.NewSize(dotSize, dotSize))
	state.StatusLabel = widget.NewLabel("Disconnected")
	state.TierLabel = widget.NewLabel(branding.PlanDisplayName(planTier))
	state.TimeLabel = widget.NewLabel("--:--:--")
	state.SpeedLabel = widget.NewLabel("Speed: --")

	state.ConnectBtn = widget.NewButton("Connect", onConnect)
	state.ConnectBtn.Importance = widget.HighImportance

	privacyCheck := widget.NewCheck("Send anonymous usage data", func(optOut bool) {
		storage.SetTelemetryOptOut(!optOut)
	})
	privacyCheck.SetChecked(!storage.GetTelemetryOptOut())

	actLabel := widget.NewLabel(fmt.Sprintf("Code: %s", maskCode(storage.LoadToken())))
	planLabel := widget.NewLabel(fmt.Sprintf("Plan: %s", branding.PlanDisplayName(planTier)))

	settingsItems := []*widget.AccordionItem{
		widget.NewAccordionItem("Settings", container.NewVBox(
			privacyCheck,
			actLabel,
			planLabel,
		)),
	}
	settingsAccord := widget.NewAccordion(settingsItems...)

	header := container.NewHBox(
		state.StatusDot,
		state.StatusLabel,
	)

	content := container.NewBorder(
		container.NewVBox(
			header,
			state.TierLabel,
			state.TimeLabel,
			widget.NewSeparator(),
			state.SpeedLabel,
			state.ConnectBtn,
		),
		settingsAccord,
		nil, nil,
	)

	bg := gradientBackground()
	root := container.NewStack(bg, content)

	state.Window.SetContent(root)
	state.Window.Resize(fyne.NewSize(300, 400))
	state.Window.SetFixedSize(true)

	go statusLoop()
	state.Window.ShowAndRun()
}

func showActivationPrompt(adminHubURL string) {
	w := a.NewWindow("MyVPN — Activate")

	codeEntry := widget.NewEntry()
	codeEntry.SetPlaceHolder("MYVPN-XXXX-XXXX-XXXX-C")

	statusLabel := widget.NewLabel("Enter your activation code")

	activateBtn := widget.NewButton("Activate", func() {
		result, err := activation.Validate(adminHubURL, codeEntry.Text)
		if err != nil {
			statusLabel.SetText(fmt.Sprintf("Error: %v", err))
			return
		}
		storage.SaveActivation(result.Token, result.Plan)
		storage.SaveDeviceFingerprint(activation.CollectFingerprint())
		w.Close()
		launchMain(adminHubURL, result.Plan)
	})

	w.SetContent(container.NewVBox(
		widget.NewLabelWithStyle("Activate MyVPN", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		statusLabel,
		codeEntry,
		activateBtn,
	))
	w.Resize(fyne.NewSize(300, 200))
	w.ShowAndRun()
}

func onConnect() {
	if state.connected {
		disconnect()
	} else {
		connect()
	}
}

func connect() {
	state.ConnectBtn.SetText("Connecting...")
	state.StatusLabel.SetText("Connecting")
	setDotColor(color.RGBA{245, 158, 11, 255})

	go func() {
		planTier := storage.LoadPlanTier()

		probeResult, err := probe.Run(state.adminHubURL, planTier)
		if err != nil {
			state.StatusLabel.SetText("Probe failed, using defaults")
		}

		if err := tunnel.Setup(); err != nil {
			state.StatusLabel.SetText(fmt.Sprintf("TUN failed: %v", err))
			state.ConnectBtn.SetText("Connect")
			return
		}
		tunnel.Engage()
		tunnel.Guard()

		protos := storage.GetProtocols()
		if len(protos) > 0 {
			capBps := 0
			if probeResult != nil {
				capBps = probeResult.BandwidthBps
			}
			if err := manager.StartEngine(protos[0], planTier, capBps); err != nil {
				state.StatusLabel.SetText(fmt.Sprintf("Engine failed: %v", err))
				disconnect()
				return
			}
		}

		state.healthChecker = health.New(state.adminHubURL)
		state.healthChecker.OnDegraded(func() {
			state.StatusLabel.SetText("Degraded")
			setDotColor(color.RGBA{245, 158, 11, 255})
		})
		state.healthChecker.OnDead(func() {
			disconnect()
		})
		state.healthChecker.Start()

		initialCap := 0
		baselineRTT := 50 * time.Millisecond
		if probeResult != nil {
			initialCap = probeResult.BandwidthBps
			baselineRTT = probeResult.BaselineRTT
		}
		state.bandwidthMonitor = monitor.New(state.adminHubURL, planTier, initialCap, baselineRTT)
		state.bandwidthMonitor.Start()

		state.connected = true
		state.startTime = time.Now()
		state.ConnectBtn.SetText("Disconnect")
		state.StatusLabel.SetText("Connected")
		setDotColor(color.RGBA{76, 175, 80, 255})
	}()
}

func disconnect() {
	state.ConnectBtn.SetText("Disconnecting...")

	go func() {
		if state.bandwidthMonitor != nil {
			state.bandwidthMonitor.Stop()
			state.bandwidthMonitor = nil
		}
		if state.healthChecker != nil {
			state.healthChecker.Stop()
			state.healthChecker = nil
		}

		manager.StopEngine()
		tunnel.UnGuard()
		tunnel.Disengage()
		tunnel.Teardown()

		state.connected = false
		state.ConnectBtn.SetText("Connect")
		state.StatusLabel.SetText("Disconnected")
		state.SpeedLabel.SetText("Speed: --")
		state.TimeLabel.SetText("--:--:--")
		setDotColor(color.RGBA{239, 68, 68, 255})
	}()
}

func setDotColor(c color.Color) {
	state.StatusDot.FillColor = c
	canvas.Refresh(state.StatusDot)
}

func statusLoop() {
	for range time.NewTicker(2 * time.Second).C {
		if state.connected {
			elapsed := time.Since(state.startTime)
			state.TimeLabel.SetText(formatDuration(elapsed))

			if state.bandwidthMonitor != nil {
				cap := state.bandwidthMonitor.CurrentCapKBps()
				if cap > 0 {
					state.SpeedLabel.SetText(fmt.Sprintf("Speed: %d KB/s (capped)", cap))
				} else {
					state.SpeedLabel.SetText("Speed: Fast")
				}
			}
		}
	}
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func maskCode(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

type myVPNTheme struct{}

func (t *myVPNTheme) Color(c fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch c {
	case theme.ColorNameBackground:
		return color.RGBA{13, 13, 15, 255}
	case theme.ColorNamePrimary:
		return color.RGBA{168, 85, 247, 255}
	case theme.ColorNameForeground:
		return color.RGBA{230, 230, 235, 255}
	case theme.ColorNameButton:
		return color.RGBA{168, 85, 247, 255}
	default:
		return theme.DefaultTheme().Color(c, v)
	}
}

func (t *myVPNTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (t *myVPNTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (t *myVPNTheme) Size(s fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(s)
}

func gradientBackground() *canvas.RadialGradient {
	g := canvas.NewRadialGradient(
		color.RGBA{168, 85, 247, 30},
		color.RGBA{13, 13, 15, 255},
	)
	g.CenterOffsetX = -0.3
	g.CenterOffsetY = -0.2
	return g
}
