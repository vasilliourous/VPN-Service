//go:build darwin

// Package tray provides an optional system-tray controller for the Locus
// desktop app.
//
// macOS IS A DELIBERATE NO-OP — see the build-tag note below.
//
// BUILD TAGS — WHY darwin IS STUBBED OUT:
//   getlantern/systray's macOS implementation (systray_darwin.m) declares an
//   Objective-C class named `AppDelegate`, which collides with the
//   `AppDelegate` that Wails links in on darwin. At link time the macOS build
//   fails with:
//
//       duplicate symbol '_OBJC_CLASS_$_AppDelegate'
//       duplicate symbol '_OBJC_METACLASS_$_AppDelegate'
//
//   Neither implementation can be renamed from our side, so on macOS we
//   exclude systray (via the `//go:build !darwin` tag on tray.go) and provide
//   this API-compatible no-op instead. Callers in the host app are unchanged:
//   they still call tray.Start(Actions{...}) and get a *Controller, but on
//   macOS the controller does nothing.
//
//   Consequence: LOCUS_TRAY=1 has no effect on macOS. The app still runs; it
//   simply has no tray icon there. Linux and Windows keep the real tray.
package tray

// Actions carry callback hooks from the host app into the tray.
//
// This mirrors the non-darwin definition exactly so that host-app call sites
// (tray.Start(tray.Actions{...})) compile unchanged on every platform.
type Actions struct {
	Toggle     func() string // connect<->disconnect; returns a status line
	OpenWindow func()
	Quit       func() // invoked on tray Quit
}

// Controller is a no-op on macOS. It exists only to satisfy the tray API used
// by the host app; none of its methods have any effect.
type Controller struct{}

// Start returns an inert Controller on macOS. The Actions are accepted and
// discarded. It never launches a goroutine and never panics, so the host app's
// LOCUS_TRAY opt-in remains safe (and silent) on macOS.
func Start(Actions) *Controller {
	return &Controller{}
}

// SetConnected is a no-op on macOS.
func (*Controller) SetConnected(bool) {}

// SetTier is a no-op on macOS.
func (*Controller) SetTier(string) {}

// Stop is a no-op on macOS (nothing was started).
func (*Controller) Stop() {}
