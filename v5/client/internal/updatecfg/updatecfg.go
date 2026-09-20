// Package updatecfg is the single definition of the `update_config` record
// contract between the Locus release tooling, the hub and the client.
//
// # WHY THIS EXISTS
//
// Two independent publish paths write `update_config`, and they disagreed on
// the per-platform download field names:
//
//	publish-release.sh (server)  wrote  download_linux, download_windows, ...
//	publish-update.sh  (root)    wrote  update_linux,   update_windows,   ...
//
// The hub reads `download_*` when building the heartbeat response, and the
// client reads the response's `update_*` keys. So a record written by
// publish-update.sh produced a heartbeat with every per-platform URL and hash
// missing. `PlatformDownloadURL()` then fell through to the legacy single
// `update_url` — which both scripts point at the LINUX binary — so every
// platform, including Windows and macOS, would have downloaded a Linux
// executable and failed the checksum.
//
// Neither script was internally wrong. There was simply no shared definition,
// so nothing could notice they described the same record differently.
//
// THE CONTRACT (three layers, deliberately distinct)
//
//  1. update_config record  (written by the publish scripts, read by the hook)
//     `download_<platform>` = absolute URL of that platform's raw binary
//     `sha256_<platform>`   = its SHA-256
//
//  2. heartbeat response    (written by the hook, read by the client)
//     `update_<platform>`         = the URL, renamed on the way out
//     `update_sha256_<platform>`  = the hash, renamed on the way out
//
//  3. client struct         (internal/heartbeat.Response)
//     reads layer 2's names verbatim.
//
// The rename between layers 1 and 2 is historical and is now load-bearing:
// deployed clients read `update_linux`, so the hook must keep translating.
// Do not "simplify" it by making the hook emit `download_linux` directly.
package updatecfg

// Platform identifiers, used as the suffix in every field name. These exact
// strings are the contract; the hook interpolates them as
// `response.update_" + key` and the client matches on them.
const (
	PlatformLinux      = "linux"
	PlatformWindows    = "windows"
	PlatformMacOSIntel = "macos_intel"
	PlatformMacOSARM   = "macos_arm"
)

// Platforms is the canonical, order-stable list. Order matters for diagnostics
// output; it does not affect correctness.
var Platforms = []string{
	PlatformLinux,
	PlatformWindows,
	PlatformMacOSIntel,
	PlatformMacOSARM,
}

// Artifact is the raw client binary CI publishes for a platform. These names
// are a SECOND contract — between CI's staging step, publish-release.sh and the
// Caddy /updates/<version>/ layout — and are asserted here so all three agree.
type Artifact struct {
	Platform string
	Filename string // as uploaded next to its .sha256 sidecar
}

// Artifacts is the CI↔hub filename contract, from
// .github/workflows/build.yml's "Stage raw updater artifacts" step.
var Artifacts = []Artifact{
	{PlatformLinux, "locus-linux-amd64"},
	{PlatformWindows, "locus-windows-amd64.exe"},
	{PlatformMacOSIntel, "locus-darwin-amd64"},
	{PlatformMacOSARM, "locus-darwin-arm64"},
}

// ArtifactFor returns the published filename for a platform, and whether the
// platform is known.
func ArtifactFor(platform string) (string, bool) {
	for _, a := range Artifacts {
		if a.Platform == platform {
			return a.Filename, true
		}
	}
	return "", false
}

// ── Layer 1: the update_config record ──

// DownloadField is the record field holding a platform's binary URL.
func DownloadField(platform string) string { return "download_" + platform }

// SHA256Field is the record field holding a platform's checksum.
func SHA256Field(platform string) string { return "sha256_" + platform }

// Legacy fields, kept populated for clients that predate per-platform support.
// They describe exactly one binary — linux — which is why a record carrying
// only these disables updates for every other platform.
const (
	LegacyURLField    = "update_url"
	LegacySHA256Field = "update_sha256"
	LegacyVersionURL  = "updates/" // relative path segment under the webroot
)

// ── Layer 2: the heartbeat response ──

// ResponseURLField is the response field the client reads for a platform URL.
func ResponseURLField(platform string) string { return "update_" + platform }

// ResponseSHA256Field is the response field the client reads for a checksum.
func ResponseSHA256Field(platform string) string { return "update_sha256_" + platform }

// UpdateAvailableField is the response field carrying the advertised version.
const UpdateAvailableField = "update_available"

// RolloutField gates which clients are offered an update at all.
const RolloutField = "rollout_percent"
