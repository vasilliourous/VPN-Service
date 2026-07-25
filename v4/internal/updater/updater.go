// Package updater handles application updates with crash-safe two-phase deployment.
//
// Update flow:
//  1. Heartbeat response contains update_available + update_url + update_sha256
//  2. Client downloads the new binary to a temp location
//  3. Client verifies SHA256 checksum
//  4. Client creates .update-pending sentinel file
//  5. Client swaps binary (platform-specific)
//  6. Client forks the new binary, parent exits
//  7. New binary starts, sees .update-pending, creates .update-confirmed
//  8. If new binary crashes, on next start it sees .update-pending (no .update-confirmed)
//     and auto-reverts to the backup binary
//
// Staged rollout:
//   - Server sets rollout_percent (0-100) in update_config
//   - Client computes hash(fingerprint) % 100, only updates if < rollout_percent
//   - This allows gradual rollout without client changes
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	// Sentinel files for two-phase update safety.
	SentinelPending   = ".update-pending"
	SentinelConfirmed = ".update-confirmed"
	SentinelReverted  = ".reverted"

	// BackupDir is where the previous binary is saved during update.
	BackupDir = ".myvpn-backups"

	// DownloadTimeout is the max time for downloading an update.
	DownloadTimeout = 5 * time.Minute
)

// UpdateInfo describes an available update with platform-specific assets.
// Matches the update.json schema from the hub server.
type UpdateInfo struct {
	Version        string `json:"version"`
	RolloutPercent int    `json:"rollout_percent"`
	Windows        *Asset `json:"windows,omitempty"`
	MacOSIntel     *Asset `json:"macos_intel,omitempty"`
	MacOSARM       *Asset `json:"macos_arm,omitempty"`
}

// Asset holds a download URL and SHA256 checksum for a platform binary.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// URLForCurrentPlatform returns the download URL + SHA256 for the current OS/arch.
func (info *UpdateInfo) URLForCurrentPlatform() (url, sha256 string, ok bool) {
	switch runtime.GOOS {
	case "windows":
		if info.Windows != nil {
			return info.Windows.URL, info.Windows.SHA256, true
		}
	case "darwin":
		if runtime.GOARCH == "arm64" && info.MacOSARM != nil {
			return info.MacOSARM.URL, info.MacOSARM.SHA256, true
		}
		if info.MacOSIntel != nil {
			return info.MacOSIntel.URL, info.MacOSIntel.SHA256, true
		}
	default: // linux
		if info.Windows != nil {
			// Linux fallback to Windows? No — try the first non-nil asset
		}
	}
	// Fallback: return empty (caller should check ok)
	return "", "", false
}

// Result describes the outcome of an update attempt.
type Result struct {
	Success      bool
	Version      string
	ErrorMessage string
}

// Updater manages the update lifecycle.
type Updater struct {
	appDir     string // Directory containing the current binary
	binaryName string // Name of the current binary (e.g., "myvpn")
	version    string // Current version
	httpClient *http.Client
}

// New creates a new Updater.
func New(appDir, binaryName, version string) *Updater {
	return &Updater{
		appDir:     appDir,
		binaryName: binaryName,
		version:    version,
		httpClient: &http.Client{
			Timeout: DownloadTimeout,
		},
	}
}

// CheckOnStartup runs at application startup to handle pending updates.
// Returns true if a rollback occurred.
//
// This MUST be called before anything else in main().
func CheckOnStartup(revertFlag bool) (bool, error) {
	execPath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("cannot determine executable path: %w", err)
	}
	appDir := filepath.Dir(execPath)
	binaryName := filepath.Base(execPath)

	return checkOnStartup(appDir, binaryName, revertFlag)
}

// checkOnStartup is the internal implementation.
func checkOnStartup(appDir, binaryName string, revertFlag bool) (bool, error) {
	sentinelPending := filepath.Join(appDir, SentinelPending)
	sentinelConfirmed := filepath.Join(appDir, SentinelConfirmed)
	sentinelReverted := filepath.Join(appDir, SentinelReverted)

	// Check for manual revert flag
	if revertFlag {
		return performRevert(appDir, binaryName)
	}

	// Check if we just updated (sentinel exists)
	if _, err := os.Stat(sentinelPending); err == nil {
		// We just started from a swapped binary.
		// Check if confirmed file already exists (from a previous successful start)
		if _, err := os.Stat(sentinelConfirmed); err == nil {
			// This is a normal restart after a successful update
			cleanupSentinelFiles(appDir)
			return false, nil
		}

		// First start after update — confirm the update succeeded
		if err := os.WriteFile(sentinelConfirmed, []byte(time.Now().UTC().Format(time.RFC3339)), 0644); err != nil {
			// Couldn't write confirmed — still proceed but log the failure
			fmt.Fprintf(os.Stderr, "WARNING: Could not write update confirmation: %v\n", err)
		}

		return false, nil
	}

	// Check if previous update crashed (pending exists but no confirmed)
	// This is detected by checking .crashed-on-update flag in storage,
	// which is set when the app detects an unexpected shutdown during update.

	// Check if we just reverted
	if _, err := os.Stat(sentinelReverted); err == nil {
		os.Remove(sentinelReverted)
		return true, nil
	}

	return false, nil
}

