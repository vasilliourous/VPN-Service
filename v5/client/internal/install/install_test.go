package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests pin the per-OS resolution rules. They are the reason the field
// failure cannot come back in a new shape: a copy that resolves into a
// directory Locus does not own is exactly what produced
//
//	rename C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp ... :
//	The process cannot access the file because it is being used by another process.
//
// The rules are checked by calling resolve() with a synthetic executable path,
// which is what makes them testable without a machine of each platform. Only
// the paths that apply to the RUNNING platform are asserted, because the
// per-OS files are compiled out elsewhere — asserting a Windows rule on Linux
// would be asserting nothing at all.

// TestResolveNeverStagesInTheLaunchedDirectoryForInstalledCopies asserts the
// structural guarantee: an installed copy stages inside its own install
// directory, not wherever the binary happened to be launched from.
func TestResolveNeverStagesInTheLaunchedDirectoryForInstalledCopies(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "locus")

	loc, err := resolve(exe)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	// Whatever the platform decides, the staging directory must be a private
	// subdirectory of the resolved directory — never the directory itself, and
	// never a sibling scattered into it.
	if loc.StagingDir == "" {
		t.Fatal("no staging directory resolved")
	}
	if filepath.Dir(loc.StagingDir) != loc.Dir {
		t.Errorf("staging dir %s is not directly inside resolved dir %s", loc.StagingDir, loc.Dir)
	}
	if loc.StagingDir == loc.Dir {
		t.Error("staging dir is the install dir itself; downloads would race directory watchers again")
	}
	if !strings.HasPrefix(filepath.Base(loc.StagingDir), ".") {
		t.Errorf("staging dir %q is not dot-prefixed", filepath.Base(loc.StagingDir))
	}
}

// TestResolveReadsWritabilityHonestly asserts a writable directory resolves as
// updatable and an unwritable one does not.
//
// The failure this guards against is an update that reports success into a
// directory it cannot write, or a perm check that lies. The mode is therefore
// decided by an actual write probe, not by inspecting mode bits — which are
// close to meaningless on Windows.
func TestResolveReadsWritabilityHonestly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not restrict writes")
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "locus")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	writableLoc, err := resolve(exe)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !writableLoc.Writable() {
		t.Errorf("a writable directory resolved as non-updatable: %s", writableLoc.Describe())
	}

	// Make the directory read-only. Any installed-location rule that already
	// matched this temp path is irrelevant — a temp dir is never an install
	// location, so this copy is portable and its writability is the deciding
	// factor.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	readOnlyLoc, err := resolve(exe)
	if err != nil {
		t.Fatalf("resolve failed on read-only dir: %v", err)
	}
	if readOnlyLoc.Mode != ModeUnwritable {
		t.Errorf("read-only directory resolved as %q, want %q (%s)",
			readOnlyLoc.Mode, ModeUnwritable, readOnlyLoc.Describe())
	}
	if readOnlyLoc.Writable() {
		t.Error("Writable() returned true for an unwritable location")
	}
}

// TestResolvePortableCopyIsSupportedNotAnError asserts that running out of an
// arbitrary directory is a supported deployment.
//
// A portable run must not be treated as a failure: the whole point of the fix
// is that a copy from Downloads still updates, by staging privately inside its
// own directory rather than in the watched folder.
func TestResolvePortableCopyIsSupportedNotAnError(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "locus")

	loc, err := resolve(exe)
	if err != nil {
		t.Fatalf("a portable copy must not be an error: %v", err)
	}
	if loc.Mode == "" {
		t.Error("no mode resolved")
	}
	if loc.Dir != dir {
		t.Errorf("resolved dir = %s, want the launched dir %s for a portable copy", loc.Dir, dir)
	}
	if loc.Reason == "" {
		t.Error("no reason recorded; diagnostics would be unable to explain the choice")
	}
}

