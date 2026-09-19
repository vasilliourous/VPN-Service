//go:build !windows

package manager

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// newProcAttr returns a SysProcAttr with process group detachment for Unix systems.
// This allows the parent to exit without killing the child process.
//
// Setpgid is also what makes killProcessGroup correct: the child (and any
// grandchildren it spawns) share a process group id equal to the child's pid,
// so we can signal the whole group at once.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// hiddenCommand builds an exec.Cmd for a helper process.
//
// On Unix there is no console to suppress — a GUI process simply does not
// allocate one for its children — so this is a plain constructor. It exists so
// shared code (which needs the same call on every platform) has one name to use.
// On Windows the same function applies CREATE_NO_WINDOW; see process_windows.go
// for why that matters (the terminal-flash bug).
func hiddenCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// hiddenRun spawns a helper and waits for it (see hiddenCommand).
func hiddenRun(name string, args ...string) error {
	return hiddenCommand(name, args...).Run()
}

// hiddenOutput spawns a helper and captures stdout (see hiddenCommand).
func hiddenOutput(name string, args ...string) ([]byte, error) {
	return hiddenCommand(name, args...).Output()
}

// killProcessGroup force-kills the whole process group led by p.
//
// Why the group and not just p: sing-box is launched via a shell wrapper on some
// installs, and any grandchildren it leaves behind inherit the stdout/stderr
// pipes. Go's cmd.Wait() blocks until those pipes reach EOF, so killing only the
// direct child leaves Wait() blocked for as long as an orphan lives — which is
// exactly what made "Disconnect" appear to hang. Signalling the group (negative
// pid) reaps the whole tree.
//
// A negative pid targets the group; errors are returned so the caller can fall
// back to a plain Process.Kill().
func killProcessGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	// Negate the pid to address the process group (see Setpgid in newProcAttr).
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if err == nil {
		return nil
	}
	// Group kill failed (e.g. no group / already reaped) — fall back to the
	// single process so we never leave the engine running.
	if killErr := p.Kill(); killErr != nil {
		return killErr
	}
	return nil
}

// foreignSingBoxRunning reports whether an untracked sing-box process is
// already running (we have not spawned one yet when Start calls this).
// Used to refuse stacking a second engine on the same TUN — two instances
// sharing locus0 corrupt routing (see Start in process.go).
func foreignSingBoxRunning() bool {
	out, err := exec.Command("pgrep", "-x", "sing-box").Output()
	if err != nil {
		return false // no match (exit 1) or pgrep missing — don't block Connect
	}
	return len(strings.TrimSpace(string(out))) > 0
}
