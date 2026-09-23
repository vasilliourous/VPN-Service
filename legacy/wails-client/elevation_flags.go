package main

import "strings"

// Command-line flags that drive the elevation handoff. They are shared by both
// platform builds (the Unix build parses them too, even though it never
// launches an elevated copy) so the semantics cannot drift between platforms.
const (
	// flagAutoConnectValue marks a process that should connect as soon as it is
	// ready. Set by the elevation handoff, and accepted on the command line so
	// an operator or shortcut can request it directly.
	flagAutoConnectValue = "--autoconnect"

	// flagElevatedAttemptValue marks a process that IS the elevated copy
	// produced by a handoff. This is deliberately a DIFFERENT flag from
	// --autoconnect.
	//
	// WHY IT MATTERS (the bug this fixes): the handoff used to infer "I am the
	// elevated relaunch" from the presence of --autoconnect. But --autoconnect
	// is also a legitimate user-facing flag, and it duplicates across repeated
	// handoffs. So a process that had simply been asked to auto-connect could
	// be mistaken for an elevated relaunch that had failed to elevate — and
	// Connect() responded by refusing to connect and printing a permission
	// error, even though the user had approved the UAC prompt and the process
	// was in fact elevated. A dedicated marker makes the two situations
	// distinguishable, which is the only way the loop guard can be correct.
	flagElevatedAttemptValue = "--elevated-attempt"

	// flagAttemptName carries the number of handoff attempts so a genuinely
	// broken elevation can be abandoned instead of retried forever. The value
	// is supplied form ("--elevation-attempt=2"); flagAttemptName is the bare
	// name that hasFlag matches on.
	flagAttemptName  = "--elevation-attempt"
	flagAttemptValue = flagAttemptName + "="
)

// mergeArgs returns existing args with adds appended, skipping any add that is
// already present. Identity is compared on the flag NAME so an existing
// "--elevation-attempt=1" is recognised when adding "--elevation-attempt=2"
// (the value is deliberately replaceable, not additive).
//
// This exists because the previous implementation appended unconditionally and
// produced "--autoconnect --autoconnect" after a second handoff.
func mergeArgs(existing, adds []string) []string {
	out := append([]string(nil), existing...)
	for _, add := range adds {
		name := flagName(add)
		replaced := false
		for i, have := range out {
			if flagName(have) == name {
				// Replace in place: a later attempt count must win, and a
				// duplicated flag must collapse to one.
				out[i] = add
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, add)
		}
	}
	return out
}

// flagName returns the identifying part of an argv entry: everything before
// '=' for a "--flag=value" form, or the whole string otherwise.
func flagName(arg string) string {
	if i := strings.IndexByte(arg, '='); i >= 0 {
		return arg[:i]
	}
	return arg
}

// hasFlag reports whether any argv entry (from the given slice) matches want,
// exactly or as the name of a "--want=value" form.
func hasFlag(args []string, want string) bool {
	for _, a := range args {
		if flagName(a) == want {
			return true
		}
	}
	return false
}

// parseAttempt extracts the handoff attempt counter from argv (0 when absent or
// malformed). Used to bound retries when elevation keeps failing.
func parseAttempt(args []string) int {
	for _, a := range args {
		if !strings.HasPrefix(a, flagAttemptValue) {
			continue
		}
		n := 0
		for _, r := range a[len(flagAttemptValue):] {
			if r < '0' || r > '9' {
				return 0
			}
			n = n*10 + int(r-'0')
			if n > 100 {
				return 100 // clamp — never trust an unbounded arg
			}
		}
		return n
	}
	return 0
}
