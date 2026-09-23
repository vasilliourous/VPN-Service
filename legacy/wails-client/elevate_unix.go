//go:build !windows

package main

import (
	"os"
)

// On non-Windows platforms there is no UAC. This file used to claim elevation
// was unnecessary, which was wrong on Linux and produced the worst possible
// failure mode: Connect() proceeded, sing-box failed to open /dev/net/tun or
// install routes, and the student saw a vague engine error with no hint that
// the real problem was permissions.
//
// KNOWN GAP (Track A in docs/ENGINE-SWAP-ANALYSIS.md): the client has NO Linux
// TUN elevation path. TUN requires root or CAP_NET_ADMIN, the privileged
// locus-helper binary is no longer shipped (direct mode is forced in Startup),
// and this app does not shell out to pkexec/polkit/sudo. So on any non-root
// Linux session the tunnel cannot come up — deterministically, on every engine.
//
// Until a real elevation mechanism lands, the client's job is to say so
// clearly and immediately rather than let it surface as a mysterious engine
// failure fifty lines into a log. macOS is unaffected: the TUN path there goes
// through utun, which does not require a separate elevation model for the way
// sing-box is invoked.

// isElevated reports whether this process can create a TUN interface.
//
// On Windows this is a real token check (see elevate_windows.go). On Unix it
// reports whether we are effectively root, because that is the only condition
// under which the current direct-mode start path can succeed on Linux.
//
// It deliberately does NOT return true on darwin-without-root: the tunnel will
// fail there too, and reporting otherwise is what hid the problem.
func isElevated() bool {
	return os.Geteuid() == 0
}

// relaunchElevated cannot work on Unix: there is no equivalent of the UAC
// "runas" verb that both elevates and is safe to invoke from inside a GUI app
// without an installed polkit action. We therefore never claim to have
// relaunched; Connect() turns a false from here into an actionable message.
func relaunchElevated(_ ...string) error { return errNoUnixElevation }

// elevationUnsupportedReason returns a platform-specific explanation shown to
// the student when elevation is required but unavailable. Empty when elevation
// is not required on this platform.
func elevationUnsupportedReason() string {
	if isElevated() {
		return ""
	}
	return "Locus needs root permission to create the VPN network interface on " +
		"this system. Run Locus with sudo (or from a root session) to connect. " +
		"Linux elevation is a known gap in this build."
}

// errNoUnixElevation is returned by relaunchElevated on Unix.
type noUnixElevationError struct{}

func (noUnixElevationError) Error() string {
	return "automatic privilege elevation is not available on this platform"
}

var errNoUnixElevation = noUnixElevationError{}
