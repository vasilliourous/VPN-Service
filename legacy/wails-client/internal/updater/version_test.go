package updater

import "testing"

// TestCompareVersions locks in the ordering rules the update gate depends on.
// The critical property is that a hub advertising an OLDER version must never
// be treated as an update — that is the difference between "no-op" and
// "silently downgrade every client".
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// Equal forms
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.2", "1.2.0", 0},
		{"1", "1.0.0.0", 0},

		// Numeric ordering (string comparison would get 1.10 < 1.9 wrong)
		{"1.10.0", "1.9.0", 1},
		{"1.9.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"0.9.9", "1.0.0", -1},

		// Patch/minor
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},

		// Pre-releases: a release outranks its own pre-release
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc2", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0-beta", "1.0.0-alpha", 1},
		{"1.0.0-rc1", "1.0.0-rc1", 0},
		{"1.0.0-alpha.2", "1.0.0-alpha.1", 1},
		{"1.0.1-rc1", "1.0.0", 1},

		// Build metadata is ignored for ordering
		{"1.0.0+build5", "1.0.0", 0},
	}

	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		// Antisymmetry: reversing the args must negate the result.
		if rev := CompareVersions(c.b, c.a); rev != -c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d (antisymmetry)", c.b, c.a, rev, -c.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	yes := []struct{ candidate, current string }{
		{"1.0.1", "1.0.0"},
		{"1.1.0", "1.0.9"},
		{"2.0.0", "1.9.9"},
		{"v1.2.0", "1.1.0"},
		{"1.0.0", "1.0.0-rc1"},
	}
	for _, c := range yes {
		if !IsNewer(c.candidate, c.current) {
			t.Errorf("IsNewer(%q, %q) = false, want true", c.candidate, c.current)
		}
	}

	no := []struct{ candidate, current string }{
		{"1.0.0", "1.0.0"},     // same
		{"0.9.0", "1.0.0"},     // older
		{"1.0.0-rc1", "1.0.0"}, // pre-release of running version
		{"", "1.0.0"},          // empty
		{"1.0.0", ""},          // empty current
	}
	for _, c := range no {
		if IsNewer(c.candidate, c.current) {
			t.Errorf("IsNewer(%q, %q) = true, want false", c.candidate, c.current)
		}
	}
}

// TestValidateVersionForUpdate is the guard that prevents an accidental
// fleet-wide downgrade when update_config is left pointing at an older release.
func TestValidateVersionForUpdate(t *testing.T) {
	ok := []struct{ candidate, current string }{
		{"1.1.0", "1.0.0"},
		{"1.0.1", "1.0.0"},
	}
	for _, c := range ok {
		if err := validateVersionForUpdate(c.candidate, c.current); err != nil {
			t.Errorf("validateVersionForUpdate(%q, %q) = %v, want nil", c.candidate, c.current, err)
		}
	}

	bad := []struct{ candidate, current string }{
		{"1.0.0", "1.0.0"},         // same version
		{"0.9.0", "1.0.0"},         // older — would be a downgrade
		{"", "1.0.0"},              // empty
		{"../etc/passwd", "1.0.0"}, // path-ish garbage
		{"1.0.0 1.0.1", "1.0.0"},   // whitespace
	}
	for _, c := range bad {
		if err := validateVersionForUpdate(c.candidate, c.current); err == nil {
			t.Errorf("validateVersionForUpdate(%q, %q) = nil, want error", c.candidate, c.current)
		}
	}
}

// TestSplitVersion covers the parser directly, including tolerated junk.
func TestSplitVersion(t *testing.T) {
	cases := []struct {
		in   string
		nums []int
		pre  string
	}{
		{"1.2.3", []int{1, 2, 3}, ""},
		{"v1.2.3", []int{1, 2, 3}, ""},
		{"1.2.3-rc1", []int{1, 2, 3}, "rc1"},
		// Build metadata is stripped, not treated as a pre-release — semver
		// ignores it for ordering.
		{"1.2.3+build", []int{1, 2, 3}, ""},
		{"1.2.3-rc1+build", []int{1, 2, 3}, "rc1"},
		{"1.2", []int{1, 2}, ""},
		{"1.2.x", []int{1, 2}, "x"},
	}
	for _, c := range cases {
		nums, pre := splitVersion(c.in)
		if len(nums) != len(c.nums) {
			t.Errorf("splitVersion(%q) nums = %v, want %v", c.in, nums, c.nums)
			continue
		}
		for i := range nums {
			if nums[i] != c.nums[i] {
				t.Errorf("splitVersion(%q) nums = %v, want %v", c.in, nums, c.nums)
				break
			}
		}
		if pre != c.pre {
			t.Errorf("splitVersion(%q) pre = %q, want %q", c.in, pre, c.pre)
		}
	}
}

