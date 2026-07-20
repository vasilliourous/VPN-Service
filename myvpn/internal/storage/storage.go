package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type AppData struct {
	Token             string            `json:"token"`
	DeviceFingerprint string            `json:"device_fingerprint"`
	PlanTier          string            `json:"plan_tier"`
	Protocols         []ProtocolConfig  `json:"protocols"`
	ActiveConn        string            `json:"active_conn"`
	TelemetryOptOut   bool              `json:"telemetry_opt_out"`
	EngineCache       map[string]string `json:"engine_cache"`
}

type ProtocolConfig struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"display_name"`
	BinaryName  string          `json:"binary_name"`
	ConfigJSON  json.RawMessage `json:"config_json"`
	Weight      int             `json:"weight"`
}

type Storage struct {
	mu      sync.RWMutex
	dataDir string
	data    *AppData
}

var (
	instance *Storage
	once     sync.Once
)

func appDataDir() string {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "Library", "Application Support")
	default:
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "MyVPN")
}

func Init() {
	once.Do(func() {
		dir := appDataDir()
		os.MkdirAll(dir, 0700)
		os.MkdirAll(filepath.Join(dir, "logs"), 0700)
		os.MkdirAll(filepath.Join(dir, "engines"), 0700)

		inst := &Storage{dataDir: dir}
		inst.load()
		instance = inst
		go inst.logRotationLoop()
	})
}

func (s *Storage) load() {
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "data.json"))
	if err != nil {
		s.data = &AppData{EngineCache: make(map[string]string)}
		s.save()
		return
	}
	s.data = &AppData{}
	json.Unmarshal(raw, s.data)
	if s.data.EngineCache == nil {
		s.data.EngineCache = make(map[string]string)
	}
}

func (s *Storage) save() {
	raw, _ := json.MarshalIndent(s.data, "", "  ")
	os.WriteFile(filepath.Join(s.dataDir, "data.json"), raw, 0600)
}

func (s *Storage) logRotationLoop() {
	const maxSize = 10 * 1024 * 1024 // 10MB
	const maxFiles = 3
	for {
		time.Sleep(5 * time.Minute)
		logDir := filepath.Join(s.dataDir, "logs")
		entries, _ := os.ReadDir(logDir)
		for _, entry := range entries {
			if entry.IsDir() || !stringsHasPrefix(entry.Name(), "engine-") {
				continue
			}
			path := filepath.Join(logDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Size() > maxSize {
				for i := maxFiles; i >= 0; i-- {
					oldName := path
					if i > 0 {
						oldName = fmt.Sprintf("%s.%d", path, i)
					}
					newName := fmt.Sprintf("%s.%d", path, i+1)
					if _, err := os.Stat(oldName); err == nil {
						if i == maxFiles {
							os.Remove(oldName)
						} else {
							os.Rename(oldName, newName)
						}
					}
				}
			}
		}
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func DataDir() string       { return instance.dataDir }
func LogDir() string        { return filepath.Join(instance.dataDir, "logs") }
func EngineDir() string     { return filepath.Join(instance.dataDir, "engines") }

func IsActivated() bool {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.Token != ""
}

func SaveActivation(token, planTier string) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.Token = token
	instance.data.PlanTier = planTier
	instance.save()
}

func LoadToken() string {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.Token
}

func SaveToken(token string) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.Token = token
	instance.save()
}

func LoadPlanTier() string {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.PlanTier
}

func SaveDeviceFingerprint(fp string) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.DeviceFingerprint = fp
	instance.save()
}

func LoadDeviceFingerprint() string {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.DeviceFingerprint
}

func GetProtocols() []ProtocolConfig {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	out := make([]ProtocolConfig, len(instance.data.Protocols))
	copy(out, instance.data.Protocols)
	return out
}

func SetProtocols(protos []ProtocolConfig) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.Protocols = protos
	instance.save()
}

func GetCachedEnginePath(protocolID string) string {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.EngineCache[protocolID]
}

func SetCachedEnginePath(protocolID, path string) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.EngineCache[protocolID] = path
	instance.save()
}

func GetTelemetryOptOut() bool {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.TelemetryOptOut
}

func SetTelemetryOptOut(optOut bool) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.TelemetryOptOut = optOut
	instance.save()
}

func GetActiveConn() string {
	instance.mu.RLock()
	defer instance.mu.RUnlock()
	return instance.data.ActiveConn
}

func SetActiveConn(conn string) {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.data.ActiveConn = conn
	instance.save()
}
