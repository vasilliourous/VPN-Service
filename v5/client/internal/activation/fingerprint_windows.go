//go:build windows

package activation

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

func init() {
	platformCollector = collectWindowsSources
}

// collectWindowsSources gathers hardware info on Windows.
// Sources:
//   - MAC address: GetAdaptersAddresses (via PowerShell)
//   - Disk serial: WMI Win32_DiskDrive
//   - Motherboard UUID: WMI Win32_ComputerSystemProduct
//   - Hostname
//
// Uses PowerShell for WMI queries (available on all supported Windows versions).
func collectWindowsSources() fingerprintSources {
	var sources fingerprintSources

	// MAC address — first physical adapter via PowerShell
	if output, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-NetAdapter | Where-Object {$_.PhysicalMediaType -ne 0 -and $_.Status -eq 'Up'} | "+
			"Select-Object -First 1 -ExpandProperty MacAddress",
	).Output(); err == nil {
		mac := strings.TrimSpace(string(output))
		if mac != "" && mac != "00:00:00:00:00:00" {
			sources.MAC = mac
		}
	} else {
		log.Printf("INFO: PowerShell Get-NetAdapter failed: %v", err)
	}

	// Disk serial via WMI
	if output, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-WmiObject Win32_DiskDrive | Select-Object -First 1 -ExpandProperty SerialNumber",
	).Output(); err == nil {
		sources.DiskSerial = strings.TrimSpace(string(output))
	} else {
		log.Printf("INFO: WMI disk serial query failed: %v", err)
	}

	// Motherboard UUID via WMI
	if output, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-WmiObject Win32_ComputerSystemProduct | Select-Object -ExpandProperty UUID",
	).Output(); err == nil {
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
