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
//   - Server only includes update fields in the heartbeat response when
//     hash(fingerprint) % 100 < rollout_percent (gated server-side)
//   - The client simply acts when update fields are present — it does not
//     compute the rollout gate itself
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

	"locus/internal/pinned"
)

const (
	// Sentinel files for two-phase update safety.
	SentinelPending   = ".update-pending"
	SentinelConfirmed = ".update-confirmed"
	SentinelReverted  = ".reverted"

	// BackupDir is where the previous binary is saved during update.
	BackupDir = ".locus-backups"

	// HandoffFlag is appended to the successor process's arguments when the
	// updater forks it. The successor waits for this process to exit before
	// showing its window, so the user never sees two Locus windows during an
	// update. Shared as a constant so main and the updater cannot drift on the
	// spelling — a mismatch would silently reintroduce the double window.
	HandoffFlag = "--handoff"

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
	Version               string
	SHA256                string
	DownloadURL           string
	DownloadURLLinux      string
	DownloadURLWindows    string
	DownloadURLMacOSIntel string
	DownloadURLMacOSARM   string

	// Per-platform checksums (preferred over SHA256 when set).
	SHA256Linux      string
	SHA256Windows    string
	SHA256MacOSIntel string
	SHA256MacOSARM   string
}

// PlatformSHA256 returns the checksum for the artifact PlatformDownloadURL
// would fetch. Falls back to the legacy single SHA256 so older update_config
// rows (which only ever set one hash) keep working.
func (ui *UpdateInfo) PlatformSHA256() string {
	switch runtime.GOOS {
	case "linux":
		if ui.SHA256Linux != "" {
			return ui.SHA256Linux
		}
	case "windows":
		if ui.SHA256Windows != "" {
			return ui.SHA256Windows
		}
	case "darwin":
		if runtime.GOARCH == "arm64" && ui.SHA256MacOSARM != "" {
			return ui.SHA256MacOSARM
		}
		if ui.SHA256MacOSIntel != "" {
			return ui.SHA256MacOSIntel
		}
	}
	return ui.SHA256
}

// PlatformDownloadURL returns the download URL for the current platform.
func (ui *UpdateInfo) PlatformDownloadURL() string {
	switch runtime.GOOS {
	case "linux":
		if ui.DownloadURLLinux != "" {
			return ui.DownloadURLLinux
		}
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

	// stagingDir is the private directory downloads are written into before
	// being renamed into the install directory. Empty means "derive it" —
	// see ensureStagingDir — which keeps New usable from tests that only care
	// about path resolution.
	stagingDir string
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
				TLSClientConfig: pinned.TLSClientConfig(),
			},
		},
	}
}

// SetStagingDir overrides where downloads are staged before installation.
//
// The caller sets this from internal/install, which is the single place that
// knows how this copy of Locus was deployed. Left unset, the updater derives a
// private subdirectory of appDir, which is correct for every deployment shape
// but less informative in diagnostics.
func (u *Updater) SetStagingDir(dir string) {
	u.stagingDir = dir
}

