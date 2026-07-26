//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// newProcAttr returns a SysProcAttr with process group detachment for Unix.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// signalInterrupt returns SIGTERM on Unix
func signalInterrupt() os.Signal {
	return syscall.SIGTERM
}

// signalKill returns SIGKILL on Unix
func signalKill() os.Signal {
	return syscall.SIGKILL
}

// killProcessGroup sends a signal to the entire process group (Unix: -PID)
func killProcessGroup(pid int, sig os.Signal) error {
	s := sig.(syscall.Signal)
	return syscall.Kill(-pid, s)
}

// processExists checks if a process is still running via Signal(0)
func processExists(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	return cmd.Process.Signal(syscall.Signal(0)) == nil
}
