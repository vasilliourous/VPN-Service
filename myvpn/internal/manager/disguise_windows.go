//go:build windows

package manager

import (
	"os/exec"
	"syscall"
)

func disguiseProcess(cmd *exec.Cmd, _ string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
