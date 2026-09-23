//go:build windows

package activation

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Minimum number of characters needed in source material for a meaningful fingerprint.
const minFingerprintEntropy = 8

// fingerprintSources collects all available hardware identifiers.
type fingerprintSources struct {
	MAC         string
	DiskSerial  string
	Motherboard string
	Hostname    string
	MachineID   string
}

// cachedFingerprint stores the first generated fingerprint so it remains
// stable for the lifetime of the process (important for the random UUID fallback).
var cachedFingerprint string
var fingerprintOnce sync.Once

// GenerateFingerprint creates a SHA256 device fingerprint from hardware sources.
// The result is cached so subsequent calls return the same value.
func GenerateFingerprint() string {
	fingerprintOnce.Do(func() {
		sources := collectWindowsSources()
		fp := hashSources(sources)
		if fp == "" {
			log.Println("WARNING: Generated empty fingerprint, using random UUID fallback")
			fp = generateRandomUUID()
		}
		cachedFingerprint = fp
	})
	return cachedFingerprint
}

// ResetFingerprint clears the cached fingerprint. Used for testing.
func ResetFingerprint() {
	fingerprintOnce = sync.Once{}
}

// hashSources combines source strings into a SHA256 fingerprint.
// It tries progressively weaker combinations until one has sufficient entropy.
func hashSources(s fingerprintSources) string {
	// 1. Strongest: MAC + disk serial + motherboard UUID
	input := s.MAC + s.DiskSerial + s.Motherboard
	if len(input) >= minFingerprintEntropy {
		return sha256Hex(input)
	}

	// 2. Medium: MAC + motherboard UUID
	input = s.MAC + s.Motherboard
	if len(input) >= minFingerprintEntropy {
		return sha256Hex(input)
	}

	// 3. Weak: MAC + hostname + machine_id
	input = s.MAC + s.Hostname + s.MachineID
	if len(input) >= minFingerprintEntropy {
		return sha256Hex(input)
	}

	// 4. Fallback: generate a random UUID (persistent per install via storage layer)
	return ""
}

// generateRandomUUID creates a version 4 UUID string.
func generateRandomUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Cryptographic randomness unavailable — use nanosecond timestamp + PID
		// (extremely unlikely on any modern OS — would only happen in a sandbox)
		log.Printf("WARNING: crypto/rand unavailable, using time-based UUID fallback: %v", err)
		return fmt.Sprintf("fallback-%x-%x", time.Now().UnixNano(), time.Now().UnixMilli())
	}

	// Set version 4 bits
	buf[6] = (buf[6] & 0x0f) | 0x40
	// Set variant bits (RFC 4122)
	buf[8] = (buf[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// sha256Hex returns the hex SHA256 hash of a string.
func sha256Hex(input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h)
}

// ValidateFingerprint checks that a fingerprint string is non-empty and has
// minimum expected length for a SHA256 hex digest.
func ValidateFingerprint(fp string) bool {
	return len(fp) >= 16
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
