//go:build darwin

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func init() {
	swapFile = swapDarwin
	forkExec = forkDarwin
}

// swapDarwin performs an atomic rename on Darwin.
func swapDarwin(newPath, currentPath string) error {
	// On Darwin, we can atomically rename the new binary over the current one.
	// The old binary is still backed up in .locus-backups/
	//
	// The chmod happens BEFORE the rename for the same reason as Linux: renaming
	// first and then failing the chmod would report a failure for an update that
	// is already committed on disk, leaving the sentinel state inconsistent with
	// the installed binary.
	if err := os.Chmod(newPath, 0755); err != nil {
		return fmt.Errorf("chmod of the downloaded binary failed: %w", err)
	}

	if err := os.Rename(newPath, currentPath); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}

	return nil
}

// forkDarwin starts the new binary on Darwin, detaching from the parent.
func forkDarwin(binaryPath string) error {
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
