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
package activation

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sync"
)

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
				platformCollector = func() fingerprintSources {
					return fingerprintSources{}
				}
			}
		})

		sources := platformCollector()
		cachedFingerprint = hashSources(sources)
	})
	return cachedFingerprint
}

// ResetFingerprint clears the cached fingerprint. Used for testing.
func ResetFingerprint() {
	fingerprintOnce = sync.Once{}
}

// hashSources combines source strings into a SHA256 fingerprint.
// It tries progressively weaker combinations until one works.
func hashSources(s fingerprintSources) string {
	// 1. Strongest: MAC + disk serial + motherboard UUID
	input := s.MAC + s.DiskSerial + s.Motherboard
	if len(input) > 3 {
		return sha256Hex(input)
	}

	// 2. Medium: MAC + motherboard UUID
	input = s.MAC + s.Motherboard
	if len(input) > 3 {
		return sha256Hex(input)
	}

	// 3. Weak: MAC + hostname + machine_id
	input = s.MAC + s.Hostname + s.MachineID
	if len(input) > 3 {
		return sha256Hex(input)
	}

	// 4. Fallback: generate a random UUID (persistent per install via storage layer)
	uuid := generateRandomUUID()
	return sha256Hex(uuid)
}

// generateRandomUUID creates a version 4 UUID string.
func generateRandomUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Cryptographic randomness unavailable — use a timestamp fallback
		// (extremely unlikely on any modern OS)
		return fmt.Sprintf("fallback-%d", sync.Once{})
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
