package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"locus/internal/buildinfo"
)

// Version consistency guard.
//
// The client version is duplicated across six files, and they can silently drift:
//
//	v5/VERSION               — the canonical runtime source (Makefile + CI read it)
//	main.go `version`        — the fallback literal, compiled in when ldflags is absent
//	wails.json "version"     — Wails build metadata
//	frontend/package.json    — npm build metadata
//
// Only v5/VERSION is authoritative for releases. The copies exist because the
// tooling reads them independently, but when they disagree the result is a
// binary whose diagnostics, installer metadata and update comparisons tell
// three different stories. This test makes the drift a build failure instead of
// a support mystery.
//
// NOTE: the test is intentionally NOT run against main.go's fallback alone —
// the official build overrides it via -ldflags. It checks that the *fallback*
// matches v5/VERSION, because a stale fallback is what an uninstrumented build
// reports, and buildinfo.Detect flags that case as untrustworthy.

// repoVersion reads the canonical version from v5/VERSION.
func repoVersion(t *testing.T) string {
	t.Helper()
	// This package lives at v5/client, so v5/VERSION is one level up.
	data, err := os.ReadFile(filepath.Join("..", "VERSION"))
	if err != nil {
		t.Fatalf("cannot read v5/VERSION: %v", err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		t.Fatal("v5/VERSION is empty")
	}
	return v
}

func TestFallbackVersionMatchesRepoVersion(t *testing.T) {
	want := repoVersion(t)
	if version != want {
		t.Errorf("main.go fallback version = %q, but v5/VERSION = %q.\n"+
			"An uninstrumented build would report a version that does not exist in "+
			"the source tree. Update the fallback in main.go to match v5/VERSION.",
			version, want)
	}
}

func TestBuildinfoFallbackMatchesRepoVersion(t *testing.T) {
	// The buildinfo package carries its own copy of the fallback so it can flag
	// uninstrumented builds. It must agree, or an uninstrumented binary reports
	// one version in the UI and another in diagnostics.
	fb := buildinfo.FallbackVersion()
	want := repoVersion(t)
	if fb != want {
		t.Errorf("internal/buildinfo fallbackVersion = %q, but v5/VERSION = %q", fb, want)
	}
}

func TestWailsJSONVersionMatchesRepoVersion(t *testing.T) {
	want := repoVersion(t)
	data, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatalf("cannot read wails.json: %v", err)
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("wails.json is not valid JSON: %v", err)
	}
	if cfg.Version != want {
		t.Errorf("wails.json version = %q, but v5/VERSION = %q", cfg.Version, want)
	}
}

