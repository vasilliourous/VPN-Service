//go:build windows

package main

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// defaultInterface returns the name of the physical interface holding the
// default IPv4 route (e.g. "Wi-Fi" or "Ethernet"), or "" if undetectable.
// This is used to bind the Shadowsocks outbound to the physical NIC so the
// TUN (auto_route) cannot capture sing-box's own server connection.
//
// The PowerShell window is hidden and the call has a hard 4-second timeout —
// a slow/hung PowerShell must never block Connect() (which previously made
// the app appear to "fail entirely" with no logs).
func defaultInterface() string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
		"(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | Select-Object -First 1).InterfaceAlias")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "" || strings.Contains(name, "myvpn") || strings.Contains(name, "tun") {
		return ""
	}
	return name
}
