// Package buildinfo exposes what this binary knows about itself.
//
// WHY THIS EXISTS: the client used to know exactly one thing about itself —
// the version string injected via -ldflags. Everything else a support report
// needs (was this build instrumented? which commit? which toolchain? which
// sing-box did we actually find?) had to be guessed from the outside, and a
// dev/scratch build was indistinguishable from a release. That made the
// updater's decisions impossible to explain: "no update available" could mean
// the hub has nothing, the running build reports a version newer than the
// hub advertises, or the binary was never instrumented at all and is reporting
// a stale fallback literal.
//
// This package centralises that self-knowledge so the version surface, the
// diagnostics report and the update flow all agree on the same facts.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Info describes the running binary.
type Info struct {
	// Version is the runtime version (from -ldflags "-X main.version=...").
	Version string
	// Instrumented reports whether Version came from a build that injected it.
	// An uninstrumented build falls back to a literal in main.go, which can be
	// stale relative to the source tree — the updater and the UI must know the
	// difference, because a scratch build can otherwise look "newer" than a
	// real release.
	Instrumented bool
	// Commit and CommitShort are the VCS revision, when the toolchain recorded
	// one (Go 1.18+ embeds it for builds inside a git checkout).
	Commit      string
	CommitShort string
	// CommitDirty reports a build from a modified working tree.
	CommitDirty bool
	// BuiltAt is the commit time reported by the toolchain (RFC3339), if known.
	BuiltAt string
	// GoVersion is the toolchain used.
	GoVersion string
	// Platform is "GOOS/GOARCH".
	Platform string
	// Modified reports whether the source tree had uncommitted changes.
	Modified bool
}

// fallbackVersion is the literal compiled into main.go. It is the value used
// when the build was NOT instrumented with -ldflags, and it is deliberately the
// same string as the one in main.go so an uninstrumented build is at least
// self-consistent.
//
// A build is considered instrumented when the ldflags value differs from this
// fallback OR when the toolchain recorded VCS information (which only happens
// for real builds inside the checkout). A plain `go build` with no flags inside
// the repo therefore reports Instrumented=false, and the updater refuses to
// treat its version as authoritative.
const fallbackVersion = "2.2.3"

// FallbackVersion returns the literal compile-time fallback version. Exposed so
// the repo's version-consistency test can assert it matches v5/VERSION — an
// uninstrumented build reports this value, and a stale one makes support
// impossible.
func FallbackVersion() string { return fallbackVersion }

// Detect assembles the Info for the running binary.
//
// version is the ldflags-injected value ("" when the build was not
// instrumented). When it is empty, fallbackVersion is used so callers always
// have something printable, but Instrumented stays false.
func Detect(version string) Info {
	info := Info{
		Version:  version,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if strings.TrimSpace(info.Version) == "" {
		info.Version = fallbackVersion
		info.Instrumented = false
	} else {
		info.Instrumented = true
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		info.GoVersion = bi.GoVersion
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Commit = s.Value
				if len(s.Value) > 7 {
					info.CommitShort = s.Value[:7]
				} else {
					info.CommitShort = s.Value
				}
			case "vcs.time":
				info.BuiltAt = s.Value
			case "vcs.modified":
				info.Modified = s.Value == "true"
				info.CommitDirty = info.Modified
			}
		}
		// A build inside the checkout with no ldflags is still not a release:
		// its version is the fallback literal, which drifts from the source.
		if info.Commit != "" && version == "" {
			info.Instrumented = false
		}
	}
	if info.GoVersion == "" {
		info.GoVersion = runtime.Version()
	}
	return info
}

// Describe returns a single line summarising the build, for logs and support
// reports. Example:
//
//	Locus 2.0.1 (instrumented) linux/amd64 go1.22.12 commit a1b2c3d clean
func (i Info) Describe() string {
	kind := "instrumented"
	if !i.Instrumented {
		kind = "UNINSTRUMENTED"
	}
	commit := ""
	if i.CommitShort != "" {
		commit = " commit " + i.CommitShort
		if i.CommitDirty {
			commit += "-dirty"
		}
	}
	built := ""
	if i.BuiltAt != "" {
		built = " built " + i.BuiltAt
	}
	return fmt.Sprintf("Locus %s (%s) %s %s%s%s",
		i.Version, kind, i.Platform, i.GoVersion, commit, built)
}

// String implements fmt.Stringer so the Info can be logged directly.
func (i Info) String() string { return i.Describe() }

// Warning returns a human-readable note when the build is not a release build,
// or "" when it is. Callers surface this in diagnostics so a support report
// cannot silently come from a dev build.
func (i Info) Warning() string {
	if i.Instrumented {
		return ""
	}
	return "This build was not produced by the release pipeline (no version was " +
		"injected at build time). It reports the fallback version " + i.Version +
		", which may not match the source tree. Update checks are informational " +
		"in this state and will not offer a downgrade."
}
