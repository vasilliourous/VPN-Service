//go:build windows

package activation

import (
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func init() {
	platformCollector = collectWindowsSources
}

// hiddenProcAttr prevents a console window from flashing when this GUI app
// spawns PowerShell (a console-subsystem process). Without this, every launch
// pops a blank PowerShell window on screen.
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// runHidden runs a command with its console window hidden (Windows only).
func runHidden(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hiddenProcAttr()
	return cmd.Output()
}

// collectWindowsSources gathers hardware info on Windows.
// Sources:
//   - MAC address: Get-NetAdapter (via PowerShell)
//   - Disk serial: WMI Win32_DiskDrive
//   - Motherboard UUID: WMI Win32_ComputerSystemProduct
//   - Hostname
//
// Uses PowerShell for WMI queries (available on all supported Windows versions).
// All PowerShell invocations run with a hidden window so no console flashes.
func collectWindowsSources() fingerprintSources {
	var sources fingerprintSources

	// MAC address — first physical adapter via PowerShell
	if output, err := runHidden("powershell", "-NoProfile", "-Command",
		"Get-NetAdapter | Where-Object {$_.PhysicalMediaType -ne 0 -and $_.Status -eq 'Up'} | "+
			"Select-Object -First 1 -ExpandProperty MacAddress",
	); err == nil {
		mac := strings.TrimSpace(string(output))
		if mac != "" && mac != "00:00:00:00:00:00" {
			sources.MAC = mac
		}
	} else {
		log.Printf("INFO: PowerShell Get-NetAdapter failed: %v", err)
	}

	// Disk serial via WMI
	if output, err := runHidden("powershell", "-NoProfile", "-Command",
		"Get-WmiObject Win32_DiskDrive | Select-Object -First 1 -ExpandProperty SerialNumber",
	); err == nil {
		sources.DiskSerial = strings.TrimSpace(string(output))
	} else {
		log.Printf("INFO: WMI disk serial query failed: %v", err)
	}

	// Motherboard UUID via WMI
	if output, err := runHidden("powershell", "-NoProfile", "-Command",
		"Get-WmiObject Win32_ComputerSystemProduct | Select-Object -ExpandProperty UUID",
	); err == nil {
		sources.Motherboard = strings.TrimSpace(string(output))
	} else {
		log.Printf("INFO: WMI motherboard UUID query failed: %v", err)
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		sources.Hostname = hostname
	}

	return sources
}
