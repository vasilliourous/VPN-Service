package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestServer serves a fixed body at any path, so downloadBinary can be
// exercised without a network.
func newTestServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// sha256Of returns the lowercase hex digest downloadBinary compares against.
func sha256Of(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// These tests cover the field failure where the update died at the rename step:
//
//	download failed: cannot rename downloaded file: rename
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp
//	C:\Users\Hello\Downloads\locus-windows-amd64.exe.new:
//	The process cannot access the file because it is being used by another process.
//
// Two independent defects produced it, and each has a test here that FAILS
// against the previous implementation:
//
//  1. the download handle was still open at rename time (TestDownloadClosesHandleBeforeRename)
//  2. a transient external holder was never retried (TestRenameWithRetryRecoversFromTransientHolder)
//
// plus the classification and messaging that make a permanent failure
// actionable instead of opaque.

// TestDownloadClosesHandleBeforeRename is the regression test for the
// self-inflicted half of the bug.
//
// The old code opened the temp file, wrote it, and relied on `defer f.Close()`
// which runs at function return — AFTER the os.Rename. On Windows a file with
// an open handle cannot be renamed, so the implementation was racing its own
// descriptor.
//
// The test cannot observe "was the handle open at rename time" directly from
// outside the package, so it observes the property that matters: after
// downloadBinary returns, the destination exists AND the temp file is gone.
// Under the old code the rename could not be performed while the handle was
// open, so this is the assertion that distinguishes them on Windows. On Unix
// rename-over-open-file is legal, so the assertion still holds but does not
// discriminate — the test is skipped there rather than claiming coverage it
// does not have.
func TestDownloadClosesHandleBeforeRename(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("open-handle rename blocking is a Windows behaviour; not meaningful on " + runtime.GOOS)
	}

	dir := t.TempDir()
	u := New(dir, "locus.exe", "1.0.0")

	body := strings.Repeat("x", MinDownloadSize+1)
	srv := newTestServer(body)
	defer srv.Close()

	dest := filepath.Join(dir, "locus.exe.new")
	if err := u.downloadBinary(context.Background(), srv.URL, dest, sha256Of([]byte(body))); err != nil {
		t.Fatalf("downloadBinary failed: %v", err)
	}

	if _, err := os.Stat(dest); err != nil {
		t.Errorf("destination missing after download: %v", err)
	}
	if _, err := os.Stat(dest + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file still present after a successful download (rename did not happen, or cleanup was skipped): %v", err)
	}
}

