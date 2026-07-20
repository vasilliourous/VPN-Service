package updater

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	sentinelPending   = ".update-pending"
	sentinelConfirmed = ".update-confirmed"
)

func CheckOnStartup(holdRevertKey bool) (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("cannot determine executable path: %w", err)
	}

	exeDir := filepath.Dir(exe)
	pendingPath := filepath.Join(exeDir, sentinelPending)
	confirmedPath := filepath.Join(exeDir, sentinelConfirmed)
	prevPath := filepath.Join(exeDir, "myvpn.prev")

	// Manual revert
	if holdRevertKey {
		if _, err := os.Stat(prevPath); err == nil {
			return performRevert(exe, prevPath, pendingPath, confirmedPath)
		}
		os.Remove(pendingPath)
		os.Remove(confirmedPath)
		return false, nil
	}

	// Phase 2: update-confirmed exists → previous first launch was successful
	if _, err := os.Stat(confirmedPath); err == nil {
		os.Remove(confirmedPath)
		os.Remove(prevPath)
		return false, nil
	}

	// Phase 1: update-pending exists → first launch after update (or crash)
	if _, err := os.Stat(pendingPath); err == nil {
		if _, err := os.Stat(prevPath); os.IsNotExist(err) {
			os.Remove(pendingPath)
			return false, nil
		}

		// Advance to Phase 2 — don't revert yet
		os.Rename(pendingPath, confirmedPath)
		return false, nil
	}

	return false, nil
}

func MarkUpdatePending() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exeDir := filepath.Dir(exe)
	f, err := os.Create(filepath.Join(exeDir, sentinelPending))
	if err != nil {
		return err
	}
	return f.Close()
}

func performRevert(exe, prevPath, pendingPath, confirmedPath string) (bool, error) {
	if err := os.Rename(prevPath, exe); err != nil {
		return false, fmt.Errorf("revert failed: %w", err)
	}
	os.Remove(pendingPath)
	os.Remove(confirmedPath)
	return true, nil
}
