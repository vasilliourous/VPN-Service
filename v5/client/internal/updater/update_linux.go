//go:build linux

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func init() {
	swapFile = swapLinux
	forkExec = forkLinux
}

// swapLinux performs an atomic rename on Linux.
func swapLinux(newPath, currentPath string) error {
	// On Linux, we can atomically rename the new binary over the current one.
	// The old binary is still backed up in .myvpn-backups/
	if err := os.Rename(newPath, currentPath); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}

	// Ensure the binary is executable
	if err := os.Chmod(currentPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	return nil
}

// forkLinux starts the new binary on Linux, detaching from the parent.
func forkLinux(binaryPath string) error {
	cmd := exec.Command(binaryPath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Detach from parent process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork failed: %w", err)
	}

	// Detach — we don't wait for the child
	return nil
}
