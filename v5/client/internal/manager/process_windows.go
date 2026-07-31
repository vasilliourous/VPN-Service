//go:build windows

package manager

import "syscall"

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
