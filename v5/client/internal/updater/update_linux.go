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
	// The old binary is still backed up in .locus-backups/
	//
	// The chmod happens BEFORE the rename, not after. Renaming first and then
	// failing the chmod (e.g. a restrictive umask or a read-only filesystem)
	// would return an error even though the new binary is already installed —
	// the caller would treat a successful swap as a failed update and the
	// two-phase sentinel would be left in a state that no longer matches the
	// binary on disk. Making the new binary executable first keeps the rename
	// as the single commit point.
	if err := os.Chmod(newPath, 0755); err != nil {
		return fmt.Errorf("chmod of the downloaded binary failed: %w", err)
	}

	if err := os.Rename(newPath, currentPath); err != nil {
		return fmt.Errorf("rename failed: %w", err)
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
