package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"locus/internal/pinned"
)

// Public release manifest support — the update path that does not need a code.
//
// The heartbeat is the primary and preferred source of update information: it
// carries the staged-rollout decision (which is per-device) and the freshest
// config. But it requires a bound activation code, so it CANNOT be the only
// path. If the installed build is bad enough that activation fails, or the code
// is suspended, the client would be permanently unable to learn that a fix has
// been published — the failure prevents escaping the failure.
//
// GET /api/release answers "what is the current public release?" with no
// credentials. This module consumes it as a fallback only.

// releaseManifest mirrors the JSON served by pb_hooks/release.pb.js.
type releaseManifest struct {
	OK        bool   `json:"ok"`
	Published bool   `json:"published"`
	Version   string `json:"version"`
	Platforms map[string]struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"platforms"`
}

// platformKey maps the running GOOS/GOARCH to the manifest's platform keys,
// which mirror the update_config column names.
func platformKey() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "macos_arm"
		}
		return "macos_intel"
	}
	return ""
}

// FetchPublicRelease asks the hub for the current published release without
// using an activation code. Returns (nil, nil) when the hub is reachable but
// has published nothing, so callers can distinguish "no release" from "could
// not ask".
//
// hubURL is the base hub URL (e.g. https://example.org).
func FetchPublicRelease(ctx context.Context, hubURL string, client *http.Client) (*UpdateInfo, error) {
	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    2,
				IdleConnTimeout: 30 * time.Second,
				TLSClientConfig: pinned.TLSClientConfig(),
			},
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hubURL+"/api/release", nil)
	if err != nil {
		return nil, fmt.Errorf("cannot build release request: %w", err)
	}
	// Identifies the caller in hub logs without leaking anything about the
	// device: no code, no fingerprint, no version.
	req.Header.Set("User-Agent", "Locus-Client/release-check")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release manifest request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release manifest returned status %d", resp.StatusCode)
	}

	// Bounded read: the manifest is tiny, and an unexpected body must not be
	// able to exhaust memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("release manifest read failed: %w", err)
	}

	var m releaseManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("release manifest is not valid JSON: %w", err)
	}
	if !m.OK {
		return nil, fmt.Errorf("release manifest reported an error")
	}
	if !m.Published || m.Version == "" {
		return nil, nil // reachable, nothing published
	}

	key := platformKey()
	p, ok := m.Platforms[key]
	if !ok || p.URL == "" {
		// Published, but not for this platform. Report it as an UpdateInfo with
		// no URL for this platform so the caller's existing "no asset" handling
		// produces the right message rather than a generic failure.
		return &UpdateInfo{Version: m.Version}, nil
	}

	info := &UpdateInfo{Version: m.Version}
	// Populate the per-platform fields so the existing
	// PlatformDownloadURL/PlatformSHA256 selectors work unchanged.
	switch key {
	case "linux":
		info.DownloadURLLinux = p.URL
		info.SHA256Linux = p.SHA256
	case "windows":
		info.DownloadURLWindows = p.URL
		info.SHA256Windows = p.SHA256
	case "macos_intel":
		info.DownloadURLMacOSIntel = p.URL
		info.SHA256MacOSIntel = p.SHA256
	case "macos_arm":
		info.DownloadURLMacOSARM = p.URL
		info.SHA256MacOSARM = p.SHA256
	}
	return info, nil
}
