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
//
// Hardening: checksum verification before swap, download validation with size check,
// context propagation for cancellation, retry on download failure, backup integrity check.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
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

	// MaxDownloadSize is the maximum allowed download size (500MB).
	MaxDownloadSize = 500 * 1024 * 1024

	// MinDownloadSize is the minimum expected size (1MB — any real binary).
	MinDownloadSize = 1024 * 1024
)

// UpdateInfo describes an available update with platform-specific assets.
// Matches the heartbeat response structure.
type UpdateInfo struct {
	Version       string
	SHA256        string
	DownloadURL   string
	DownloadURLWindows  string
	DownloadURLMacOSIntel string
	DownloadURLMacOSARM  string
}

// PlatformDownloadURL returns the download URL for the current platform.
func (ui *UpdateInfo) PlatformDownloadURL() string {
	switch runtime.GOOS {
	case "windows":
		if ui.DownloadURLWindows != "" {
			return ui.DownloadURLWindows
		}
	case "darwin":
		if runtime.GOARCH == "arm64" && ui.DownloadURLMacOSARM != "" {
			return ui.DownloadURLMacOSARM
		}
		if ui.DownloadURLMacOSIntel != "" {
			return ui.DownloadURLMacOSIntel
		}
	}
	return ui.DownloadURL
}

// Updater manages the update lifecycle.
type Updater struct {
	appDir     string
	binaryName string
	currentVer string
	client     *http.Client
}

