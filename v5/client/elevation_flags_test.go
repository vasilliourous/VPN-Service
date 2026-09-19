package main

import (
	"reflect"
	"testing"
)

// Tests for the elevation handoff argument handling.
//
// Context: the elevation bug this covers was caused by flags accumulating
// across handoffs ("--autoconnect --autoconnect"), and by treating
// --autoconnect as proof that a process was an elevated relaunch. Both are
// pure functions of argv, so they are testable without Windows.

func TestMergeArgsDoesNotDuplicateExistingFlag(t *testing.T) {
	existing := []string{flagAutoConnectValue}
	got := mergeArgs(existing, []string{flagAutoConnectValue})
	want := []string{flagAutoConnectValue}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeArgs = %v, want %v (the flag must not be duplicated)", got, want)
	}
}

func TestMergeArgsRepeatedHandoffsStayFlat(t *testing.T) {
	// Simulate three successive handoffs, each adding the same flags. The
	// result must never grow.
	args := []string{}
	for i := 0; i < 3; i++ {
		args = mergeArgs(args, []string{
			flagElevatedAttemptValue,
			flagAutoConnectValue,
		})
	}
	want := []string{flagElevatedAttemptValue, flagAutoConnectValue}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("after repeated handoffs args = %v, want %v", args, want)
	}
}

func TestMergeArgsReplacesValuedFlag(t *testing.T) {
	// The attempt counter must be REPLACED, not appended a second time,
	// otherwise parseAttempt would read a stale value.
	args := []string{flagAttemptValue + "1"}
	got := mergeArgs(args, []string{flagAttemptValue + "2"})
	want := []string{flagAttemptValue + "2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeArgs = %v, want %v", got, want)
	}
}

func TestMergeArgsPreservesUnrelatedArgs(t *testing.T) {
	got := mergeArgs([]string{"--revert", "--other=x"}, []string{flagAutoConnectValue})
	want := []string{"--revert", "--other=x", flagAutoConnectValue}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeArgs = %v, want %v", got, want)
	}
}

func TestMergeArgsDoesNotMutateInput(t *testing.T) {
	existing := []string{"--keep"}
	backup := append([]string(nil), existing...)
	_ = mergeArgs(existing, []string{flagAutoConnectValue})
	if !reflect.DeepEqual(existing, backup) {
		t.Errorf("mergeArgs mutated its input: %v, want %v", existing, backup)
	}
}

func TestHasFlagMatchesExactAndValuedForms(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
		flag string
	}{
		{"exact", []string{flagAutoConnectValue}, true, flagAutoConnectValue},
		{"absent", []string{"--other"}, false, flagAutoConnectValue},
		{"valued form", []string{flagAttemptValue + "2"}, true, flagAttemptName},
		{"not a prefix match", []string{"--autoconnect-extra"}, false, flagAutoConnectValue},
		{"empty", nil, false, flagAutoConnectValue},
	}
	for _, tc := range cases {
		if got := hasFlag(tc.args, tc.flag); got != tc.want {
			t.Errorf("%s: hasFlag(%v, %q) = %v, want %v", tc.name, tc.args, tc.flag, got, tc.want)
		}
	}
}

func TestParseAttempt(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{nil, 0},
		{[]string{"--autoconnect"}, 0},
		{[]string{flagAttemptValue + "1"}, 1},
		{[]string{flagAttemptValue + "3"}, 3},
		{[]string{"--x", flagAttemptValue + "2", "--y"}, 2},
		{[]string{flagAttemptValue + ""}, 0},
		{[]string{flagAttemptValue + "abc"}, 0},    // malformed → 0
		{[]string{flagAttemptValue + "9999"}, 100}, // clamped
	}
	for _, tc := range cases {
		if got := parseAttempt(tc.args); got != tc.want {
			t.Errorf("parseAttempt(%v) = %d, want %d", tc.args, got, tc.want)
		}
	}
}

// TestElevatedAttemptIsDistinctFromAutoConnect is the regression test for the
// reported bug: the loop guard must not be triggered by --autoconnect alone.
func TestElevatedAttemptIsDistinctFromAutoConnect(t *testing.T) {
	// A user running with --autoconnect is NOT an elevated relaunch.
	autoOnly := []string{flagAutoConnectValue}
	if hasFlag(autoOnly, flagElevatedAttemptValue) {
		t.Error("--autoconnect must not be treated as --elevated-attempt; " +
			"doing so makes the loop guard misfire on ordinary auto-connect runs " +
			"and shows a bogus permission error to an elevated process")
	}
	// A genuine elevated relaunch carries both.
	relaunch := []string{flagElevatedAttemptValue, flagAutoConnectValue}
	if !hasFlag(relaunch, flagElevatedAttemptValue) {
		t.Error("an elevated relaunch must be detected by --elevated-attempt")
	}
	if !hasFlag(relaunch, flagAutoConnectValue) {
		t.Error("an elevated relaunch must still auto-connect")
	}
}
