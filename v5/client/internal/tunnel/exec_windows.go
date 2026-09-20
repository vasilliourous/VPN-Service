//go:build windows

package tunnel

import (
	"os/exec"
	"syscall"
)

// hiddenCommand builds an exec.Cmd whose console window is suppressed.
//
// # WHY EVERY SPAWN IN THIS PACKAGE MUST GO THROUGH HERE
//
// This is a GUI application (windowsgui subsystem). When it spawns a
// console-subsystem child — netsh, taskkill, ipconfig — the child has no
// console to inherit, so Windows allocates a fresh one for it. That appears on
// screen as a terminal window that flashes open and vanishes. The user sees
// this on every Connect and Disconnect, because this package shells out to
// netsh for DNS and for the kill-switch firewall rule on both paths.
//
// HideWindow sets CREATE_NO_WINDOW, which stops the allocation entirely.
//
// This exact bug was fixed once already in internal/manager and internal/
// activation, each time one call site at a time, which is how it kept coming
// back: the sites that were missed still flashed. Routing every spawn through
// a single constructor — rather than sprinkling SysProcAttr at each call —
// is what makes the class of bug unrepeatable. Any new netsh/ipconfig call in
// this package should use hiddenCommand, not exec.Command.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd
}
