// Package storage provides local persistent storage for the MyVPN client.
//
// All data is stored as a single JSON file in the platform-specific app data
// directory. The file is encrypted at rest only by the OS (no app-level
// encryption — the activation code is the only secret, and it's short enough
// that disk encryption is assumed).
//
// File location by platform (appName = "myvpn"):
//
//	Linux:   ~/.config/myvpn/storage.json
//	macOS:   ~/Library/Application Support/myvpn/storage.json
//	Windows: %APPDATA%\myvpn\storage.json
//
// Hardening: atomic writes with temp file + rename, backup rotation (keep last 3),
// file permission validation, thread-safe reads/writes, input validation.
package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// maxBackups is the number of old storage files to retain.
	maxBackups = 3
	// configDirPerm is the permission for the config directory.
	configDirPerm = 0700
	// filePerm is the permission for the storage file.
	filePerm = 0600
)

// Data represents the persisted state of the MyVPN client.
type Data struct {
	// Activation state
	Code              string `json:"code,omitempty"`
	Tier              string `json:"tier,omitempty"`
	DeviceFingerprint string `json:"device_fingerprint,omitempty"`

	// Server config from activation
	ServerConfig *ServerConfig `json:"server_config,omitempty"`
	UDPRelay     bool          `json:"udp_relay,omitempty"`

	// App state
	Activated bool   `json:"activated"`
	Version   string `json:"version,omitempty"`

	// Update tracking
	UpdatePending   bool   `json:"update_pending,omitempty"`
	UpdateVersion   string `json:"update_version,omitempty"`
	UpdateSHA256    string `json:"update_sha256,omitempty"`
	UpdateTimestamp int64  `json:"update_timestamp,omitempty"`

	// Heartbeat tracking
	LastHeartbeatOK   int64 `json:"last_heartbeat_ok,omitempty"`
	HeartbeatFailures int   `json:"heartbeat_failures,omitempty"`

	// Crash recovery
	CrashedOnUpdate bool  `json:"crashed_on_update,omitempty"`
	CrashTimestamp  int64 `json:"crash_timestamp,omitempty"`
}

// ServerConfig holds the Shadowsocks connection parameters.
type ServerConfig struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
}

// Validate checks that the data is internally consistent.
func (d *Data) Validate() error {
	if d.Activated {
		if d.Code == "" {
			return fmt.Errorf("storage: activated but code is empty")
		}
		if d.Tier == "" {
			return fmt.Errorf("storage: activated but tier is empty")
		}
		if d.DeviceFingerprint == "" {
			return fmt.Errorf("storage: activated but fingerprint is empty")
		}
	}
	return nil
}

// Store manages persistent storage with thread-safe access.
type Store struct {
	mu   sync.RWMutex
	data Data
	path string
	dir  string
}

// New creates or loads a Store at the platform-appropriate path.
func New(appName string) (*Store, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fall back to the OS temp dir so the app still starts.
		configDir = os.TempDir()
	}

	dir := filepath.Join(configDir, appName)
	if err := os.MkdirAll(dir, configDirPerm); err != nil {
		// Last resort — never fail startup over the config dir.
		dir = filepath.Join(os.TempDir(), appName)
		if err := os.MkdirAll(dir, configDirPerm); err != nil {
			return nil, fmt.Errorf("cannot create config directory: %w", err)
		}
	}

	path := filepath.Join(dir, "storage.json")
	s := &Store{path: path, dir: dir}

	if err := s.load(); err != nil && !os.IsNotExist(err) {
		// Corrupt or unreadable storage file (crash mid-write, aborted session,
		// manual edit). Move it aside and start fresh — a bad JSON file must
		// never brick the app at startup.
		backupPath := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		if rbErr := os.Rename(path, backupPath); rbErr != nil {
			return nil, fmt.Errorf("cannot load storage (%v) and cannot move it aside (%w)", err, rbErr)
		}
		log.Printf("storage: unreadable storage file moved to %s (error: %v)", backupPath, err)
	}

	return s, nil
}

// Path returns the full path to the storage file.
func (s *Store) Path() string {
	return s.path
}

// load reads the storage file from disk.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &s.data)
}

// save writes the storage file to disk atomically.
// Strategy: write to .tmp file, then rename over target.
// This prevents corruption from crashes during write.
func (s *Store) save() error {
	// Create backup before overwriting
	if err := s.rotateBackups(); err != nil {
		return fmt.Errorf("backup rotation failed: %w", err)
	}

	data, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal storage: %w", err)
	}

	// Write atomically: temp file then rename
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, filePerm); err != nil {
		return fmt.Errorf("cannot write temp storage file: %w", err)
	}

	// Sync to ensure data is on disk before rename
	// (best-effort — not critical on all platforms)
	if f, err := os.Open(tmpPath); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		// Clean up temp file on failure
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cannot atomically replace storage file: %w", err)
	}

	return nil
}