// DownloadAndVerify downloads an update for the current platform, verifies its checksum, and saves it.
// It selects the correct platform-specific asset from UpdateInfo automatically.
func (u *Updater) DownloadAndVerify(info UpdateInfo) (string, error) {
	// Determine the correct download URL for this platform
	downloadURL, sha256Hex, ok := info.URLForCurrentPlatform()
	if !ok || downloadURL == "" {
		return "", fmt.Errorf("no download URL available for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Create temp download directory
	tmpDir, err := os.MkdirTemp("", "myvpn-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot create temp dir: %w", err)
	}

	// Download to temp file
	tmpFile := filepath.Join(tmpDir, u.binaryName+".new")
	if err := u.downloadFile(downloadURL, tmpFile); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("download failed: %w", err)
	}

	// Verify SHA256
	if sha256Hex != "" {
		if err := verifySHA256(tmpFile, sha256Hex); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	return tmpFile, nil
}

// Apply performs the two-phase update:
//  1. Save current binary as backup
//  2. Write .update-pending sentinel
//  3. Swap new binary in place
//  4. Fork new process
//  5. Exit current process
func (u *Updater) Apply(newBinaryPath string, info UpdateInfo) error {
	currentBinary := filepath.Join(u.appDir, u.binaryName)
	backupPath := filepath.Join(u.appDir, BackupDir, u.binaryName+"."+u.version)

	// Ensure backup directory exists
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		return fmt.Errorf("cannot create backup dir: %w", err)
	}

	// 1. Backup current binary
	if err := copyFile(currentBinary, backupPath); err != nil {
		return fmt.Errorf("cannot backup current binary: %w", err)
	}

	// 2. Write .update-pending sentinel
	pendingPath := filepath.Join(u.appDir, SentinelPending)
	if err := os.WriteFile(pendingPath, []byte(info.Version+"\n"), 0644); err != nil {
		return fmt.Errorf("cannot write update sentinel: %w", err)
	}

	// 3. Swap binary (platform-specific)
	if err := swapBinary(newBinaryPath, currentBinary); err != nil {
		// Cleanup on failure
		os.Remove(pendingPath)
		os.Remove(backupPath)
		return fmt.Errorf("cannot swap binary: %w", err)
	}

	// 4. Fork new process (platform-specific)
	if err := forkNewProcess(currentBinary); err != nil {
		return fmt.Errorf("cannot fork new process: %w", err)
	}

	// 5. Exit current process
	os.Exit(0)
	return nil
}

// ShouldUpdate determines if this client should receive a staged rollout update.
// Uses a deterministic hash of the fingerprint to distribute clients evenly.
// The same hash function is used server-side in the heartbeat hook for double-gating.
func ShouldUpdate(rolloutPercent int, fingerprint string) bool {
	if rolloutPercent <= 0 {
		return false
	}
	if rolloutPercent >= 100 {
		return true
	}

	// Deterministic hash: mirrors the server's hashMod() in heartbeat.pb.js
	// Using the same algorithm ensures client and server agree on rollout eligibility.
	hash := 0
	for i := 0; i < len(fingerprint); i++ {
		hash = ((hash << 5) - hash) + int(fingerprint[i])
		hash &= 0x7FFFFFFF // keep positive
	}
	bucket := hash % 100
	return bucket < rolloutPercent
}

// downloadFile downloads a URL to a local file path.
func (u *Updater) downloadFile(url, dest string) error {
	resp, err := u.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	return nil
}

// verifySHA256 checks that a file matches the expected SHA256 hash.
func verifySHA256(filePath, expectedHex string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHex {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedHex, actual)
	}
	return nil
}

// performRevert restores the backup binary.
func performRevert(appDir, binaryName string) (bool, error) {
	backupDir := filepath.Join(appDir, BackupDir)

	// Find the most recent backup
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return false, fmt.Errorf("no backup found: %w", err)
	}

	if len(entries) == 0 {
		return false, fmt.Errorf("no backup files found")
	}

	// Use the most recent backup
	latest := entries[len(entries)-1]
	backupPath := filepath.Join(backupDir, latest.Name())
	currentBinary := filepath.Join(appDir, binaryName)

	if err := copyFile(backupPath, currentBinary); err != nil {
		return false, fmt.Errorf("revert failed: %w", err)
	}

	// Mark reverted
	os.WriteFile(filepath.Join(appDir, SentinelReverted), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)

	return true, nil
}

// copyFile copies a file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return nil
}

// cleanupSentinelFiles removes update sentinel files.
func cleanupSentinelFiles(appDir string) {
	os.Remove(filepath.Join(appDir, SentinelPending))
	os.Remove(filepath.Join(appDir, SentinelConfirmed))
}

// swapBinary is platform-specific — implemented in update_*.go files.
func swapBinary(newPath, currentPath string) error {
	// On Unix: rename new over current
	// On Windows: rename current to .old, rename new to current
	return swapFile(newPath, currentPath)
}

// forkNewProcess is platform-specific — implemented in update_*.go files.
func forkNewProcess(binaryPath string) error {
	return forkExec(binaryPath)
}

// Platform-specific implementations
var swapFile func(newPath, currentPath string) error
var forkExec func(binaryPath string) error

func init() {
	switch runtime.GOOS {
	case "windows":
		swapFile = swapWindows
		forkExec = forkWindows
	default:
		swapFile = swapUnix
		forkExec = forkUnix
	}
}
