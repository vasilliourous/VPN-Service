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

// hiddenCommand builds an exec.Cmd whose console window is suppressed.
//
// EVERY helper process this package spawns must go through here. Windows
// console-subsystem children of a GUI process do not inherit a console, so the
// OS allocates a fresh one for each — which appears on screen as a terminal
// window that flashes open and vanishes. Attaching HideWindow (CREATE_NO_WINDOW)
// stops that allocation entirely.
//
// This exists as a helper because the same bug was fixed one call site at a
// time and kept coming back: `tasklist`, `netsh`, `taskkill` and `powershell`
// were each spawned bare, so a single Connect produced several flashes (the
// foreign-engine check and stale-TUN cleanup run on every Start), and the
// watchdog's escalation produced more on every retry. Routing every spawn
// through one constructor is what makes that class of bug unrepeatable.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = newProcAttr()
	return cmd
}

// hiddenRun spawns a hidden helper and waits for it. Use for the short-lived
// queries (tasklist/netsh) and one-shot actions (taskkill) this package needs.
func hiddenRun(name string, args ...string) error {
	return hiddenCommand(name, args...).Run()
}

// hiddenOutput spawns a hidden helper and captures stdout. Use for the
// read-only queries whose output we parse.
func hiddenOutput(name string, args ...string) ([]byte, error) {
	return hiddenCommand(name, args...).Output()
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
	// we only care about the exit status. Spawned hidden: this runs on every
	// disconnect and on every watchdog escalation, so a visible console here
	// would flash on each retry.
	if err := hiddenRun("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)); err == nil {
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
	out, err := hiddenOutput("tasklist", "/FI", "IMAGENAME eq sing-box.exe", "/NH")
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
