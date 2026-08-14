//go:build !windows

package main

// On non-Windows platforms there is no UAC; sing-box creates the TUN interface
// directly (root/sudo wrapper as appropriate). These stubs keep the elevation
// gate in app.go platform-portable.

// isElevated is always true on non-Windows (no separate elevation model for TUN
// creation handled here).
func isElevated() bool { return true }

// relaunchElevated is a no-op on non-Windows.
func relaunchElevated(_ ...string) error { return nil }