func TestFrontendPackageVersionMatchesRepoVersion(t *testing.T) {
	want := repoVersion(t)
	data, err := os.ReadFile(filepath.Join("frontend", "package.json"))
	if err != nil {
		t.Fatalf("cannot read frontend/package.json: %v", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("frontend/package.json is not valid JSON: %v", err)
	}
	if pkg.Version != want {
		t.Errorf("frontend/package.json version = %q, but v5/VERSION = %q", pkg.Version, want)
	}
}

// TestFrontendLockfileVersionMatchesRepoVersion guards the npm lockfile root.
//
// WHY: found 2026-09-19 — frontend/package-lock.json was rooted at "2.0.0"
// while package.json said "2.2.0", and nothing checked the lockfile at all, so
// it had drifted across two releases unnoticed. It was harmless only because CI
// runs `npm install` (which tolerates a stale root) rather than `npm ci` (which
// fails hard on a package.json/lockfile mismatch) and only the built output
// ships. That is luck, not safety: this test makes the drift visible now, long
// before someone tightens the CI install step.
//
// The lockfile carries the version twice: once at the document root and once in
// packages[""]. Both must agree with v5/VERSION, or `npm ci` becomes a
// release-blocking failure.
func TestFrontendLockfileVersionMatchesRepoVersion(t *testing.T) {
	want := repoVersion(t)
	data, err := os.ReadFile(filepath.Join("frontend", "package-lock.json"))
	if err != nil {
		t.Fatalf("cannot read frontend/package-lock.json: %v", err)
	}
	var lock struct {
		Version  string `json:"version"`
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("frontend/package-lock.json is not valid JSON: %v", err)
	}
	if lock.Version != want {
		t.Errorf("frontend/package-lock.json root version = %q, but v5/VERSION = %q.\n"+
			"Bump it with: v5/server/scripts/bump-version.sh <version>",
			lock.Version, want)
	}
	// packages[""] is the entry describing this package itself. It is a
	// separate field from the document root and drifts independently.
	if root, ok := lock.Packages[""]; ok {
		if root.Version != want {
			t.Errorf("frontend/package-lock.json packages[\"\"] version = %q, but "+
				"v5/VERSION = %q — `npm ci` would fail on this mismatch",
				root.Version, want)
		}
	} else {
		t.Error("frontend/package-lock.json has no packages[\"\"] entry — " +
			"the lockfile layout changed, so this guard is now blind and must be " +
			"updated rather than deleted")
	}
}

// TestGeneratedWindowsResourceVersionMatchesRepoVersion guards the go:generate
// directive that REBUILDS rsrc_windows_*.syso.
//
// This asserts the directive text only. It is a necessary check but NOT a
// sufficient one — the directive can be correct while the committed .syso files
// are stale, because nothing re-runs `go generate` automatically. The check
// that actually protects the release is
// TestCommittedWindowsResourceBinariesMatchRepoVersion below, which reads the
// version OUT of the compiled resources.
func TestGeneratedWindowsResourceVersionMatchesRepoVersion(t *testing.T) {
	want := repoVersion(t)
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	src := string(data)
	needle := "--product-version " + want + " --file-version " + want
	if !strings.Contains(src, needle) {
		t.Errorf("main.go go:generate directive does not contain %q; "+
			"a stale value here ships a Windows binary whose file version "+
			"disagrees with v5/VERSION (%s)", needle, want)
	}
}

// windowsResource holds the version strings a .syso embeds.
type windowsResource struct {
	FileVersion    string
	ProductVersion string
	ProductName    string
	FileDesc       string
}

// readWindowsResource extracts VS_VERSION_INFO string values from a compiled
// Windows resource object.
//
// go-winres writes the version block as UTF-16LE, so the interesting strings are
// not visible to a plain byte scan of the file. We decode each even-offset
// UTF-16LE alignment and keep the plausible ASCII strings, then associate the
// keys with the values that follow them. That is robust enough for a guard whose
// job is to notice "this says 2.0.0 when it should say 2.1.0", without pulling
// in a PE parser.
func readWindowsResource(t *testing.T, path string) windowsResource {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}

	// Decode both UTF-16 alignments; a resource block is 4-byte aligned and the
	// alignment depends on the offsets of everything preceding it.
	var best []string
	for _, off := range []int{0, 1} {
		if off >= len(raw) {
			continue
		}
		tail := raw[off:]
		if len(tail)%2 == 1 {
			tail = tail[:len(tail)-1]
		}
		runes := make([]uint16, len(tail)/2)
		for i := range runes {
			runes[i] = uint16(tail[2*i]) | uint16(tail[2*i+1])<<8
		}
		var (
			strs []string
			cur  []rune
		)
		flush := func() {
			if len(cur) >= 2 {
				strs = append(strs, string(cur))
			}
			cur = cur[:0]
		}
		for _, r := range runes {
			if r >= 32 && r < 127 {
				cur = append(cur, rune(r))
			} else {
				flush()
			}
		}
		flush()
		if len(strs) > len(best) {
			best = strs
		}
	}

	var out windowsResource
	// The version block is a flat key/value sequence: "FileVersion", "2.1.0",
	// "ProductName", "Locus", ... so pair each known key with the next string.
	keys := map[string]*string{
		"FileVersion":     &out.FileVersion,
		"ProductVersion":  &out.ProductVersion,
		"ProductName":     &out.ProductName,
		"FileDescription": &out.FileDesc,
	}
	for i, s := range best {
		if dst, ok := keys[s]; ok && i+1 < len(best) {
			*dst = best[i+1]
		}
	}
	return out
}

