//go:build !windows

package manager

import (
	"net"
	"os/exec"
	"strings"
)

const tunInterfaceName = "myvpn0"

// tunInterfaceUp reports whether the TUN interface is present and up.
// Returns (up, available) where available=false means the platform/tooling
// could not determine it (caller treats unavailable as "don't block on it").
func tunInterfaceUp() (up bool, available bool) {
	iface, err := net.InterfaceByName(tunInterfaceName)
	if err != nil {
		// Not present at all — this is a definitive "not up" signal.
		return false, true
	}
	return iface.Flags&net.FlagUp != 0, true
}

// killForeignEngines terminates any sing-box processes this app does not track,
// which could otherwise share the myvpn0 TUN and corrupt routing. Best-effort.
func killForeignEngines() {
	// pkill may not be present; ignore failures.
	_ = exec.Command("pkill", "-9", "-x", "sing-box").Run()
}

// removeStaleTUN deletes a leftover myvpn0 TUN interface so a fresh engine can
// create a clean one. Best-effort; requires privileges (root on Linux, which
// the client obtains via the same elevation path as TUN creation).
func removeStaleTUN() error {
	if up, _ := tunInterfaceUp(); !up {
		return nil // nothing stale
	}
	return exec.Command("ip", "link", "del", tunInterfaceName).Run()
}

// describeForeignEngines returns a short human description of any foreign
// sing-box processes for diagnostics (best effort).
func describeForeignEngines() string {
	out, err := exec.Command("pgrep", "-a", "-x", "sing-box").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ForeignEngines returns a human-readable summary of any non-tracked sing-box
// processes for diagnostics ("" when none/best-effort).
func (m *Manager) ForeignEngines() string {
	return describeForeignEngines()
}
