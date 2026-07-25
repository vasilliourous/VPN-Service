package gui

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"myvpn/internal/storage"
)

// Diagnostics collects system and application state for support.
type Diagnostics struct {
	Version        string `json:"version"`
	GoVersion      string `json:"go_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Activated      bool   `json:"activated"`
	Connected      bool   `json:"connected"`
	Tier           string `json:"tier,omitempty"`
	Uptime         string `json:"uptime,omitempty"`
	UpdatePending  bool   `json:"update_pending"`
	StorageExists  bool   `json:"storage_exists"`
	SingBoxExists  bool   `json:"sing_box_exists"`
	FingerprintSet bool   `json:"fingerprint_set"`
	ReportTime     string `json:"report_time"`
}

// collectDiagnostics gathers diagnostic information.
func collectDiagnostics(store *storage.Store, version string, connected bool) string {
	data := store.GetData()

	d := Diagnostics{
		Version:        version,
		GoVersion:      runtime.Version(),
		OS:             runtime.GOOS + "/" + runtime.GOARCH,
		Arch:           runtime.GOARCH,
		Activated:      data.Activated,
		Connected:      connected,
		Tier:           data.Tier,
		UpdatePending:  data.UpdatePending,
		FingerprintSet: data.DeviceFingerprint != "",
		ReportTime:     time.Now().UTC().Format(time.RFC3339),
	}

	// Check storage exists
	configDir, _ := os.UserConfigDir()
	storagePath := configDir + "/myvpn/storage.json"
	d.StorageExists = fileExists(storagePath)

	// Check sing-box exists
	d.SingBoxExists = commandExists("sing-box")

	// Build report
	report := fmt.Sprintf(`MyVPN Diagnostics Report
═══════════════════════════════════════
Time:      %s
Version:   %s
Go:        %s
OS:        %s

Status:
  Activated:      %v
  Connected:      %v
  Tier:           %s
  Update Pending: %v

Files:
  Storage:        %v
  sing-box:       %v

Config:
  Fingerprint:    %v
  Server:         %s
═══════════════════════════════════════`,
		d.ReportTime,
		d.Version,
		d.GoVersion,
		d.OS,
		d.Activated,
		d.Connected,
		d.Tier,
		d.UpdatePending,
		d.StorageExists,
		d.SingBoxExists,
		d.FingerprintSet,
		data.ServerConfig.Server,
	)

	return report
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func commandExists(name string) bool {
	_, err := os.Stat(name)
	if err == nil {
		return true
	}
	_, err = os.Stat("/usr/bin/" + name)
	return err == nil
}
