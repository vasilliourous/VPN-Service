package buildinfo

import (
	"strings"
	"testing"
)

func TestDetectInstrumentedWhenVersionInjected(t *testing.T) {
	info := Detect("9.9.9")
	if info.Version != "9.9.9" {
		t.Fatalf("Version = %q, want 9.9.9", info.Version)
	}
	if !info.Instrumented {
		t.Fatal("Instrumented = false, want true when ldflags injected a version")
	}
	if info.Warning() != "" {
		t.Fatalf("Warning() = %q, want empty for an instrumented build", info.Warning())
	}
	if info.Platform == "" {
		t.Fatal("Platform is empty")
	}
}

func TestDetectUninstrumentedFallsBack(t *testing.T) {
	info := Detect("")
	if info.Version != fallbackVersion {
		t.Fatalf("Version = %q, want fallback %q", info.Version, fallbackVersion)
	}
	if info.Instrumented {
		t.Fatal("Instrumented = true, want false when no version was injected")
	}
	if info.Warning() == "" {
		t.Fatal("Warning() is empty, want a note for an uninstrumented build")
	}
}

func TestDetectTreatsWhitespaceVersionAsUninstrumented(t *testing.T) {
	info := Detect("   ")
	if info.Instrumented {
		t.Fatal("Instrumented = true, want false for a whitespace-only version")
	}
	if info.Version != fallbackVersion {
		t.Fatalf("Version = %q, want fallback %q", info.Version, fallbackVersion)
	}
}

func TestDescribeIncludesVersionAndPlatform(t *testing.T) {
	info := Detect("2.5.0")
	got := info.Describe()
	for _, want := range []string{"Locus 2.5.0", "instrumented", info.Platform} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, missing %q", got, want)
		}
	}
}

func TestDescribeFlagsUninstrumented(t *testing.T) {
	got := Detect("").Describe()
	if !strings.Contains(got, "UNINSTRUMENTED") {
		t.Errorf("Describe() = %q, want it to flag an uninstrumented build", got)
	}
}

func TestStringMatchesDescribe(t *testing.T) {
	info := Detect("1.0.0")
	if info.String() != info.Describe() {
		t.Errorf("String() = %q, Describe() = %q; they must agree", info.String(), info.Describe())
	}
}
