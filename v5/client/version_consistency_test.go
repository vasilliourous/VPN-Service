package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"locus/internal/buildinfo"
)

// Version consistency guard.
//
// The client version is duplicated in four places, and they can silently drift:
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

// TestGeneratedWindowsResourceVersionMatchesRepoVersion guards the go:generate
// directive that rebuilds rsrc_windows_*.syso. Those files stamp the Windows
// executable's file/product version, so a stale literal there ships a binary
// whose Properties tab reports a different version than the app itself — a
// support trap, because the student reads one number and the log says another.
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
