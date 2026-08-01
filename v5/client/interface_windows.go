//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

// defaultInterface returns the name of the physical interface holding the
// default IPv4 route (e.g. "Wi-Fi" or "Ethernet"), or "" if undetectable.
// This is used to bind the Shadowsocks outbound to the physical NIC so the
// TUN (auto_route) cannot capture sing-box's own server connection.
// The PowerShell window is hidden so no console flashes.
func defaultInterface() string {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
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