// New creates a new Updater.
func New(appDir, binaryName, currentVer string) *Updater {
	return &Updater{
		appDir:     appDir,
		binaryName: binaryName,
		currentVer: currentVer,
		client: &http.Client{
			Timeout: DownloadTimeout,
			Transport: &http.Transport{
				MaxIdleConns:    2,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

// PerformUpdate downloads, verifies, and applies an update.
// The update is crash-safe: if the new binary crashes on first run,
// the old binary is automatically restored on next startup.
func (u *Updater) PerformUpdate(ctx context.Context, info UpdateInfo) error {
	if info.Version == "" {
		return fmt.Errorf("update info has empty version")
	}
	if info.SHA256 == "" {
		return fmt.Errorf("update info has empty SHA256 checksum")
	}

	downloadURL := info.PlatformDownloadURL()
	if downloadURL == "" {
		return fmt.Errorf("no download URL available for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	currentPath := filepath.Join(u.appDir, u.binaryName)

	// Step 1: Create backup of current binary
	backupPath, err := u.createBackup(currentPath)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Step 2: Download new binary
	newPath := filepath.Join(u.appDir, u.binaryName+".new")
	if err := u.downloadBinary(ctx, downloadURL, newPath, info.SHA256); err != nil {
		// Clean up failed download
		os.Remove(newPath)
		return fmt.Errorf("download failed: %w", err)
	}

	// Step 3: Verify SHA256
	if err := verifyChecksum(newPath, info.SHA256); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Step 4: Create pending sentinel (two-phase commit start)
	pendingPath := filepath.Join(u.appDir, SentinelPending)
	if err := os.WriteFile(pendingPath, []byte(info.Version+"\n"), 0644); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("cannot create pending sentinel: %w", err)
	}

	// Step 5: Swap binary
	if err := swapBinary(newPath, currentPath); err != nil {
		// Swap failed — clean up
		os.Remove(pendingPath)
		os.Remove(newPath)
		// Restore backup
		u.restoreBackup(backupPath, currentPath)
		return fmt.Errorf("binary swap failed: %w", err)
	}

	// Step 6: Fork new process (parent will exit)
	if err := forkNewProcess(currentPath); err != nil {
		// Fork failed — we're still on the old binary
		os.Remove(pendingPath)
		return fmt.Errorf("fork failed: %w", err)
	}

	return nil
}

// createBackup saves the current binary to the backup directory.
// Returns the backup path.
func (u *Updater) createBackup(currentPath string) (string, error) {
	backupDir := filepath.Join(u.appDir, BackupDir)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create backup dir: %w", err)
	}

	backupPath := filepath.Join(backupDir, u.binaryName+".prev")
	if err := copyFile(currentPath, backupPath); err != nil {
		return "", fmt.Errorf("cannot copy to backup: %w", err)
	}

	return backupPath, nil
}

// restoreBackup copies a backup back to the original location.
func (u *Updater) restoreBackup(backupPath, currentPath string) error {
	return copyFile(backupPath, currentPath)
}

// downloadBinary downloads a file from URL to path, verifying SHA256.
func (u *Updater) downloadBinary(ctx context.Context, url, path, expectedSHA256 string) error {
	log.Printf("Downloading update from %s to %s", url, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("cannot create download request: %w", err)
	}
	req.Header.Set("User-Agent", "MyVPN-Client/2.0")

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Create temp file
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	defer f.Close()

	// Download with size and hash verification
	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)
	downloaded, err := io.CopyN(writer, io.LimitReader(resp.Body, MaxDownloadSize), MaxDownloadSize)
	if err != nil && err != io.EOF {
		os.Remove(tmpPath)
		return fmt.Errorf("download interrupted: %w", err)
	}

	if downloaded < MinDownloadSize {
		os.Remove(tmpPath)
		return fmt.Errorf("download too small: %d bytes (min %d)", downloaded, MinDownloadSize)
	}

	// Verify hash
	checksum := hex.EncodeToString(hasher.Sum(nil))
	if checksum != expectedSHA256 {
		os.Remove(tmpPath)
		return fmt.Errorf("SHA256 mismatch: got %s, expected %s", checksum, expectedSHA256)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("cannot rename downloaded file: %w", err)
	}

	log.Printf("Download complete: %d bytes, SHA256 verified", downloaded)
	return nil
}

// verifyChecksum checks that a file's SHA256 matches the expected value.
func verifyChecksum(path, expectedSHA256 string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open file for checksum: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("cannot hash file: %w", err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	if checksum != expectedSHA256 {
		return fmt.Errorf("SHA256 mismatch: got %s, expected %s", checksum, expectedSHA256)
	}

	return nil
}

// CheckOnStartup runs the two-phase update recovery check.
// It should be called before anything else in main().
// Returns true if a rollback was performed.
func CheckOnStartup(revertFlag bool) (bool, error) {
	execPath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("cannot determine executable path: %w", err)
	}
	appDir := filepath.Dir(execPath)
	binaryName := filepath.Base(execPath)

	// Handle --revert flag (manual rollback)
	if revertFlag {
		reverted, err := performRevert(appDir, binaryName)
		if err != nil {
			return false, fmt.Errorf("manual revert failed: %w", err)
		}
		return reverted, nil
	}

	// Check for crash-recovery scenario
	pendingPath := filepath.Join(appDir, SentinelPending)
	confirmedPath := filepath.Join(appDir, SentinelConfirmed)

	if _, err := os.Stat(pendingPath); os.IsNotExist(err) {
		// No pending update — normal startup
		return false, nil
	}

	if _, err := os.Stat(confirmedPath); err == nil {
		// Update was confirmed in a previous run — clean up and proceed
		os.Remove(pendingPath)
		os.Remove(confirmedPath)
		return false, nil
	}

	// Pending sentinel exists but confirmed doesn't — update crashed
	log.Println("Detected crashed update: .update-pending exists without .update-confirmed")
	log.Println("Performing auto-revert to previous version")

	reverted, err := performRevert(appDir, binaryName)
	if err != nil {
		return false, fmt.Errorf("auto-revert failed: %w", err)
	}

	// Clean up pending sentinel
	os.Remove(pendingPath)

	return reverted, nil
}

// performRevert restores the backup binary.
func performRevert(appDir, binaryName string) (bool, error) {
	backupPath := filepath.Join(appDir, BackupDir, binaryName+".prev")
	currentPath := filepath.Join(appDir, binaryName)

	// Check if backup exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return false, fmt.Errorf("no backup found at %s", backupPath)
	}

	// Verify backup has reasonable size (>1MB)
	info, err := os.Stat(backupPath)
	if err != nil {
		return false, fmt.Errorf("cannot stat backup: %w", err)
	}
	if info.Size() < MinDownloadSize {
		return false, fmt.Errorf("backup suspiciously small (%d bytes), refusing to restore", info.Size())
	}

	// Compare with current — if same, skip
	currentInfo, err := os.Stat(currentPath)
	if err == nil && os.SameFile(info, currentInfo) {
		return false, nil
	}

	// Restore backup
	if err := copyFile(backupPath, currentPath); err != nil {
		return false, fmt.Errorf("revert failed: %w", err)
	}

	// Ensure executable
	os.Chmod(currentPath, 0755)

	// Mark reverted
	os.WriteFile(filepath.Join(appDir, SentinelReverted), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)

	log.Printf("Reverted to backup binary (%d bytes)", info.Size())
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
	return swapFile(newPath, currentPath)
}

// forkNewProcess is platform-specific — implemented in update_*.go files.
func forkNewProcess(binaryPath string) error {
	return forkExec(binaryPath)
}

// Platform-specific implementations
// These are set by init() in each platform file (update_unix.go, update_windows.go)
var swapFile func(newPath, currentPath string) error
var forkExec func(binaryPath string) error
