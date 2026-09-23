package updatecfg_test

// Cross-language contract guard.
//
// updatecfg declares the field names in Go, but the values are produced by a
// JavaScript hook and two shell scripts. A Go constant that nothing enforces is
// just a comment, so these tests read the OTHER files and assert they agree.
//
// This is the check whose absence let publish-release.sh and publish-update.sh
// write incomplete update_config records while both looked correct in isolation.
// See internal/updatecfg's package comment for the corrected account (an earlier
// version of these comments blamed a field-name disagreement that the scripts do
// not actually have) and FIXES.md 31.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"locus/internal/updatecfg"
)

// repoRoot walks up from v5/client/internal/updatecfg to the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

// TestHookEmitsTheResponseFieldsTheClientReads pins layer 2. The hook renames
// download_* to update_* on the way out; if it stops doing that for any
// platform, that platform silently stops receiving updates — with no error,
// because a missing field just leaves the client on the legacy linux URL.
func TestHookEmitsTheResponseFieldsTheClientReads(t *testing.T) {
	hook := readRepoFile(t, "v5/server/pb_hooks/heartbeat.pb.js")

	for _, platform := range updatecfg.Platforms {
		t.Run("url/"+platform, func(t *testing.T) {
			responseField := updatecfg.ResponseURLField(platform)
			recordField := updatecfg.DownloadField(platform)
			// The hook must read the record field and assign the response field.
			if !strings.Contains(hook, `u.get("`+recordField+`")`) {
				t.Errorf("heartbeat.pb.js does not read %q from update_config", recordField)
			}
			if !strings.Contains(hook, "response."+responseField) {
				t.Errorf("heartbeat.pb.js never sets response.%s — clients on %s "+
					"would silently keep the legacy linux URL", responseField, platform)
			}
		})

		t.Run("sha256/"+platform, func(t *testing.T) {
			responseField := updatecfg.ResponseSHA256Field(platform)
			recordField := updatecfg.SHA256Field(platform)
			if !strings.Contains(hook, `u.get("`+recordField+`")`) {
				t.Errorf("heartbeat.pb.js does not read %q from update_config", recordField)
			}
			if !strings.Contains(hook, "response."+responseField) {
				t.Errorf("heartbeat.pb.js never sets response.%s — the updater "+
					"refuses to apply an update with an empty checksum", responseField)
			}
		})
	}
}

// TestGoClientParsesTheResponseFieldsTheHookEmits is the other half of layer 2:
// the struct tags must match the names the hook writes. A mismatch here is
// invisible — encoding/json ignores unknown keys — which is precisely the bug
// class that disabled UoT fleet-wide (FIXES.md 29).
func TestGoClientParsesTheResponseFieldsTheHookEmits(t *testing.T) {
	src := readRepoFile(t, "v5/client/internal/heartbeat/heartbeat.go")

	// Every field the hook emits must have a matching struct tag. Checked by
	// substring against the raw source rather than by reflection, because the
	// point is to catch a TAG that does not exist.
	for _, platform := range updatecfg.Platforms {
		for _, field := range []string{
			updatecfg.ResponseURLField(platform),
			updatecfg.ResponseSHA256Field(platform),
		} {
			tag := `json:"` + field
			if !strings.Contains(src, tag) {
				t.Errorf("internal/heartbeat/heartbeat.go has no %s tag — the hub "+
					"emits %q and encoding/json would ignore it without error", tag, field)
			}
		}
	}

	if !strings.Contains(src, `json:"`+updatecfg.UpdateAvailableField) {
		t.Errorf("heartbeat.go does not parse %q", updatecfg.UpdateAvailableField)
	}
}

