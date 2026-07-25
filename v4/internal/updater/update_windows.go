//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// swapWindows renames the current binary to .old, then renames the new one in place.
// Windows doesn't support atomic rename of a running executable, so we use the .old trick.
func swapWindows(newPath, currentPath string) error {
	oldPath := currentPath + ".old"

	// Remove any existing .old file
	os.Remove(oldPath)

	// Rename current binary to .old (succeeds even if current is running on Windows)
	if err := os.Rename(currentPath, oldPath); err != nil {
		return fmt.Errorf("cannot rename current binary: %w", err)
	}

	// Move new binary into place
	if err := os.Rename(newPath, currentPath); err != nil {
		// Attempt to restore old binary
		os.Rename(oldPath, currentPath)
		return fmt.Errorf("cannot move new binary: %w", err)
	}

	return nil
}

// forkWindows starts the new binary on Windows.
func forkWindows(binaryPath string) error {
	cmd := exec.Command(binaryPath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Detach from parent
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork failed: %w", err)
	}

	return nil
}
