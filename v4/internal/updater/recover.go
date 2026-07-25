// Package updater — crash recovery subsystem.
//
// The recovery system provides:
//  1. Auto-revert: If the new binary crashes before confirming, revert on next start
//  2. Manual revert: User passes --revert flag to restore the backup
//  3. Crash detection: Detects unexpected shutdowns during update via heartbeat timing
//  4. Sentinel handshake: .update-pending → .update-confirmed two-phase commit
package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CrashDetector monitors for unexpected process termination during updates.
type CrashDetector struct {
	appDir     string
	binaryName string
}

// NewCrashDetector creates a crash detector.
func NewCrashDetector(appDir, binaryName string) *CrashDetector {
	return &CrashDetector{
		appDir:     appDir,
		binaryName: binaryName,
	}
}

// WasCrashedUpdate checks if the previous run was an update that crashed.
// This is determined by:
//  1. .update-pending exists (binary was swapped)
//  2. .update-confirmed does NOT exist (new binary never confirmed)
//  3. The process was not manually killed (heuristic: storage has crashed_on_update flag)
func (cd *CrashDetector) WasCrashedUpdate() bool {
	pendingPath := filepath.Join(cd.appDir, SentinelPending)
	confirmedPath := filepath.Join(cd.appDir, SentinelConfirmed)

	// No pending update
	if _, err := os.Stat(pendingPath); os.IsNotExist(err) {
		return false
	}

	// Update was confirmed — not a crash
	if _, err := os.Stat(confirmedPath); err == nil {
		return false
	}

	return true
}

// HandleCrashedUpdate performs auto-revert after a detected crash.
// Returns true if a rollback was performed.
func (cd *CrashDetector) HandleCrashedUpdate() (bool, error) {
	if !cd.WasCrashedUpdate() {
		return false, nil
	}

	reverted, err := performRevert(cd.appDir, cd.binaryName)
	if err != nil {
		return false, fmt.Errorf("auto-revert failed: %w", err)
	}

	if reverted {
		// Remove pending sentinel (revert already placed .reverted)
		os.Remove(filepath.Join(cd.appDir, SentinelPending))
	}

	return reverted, nil
}

// CheckHeartbeatCrash detects crashes by comparing the last heartbeat timestamp
// with the update timestamp. If a heartbeat was expected but missing after an update,
// it's likely the new version crashed.
//
// This is the second line of defense after the sentinel handshake.
func CheckHeartbeatCrash(lastHeartbeatOK int64, updateTimestamp int64, gracePeriod time.Duration) bool {
	if updateTimestamp == 0 {
		return false // No update in progress
	}

	if lastHeartbeatOK == 0 {
		// No heartbeat ever — still within grace period after update
		return time.Since(time.Unix(updateTimestamp, 0)) > gracePeriod
	}

	// Heartbeat stopped after update
	return lastHeartbeatOK < updateTimestamp
}

// MarkUpdateStart records that we're about to perform an update.
// This creates a crash-recovery marker in the filesystem.
func MarkUpdateStart(appDir string, version string) error {
	marker := filepath.Join(appDir, ".update-start-"+version)
	return os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)
}

// ClearUpdateStart removes the update start marker on success.
func ClearUpdateStart(appDir string, version string) {
	os.Remove(filepath.Join(appDir, ".update-start-"+version))
}

// IsUpdateStale checks if an update marker is older than the grace period.
// Used during startup to detect updates that never completed.
func IsUpdateStale(appDir string, version string, maxAge time.Duration) bool {
	marker := filepath.Join(appDir, ".update-start-"+version)
	info, err := os.Stat(marker)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > maxAge
}

// RecoveryState summarizes the current recovery status.
type RecoveryState struct {
	// Was a rollback performed?
	RolledBack bool

	// What was the previous version?
	PreviousVersion string

	// What was the attempted version?
	AttemptedVersion string

	// Reason for the rollback
	Reason string
}

// DiagnoseRecovery examines the system state and returns a recovery diagnosis.
func DiagnoseRecovery(appDir string) *RecoveryState {
	state := &RecoveryState{}

	// Check reverted sentinel
	if _, err := os.Stat(filepath.Join(appDir, SentinelReverted)); err == nil {
		state.RolledBack = true
		state.Reason = "manual revert or auto-revert after crash"
	}

	// Check for stale update markers
	entries, _ := os.ReadDir(appDir)
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > 12 && name[:12] == ".update-start-" {
			state.AttemptedVersion = name[12:]
			if IsUpdateStale(appDir, state.AttemptedVersion, 24*time.Hour) {
				state.Reason = "update started but never completed (stale marker)"
			}
		}
	}

	return state
}
