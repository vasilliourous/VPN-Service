//go:build !windows

package manager

import "syscall"

// newProcAttr returns a SysProcAttr with process group detachment for Unix systems.
// This allows the parent to exit without killing the child process.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}
