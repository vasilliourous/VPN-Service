package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Version comparison for the update gate.
//
// WHY THIS EXISTS: the client used to decide "is there an update?" with a bare
// string comparison (`info.Version == a.version`). That is only safe while the
// hub never advertises anything but the newest build. If update_config is ever
// left pointing at an OLDER release — a rollback, a mis-typed version, a stale
// row after a bad deploy — every client would dutifully download it and
// downgrade itself. There is no server-driven downgrade in this system, so a
// mistaken advertisement is the only way that can happen; we make it harmless
// by refusing anything that is not strictly newer.
//
// We implement a small, dependency-free comparison rather than pulling in a
// semver library: the version strings here come from git tags and are
// controlled by us (`1.2.3`, `v1.2.3`, optionally `-rc1`/`-beta.2`).

// CompareVersions compares two dotted version strings.
//
// Returns:
//
//	-1 if a < b
//	 0 if a == b
//	 1 if a > b
//
// A leading "v" is ignored ("v1.2.3" == "1.2.3"). Numeric components are
// compared numerically, so 1.10.0 > 1.9.0 (which string comparison gets wrong).
// Missing components are treated as zero, so "1.2" == "1.2.0".
//
// Pre-release handling follows the usual convention: 1.2.3-rc1 < 1.2.3, and
// two pre-releases compare component-wise. Comparisons never return an error;
// unparseable input degrades to string comparison so we fail closed rather
// than crash mid-update.
func CompareVersions(a, b string) int {
	an, ap := splitVersion(a)
	bn, bp := splitVersion(b)

	// Compare numeric segments, padding the shorter with zeros.
	max := len(an)
	if len(bn) > max {
		max = len(bn)
	}
	for i := 0; i < max; i++ {
		var av, bv int
		if i < len(an) {
			av = an[i]
		}
		if i < len(bn) {
			bv = bn[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}

	// Numeric parts equal — a release beats a pre-release of the same version.
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "" && bp != "":
		return 1 // 1.2.3 > 1.2.3-rc1
	case ap != "" && bp == "":
		return -1
	}

	// Both pre-release: compare dot/dash separated identifiers.
	return comparePreRelease(ap, bp)
}

// IsNewer reports whether candidate is strictly newer than current.
// This is the gate the updater uses; an unparseable candidate is rejected.
func IsNewer(candidate, current string) bool {
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(current) == "" {
		return false
	}
	return CompareVersions(candidate, current) > 0
}

// splitVersion splits "v1.2.3-rc1" into numeric segments [1,2,3] and the
// pre-release string "rc1".
func splitVersion(v string) ([]int, string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	pre := ""
	// Strip build metadata first: per semver it is IGNORED for ordering, so
	// "1.0.0+build5" must equal "1.0.0". (Removing it after the pre-release
	// split would wrongly classify the metadata as a pre-release, making a
	// build-tagged release rank LOWER than the plain one.)
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	// A pre-release starts at the first '-'.
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}

	parts := strings.Split(v, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Tolerate a non-numeric segment (e.g. "1.2.x") by stopping the
		// numeric parse; the remainder is folded into the pre-release string
		// so the comparison still has something to work with.
		n, err := strconv.Atoi(p)
		if err != nil {
			if pre == "" {
				pre = p
			}
			break
		}
		nums = append(nums, n)
	}
	return nums, pre
}

// comparePreRelease compares two pre-release strings identifier by identifier.
// Numeric identifiers compare numerically and rank BELOW alphanumeric ones
// (semver rule), so 1.0.0-1 < 1.0.0-alpha.
func comparePreRelease(a, b string) int {
	as := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == '-' })
	bs := strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == '-' })

	max := len(as)
	if len(bs) > max {
		max = len(bs)
	}
	for i := 0; i < max; i++ {
		// A shorter pre-release is smaller when all shared parts match.
		if i >= len(as) {
			return -1
		}
		if i >= len(bs) {
			return 1
		}
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		case aerr == nil && berr != nil:
			return -1 // numeric ranks below alphanumeric
		case aerr != nil && berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return 0
}

// validateVersionForUpdate guards the values we are willing to act on.
// Returns a descriptive error so the UI can explain why an advertised update
// was ignored instead of silently doing nothing.
func validateVersionForUpdate(candidate, current string) error {
	if strings.TrimSpace(candidate) == "" {
		return fmt.Errorf("hub advertised an empty version")
	}
	if strings.ContainsAny(candidate, "/\\ \t\n") {
		return fmt.Errorf("hub advertised a malformed version %q", candidate)
	}
	if CompareVersions(candidate, current) <= 0 {
		return fmt.Errorf("hub advertised %s, which is not newer than the running %s", candidate, current)
	}
	return nil
}