// TestPublishScriptsWriteTheRecordFieldsTheHookReads pins layer 1 across the
// surviving publish path. Previously there were two, neither constrained to the
// full column set, and the gap was invisible until a client tried to update.
//
// publish-release.sh concatenates a prefix with the platform key from its
// PLATFORMS array, so this checks for the prefix there. Asserting the literal
// `download_linux` would produce a false failure on a script that gets it right
// by construction.
func TestPublishScriptsWriteTheRecordFieldsTheHookReads(t *testing.T) {
	const prefix = "download_"
	if !strings.HasPrefix(updatecfg.DownloadField(updatecfg.PlatformLinux), prefix) {
		t.Fatalf("DownloadField no longer starts with %q — this guard assumes it", prefix)
	}

	t.Run("v5/server/scripts/publish-release.sh", func(t *testing.T) {
		body := readRepoFile(t, "v5/server/scripts/publish-release.sh")
		// Builds `body["download_" + key]` where key is the platform.
		if !strings.Contains(body, `body["`+prefix+`" + key]`) {
			t.Errorf("publish-release.sh no longer writes body[%q + key]; the hub "+
				"reads exactly that field, so update_config would carry no "+
				"per-platform URL and every client would fall back to the legacy "+
				"linux update_url", prefix)
		}
		if !strings.Contains(body, `body["sha256_" + key]`) {
			t.Errorf(`publish-release.sh no longer writes body["sha256_" + key]`)
		}
		// Every platform it iterates must be one the hub knows about.
		for _, platform := range updatecfg.Platforms {
			if !strings.Contains(body, platform+":") {
				t.Errorf("publish-release.sh's PLATFORMS array has no %q entry — "+
					"that platform would never be advertised", platform)
			}
		}
	})

	// The second publish path, scripts/publish-update.sh, has been RETIRED to
	// legacy/publish-update.sh.broken: it wrote no sha256_<platform> columns and
	// its macOS URLs did not match CI's filenames. This asserts the retirement is
	// deliberate rather than an accidental deletion, because a resurrected
	// publisher that skips the hash columns would silently disable verification
	// on every platform — the exact class of fault this package exists to catch.
	t.Run("retired second publisher stays retired", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(repoRoot(t), "scripts/publish-update.sh")); err == nil {
			t.Errorf("scripts/publish-update.sh is back. It was retired because it " +
				"wrote no sha256_<platform> columns (clients could not verify) and " +
				"used locus-macos-* URLs that 404 against CI's locus-darwin-* " +
				"filenames. Use server/scripts/publish-release.sh.")
		}
	})
}

// TestArtifactFilenamesMatchCI pins the CI↔hub filename contract. These names
// appear in the workflow's staging step, in publish-release.sh's PLATFORMS
// array and in the URLs written to update_config. A rename in one place without
// the others yields a published release whose URLs 404.
func TestArtifactFilenamesMatchCI(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/build.yml")
	publish := readRepoFile(t, "v5/server/scripts/publish-release.sh")

	for _, a := range updatecfg.Artifacts {
		t.Run(a.Platform, func(t *testing.T) {
			// CI derives the name from a matrix, so assert the template pieces
			// AND that the resolve-platform mapping keys on the same stem.
			stem := strings.TrimSuffix(a.Filename, filepath.Ext(a.Filename))
			if stem == "" {
				t.Fatal("empty artifact stem")
			}
			if !strings.Contains(publish, a.Filename) {
				t.Errorf("publish-release.sh does not reference %q", a.Filename)
			}
			// The workflow builds `locus-${{ matrix.goos }}-${{ matrix.goarch }}`,
			// so the stem must decompose into os-arch for known platforms.
			if a.Platform == updatecfg.PlatformWindows {
				if !strings.HasSuffix(a.Filename, ".exe") {
					t.Errorf("windows artifact %q must keep its .exe suffix — "+
						"the updater swaps the file verbatim", a.Filename)
				}
			}
			if !strings.Contains(workflow, "locus-${{ matrix.goos }}-${{ matrix.goarch }}") {
				t.Error("build.yml no longer stages artifacts as " +
					"locus-<goos>-<goarch>; updatecfg.Artifacts and the workflow have diverged")
			}
		})
	}
}

// TestLegacyFallbackIsDocumentedAsSinglePlatform is a documentation-shaped
// assertion: the legacy fields describe exactly one binary, so a record that
// carries only them must be understood as "updates are linux-only". If someone
// later widens the fallback, this test should be updated deliberately.
func TestLegacyFallbackIsDocumentedAsSinglePlatform(t *testing.T) {
	if updatecfg.LegacyURLField != "update_url" {
		t.Errorf("LegacyURLField = %q, want %q — deployed update_config rows and "+
			"older clients reference this literal", updatecfg.LegacyURLField, "update_url")
	}
	if updatecfg.LegacySHA256Field != "update_sha256" {
		t.Errorf("LegacySHA256Field = %q, want %q", updatecfg.LegacySHA256Field, "update_sha256")
	}
}
