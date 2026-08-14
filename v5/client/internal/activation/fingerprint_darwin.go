//go:build darwin

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
		sources := collectDarwinSources()
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

// collectDarwinSources gathers hardware info on macOS.
// Sources:
//   - MAC address: en0/en1 interface (networksetup)
//   - Disk serial: ioreg IOPlatformSerialNumber
//   - Motherboard UUID: ioreg IOPlatformUUID
//   - Hostname
func collectDarwinSources() fingerprintSources {
	var sources fingerprintSources

	// MAC address — en0 (Wi-Fi) or en1 (Ethernet)
	for _, iface := range []string{"en0", "en1"} {
		if output, err := exec.Command("networksetup", "-getmacaddress", iface).Output(); err == nil {
			parts := strings.Split(string(output), " ")
			if len(parts) >= 3 {
				mac := strings.TrimSpace(parts[2])
				if mac != "" && mac != "00:00:00:00:00:00" {
					sources.MAC = mac
					break
				}
			}
		}
	}

	// Disk serial + motherboard UUID from ioreg
	if output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output(); err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "\"IOPlatformSerialNumber\"") {
				parts := strings.Split(line, "\"")
				if len(parts) >= 4 {
					sources.DiskSerial = parts[3]
				}
			}
			if strings.Contains(line, "\"IOPlatformUUID\"") {
				parts := strings.Split(line, "\"")
				if len(parts) >= 4 {
					sources.Motherboard = parts[3]
				}
			}
		}
	} else {
		log.Printf("INFO: ioreg not available (common in restricted environments): %v", err)
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		sources.Hostname = hostname
	}

	return sources
}
