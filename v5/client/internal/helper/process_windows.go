//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// newProcAttr returns a SysProcAttr for Windows.
// Windows doesn't support Setpgid.
func newProcAttr() *syscall.SysProcAttr {
	return nil
}

// signalInterrupt returns the interrupt signal for Windows.
// Windows doesn't have SIGTERM — use os.Kill as fallback.
func signalInterrupt() os.Signal {
	return os.Kill
}

// signalKill returns the kill signal for Windows.
func signalKill() os.Signal {
	return os.Kill
}

// killProcessGroup kills a single process on Windows.
// Windows doesn't support process groups in the same way as Unix.
func killProcessGroup(pid int, sig os.Signal) error {
	// On Windows, we can't signal a process group.
	// The process handle is needed — we use Process.Kill() instead.
	return nil
}

// processExists checks if a process is still running on Windows.
func processExists(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	// Signal(0) with os.Kill checks if process exists on Windows
	return cmd.Process.Signal(os.Kill) == nil
}