// ensureStagingDir returns the private staging directory, creating it if
// needed. Falls back to deriving one from appDir when none was set.
func (u *Updater) ensureStagingDir() (string, error) {
	dir := u.stagingDir
	if dir == "" {
		dir = filepath.Join(u.appDir, defaultStagingDirName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create staging directory %s: %w", dir, err)
	}
	return dir, nil
}

// defaultStagingDirName matches internal/install's directory name. Duplicated
// as a constant rather than imported so the updater keeps no dependency on the
// install package — it is usable standalone in tests and the two names are
// asserted equal by a test in the install package.
const defaultStagingDirName = ".locus-staging"

// PerformUpdate downloads, verifies, and applies an update.
// The update is crash-safe: if the new binary crashes on first run,
// the old binary is automatically restored on next startup.
func (u *Updater) PerformUpdate(ctx context.Context, info UpdateInfo) error {
	if info.Version == "" {
		return fmt.Errorf("update info has empty version")
	}
	// Resolve the checksum for the artifact we will actually fetch. Each
	// release publishes a different binary per platform, so the generic
	// SHA256 is only a fallback for older update_config rows.
	expectedSHA := info.PlatformSHA256()
	if expectedSHA == "" {
		return fmt.Errorf("update info has empty SHA256 checksum")
	}

	downloadURL := info.PlatformDownloadURL()
	if downloadURL == "" {
		return fmt.Errorf("no download URL available for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	currentPath := filepath.Join(u.appDir, u.binaryName)

	// Step 0: Resolve the private staging directory.
	//
	// The download is staged inside the app's own directory, NOT the system
	// temp directory. os.Rename is only atomic within one filesystem, and
	// %TEMP% is frequently a different volume from the install location;
	// renaming across volumes degrades to a copy, which is neither atomic nor
	// safe for an executable. See internal/install for the full reasoning, and
	// rename.go for the field failure this prevents.
	stagingDir, err := u.ensureStagingDir()
	if err != nil {
		return fmt.Errorf("cannot prepare update staging area: %w", err)
	}

	// Step 1: Create backup of current binary
	backupPath, err := u.createBackup(currentPath)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Step 2: Download new binary into the staging directory.
	newPath := filepath.Join(stagingDir, u.binaryName+".new")
	if err := u.downloadBinary(ctx, downloadURL, newPath, expectedSHA); err != nil {
		// Clean up failed download
		_ = os.Remove(newPath)
		return fmt.Errorf("download failed: %w", err)
	}

	// Step 3: Verify SHA256
	if err := verifyChecksum(newPath, expectedSHA); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Step 4: Create pending sentinel (two-phase commit start)
	pendingPath := filepath.Join(u.appDir, SentinelPending)
	if err := os.WriteFile(pendingPath, []byte(info.Version+"\n"), 0644); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("cannot create pending sentinel: %w", err)
	}

	// Step 5: Swap binary
	if err := swapBinary(newPath, currentPath); err != nil {
		// Swap failed — clean up
		_ = os.Remove(pendingPath)
		_ = os.Remove(newPath)
		// Restore backup
		_ = u.restoreBackup(backupPath, currentPath)
		return fmt.Errorf("binary swap failed: %w", err)
	}

	// Step 6: Fork new process (parent will exit)
	if err := forkNewProcess(currentPath); err != nil {
		// Fork failed — we're still on the old binary
		_ = os.Remove(pendingPath)
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
	req.Header.Set("User-Agent", "Locus-Client/2.0")

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Create temp file
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}

	// The handle is closed EXPLICITLY below, before any rename.
	//
	// It used to rely on `defer f.Close()`, which runs at function return —
	// i.e. after the rename. On Windows a file with an open handle cannot be
	// renamed, so the code was racing its own file descriptor and failed
	// whenever the OS had not yet flushed the close. The defer is kept as a
	// safety net for the early-error paths below, where the close is harmless
	// whether or not it already happened.
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	// Download with size and hash verification
	// io.LimitReader ensures we cap at MaxDownloadSize (no unbounded reads)
	// io.Copy returns nil + bytes when LimitReader hits its limit (EOF-like)
	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)
	downloaded, err := io.Copy(writer, io.LimitReader(resp.Body, MaxDownloadSize))
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("download interrupted: %w", err)
	}

	if downloaded < MinDownloadSize {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("download too small: %d bytes (min %d)", downloaded, MinDownloadSize)
	}

	// Verify hash
	checksum := hex.EncodeToString(hasher.Sum(nil))
	if checksum != expectedSHA256 {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("SHA256 mismatch: got %s, expected %s", checksum, expectedSHA256)
	}

	// Close before renaming, and surface a failure to flush as its own error
	// rather than silently proceeding into a rename that cannot succeed. A
	// close error here means bytes may not have reached the disk, and the hash
	// we just computed was over what we *wrote*, not what landed — so this is
	// a real failure, not a formality.
	if err := f.Close(); err != nil {
		closed = true
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cannot flush downloaded file: %w", err)
	}
	closed = true

	// Rename into place. Retried against a transient external holder (an
	// antivirus scanner, an indexer, a sync client) rather than failing on the
	// first sharing violation — see rename.go for why this is a separate,
	// classified path instead of a bare os.Rename.
	if err := renameWithRetry(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		if isRetryableRenameError(err) {
			return fmt.Errorf("%s: %w", renameBlockedError(path), err)
		}
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
	defer func() { _ = f.Close() }()

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
		_ = os.Remove(pendingPath)
		_ = os.Remove(confirmedPath)
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
	_ = os.Remove(pendingPath)

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
	_ = os.Chmod(currentPath, 0755)

	// Mark reverted
	_ = os.WriteFile(filepath.Join(appDir, SentinelReverted), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)

	log.Printf("Reverted to backup binary (%d bytes)", info.Size())
	return true, nil
}

// copyFile copies a file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	d, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return nil
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
// These are set by init() in each platform file (update_linux.go, update_windows.go)
var swapFile func(newPath, currentPath string) error
var forkExec func(binaryPath string) error
