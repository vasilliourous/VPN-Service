//go:build linux

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ownedInstallDir reports whether dir is a Locus *user-local* install location.
//
// Linux is intentionally the least intrusive of the three platforms. There is
// no machine-wide install target, nothing is written to /usr or /opt, and no
// package manager is involved. That is a deliberate scope decision, not an
// omission: the deployment model here is a portable binary, and the update
// failure this package exists to fix was never Linux-specific — os.Rename over
// a running binary is legal on Linux, so an in-place update from any writable
// directory already worked.
//
// What Linux does get is the one thing that matters: a stable, user-owned home
// for the binary so a copy that was launched from ~/Downloads can settle into a
// predictable path. Two locations are recognised, both user-local:
//
//	~/.local/bin/locus            XDG user executables (the common case)
//	~/.local/share/locus/bin/locus  app-private data-style location
//
// A copy run from anywhere else stays portable, which keeps `./locus` from an
// extracted tarball working exactly as it does today.
func ownedInstallDir(dir string) (string, string, bool) {
	clean := filepath.Clean(dir)

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", false
	}

	xdgBin := filepath.Join(home, ".local", "bin")
	appBin := filepath.Join(home, ".local", "share", "locus", "bin")

	if clean == xdgBin || clean == appBin {
		return clean, "installed at " + clean, true
	}

	// A portable copy running while a user-local install exists: prefer the
	// install, on the same reasoning as Windows and macOS. Never created
	// speculatively — only an existing directory is adopted.
	for _, candidate := range []string{xdgBin, appBin} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(candidate, "locus")); err == nil {
				return candidate, "installed at " + candidate + " (launched copy from " + clean + ")", true
			}
		}
	}

	return "", "", false
}

// DesktopIntegrationSummary describes where Linux users should expect to find
// the binary, for the diagnostics report. It exists so the client can tell a
// Linux user the truth — that nothing was installed system-wide — instead of
// implying an install that never happened.
func DesktopIntegrationSummary() string {
	home, err := os.UserHomeDir()
	if err != nil || err == nil && home == "" {
		return "portable binary; no system-wide install is performed on Linux"
	}
	return fmt.Sprintf("portable binary; user-local locations are %s and %s (nothing is written to /usr or /opt)",
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "share", "locus", "bin"))
}

// immutableMount reports whether this process runs from a read-only mount.
//
// An AppImage is a squashfs mount, so in-place self-update is impossible by
// construction — the mount is immutable. Detecting it lets the client explain
// the situation accurately ("download the new AppImage") rather than reporting
// a rename failure against a read-only filesystem.
//
// APPIMAGE is set by the AppImage runtime to the path of the .AppImage file
// itself, which is the cleanest signal. The /tmp/.mount_ argv[0] prefix is the
// older convention and is kept because it costs nothing.
func immutableMount() bool {
	if os.Getenv("APPIMAGE") != "" {
		return true
	}
	if exe, err := os.Executable(); err == nil && strings.HasPrefix(exe, "/tmp/.mount_") {
		return true
	}
	return false
}