// TestPlatformDownloadURLSelection verifies the per-platform URL preference,
// including the macOS arm64-vs-intel branch. A regression here would hand an
// Intel Mac an arm64 build (or vice versa).
func TestPlatformDownloadURLSelection(t *testing.T) {
	// Generic fallback is used when no platform-specific URL is set.
	info := &UpdateInfo{DownloadURL: "https://hub/updates/1.1.0/locus"}
	if got := info.PlatformDownloadURL(); got != "https://hub/updates/1.1.0/locus" {
		t.Errorf("fallback PlatformDownloadURL() = %q, want the generic URL", got)
	}

	// With every platform set, the chosen URL must be non-empty and must be
	// one of the platform-specific entries (exact choice depends on GOOS/GOARCH
	// of the test host, so we assert membership rather than a fixed value).
	full := &UpdateInfo{
		DownloadURL:           "https://hub/updates/1.1.0/generic",
		DownloadURLLinux:      "https://hub/updates/1.1.0/linux",
		DownloadURLWindows:    "https://hub/updates/1.1.0/windows",
		DownloadURLMacOSIntel: "https://hub/updates/1.1.0/macos-intel",
		DownloadURLMacOSARM:   "https://hub/updates/1.1.0/macos-arm",
	}
	got := full.PlatformDownloadURL()
	allowed := map[string]bool{
		full.DownloadURLLinux:      true,
		full.DownloadURLWindows:    true,
		full.DownloadURLMacOSIntel: true,
		full.DownloadURLMacOSARM:   true,
	}
	if !allowed[got] {
		t.Errorf("PlatformDownloadURL() = %q, not one of the platform-specific URLs", got)
	}
	if got == "" {
		t.Error("PlatformDownloadURL() returned empty with all platforms configured")
	}
}

// TestUpdateInfoEmptyPlatformFallsBack ensures a hub that only sets the
// generic URL still works (older update_config rows).
func TestUpdateInfoEmptyPlatformFallsBack(t *testing.T) {
	info := &UpdateInfo{DownloadURL: "https://hub/generic"}
	if got := info.PlatformDownloadURL(); got != "https://hub/generic" {
		t.Errorf("expected generic fallback, got %q", got)
	}

	empty := &UpdateInfo{}
	if got := empty.PlatformDownloadURL(); got != "" {
		t.Errorf("expected empty URL when nothing is set, got %q", got)
	}
}

// TestPlatformSHA256 verifies the checksum chosen matches the artifact the
// downloader will fetch. A mismatch here means every update fails at
// verification ("SHA256 mismatch") even though the download succeeded.
func TestPlatformSHA256(t *testing.T) {
	// Legacy fallback: only the generic hash is set.
	legacy := &UpdateInfo{SHA256: "generic-hash"}
	if got := legacy.PlatformSHA256(); got != "generic-hash" {
		t.Errorf("PlatformSHA256() = %q, want the generic fallback", got)
	}

	empty := &UpdateInfo{}
	if got := empty.PlatformSHA256(); got != "" {
		t.Errorf("expected empty hash when nothing is set, got %q", got)
	}

	// With per-platform hashes present, the chosen hash must correspond to the
	// platform whose URL PlatformDownloadURL() selects.
	full := &UpdateInfo{
		SHA256:           "generic-hash",
		DownloadURLLinux: "linux-url", DownloadURLWindows: "windows-url",
		DownloadURLMacOSIntel: "intel-url", DownloadURLMacOSARM: "arm-url",
		SHA256Linux: "linux-hash", SHA256Windows: "windows-hash",
		SHA256MacOSIntel: "intel-hash", SHA256MacOSARM: "arm-hash",
	}
	url := full.PlatformDownloadURL()
	sha := full.PlatformSHA256()

	wantSHA := map[string]string{
		"linux-url":   "linux-hash",
		"windows-url": "windows-hash",
		"intel-url":   "intel-hash",
		"arm-url":     "arm-hash",
	}[url]
	if wantSHA == "" {
		t.Fatalf("PlatformDownloadURL() returned unexpected URL %q", url)
	}
	if sha != wantSHA {
		t.Errorf("PlatformSHA256() = %q for URL %q, want %q", sha, url, wantSHA)
	}
	if sha == "generic-hash" {
		t.Error("PlatformSHA256() fell back to the generic hash despite per-platform hashes being set")
	}
}
