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
// Hardening: thread-safe, error wrapping, minimum-entropy check.
package activation

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
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

// platformCollector is set by platform-specific init() functions.
var platformCollector func() fingerprintSources
var collectorOnce sync.Once

// cachedFingerprint stores the first generated fingerprint so it remains
// stable for the lifetime of the process (important for the random UUID fallback).
var cachedFingerprint string
var fingerprintOnce sync.Once

// GenerateFingerprint creates a SHA256 device fingerprint from hardware sources.
// The result is cached so subsequent calls return the same value.
func GenerateFingerprint() string {
	fingerprintOnce.Do(func() {
		collectorOnce.Do(func() {
			if platformCollector == nil {
				log.Println("WARNING: No platform collector registered, using fallback fingerprint")
				platformCollector = func() fingerprintSources {
					return fingerprintSources{}
				}
			}
		})

		sources := platformCollector()
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