// TestCommittedWindowsResourceBinariesMatchRepoVersion is the check that would
// have caught the shipped defect: the committed rsrc_windows_*.syso files
// stamped version 2.0.0 (and product name "MyVPN") while v5/VERSION said 2.1.0
// and the app called itself Locus. Nothing detected it, because the previous
// guard only grepped the go:generate directive for the right number — and the
// directive WAS right. The stale artifacts are what shipped.
//
// Consequence if it regresses: a student right-clicks locus.exe, sees an
// outdated version and the wrong product name, and reports a build that does
// not exist.
func TestCommittedWindowsResourceBinariesMatchRepoVersion(t *testing.T) {
	want := repoVersion(t)

	for _, arch := range []string{"amd64", "arm64"} {
		path := "rsrc_windows_" + arch + ".syso"
		t.Run(arch, func(t *testing.T) {
			if _, err := os.Stat(path); err != nil {
				t.Skipf("%s not present (regenerate with: go generate -tags windows)", path)
			}
			res := readWindowsResource(t, path)

			if res.FileVersion == "" && res.ProductVersion == "" {
				t.Fatalf("could not read any version strings from %s — the resource "+
					"format may have changed; this guard is now blind and must be "+
					"updated rather than deleted", path)
			}
			if res.FileVersion != want {
				t.Errorf("%s FileVersion = %q, but v5/VERSION = %q.\n"+
					"The Windows Properties tab would show a version that no longer "+
					"exists. Regenerate: cd v5/client && go generate -tags windows",
					path, res.FileVersion, want)
			}
			if res.ProductVersion != want {
				t.Errorf("%s ProductVersion = %q, but v5/VERSION = %q",
					path, res.ProductVersion, want)
			}
			// Brand check: the old resources said "MyVPN" long after the product
			// was renamed, so assert the current name rather than merely
			// asserting "not the old one".
			if res.ProductName != "Locus" {
				t.Errorf("%s ProductName = %q, want %q — a renamed product left the "+
					"old brand in the executable's metadata", path, res.ProductName, "Locus")
			}
		})
	}
}

// ── Drift guard: every copy of the version must be accounted for ───────────
//
// The tests above assert that the copies WE KNOW ABOUT agree. None of them can
// notice a NEW copy appearing — a version constant added to a new package, a
// hardcoded version in the Vue UI, a JSON fixture the build reads. That is how
// drift recurs: the seventh copy is invisible to a list of six.
//
// This test inverts the question. It derives the running version, greps the
// whole client tree for it, and requires every hit to be in a file that
// bump-version.sh maintains. A hit anywhere else fails the build and names the
// file, so the fix is always "add it to the accounting list (and to
// bump-version.sh's KNOWN_VERSION_FILES)" or "remove the copy".
//
// Deliberately greps for the CONCRETE version string rather than looking for
// something version-shaped: a version-shaped regex would match dependency
// versions, ports, timeouts and dates, and would have to be loosened until it
// caught nothing. An exact-string search over the current release version has
// no false positives worth speaking of.
func TestNoUnaccountedCopyOfVersion(t *testing.T) {
	want := repoVersion(t)

	// Files whose copy of the version is intentional and maintained. Keep in
	// sync with KNOWN_VERSION_FILES in v5/server/scripts/bump-version.sh.
	accounted := map[string]string{
		filepath.FromSlash("../VERSION"):                      "canonical source of truth",
		filepath.FromSlash("main.go"):                         "runtime fallback + go:generate directive",
		filepath.FromSlash("internal/buildinfo/buildinfo.go"): "uninstrumented-build fallback",
		filepath.FromSlash("wails.json"):                      "Wails build metadata",
		filepath.FromSlash("frontend/package.json"):           "npm build metadata",
		filepath.FromSlash("frontend/package-lock.json"):      "npm lockfile root",
	}

	// Directories that are build output, vendored, or not part of the source.
	skipDirs := map[string]bool{
		"node_modules": true, "dist": true, ".git": true,
		"build": true, "wailsjs": true,
	}

	var offenders []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable path is not this test's concern
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Text sources only. A version string inside a binary (.syso, a built
		// executable) is checked by the resource tests above, which read the
		// actual artifact rather than raw bytes.
		switch filepath.Ext(path) {
		case ".go", ".json", ".ts", ".js", ".vue", ".sh", ".yml", ".yaml":
		default:
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			// Tests deliberately contain literal versions as fixtures.
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), want) {
			return nil
		}
		if _, ok := accounted[filepath.Clean(path)]; !ok {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("version %s appears in %d file(s) not maintained by the release tooling:\n%s\n\n"+
			"Either add each file to KNOWN_VERSION_FILES in "+
			"v5/server/scripts/bump-version.sh (and to the accounting map in this "+
			"test), or read the version from a maintained source instead of "+
			"hardcoding it. An unaccounted copy is what makes a release ship a "+
			"binary that reports a version the source tree disagrees with.",
			want, len(offenders), "  "+strings.Join(offenders, "\n  "))
	}
}
