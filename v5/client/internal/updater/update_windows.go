//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func init() {
	swapFile = swapWindows
	forkExec = forkWindows
}

// swapWindows renames the current binary to .old, then renames the new one in place.
// Windows doesn't support atomic rename of a running executable, so we use the .old trick.
//
// CRITICAL: the rollback path must not be silent. If the second rename fails
// AND the restore rename also fails, the install is left with NO runnable
// binary (the old one is stranded as ".old" and the new one never landed). That
// is unrecoverable for a student, so the error must say exactly where the
// backup is rather than being discarded.
func swapWindows(newPath, currentPath string) error {
	oldPath := currentPath + ".old"

	// Remove any existing .old file
	_ = os.Remove(oldPath)

	// Rename current binary to .old (succeeds even if current is running on Windows)
	if err := os.Rename(currentPath, oldPath); err != nil {
		return fmt.Errorf("cannot rename current binary: %w", err)
	}

	// Move new binary into place
	if err := os.Rename(newPath, currentPath); err != nil {
		// Attempt to restore old binary.
		if restoreErr := os.Rename(oldPath, currentPath); restoreErr != nil {
			// Both renames failed: there is no binary at currentPath. Report
			// the backup location so support can recover the machine by hand
			// instead of leaving them with a renamed-away executable.
			return fmt.Errorf("cannot move new binary (%w), and restoring the previous "+
				"binary also failed (%v) — the previous build is preserved at %s; "+
				"rename it back to %s to recover", err, restoreErr, oldPath, currentPath)
		}
		return fmt.Errorf("cannot move new binary: %w", err)
	}

	// Clean up old binary
	_ = os.Remove(oldPath)

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
