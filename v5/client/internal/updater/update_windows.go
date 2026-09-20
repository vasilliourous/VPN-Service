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

	// Best-effort cleanup of the superseded binary.
	//
	// This FAILS on the immediate update path and that is expected: oldPath is
	// the image this process is still running from, and Windows refuses to
	// delete a mapped executable. The error is deliberately not fatal — the
	// swap itself succeeded, which is what matters.
	//
	// The file is not leaked, though: Housekeeping() removes it on the next
	// launch, once this process has exited and the mapping is gone. That is
	// why the removal is attempted here at all rather than skipped — when the
	// swap is performed by a process running from somewhere else (a manual
	// swap, a test), this succeeds immediately and no .old is ever created.
	_ = os.Remove(oldPath)

	return nil
}

// forkWindows starts the new binary on Windows.
//
// HideWindow is REQUIRED here. Locus is built as a GUI (windowsgui) binary, so
// it has no console. A child process spawned without CREATE_NO_WINDOW gets a
// brand-new console allocated for it by Windows, which the user sees as a
// terminal window flashing open. That is especially visible in the updater:
// this is the last thing that runs before the app restarts, so the flash lands
// at the exact moment the UI disappears and the user is watching the screen.
//
// CREATE_NO_WINDOW suppresses the allocation. CREATE_NEW_PROCESS_GROUP is kept:
// it detaches the child from this process's group so the parent exiting does
// not take the freshly started update down with it.
//
// The child is started with NO inherited stdio, and that matters more than it
// looks. Locus is a GUI binary with no console; passing os.Stdin/Stdout/Stderr
// to a child of a GUI process hands it invalid or console-less handles. Go's
// exec then has to resolve the inheritance, and the child's startup is delayed
// behind it — long enough that the parent's own window is still on screen when
// the child paints its WebView. The user sees two Locus windows, the old one
// closing only when the parent finally finishes tearing down.
//
// Leaving them nil starts the child with no inherited handles at all: nothing
// to resolve, nothing to block on, and the new window is the only one that
// appears. The child logs to its own file (see openLogFile) rather than to a
// console it does not have, so nothing is lost by not inheriting.
func forkWindows(binaryPath string) error {
	// The handoff flag tells the successor to wait for THIS process to exit
	// before showing its window, so the user never sees two Locus windows.
	args := append([]string{}, os.Args[1:]...)
	args = append(args, HandoffFlag)

	cmd := exec.Command(binaryPath, args...)

	// Deliberately NOT os.Stdin/os.Stdout/os.Stderr — see above.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Detach from parent, and do not allocate a console for the child.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork failed: %w", err)
	}

	return nil
}
