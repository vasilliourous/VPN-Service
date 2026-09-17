// Package tray provides an optional system-tray controller for the Locus
// desktop app (Wails v2 has no native tray support, so we use
// getlantern/systray).
//
// IMPORTANT — WHY IT IS OPT-IN:
//   - Wails v2 cannot intercept "window close -> hide", so closing the window
//     still quits the app. The tray here gives live status + quick actions
//     while the window is open (not minimize-to-tray).
//   - systray runs native platform code that cannot be exercised in CI. It is
//     gated behind LOCUS_TRAY=1 and OFF by default so it can never regress a
//     normal release. Validate on each real desktop OS before enabling it.
//   - Any panic in the tray goroutine is recovered and logged (never fatal).
package tray

import (
	"log"
	"strings"
	"sync"

	"github.com/getlantern/systray"
)

// Actions carry callback hooks from the host app into the tray.
type Actions struct {
	Toggle     func() string // connect<->disconnect; returns a status line
	OpenWindow func()
	Quit       func() // invoked on tray Quit
}

// Controller owns the tray menu and keeps labels in sync with app state.
type Controller struct {
	mu      sync.Mutex
	actions Actions

	connected bool
	tier      string

	// Menu item handles (valid only after ready).
	status *systray.MenuItem
	toggle *systray.MenuItem

	ready     chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

// Start launches systray in the background and returns a *Controller.
// It is a no-op keeper: callers should check LOCUS_TRAY and only Start when
// enabled. Start may only be called once for the process lifetime.
func Start(act Actions) *Controller {
	c := &Controller{actions: act, ready: make(chan struct{})}
	c.startOnce.Do(func() {
		go c.runSystray()
	})
	return c
}

func (c *Controller) runSystray() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tray: recovered from panic: %v", r)
		}
	}()
	systray.Run(func() {
		c.mu.Lock()
		c.status = systray.AddMenuItem("Locus", "Connection status")
		c.status.Disable()
		c.toggle = systray.AddMenuItem("Connect", "Connect or disconnect the tunnel")
		systray.AddSeparator()
		openItem := systray.AddMenuItem("Open Locus", "Focus the app window")
		quitItem := systray.AddMenuItem("Quit", "Exit Locus")
		c.mu.Unlock()
		c.refresh()

		// ready now; controllers may start posting refreshes.
		close(c.ready)

		go c.watchClicks(openItem, quitItem)
	}, func() {
		// systray terminated (user picked Quit / our Stop called).
		if c.actions.Quit != nil {
			c.actions.Quit()
		}
	})
}

func (c *Controller) watchClicks(openItem, quitItem *systray.MenuItem) {
	go func() {
		for range c.toggle.ClickedCh {
			if c.actions.Toggle != nil {
				_ = c.actions.Toggle()
			}
			c.refresh()
		}
	}()
	go func() {
		for range openItem.ClickedCh {
			if c.actions.OpenWindow != nil {
				c.actions.OpenWindow()
			}
		}
	}()
	go func() {
		for range quitItem.ClickedCh {
			systray.Quit()
		}
	}()
}

// SetConnected refreshes connectivity state shown in the tray.
func (c *Controller) SetConnected(connected bool) {
	c.mu.Lock()
	c.connected = connected
	c.mu.Unlock()
	c.refresh()
}

// SetTier displays which tier this device is on.
func (c *Controller) SetTier(tier string) {
	c.mu.Lock()
	c.tier = tier
	c.mu.Unlock()
	c.refresh()
}

func (c *Controller) genTitleLocked() string {
	st := "Disconnected"
	if c.connected {
		st = "Connected"
	}
	if strings.TrimSpace(c.tier) != "" {
		st += " · " + c.tier
	}
	return "Locus — " + st
}

func (c *Controller) refresh() {
	select {
	case <-c.ready:
	default:
		return // not ready yet; ignore calls that arrive before the menu exists
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.SetTitle(c.genTitleLocked())
	if c.connected {
		c.toggle.SetTitle("Disconnect")
	} else {
		c.toggle.SetTitle("Connect")
	}
}

// Stop asks systray to shut down (idempotent). Does not block.
func (c *Controller) Stop() {
	c.stopOnce.Do(func() { systray.Quit() })
}
