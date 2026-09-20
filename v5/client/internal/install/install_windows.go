//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
)

// ownedInstallDir reports whether dir is a Locus installer location, and if so
// returns the canonical directory to use.
//
// Two locations are trusted, in order:
//
//  1. %ProgramFiles%\Locus — the machine-wide installer target. Writable when
//     the process is elevated, which the client already is on Windows: the
//     embedded manifest sets requestedExecutionLevel="requireAdministrator"
//     (see rsrc_windows_amd64.syso), so self-update into Program Files works.
//
//  2. %LOCALAPPDATA%\Programs\Locus — the conventional per-user install target
//     used when the machine-wide install is not available. Always writable by
//     the installing user, so it is the more robust of the two.
//
// Anything else — Downloads, Desktop, a USB stick — is deliberately NOT an
// install location, so it resolves as portable and gets the private-staging
// treatment instead.
func ownedInstallDir(dir string) (string, string, bool) {
	clean := filepath.Clean(dir)

	for _, candidate := range windowsInstallDirs() {
		if candidate == "" {
			continue
		}
		if strings.EqualFold(filepath.Clean(candidate), clean) {
			return clean, "installed at " + clean, true
		}
	}

	// The executable is running from somewhere else entirely. If a Locus
	// installer location exists on this machine, prefer it: the user has an
	// installed copy and has somehow launched a stray portable one, and
	// updating the installed copy is what they actually want.
	//
	// Only a location that already exists is adopted. Creating Program Files\
	// Locus on a machine that never had it would silently relocate a portable
	// user's install out from under them.
	for _, candidate := range windowsInstallDirs() {
		if candidate == "" {
			continue
		}
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, "installed at " + candidate + " (launched portable copy from " + clean + ")", true
		}
	}

	return "", "", false
}

// windowsInstallDirs returns the candidate install directories, most preferred
// first, skipping any whose environment variable is unset.
func windowsInstallDirs() []string {
	var dirs []string
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		dirs = append(dirs, filepath.Join(pf, "Locus"))
	}
	if pf := os.Getenv("ProgramW6432"); pf != "" {
		// On 64-bit Windows a 32-bit process sees the (x86) view via
		// ProgramFiles. Check the native view explicitly so both resolve to the
		// same directory rather than two different ones.
		dirs = append(dirs, filepath.Join(pf, "Locus"))
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		dirs = append(dirs, filepath.Join(la, "Programs", "Locus"))
	}
	return dirs
}
