//go:build windows

package manager

import (
	"os/exec"
	"strings"
	"syscall"
)

// newProcAttr returns a SysProcAttr appropriate for Windows.
// Windows does not support Setpgid — we use CreationFlags to detach instead.
// HideWindow prevents a console window from flashing when this GUI app
// spawns sing-box (a console-subsystem process).
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// foreignSingBoxRunning reports whether an untracked sing-box process is
// already running (we have not spawned one yet when Start calls this).
// Used to refuse stacking a second engine on the same TUN — two instances
// sharing myvpn0 corrupt routing (see Start in process.go).
func foreignSingBoxRunning() bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq sing-box.exe", "/NH").Output()
	if err != nil {
		return false // can't tell — don't block Connect
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), "sing-box.exe") {
			return true
		}
	}
	return false
}