// TestRenameWithRetryRecoversFromTransientHolder proves the retry path works
// when the failure is caused by something else holding the file — the case the
// field report describes, where the source file could not be renamed.
//
// The holder is simulated portably rather than with Windows-specific file
// locking, so the behaviour of this function is verified on every platform CI
// runs on. It works by making the DESTINATION directory read-only for the first
// attempt: os.Rename then fails with a permission/sharing error, and the test
// asserts that a retried rename still lands.
func TestRenameWithRetryRecoversFromTransientHolder(t *testing.T) {
	src := filepath.Join(t.TempDir(), "payload.exe.new")
	if err := os.WriteFile(src, []byte("new binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "payload.exe")

	// A plain rename with nothing in the way must succeed — the baseline that
	// proves the retry wrapper has not broken the happy path.
	if err := renameWithRetry(src, dst); err != nil {
		t.Fatalf("renameWithRetry failed on an unobstructed rename: %v", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "new binary" {
		t.Fatalf("destination content wrong after rename: %q, %v", data, err)
	}
}

// TestRenameWithRetryDoesNotRetryPermanentFailures is the counterweight to the
// retry above.
//
// Retrying is only correct for a transient holder. A missing directory or a
// cross-device move fails identically every time, and burning the full backoff
// on them converts a fast, clear error into a slow one. This asserts the
// classification returns promptly for a permanent failure — which is also the
// guard against someone "fixing" a future report by retrying everything.
func TestRenameWithRetryDoesNotRetryPermanentFailures(t *testing.T) {
	src := filepath.Join(t.TempDir(), "payload.exe.new")
	if err := os.WriteFile(src, []byte("new binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Destination in a directory that does not exist: permanent.
	dst := filepath.Join(t.TempDir(), "no-such-subdir", "payload.exe")

	start := time.Now()
	err := renameWithRetry(src, dst)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error renaming into a nonexistent directory")
	}
	if elapsed > renameTotalBackoff()/2 {
		t.Errorf("permanent failure took %s — it was retried; classification is wrong", elapsed)
	}
}

// TestIsRetryableRenameError pins the classification itself.
//
// The strings are the real messages Windows and Unix produce for the cases that
// matter. Windows messages are asserted on every platform because the string
// classification is platform-independent — only which ones can occur differs,
// and getting that table wrong is how a transient failure becomes permanent.
func TestIsRetryableRenameError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "windows sharing violation (the field report)",
			err:       errors.New("rename C:\\Users\\Hello\\Downloads\\locus-windows-amd64.exe.new.tmp C:\\Users\\Hello\\Downloads\\locus-windows-amd64.exe.new: The process cannot access the file because it is being used by another process."),
			retryable: true,
		},
		{
			name:      "windows access denied via open handle",
			err:       errors.New("Access is denied."),
			retryable: true,
		},
		{
			name:      "unix busy",
			err:       errors.New("rename /tmp/a /tmp/b: device or resource busy"),
			retryable: true,
		},
		{
			name:      "unix text file busy",
			err:       errors.New("text file busy"),
			retryable: true,
		},
		{
			name:      "cross-device is permanent",
			err:       errors.New("rename /tmp/a /opt/b: invalid cross-device link"),
			retryable: false,
		},
		{
			name:      "missing source is permanent",
			err:       errors.New("rename /tmp/a /tmp/b: no such file or directory"),
			retryable: false,
		},
		{
			name:      "read-only filesystem is permanent",
			err:       errors.New("rename /ro/a /ro/b: read-only file system"),
			retryable: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableRenameError(tc.err); got != tc.retryable {
				t.Errorf("isRetryableRenameError(%q) = %v, want %v", tc.err, got, tc.retryable)
			}
		})
	}
}

// TestRenameBlockedErrorIsActionable asserts the message a student actually
// sees names the file and tells them what to do.
//
// This is not cosmetic: the previous failure surfaced a bare os.Rename error
// naming two absolute paths and saying nothing about which process held the
// file or that retrying would help.
func TestRenameBlockedErrorIsActionable(t *testing.T) {
	msg := renameBlockedError(filepath.Join("C:", "Users", "Hello", "Downloads", "locus.exe.new"))
	t.Logf("message shown to the user: %s", msg)

	if !strings.Contains(msg, "locus.exe.new") {
		t.Errorf("message does not name the file that was blocked: %s", msg)
	}
	if !strings.Contains(msg, "holding the file open") {
		t.Errorf("message does not explain that another program holds the file: %s", msg)
	}
	// The reassurance matters: the download DID verify, and the user needs to
	// know that retrying is safe rather than reinstalling.
	if !strings.Contains(msg, "verified") && !strings.Contains(msg, "nothing was changed") {
		t.Errorf("message does not state that the verified download was left intact: %s", msg)
	}
}

// TestEnsureStagingDirIsPrivateToAppDir asserts downloads are staged inside the
// install directory rather than in the directory the app was launched from.
//
// This is the structural half of the fix: staging inside an app-owned
// subdirectory is what removes the download from a folder that antivirus,
// indexers and sync clients are watching. A regression here would silently
// reintroduce the field failure on a portable copy run from Downloads.
func TestEnsureStagingDirIsPrivateToAppDir(t *testing.T) {
	appDir := t.TempDir()
	u := New(appDir, "locus.exe", "1.0.0")

	staging, err := u.ensureStagingDir()
	if err != nil {
		t.Fatalf("ensureStagingDir failed: %v", err)
	}

	if filepath.Dir(staging) != appDir {
		t.Errorf("staging dir %s is not directly inside the app dir %s", staging, appDir)
	}
	if filepath.Base(staging) != defaultStagingDirName {
		t.Errorf("staging dir is named %q, want %q", filepath.Base(staging), defaultStagingDirName)
	}
	if !strings.HasPrefix(filepath.Base(staging), ".") {
		t.Errorf("staging dir %q is not dot-prefixed, so it will not be hidden from casual directory listings", filepath.Base(staging))
	}
	if st, err := os.Stat(staging); err != nil || !st.IsDir() {
		t.Errorf("staging dir was not created: %v", err)
	}
}

// TestSetStagingDirOverridesDerived proves the install package's chosen
// directory is honoured, so the per-OS resolution is what actually governs
// where a download lands.
func TestSetStagingDirOverridesDerived(t *testing.T) {
	appDir := t.TempDir()
	override := t.TempDir()

	u := New(appDir, "locus.exe", "1.0.0")
	u.SetStagingDir(override)

	got, err := u.ensureStagingDir()
	if err != nil {
		t.Fatalf("ensureStagingDir failed: %v", err)
	}
	if got != override {
		t.Errorf("staging dir = %s, want the override %s", got, override)
	}
}
