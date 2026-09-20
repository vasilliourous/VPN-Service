package updater

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Housekeeping reclaims disk left behind by previous updates.
//
// # WHY THIS IS NEEDED
//
// The Windows swap cannot delete the binary it is replacing, because that
// binary is still the running process. swapWindows therefore renames
// locus.exe -> locus.exe.old and then calls os.Remove(".old") — which fails,
// silently, because the file is still mapped as the current image. Nothing
// ever retried it. The result is that every update leaves a full ~10 MB copy of
// the application in the install directory, permanently, and a machine that has
// updated a dozen times carries a hundred megabytes of dead executables in
// Program Files.
//
// The same applies to .locus-backups/locus.exe.prev, which is written before
// every update and only ever read during a revert. It is a legitimate artefact
// to keep (it is what makes rollback possible), but not to keep indefinitely:
// only the one from the most recent update can legitimately be needed.
//
// This runs at startup, before the update check, so the cleanup happens on the
// same launch that would otherwise be blocked by a full disk. A file that
// cannot be removed is skipped rather than fatal: on Windows the .old file is
// usually still locked by the *previous* process for a short window, and it
// will be reclaimed on the next launch.
func Housekeeping(appDir, binaryName string) {
	removeStaleOldBinaries(appDir, binaryName)
	pruneStagingDir(appDir)
}

// removeStaleOldBinaries deletes the superseded binaries left by earlier swaps.
//
// Deliberately unconditional on age: by the time this runs, the process doing
// the running IS the binary at appDir/binaryName, so any "<binaryName>.old"
// beside it is by definition superseded and safe to remove. Waiting for a
// timeout would only mean the space is held longer than necessary.
func removeStaleOldBinaries(appDir, binaryName string) {
	candidates := []string{
		filepath.Join(appDir, binaryName+".old"),
		// Left by an aborted swap on a non-Windows build, harmless elsewhere.
		filepath.Join(appDir, binaryName+".old.tmp"),
	}

	for _, path := range candidates {
		if err := os.Remove(path); err == nil {
			log.Printf("Housekeeping: removed superseded binary %s", filepath.Base(path))
		}
	}
}

// pruneStagingDir removes the temporary files a staged download can leave
// behind if the process was killed between writing and renaming.
//
// Only *.tmp is touched. The directory also holds a live .new during an active
// update, and deleting that underneath a running download would be worse than
// the leak it fixes — so this is intentionally narrow.
func pruneStagingDir(appDir string) {
	stagingDir := filepath.Join(appDir, defaultStagingDirName)

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return // no staging dir yet, or not readable — nothing to do
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".tmp") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// A .tmp younger than this is likely the active download's own file.
		// The download holds an exclusive handle on Windows anyway, so the
		// remove would fail rather than corrupt it — but the age check makes
		// the intent explicit and keeps the operation obviously safe to read.
		if time.Since(info.ModTime()) < time.Hour {
			continue
		}

		path := filepath.Join(stagingDir, name)
		if err := os.Remove(path); err == nil {
			log.Printf("Housekeeping: removed stale staged download %s", name)
		}
	}
}
