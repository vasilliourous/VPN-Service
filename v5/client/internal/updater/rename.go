package updater

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Renaming a freshly downloaded file into place is the single most fragile step
// in the update, and the reason is almost never the network.
//
// The failure this file exists to fix, reported from the field:
//
//	download failed: cannot rename downloaded file: rename
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe.new:
//	The process cannot access the file because it is being used by another process.
//
// Three separate problems are stacked in that one message:
//
//  1. THE RENAME TARGET WAS IN A DIRECTORY WE DO NOT CONTROL. The updater
//     resolved its directory as filepath.Dir(os.Executable()), so a binary run
//     from Downloads staged its download *in Downloads* — the most heavily
//     observed directory on Windows. Defender, SmartScreen, the search
//     indexer, sync clients and download managers all open a new .exe within
//     milliseconds. Something had a handle on the file at rename time.
//
//  2. OUR OWN HANDLE WAS STILL OPEN. The download wrote to tmpPath and closed
//     it only via `defer` at function return — after the rename. On Windows a
//     file with any open handle cannot be renamed, so the code was guaranteed
//     to fail whenever the timing lined up, independent of any third party.
//
//  3. THE ERROR WAS OPAQUE. A raw os.Rename error names two paths and says
//     nothing a student or an operator can act on.
//
// The fix addresses all three: stage inside the app's own private directory
// (see internal/install), close explicitly before renaming, and retry a
// genuinely external holder with a bounded backoff before giving up with a
// message that says what to do.

const (
	// renameAttempts is how many times a rename is retried before giving up.
	//
	// The holder this defends against is transient by nature — a scanner that
	// opened the file the instant it appeared, a download manager finishing up.
	// Five attempts spanning ~1.9s covers those without making a genuinely
	// stuck file (a locked sync folder, a permissions problem) hang for long.
	renameAttempts = 5

	// renameBackoffBase is doubled each attempt: 100, 200, 400, 800, 1600ms.
	renameBackoffBase = 100 * time.Millisecond
)

// renameWithRetry renames src to dst, retrying transient sharing violations.
//
// Only the share-violation class is retried. A missing source directory, a
// read-only destination or a cross-device rename will fail identically on every
// attempt, and burning ~2s on them before returning the same error is pure
// delay — so those are classified and returned immediately.
func renameWithRetry(src, dst string) error {
	var lastErr error

	for attempt := 1; attempt <= renameAttempts; attempt++ {
		err := os.Rename(src, dst)
		if err == nil {
			if attempt > 1 {
				log.Printf("rename %s -> %s succeeded on attempt %d", filepath.Base(src), filepath.Base(dst), attempt)
			}
			return nil
		}
		lastErr = err

		if !isRetryableRenameError(err) {
			return err
		}
		if attempt == renameAttempts {
			break
		}

		delay := renameBackoffBase << (attempt - 1)
		log.Printf("rename %s -> %s blocked (%v); retrying in %s (attempt %d/%d)",
			filepath.Base(src), filepath.Base(dst), err, delay, attempt+1, renameAttempts)
		time.Sleep(delay)
	}

	return fmt.Errorf("%w (after %d attempts over %s)", lastErr, renameAttempts, renameTotalBackoff())
}

// renameTotalBackoff is the sum of every delay, reported in the final error so
// "we waited a while and it never freed up" is visible in the message.
func renameTotalBackoff() time.Duration {
	var total time.Duration
	for attempt := 1; attempt < renameAttempts; attempt++ {
		total += renameBackoffBase << (attempt - 1)
	}
	return total
}

// isRetryableRenameError reports whether an error is the transient
// sharing-violation class worth retrying, as opposed to a permanent failure.
//
// Matching on error text is unpleasant, and is done only because neither
// platform exposes a typed error for this through os.Rename:
//
//   - Windows surfaces ERROR_SHARING_VIOLATION (32) and
//     ERROR_ACCESS_DENIED (5) as "being used by another process" / "Access is
//     denied" when the cause is an open handle.
//   - Unix surfaces EBUSY ("device or resource busy") and, on some
//     filesystems, ETXTBSY ("text file busy") which is exactly the
//     "executable is open" case.
//
// Deliberately excluded: ENOENT/EACCES from a missing or read-only *directory*,
// and EXDEV (cross-device), which no amount of waiting will fix. Retrying those
// would convert a fast, clear failure into a slow one.
func isRetryableRenameError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	// Cross-device and missing-path failures are permanent. Checked first so a
	// message containing both a permanent and a transient phrase (some
	// filesystems are verbose) resolves to "do not retry".
	for _, permanent := range []string{
		"cross-device link",
		"invalid cross-device",
		"no such file or directory",
		"cannot find the file",
		"cannot find the path",
		"read-only file system",
	} {
		if strings.Contains(msg, permanent) {
			return false
		}
	}

	// The phrase table is deliberately platform-independent.
	//
	// Matching is on error text, and the same text can arrive on any platform:
	// a Windows-style message appears in a log quoted from a bug report, a
	// cross-mounted Windows volume can return NT statuses on Linux, and a
	// Wine/Proton run produces Windows strings on a Unix kernel. Gating the
	// table on runtime.GOOS meant the exact message from the field report was
	// classified "permanent" when tested off Windows — which is how a real
	// transient failure silently becomes unretryable.
	//
	// Retrying a phrase that cannot occur on this platform is harmless: the
	// retry only ever runs after a rename has already failed.
	for _, transient := range []string{
		// Windows
		"being used by another process",
		"used by another process",
		"cannot access the file",
		"access is denied", // ERROR_ACCESS_DENIED with the file held open
		"process cannot access",
		"sharing violation",
		// Unix
		"resource busy",
		"text file busy",
		"device busy",
	} {
		if strings.Contains(msg, transient) {
			return true
		}
	}

	return false
}

// renameBlockedError renders an actionable message for a rename that could not
// be completed, naming the file that was in the way and what to do about it.
//
// This replaces the raw os.Rename error, which named two absolute paths and
// left the student with "the process cannot access the file" and no way to
// discover *which* process, or that retrying after closing a folder window
// would work.
func renameBlockedError(dst string) string {
	base := filepath.Base(dst)

	var hint string
	switch runtime.GOOS {
	case "windows":
		hint = "Close any File Explorer window open on that folder, and check that " +
			"antivirus or a cloud-sync client is not scanning the Locus folder, then try again."
	case "darwin":
		hint = "Check that no backup or sync client is reading the Locus folder, then try again."
	default:
		hint = "Check that no other process is running or updating Locus, then try again."
	}

	return fmt.Sprintf(
		"could not replace %s — another program is holding the file open. %s "+
			"(The downloaded update was verified and is intact; nothing was changed.)",
		base, hint)
}
