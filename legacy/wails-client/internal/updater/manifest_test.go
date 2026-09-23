package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// Tests for the credential-free public release manifest fallback.
//
// This path exists so a client that cannot use the heartbeat (not activated, or
// activation blocked by a bad build) can still learn that a fix has been
// published. The failure mode it guards against is a deadlock: the thing that
// is broken is the thing that would have delivered the repair.

func manifestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/release" {
			t.Errorf("unexpected path %q, want /api/release", r.URL.Path)
		}
		// The request must carry no credentials of any kind.
		if r.Header.Get("Authorization") != "" {
			t.Error("release manifest request must not send Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestFetchPublicReleaseParsesPublishedRelease(t *testing.T) {
	key := platformKey()
	if key == "" {
		t.Skip("no platform key for this GOOS/GOARCH")
	}
	body := `{"ok":true,"published":true,"version":"9.9.9","platforms":{` +
		`"` + key + `":{"url":"https://example.org/updates/9.9.9/locus","sha256":"abc123"}}}`
	srv := manifestServer(t, body, 200)
	defer srv.Close()

	info, err := FetchPublicRelease(context.Background(), srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("FetchPublicRelease: %v", err)
	}
	if info == nil {
		t.Fatal("info is nil, want a parsed release")
	}
	if info.Version != "9.9.9" {
		t.Errorf("Version = %q, want 9.9.9", info.Version)
	}
	if got := info.PlatformDownloadURL(); got != "https://example.org/updates/9.9.9/locus" {
		t.Errorf("PlatformDownloadURL() = %q", got)
	}
	if got := info.PlatformSHA256(); got != "abc123" {
		t.Errorf("PlatformSHA256() = %q, want abc123", got)
	}
}

func TestFetchPublicReleaseNothingPublishedIsNotAnError(t *testing.T) {
	// "the hub is reachable and has published nothing" must be distinguishable
	// from "we could not ask" — the caller reports them differently to the user.
	srv := manifestServer(t, `{"ok":true,"published":false}`, 200)
	defer srv.Close()

	info, err := FetchPublicRelease(context.Background(), srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("expected no error for an empty manifest, got %v", err)
	}
	if info != nil {
		t.Errorf("info = %+v, want nil when nothing is published", info)
	}
}

func TestFetchPublicReleaseHandlesMissingPlatform(t *testing.T) {
	// Published, but not for us: must yield a version with no URL so the
	// caller's existing "no asset for this platform" path handles it.
	srv := manifestServer(t, `{"ok":true,"published":true,"version":"9.9.9","platforms":{}}`, 200)
	defer srv.Close()

	info, err := FetchPublicRelease(context.Background(), srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("FetchPublicRelease: %v", err)
	}
	if info == nil || info.Version != "9.9.9" {
		t.Fatalf("info = %+v, want version 9.9.9 with no platform asset", info)
	}
	if info.PlatformDownloadURL() != "" {
		t.Errorf("PlatformDownloadURL() = %q, want empty", info.PlatformDownloadURL())
	}
}

func TestFetchPublicReleaseRejectsBadResponses(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"http error", `{}`, 500},
		{"not json", `<html>nope</html>`, 200},
		{"reported error", `{"ok":false}`, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := manifestServer(t, tc.body, tc.status)
			defer srv.Close()
			if _, err := FetchPublicRelease(context.Background(), srv.URL, srv.Client()); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestPlatformKeyIsStable(t *testing.T) {
	key := platformKey()
	switch runtime.GOOS {
	case "linux":
		if key != "linux" {
			t.Errorf("platformKey() = %q, want linux", key)
		}
	case "windows":
		if key != "windows" {
			t.Errorf("platformKey() = %q, want windows", key)
		}
	case "darwin":
		if key != "macos_arm" && key != "macos_intel" {
			t.Errorf("platformKey() = %q, want a macos_* key", key)
		}
	}
}

// TestReleaseManifestShapeMatchesHub ensures we decode the exact keys the hook
// emits. A mismatch here would silently produce "no update" for every client —
// the same class of bug as the server_port_uot/uot_port field-name mismatch.
func TestReleaseManifestShapeMatchesHub(t *testing.T) {
	body := `{"ok":true,"published":true,"version":"1.2.3",
	  "platforms":{"linux":{"url":"u","sha256":"s"}}}`
	var m releaseManifest
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("cannot decode hub manifest shape: %v", err)
	}
	if !m.OK || !m.Published || m.Version != "1.2.3" {
		t.Errorf("decoded manifest wrong: %+v", m)
	}
	if m.Platforms["linux"].URL != "u" || m.Platforms["linux"].SHA256 != "s" {
		t.Errorf("platform entry decoded wrong: %+v", m.Platforms)
	}
}
