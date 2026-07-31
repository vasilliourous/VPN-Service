//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

var (
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = modkernel32.NewProc("OpenProcess")
	procCloseHandle = modkernel32.NewProc("CloseHandle")
)

const (
	PROCESS_QUERY_INFORMATION = 0x0400
)

// newProcAttr returns a SysProcAttr for Windows.
func newProcAttr() *syscall.SysProcAttr {
	return nil
}

// signalInterrupt returns the interrupt signal for Windows.
func signalInterrupt() os.Signal {
	return os.Kill
}

// signalKill returns the kill signal for Windows.
func signalKill() os.Signal {
	return os.Kill
}

// killProcessGroup terminates the sing-box process on Windows.
// Uses the process handle directly since Windows doesn't support process groups.
func killProcessGroup(pid int, sig os.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	// Open process with PROCESS_TERMINATE access
	handle, _, _ := procOpenProcess.Call(0x0001, 0, uintptr(pid))
	if handle == 0 {
		return fmt.Errorf("cannot open process %d: access denied or not found", pid)
	}
	defer procCloseHandle.Call(handle)

	// Find and kill the process
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("cannot find process %d: %w", pid, err)
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("cannot kill process %d: %w", pid, err)
	}
	return nil
}

// processExists checks if a process is still running without killing it.
// Uses OpenProcess with PROCESS_QUERY_INFORMATION instead of Signal(os.Kill).
func processExists(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	// On Windows, Signal() with anything other than os.Kill fails.
	// Use OpenProcess to check if the process exists without side effects.
	handle, _, _ := procOpenProcess.Call(PROCESS_QUERY_INFORMATION, 0, uintptr(cmd.Process.Pid))
	if handle == 0 {
		return false // Process doesn't exist or access denied
	}
	procCloseHandle.Call(handle)
	return true
}
