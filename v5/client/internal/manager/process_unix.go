//go:build !windows

package manager

import (
	"os/exec"
	"strings"
	"syscall"
)

// newProcAttr returns a SysProcAttr with process group detachment for Unix systems.
// This allows the parent to exit without killing the child process.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// foreignSingBoxRunning reports whether an untracked sing-box process is
// already running (we have not spawned one yet when Start calls this).
// Used to refuse stacking a second engine on the same TUN — two instances
// sharing myvpn0 corrupt routing (see Start in process.go).
func foreignSingBoxRunning() bool {
	out, err := exec.Command("pgrep", "-x", "sing-box").Output()
	if err != nil {
		return false // no match (exit 1) or pgrep missing — don't block Connect
	}
	return len(strings.TrimSpace(string(out))) > 0
}