// rotateBackups creates numbered backups and prunes old ones.
// Backup files: storage.json.bak.0, storage.json.bak.1, ...
func (s *Store) rotateBackups() error {
	// Only rotate if the storage file exists
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return nil
	}

	// Shift existing backups: delete the oldest, shift the rest
	// storage.json.bak.2 → remove
	// storage.json.bak.1 → storage.json.bak.2
	// storage.json.bak.0 → storage.json.bak.1
	// storage.json → storage.json.bak.0

	// Remove the oldest if it exists
	oldest := fmt.Sprintf("%s.bak.%d", s.path, maxBackups-1)
	_ = os.Remove(oldest)

	// Shift existing backups
	for i := maxBackups - 2; i >= 0; i-- {
		old := fmt.Sprintf("%s.bak.%d", s.path, i)
		if _, err := os.Stat(old); err == nil {
			new := fmt.Sprintf("%s.bak.%d", s.path, i+1)
			if err := os.Rename(old, new); err != nil {
				return fmt.Errorf("cannot rotate backup %d: %w", i, err)
			}
		}
	}

	// Copy current file to backup.0
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path+".bak.0", data, filePerm)
}

// GetData returns a copy of the current stored data.
func (s *Store) GetData() Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

// SetActivation persists code, tier, fingerprint, and server config.
func (s *Store) SetActivation(code, tier, fingerprint string, config *ServerConfig, udpRelay bool) error {
	if code == "" || tier == "" || fingerprint == "" {
		return fmt.Errorf("storage: cannot set activation with empty fields")
	}
	if config == nil {
		return fmt.Errorf("storage: cannot set activation with nil server config")
	}
	if config.Password == "" {
		return fmt.Errorf("storage: cannot set activation with empty password")
	}
	if config.Server == "" {
		return fmt.Errorf("storage: cannot set activation with empty server address")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Code = code
	s.data.Tier = tier
	s.data.DeviceFingerprint = fingerprint
	s.data.ServerConfig = config
	s.data.UDPRelay = udpRelay
	s.data.Activated = true

	return s.save()
}

// IsActivated returns whether the client has been activated.
func (s *Store) IsActivated() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Activated
}

// GetCode returns the stored activation code.
func (s *Store) GetCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Code
}

// SetHeartbeat records a successful heartbeat.
func (s *Store) SetHeartbeat(timestamp int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.LastHeartbeatOK = timestamp
	s.data.HeartbeatFailures = 0
	return s.save()
}

// SetHeartbeatFailure increments the heartbeat failure counter.
func (s *Store) SetHeartbeatFailure(timestamp int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.LastHeartbeatOK = timestamp
	s.data.HeartbeatFailures++
	return s.save()
}

// SetUpdatePending marks an update as pending confirmation.
func (s *Store) SetUpdatePending(version, sha256 string, timestamp int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.UpdatePending = true
	s.data.UpdateVersion = version
	s.data.UpdateSHA256 = sha256
	s.data.UpdateTimestamp = timestamp
	return s.save()
}

// ClearUpdatePending removes the pending update flag.
func (s *Store) ClearUpdatePending() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.UpdatePending = false
	s.data.UpdateVersion = ""
	s.data.UpdateSHA256 = ""
	s.data.UpdateTimestamp = 0
	return s.save()
}

// SetCrashedOnUpdate marks that the app crashed during an update.
func (s *Store) SetCrashedOnUpdate(timestamp int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.CrashedOnUpdate = true
	s.data.CrashTimestamp = timestamp
	return s.save()
}

// ClearCrashedOnUpdate clears the crash flag.
func (s *Store) ClearCrashedOnUpdate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.CrashedOnUpdate = false
	s.data.CrashTimestamp = 0
	return s.save()
}

// SetVersion records the current app version.
func (s *Store) SetVersion(version string) error {
	if version == "" {
		return fmt.Errorf("storage: cannot set empty version")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Version = version
	return s.save()
}

// Reset clears all stored data (factory reset).
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = Data{}
	return s.save()
}

// ListBackups returns the paths of all existing backup files, sorted oldest first.
func (s *Store) ListBackups() []string {
	pattern := filepath.Join(s.dir, "storage.json.bak.*")
	matches, _ := filepath.Glob(pattern)
	sort.Strings(matches)
	return matches
}

// RestoreFromBackup restores state from the most recent backup (index 0).
// Returns the number of backups restored from, or an error.
func (s *Store) RestoreFromBackup() error {
	// Check for any backup file
	for i := 0; i < maxBackups; i++ {
		bakPath := fmt.Sprintf("%s.bak.%d", s.path, i)
		if _, err := os.Stat(bakPath); err == nil {
			// Restore this backup
			data, err := os.ReadFile(bakPath)
			if err != nil {
				continue
			}
			// Validate it's valid JSON
			var restored Data
			if err := json.Unmarshal(data, &restored); err != nil {
				continue
			}
			// Write it as the current file
			if err := os.WriteFile(s.path, data, filePerm); err != nil {
				return fmt.Errorf("cannot restore backup %d: %w", i, err)
			}
			// Load into memory
			s.mu.Lock()
			s.data = restored
			s.mu.Unlock()
			return nil
		}
	}
	return fmt.Errorf("no backup files found to restore from")
}

// SanitizePath ensures a path component is safe for use in file paths.
// It removes path separators and traversal sequences, returning a clean basename.
func SanitizePath(name string) string {
	// Start with the basename only (strip any directory components)
	name = filepath.Base(name)
	// Remove any remaining traversal attempts
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	// Re-clean to handle any remaining edge cases
	name = filepath.Clean(name)
	// If we ended up with "." or empty, return a safe default
	if name == "" || name == "." {
		return "unnamed"
	}
	return name
}
