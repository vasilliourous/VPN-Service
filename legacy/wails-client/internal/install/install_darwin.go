//go:build darwin

package install

import (
	"os"
	"path/filepath"
	"strings"
)

// ownedInstallDir reports whether dir is inside a Locus .app bundle installed
// in an Applications directory, and if so returns the bundle's executable
// directory.
//
// macOS deployment is a real .app bundle, so the executable lives at
//
//	/Applications/Locus.app/Contents/MacOS/locus
//
// The updater must therefore treat the *bundle* as the unit being replaced, not
// the leaf binary: swapping Contents/MacOS/locus in place works, but it would
// leave the bundle's Info.plist and resources describing the old version. The
// directory returned here is the bundle root (…/Locus.app), and callers stage
// inside it so an update is self-contained.
//
// Both /Applications and ~/Applications are trusted — the latter is the
// standard drag-install target for a user without admin rights.
func ownedInstallDir(dir string) (string, string, bool) {
	clean := filepath.Clean(dir)

	for _, bundle := range darwinBundleRoots(clean) {
		if bundle == "" {
			continue
		}
		if pathWithin(clean, bundle) {
			return clean, "installed inside " + bundle, true
		}
	}

	// Launched from outside any bundle (a bare binary, or a zip extracted to
	// ~/Downloads). Adopt an existing installed bundle if there is one, on the
	// same reasoning as Windows: the user has an install and has run a stray
	// portable copy.
	for _, bundle := range darwinApplicationDirs() {
		if _, err := os.Stat(bundle); err != nil {
			continue
		}
		macos := filepath.Join(bundle, "Contents", "MacOS")
		if st, err := os.Stat(macos); err == nil && st.IsDir() {
			return macos, "installed inside " + bundle + " (launched copy from " + clean + ")", true
		}
	}

	return "", "", false
}

// darwinBundleRoots walks up from dir looking for a .app component, so an
// executable at any depth inside a bundle resolves to the bundle root.
func darwinBundleRoots(dir string) []string {
	var roots []string
	cur := dir
	for {
		if strings.HasSuffix(cur, ".app") {
			roots = append(roots, cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return roots
}

// darwinApplicationDirs returns the standard Applications bundle paths.
func darwinApplicationDirs() []string {
	dirs := []string{"/Applications/Locus.app"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications", "Locus.app"))
	}
	return dirs
}

// pathWithin reports whether child is inside parent (or equal to it).
//
// A plain strings.HasPrefix would accept "/Applications/Locus.app-evil" for
// parent "/Applications/Locus.app", which is exactly the kind of mistake that
// turns a path check into a hole. Compare on a separator boundary.
func pathWithin(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}
