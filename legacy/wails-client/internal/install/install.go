// Package install answers one question: where does Locus live on this machine,
// and is that place safe to self-update into?
//
// # WHY THIS PACKAGE EXISTS
//
// The updater used to resolve its own directory as
// filepath.Dir(os.Executable()) and then download, rename and swap *in place*.
// That makes correctness depend entirely on where the student happened to
// launch the app from, and the worst case is the most common one: a binary run
// straight out of Downloads.
//
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe
//
// Downloads is the most externally-observed directory on Windows — Defender,
// SmartScreen, the search indexer and assorted sync clients all open a freshly
// written .exe within milliseconds of it appearing. The old code downloaded to
// `<binary>.new.tmp` and renamed it to `<binary>.new` *in that same directory*,
// while its own write handle was still open. On Windows an open handle blocks a
// rename, so the update failed with:
//
//	download failed: cannot rename downloaded file: rename
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe.new:
//	The process cannot access the file because it is being used by another process.
//
// The fix is not to retry harder in an arbitrary directory. It is to install
// into a directory this application owns:
//
//   - Windows: %ProgramFiles%\Locus (the installer's location), falling back to
//     %LOCALAPPDATA%\Programs\Locus for a per-user install.
//   - macOS:   /Applications/Locus.app
//   - Linux:   deliberately unintrusive — no system-wide write is attempted.
//     A user-local directory is used when one applies, and an AppImage-style
//     portable run keeps working exactly as before.
//
// The launched directory remains a *fallback*, never the preference, so a
// portable copy extracted anywhere (including Downloads) still updates — it
// just stages and renames inside a private subdirectory instead of racing the
// filesystem watchers on the directory itself.
package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// Mode describes how this copy of Locus was deployed. The distinction decides
// whether the updater may replace the executable itself, or must hand the new
// binary to the user / a package manager.
type Mode string

const (
	// ModeInstalled means the executable lives in a directory the application
	// owns (an installer location). Full in-place self-update is safe.
	ModeInstalled Mode = "installed"

	// ModePortable means the executable was run from an arbitrary directory —
	// an extracted zip, a Downloads folder, a USB stick. Self-update is still
	// attempted, but staged privately and reported as portable in diagnostics.
	ModePortable Mode = "portable"

	// ModeUnwritable means the executable's directory cannot be written to.
	// Self-update is impossible without elevation; the caller must say so
	// clearly instead of failing mid-download.
	ModeUnwritable Mode = "unwritable"

	// ModeImmutable means the executable lives inside a read-only mount (an
	// AppImage). No write of any kind can succeed, so the only correct update
	// is replacing the bundle. Distinct from ModeUnwritable because the remedy
	// the user needs is different: elevation does not help here.
	ModeImmutable Mode = "immutable"
)

// Location is the resolved answer: where the app lives, how it was deployed,
// where updates should be staged, and why this answer was chosen.
//
// The Reason field is not decoration — it is the difference between a support
// report that says "update failed" and one that says which directory was used
// and which rule selected it.
type Location struct {
	// Dir is the directory holding the running executable.
	Dir string

	// Binary is the executable's file name (locus.exe, locus, …).
	Binary string

	// Mode is how this copy was deployed.
	Mode Mode

	// StagingDir is a private, app-owned directory used for downloads before
	// they are renamed into place. It is created on demand. Keeping it private
	// is the entire point: a filesystem watcher indexing the parent directory
	// no longer sits on the file being renamed.
	StagingDir string

	// Reason explains how this Location was chosen, for diagnostics.
	Reason string
}

// Executable resolves the deployment location of the currently running binary.
//
// It never returns an error for the "portable" case: running from an arbitrary
// directory is a supported deployment, not a failure. An error is returned only
// when the executable path cannot be determined at all, which means the process
// is in a state where updating is meaningless anyway.
func Executable() (Location, error) {
	execPath, err := os.Executable()
	if err != nil {
		return Location{}, fmt.Errorf("cannot determine executable path: %w", err)
	}
	// Resolve symlinks: a Homebrew- or symlink-launched binary must install
	// next to its real file, not next to the link (whose directory is often
	// /usr/local/bin and not ours to write).
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}

	return resolve(execPath)
}

// resolve builds a Location for an already-resolved executable path.
//
// It is split out from Executable so it can be tested against a synthetic path,
// which is how the per-OS rules below are verified without needing a machine of
// each platform.
func resolve(execPath string) (Location, error) {
	dir := filepath.Dir(execPath)
	loc := Location{
		Dir:    dir,
		Binary: filepath.Base(execPath),
	}

	if immutableMount() {
		// A read-only container (an AppImage, most commonly) cannot be updated
		// in place by construction — the mount is immune to writes and the
		// binary is inside it. Reporting this up front turns what would
		// otherwise surface as a rename/permission failure deep in the
		// download path into an accurate, actionable statement.
		loc.Mode = ModeImmutable
		loc.Reason = dir + " is on a read-only mounted bundle; replace the bundle itself rather than self-updating"
	} else if owned, reason, ok := ownedInstallDir(dir); ok {
		loc.Dir = owned
		loc.Mode = ModeInstalled
		loc.Reason = reason
	} else if writable(dir) {
		loc.Mode = ModePortable
		loc.Reason = "portable run from " + dir + " (writable, no installer location detected)"
	} else {
		loc.Mode = ModeUnwritable
		loc.Reason = dir + " is not writable and no installer location applies"
	}

	// The staging directory lives *inside* the install directory, deliberately.
	//
	// Not the system temp directory: os.Rename is only atomic within a single
	// filesystem, and /tmp or %TEMP% is frequently a different volume from the
	// install location. Renaming across volumes falls back to a copy, which is
	// neither atomic nor safe for a running executable.
	//
	// Not the install directory itself either: a hidden, app-owned subdirectory
	// keeps our transient download filenames out of sight of indexers and
	// cleaners watching the parent.
	loc.StagingDir = filepath.Join(loc.Dir, stagingDirName)

	return loc, nil
}

// stagingDirName is the private download area. It is dot-prefixed so it sorts
// out of the way on Unix and is treated as hidden, and it is excluded from the
// cleaning passes that walk the install directory.
const stagingDirName = ".locus-staging"

// Writable reports whether this location can be self-updated in place.
func (l Location) Writable() bool { return l.Mode != ModeUnwritable }

// EnsureStagingDir creates the private staging directory, returning its path.
func (l Location) EnsureStagingDir() (string, error) {
	if l.StagingDir == "" {
		return "", fmt.Errorf("no staging directory resolved")
	}
	if err := os.MkdirAll(l.StagingDir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create staging directory %s: %w", l.StagingDir, err)
	}
	return l.StagingDir, nil
}

// Describe renders a one-line summary for diagnostics and support reports.
func (l Location) Describe() string {
	return fmt.Sprintf("%s (%s) — %s", l.Dir, l.Mode, l.Reason)
}

// writable reports whether a directory can be written to by this process.
//
// A real write is attempted rather than inspecting mode bits: on Windows the
// mode bits are close to meaningless, and on both platforms an ACL or a
// read-only mount can deny a write that the permissions suggest should work.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".locus-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
