//go:build !windows

package tunnel

import (
	"os/exec"
)

// hiddenCommand builds an exec.Cmd for a helper process.
//
// On Unix there is no console to allocate, so this is a plain exec.Command.
// It exists so every call site in this package goes through one constructor —
// see the Windows twin for why that matters.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
