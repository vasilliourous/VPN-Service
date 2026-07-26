//go:build windows

package manager

import "syscall"

// newProcAttr returns a SysProcAttr appropriate for Windows.
// Windows does not support Setpgid — we use CreationFlags to detach instead.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
