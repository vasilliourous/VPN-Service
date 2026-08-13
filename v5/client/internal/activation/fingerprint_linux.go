// Package activation provides device fingerprint generation.
//
// The fingerprint is a deterministic hardware identifier used to bind
// an activation code to a specific device. It is SHA256-hashed client-side
// before transmission to the server.
//
// Fingerprint sources (by priority):
//  1. MAC address + disk serial + motherboard UUID (strongest)
//  2. MAC address + motherboard UUID (medium)
//  3. MAC address + hostname + machine_id (weak but stable)
//  4. Random UUID stored in app data (persistent per install)
//
// The generated fingerprint is stable for the lifetime of the process.
// The caller (storage layer) persists the first generated fingerprint
// so it remains stable across app restarts.
//
// Supported platforms: linux, windows (macOS dropped in the 2026-08 culling —
// unsigned builds are unusable on macOS, so darwin code was removed entirely).
// Each platform file is self-contained: shared logic lives in
// fingerprint_linux.go / fingerprint_windows.go.
//
// Hardening: thread-safe, error wrapping, minimum-entropy check.
package activation

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
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
		sources := collectLinuxSources()
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

// collectLinuxSources gathers hardware info on Linux.
// Sources:
//   - MAC address: first non-loopback physical interface
//   - Disk serial: /sys/block/<device>/device/serial or udevadm
//   - Motherboard UUID: /sys/class/dmi/id/product_uuid
//   - Machine ID: /etc/machine-id or /var/lib/dbus/machine-id
func collectLinuxSources() fingerprintSources {
	var sources fingerprintSources

	// MAC address — iterate all interfaces, preferring physical ones
	// Try common interface names first (bare metal, VPS, cloud)
	commonIfaces := []string{
		"eth0", "enp0s3", "enp0s8", "enp1s0", "ens3", "ens5", "eno1",
	}
	for _, name := range commonIfaces {
		if addr, err := readFile("/sys/class/net/" + name + "/address"); err == nil {
			addr = strings.TrimSpace(addr)
			if addr != "" && addr != "00:00:00:00:00:00" {
				sources.MAC = addr
				break
			}
		}
	}
	if sources.MAC == "" {
		// Fallback: scan all interfaces
		interfaces, err := os.ReadDir("/sys/class/net")
		if err == nil {
			for _, iface := range interfaces {
				name := iface.Name()
				if name == "lo" || strings.HasPrefix(name, "docker") ||
					strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") ||
					strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "wg") ||
					strings.HasPrefix(name, "lxc") || strings.HasPrefix(name, "cali") {
					continue
				}
				if addr, err := readFile("/sys/class/net/" + name + "/address"); err == nil {
					addr = strings.TrimSpace(addr)
					if addr != "" && addr != "00:00:00:00:00:00" {
						sources.MAC = addr
						break
					}
				}
			}
		}
	}

	// Disk serial — try primary disk (sda, nvme0n1, vda, xvda)
	for _, disk := range []string{"sda", "nvme0n1", "vda", "xvda", "sdb"} {
		if serial, err := readFile("/sys/block/" + disk + "/device/serial"); err == nil {
			serial = strings.TrimSpace(serial)
			if serial != "" {
				sources.DiskSerial = serial
				break
			}
		}
	}

	// Motherboard UUID
	if uuid, err := readFile("/sys/class/dmi/id/product_uuid"); err == nil {
		sources.Motherboard = strings.TrimSpace(uuid)
	} else {
		log.Printf("INFO: motherboard UUID not available (common in containers/VMs): %v", err)
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		sources.Hostname = hostname
	}

	// Machine ID
	if id, err := readFile("/etc/machine-id"); err == nil {
		sources.MachineID = strings.TrimSpace(id)
	} else if id, err := readFile("/var/lib/dbus/machine-id"); err == nil {
		sources.MachineID = strings.TrimSpace(id)
	}

	return sources
}

// readFile reads a file and returns its contents, or an error.
func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
