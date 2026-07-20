package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

func DownloadAndVerify(url, expectedSHA256, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("update download failed: %w", err)
	}
	defer resp.Body.Close()

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("update temp file failed: %w", err)
	}

	hash := sha256.New()
	writer := io.MultiWriter(f, hash)
	_, err = io.Copy(writer, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("update write failed: %w", err)
	}
	f.Close()

	got := hex.EncodeToString(hash.Sum(nil))
	if got != expectedSHA256 {
		os.Remove(tmpPath)
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedSHA256, got)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("update rename failed: %w", err)
	}

	if runtime.GOOS != "windows" {
		os.Chmod(destPath, 0755)
	}

	return nil
}

func SwapBinary(newBinaryPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	exeDir := filepath.Dir(exe)
	prevPath := filepath.Join(exeDir, "myvpn.prev")

	// Backup current binary
	if err := copyFile(exe, prevPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Replace current binary
	if err := os.Rename(newBinaryPath, exe); err != nil {
		return fmt.Errorf("swap failed: %w", err)
	}

	return nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	_, err = io.Copy(d, s)
	return err
}
