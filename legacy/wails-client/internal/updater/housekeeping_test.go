package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHousekeepingRemovesOldAndStaleTmp(t *testing.T) {
	dir := t.TempDir()
	bin := "locus.exe"

	old := filepath.Join(dir, bin+".old")
	if err := os.WriteFile(old, []byte("stale 10MB binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	staging := filepath.Join(dir, defaultStagingDirName)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	// stale .tmp -> removed
	staleTmp := filepath.Join(staging, bin+".new.tmp")
	if err := os.WriteFile(staleTmp, []byte("partial download"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(staleTmp, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	// fresh .tmp -> kept
	freshTmp := filepath.Join(staging, bin+".new.tmp2.tmp")
	if err := os.WriteFile(freshTmp, []byte("active download"), 0o644); err != nil {
		t.Fatal(err)
	}
	// live .new must never be touched
	liveNew := filepath.Join(staging, bin+".new")
	if err := os.WriteFile(liveNew, []byte("verified update"), 0o755); err != nil {
		t.Fatal(err)
	}

	Housekeeping(dir, bin)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf(".old NOT removed: %v", err)
	}
	if _, err := os.Stat(staleTmp); !os.IsNotExist(err) {
		t.Errorf("stale .tmp NOT removed: %v", err)
	}
	if _, err := os.Stat(freshTmp); err != nil {
		t.Errorf("fresh .tmp wrongly removed: %v", err)
	}
	if _, err := os.Stat(liveNew); err != nil {
		t.Errorf("live .new wrongly removed — would break an active update: %v", err)
	}
}

func TestHousekeepingMissingDirIsSafe(t *testing.T) {
	dir := t.TempDir()
	Housekeeping(dir, "locus.exe") // must not panic
}
