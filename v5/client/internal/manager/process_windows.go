//go:build windows

package manager

import (
	"os"
	"os/exec"
	"strconv"
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

// killProcessGroup force-kills the process tree rooted at p.
//
// Windows has no negative-pid group kill, so we use taskkill /T /F which
// terminates the process AND its children. This matters for the same reason as
// the Unix path: any orphaned grandchild holding the inherited stdout/stderr
// pipes keeps Go's cmd.Wait() blocked, which makes Disconnect look hung.
// Falls back to Process.Kill() if taskkill is unavailable.
func killProcessGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	// /T = tree (children too), /F = force, /PID = target. Output is discarded —
	// we only care about the exit status.
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err == nil {
		return nil
	}
	// taskkill missing or refused — fall back to killing the single process so
	// we never leave the engine running.
	return p.Kill()
}

// foreignSingBoxRunning reports whether an untracked sing-box process is
// already running (we have not spawned one yet when Start calls this).
// Used to refuse stacking a second engine on the same TUN — two instances
// sharing locus0 corrupt routing (see Start in process.go).
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
