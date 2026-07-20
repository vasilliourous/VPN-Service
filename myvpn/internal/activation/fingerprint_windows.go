//go:build windows

package activation

import (
	"os/exec"
	"strings"
)

func getMAC() string {
	out, err := exec.Command("getmac", "/fo", "csv", "/nh").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) >= 3 {
			mac := strings.Trim(parts[1], `"`)
			mac = strings.ReplaceAll(mac, "-", ":")
			if mac != "" && mac != "Disabled" {
				return mac
			}
		}
	}
	return ""
}

func getDiskSerial() string {
	out, err := exec.Command("wmic", "diskdrive", "get", "SerialNumber").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) >= 2 {
		return strings.TrimSpace(lines[1])
	}
	return ""
}

func getMoboSerial() string {
	out, err := exec.Command("wmic", "baseboard", "get", "SerialNumber").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) >= 2 {
		return strings.TrimSpace(lines[1])
	}
	return ""
}