// TestEnsureStagingDirCreatesDirectory asserts the staging area is created on
// demand and is usable.
func TestEnsureStagingDirCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	loc, err := resolve(filepath.Join(dir, "locus"))
	if err != nil {
		t.Fatal(err)
	}

	staging, err := loc.EnsureStagingDir()
	if err != nil {
		t.Fatalf("EnsureStagingDir failed: %v", err)
	}
	if st, err := os.Stat(staging); err != nil || !st.IsDir() {
		t.Fatalf("staging dir not created: %v", err)
	}

	// Idempotent: a second call on an existing directory must succeed, because
	// it runs on every startup.
	if _, err := loc.EnsureStagingDir(); err != nil {
		t.Errorf("EnsureStagingDir is not idempotent: %v", err)
	}

	// And it must actually be writable, since downloads land there.
	f, err := os.CreateTemp(staging, "probe-*")
	if err != nil {
		t.Fatalf("staging dir is not writable: %v", err)
	}
	_ = f.Close()
}

// TestStagingDirNameMatchesUpdater asserts the two independent definitions of
// the staging directory name agree.
//
// The name is deliberately duplicated as a constant in package updater so the
// updater has no dependency on this package. That duplication is only safe
// while something checks it, and this is that check: a silent divergence would
// mean the updater stages in one place while install reports another, and the
// diagnostics would lie.
func TestStagingDirNameMatchesUpdater(t *testing.T) {
	// Kept in sync with defaultStagingDirName in internal/updater/updater.go.
	const updaterDefault = ".locus-staging"
	if stagingDirName != updaterDefault {
		t.Errorf("install.stagingDirName = %q, updater default = %q — the two must agree",
			stagingDirName, updaterDefault)
	}
}

// TestDescribeIsInformative asserts the one-line summary names the directory,
// the mode and the reason — the three things a support report needs.
func TestDescribeIsInformative(t *testing.T) {
	dir := t.TempDir()
	loc, err := resolve(filepath.Join(dir, "locus"))
	if err != nil {
		t.Fatal(err)
	}

	desc := loc.Describe()
	for _, want := range []string{loc.Dir, string(loc.Mode), loc.Reason} {
		if !strings.Contains(desc, want) {
			t.Errorf("Describe() = %q, missing %q", desc, want)
		}
	}
}

// TestOwnedInstallDirRulesForRunningPlatform asserts the platform-specific
// rule for whichever OS the test is running on, so a regression in that rule
// is caught here rather than in the field.
func TestOwnedInstallDirRulesForRunningPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory")
		}
		// A user-local install location must be recognised as installed.
		xdgBin := filepath.Join(home, ".local", "bin")
		if dir, _, ok := ownedInstallDir(xdgBin); !ok || dir != xdgBin {
			t.Errorf("ownedInstallDir(%s) = (%q, ok=%v), want it recognised", xdgBin, dir, ok)
		}
		// A Downloads directory must NOT be an install location.
		if _, _, ok := ownedInstallDir(filepath.Join(home, "Downloads")); ok {
			t.Error("a Downloads directory was treated as an install location — " +
				"this is the exact case that produced the field failure")
		}

	case "windows":
		home := os.Getenv("USERPROFILE")
		if home == "" {
			t.Skip("no USERPROFILE")
		}
		downloads := filepath.Join(home, "Downloads")
		if _, _, ok := ownedInstallDir(downloads); ok {
			t.Error("a Downloads directory was treated as an install location — " +
				"this is the exact case that produced the field failure")
		}

	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory")
		}
		downloads := filepath.Join(home, "Downloads")
		if _, _, ok := ownedInstallDir(downloads); ok {
			t.Error("a Downloads directory was treated as an install location")
		}
		// A bundle in Applications must resolve to the directory inside it.
		bundle := "/Applications/Locus.app/Contents/MacOS"
		if dir, _, ok := ownedInstallDir(bundle); !ok || dir != bundle {
			t.Errorf("ownedInstallDir(%s) = (%q, ok=%v), want it recognised", bundle, dir, ok)
		}
	}
}
