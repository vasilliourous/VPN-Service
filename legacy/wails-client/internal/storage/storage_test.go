// Package storage tests — corrupt-file recovery and backup restore.
package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// writeJSON writes a JSON file at path (helper).
func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), filePerm); err != nil {
		t.Fatal(err)
	}
}

// TestCorruptFileRestoresFromBackup is the money test: a corrupt storage.json
// (crash mid-write) must NOT deactivate the device — the newest valid .bak.N
// backup is restored automatically.
func TestCorruptFileRestoresFromBackup(t *testing.T) {
	dir := t.TempDir()

	// 1. Create a store with an activation and let save() rotate a backup.
	s, err := openAt(dir)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	cfg := &ServerConfig{Server: "vpn.example.com", ServerPort: 8443, Password: "secret", Method: "aes-256-gcm"}
	if err := s.SetActivation("RQ-AAAA-BBBB-CCCC-C", "eco", "fp1234567890abcdef", cfg, false); err != nil {
		t.Fatalf("SetActivation: %v", err)
	}

	// 2. A second save rotates the first backup (the .bak.0 of the activated
	// state — backups are created by the save AFTER the one being protected).
	if err := s.SetHeartbeat(12345); err != nil {
		t.Fatalf("SetHeartbeat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "storage.json.bak.0")); err != nil {
		t.Fatalf("backup .bak.0 not created: %v", err)
	}

	// 3. Corrupt the live file (simulates a crash mid-write).
	writeJSON(t, filepath.Join(dir, "storage.json"), "{not valid json!!!")

	// 4. Reopen — must recover from .bak.0 and keep the activation.
	s2, err := openAt(dir)
	if err != nil {
		t.Fatalf("openAt after corruption: %v", err)
	}
	if !s2.IsActivated() {
		t.Fatal("activation lost after corrupt-file recovery — backup restore failed")
	}
	if got := s2.GetCode(); got != "RQ-AAAA-BBBB-CCCC-C" {
		t.Fatalf("code = %q after recovery, want RQ-AAAA-BBBB-CCCC-C", got)
	}
	if s2.GetData().ServerConfig == nil || s2.GetData().ServerConfig.Password != "secret" {
		t.Fatal("server config not restored from backup")
	}
}

// TestCorruptFileWithoutBackupStartsFresh confirms the fallback: no valid
// backup → corrupt file is moved aside and the store starts empty (never
// bricks startup).
func TestCorruptFileWithoutBackupStartsFresh(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "storage.json"), "{not valid json!!!")

	s, err := openAt(dir)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	if s.IsActivated() {
		t.Fatal("store must start unactivated without a usable backup")
	}
	// The corrupt file must be moved aside, not left in place.
	if _, err := os.Stat(filepath.Join(dir, "storage.json")); !os.IsNotExist(err) {
		t.Fatal("corrupt storage.json was not moved aside")
	}
	// And the app must still be able to persist new state.
	if err := s.SetHeartbeat(12345); err != nil {
		t.Fatalf("SetHeartbeat after fresh start: %v", err)
	}
}

// TestBackupRestoreSkipsInvalidBackups: a corrupt backup is skipped in favour
// of an older valid one, and an internally inconsistent "activated but empty
// code" snapshot is not used for recovery.
func TestBackupRestoreSkipsInvalidBackups(t *testing.T) {
	dir := t.TempDir()

	// Newest backup (.bak.0) is garbage — must be skipped.
	writeJSON(t, filepath.Join(dir, "storage.json.bak.0"), "{nope")
	// Middle backup (.bak.1) is "activated but empty code" — inconsistent,
	// must be skipped too.
	writeJSON(t, filepath.Join(dir, "storage.json.bak.1"), `{"activated": true, "code": ""}`)
	// Oldest backup (.bak.2) holds the valid activated state.
	writeJSON(t, filepath.Join(dir, "storage.json.bak.2"), `{
	  "code": "RQ-AAAA-BBBB-CCCC-C",
	  "tier": "eco",
	  "device_fingerprint": "fp1234567890abcdef",
	  "activated": true,
	  "server_config": {"server": "vpn.example.com", "server_port": 8443, "password": "secret", "method": "aes-256-gcm"}
	}`)
	// Live file is corrupt.
	writeJSON(t, filepath.Join(dir, "storage.json"), "{nope")

	s, err := openAt(dir)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	if !s.IsActivated() {
		t.Fatal("recovery failed to restore the valid .bak.2 snapshot")
	}
	if got := s.GetCode(); got != "RQ-AAAA-BBBB-CCCC-C" {
		t.Fatalf("code = %q, want RQ-AAAA-BBBB-CCCC-C", got)
	}
}

// TestBackupRotation tests the numbered rotation (newest at .bak.0).
func TestBackupRotation(t *testing.T) {
	dir := t.TempDir()
	s, err := openAt(dir)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	for i := 0; i < maxBackups+2; i++ {
		if err := s.SetHeartbeat(int64(1000 + i)); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	// Oldest backup must be pruned: only maxBackups .bak files exist.
	matches, _ := filepath.Glob(filepath.Join(dir, "storage.json.bak.*"))
	if len(matches) != maxBackups {
		t.Fatalf("backup count = %d, want %d", len(matches), maxBackups)
	}
}
