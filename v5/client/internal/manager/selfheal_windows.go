//go:build windows

package manager

import (
	"os/exec"
	"strings"
)

const tunInterfaceName = "myvpn0"

// tunInterfaceUp reports whether the TUN interface is present and up.
// Returns (up, available). On Windows sing-box uses a Wintun adapter whose
// name is myvpn0; we query netsh to see it. If netsh is unavailable we report
// (false, false) so the caller does not block on an unverifiable check.
func tunInterfaceUp() (up bool, available bool) {
	out, err := exec.Command("netsh", "interface", "show", "interface").Output()
	if err != nil {
		return false, false
	}
	s := strings.ToLower(string(out))
	if !strings.Contains(s, strings.ToLower(tunInterfaceName)) {
		return false, true
	}
	// Heuristic: a line containing the adapter name and "connected"/"up".
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(tunInterfaceName)) {
			low := strings.ToLower(line)
			return strings.Contains(low, "connected") || strings.Contains(low, "up"), true
		}
	}
	return false, true
}

// killForeignEngines terminates any sing-box.exe processes this app does not
// track (taskkill). Best-effort.
func killForeignEngines() {
	_ = exec.Command("taskkill", "/F", "/IM", "sing-box.exe", "/T").Run()
}

// removeStaleTUN deletes a leftover myvpn0 Wintun adapter so a fresh engine can
// create a clean one. Requires admin (the client already runs elevated via the
// TUN helper path on Windows). Best-effort.
func removeStaleTUN() error {
	if up, _ := tunInterfaceUp(); !up {
		return nil
	}
	return exec.Command("netsh", "interface", "delete", "interface", tunInterfaceName).Run()
}

// describeForeignEngines returns a short human description of any foreign
// sing-box processes for diagnostics (best effort).
func describeForeignEngines() string {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq sing-box.exe", "/FO", "CSV", "/NH").Output()
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
