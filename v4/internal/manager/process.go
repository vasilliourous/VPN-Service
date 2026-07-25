// Package manager manages the sing-box tunnel process lifecycle.
//
// It handles:
//   - Generating sing-box JSON configuration from server parameters
//   - Starting and stopping the sing-box process
//   - Monitoring process health
//   - Graceful shutdown
//
// The manager can operate in two modes:
//  1. Direct mode (default): spawns sing-box directly from the app process
//  2. Helper mode: sends config to the privileged TUN helper via IPC
//     Helper mode is preferred when available (sing-box needs root for TUN).
package manager

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// HelperClient communicates with the privileged TUN helper service.
type HelperClient struct {
	socketPath string
	timeout    time.Duration
}

// NewHelperClient creates a client for the TUN helper service.
func NewHelperClient() *HelperClient {
	path := "/var/run/myvpn-helper.sock"
	return &HelperClient{
		socketPath: path,
		timeout:    30 * time.Second,
	}
}

// SendCommand sends an IPC command to the helper and returns the response.
func (hc *HelperClient) SendCommand(action string, args []string) (bool, string, error) {
	conn, err := net.DialTimeout("unix", hc.socketPath, hc.timeout)
	if err != nil {
		return false, "", fmt.Errorf("cannot connect to helper: %w", err)
	}
	defer conn.Close()

	cmd := map[string]interface{}{
		"action": action,
		"args":   args,
	}

	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return false, "", fmt.Errorf("cannot send command: %w", err)
	}

	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
		Error   string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return false, "", fmt.Errorf("cannot decode response: %w", err)
	}

	if !resp.Success {
		return false, resp.Error, nil
	}

	return true, resp.Message, nil
}

// IsHelperRunning checks if the helper service is available.
func (hc *HelperClient) IsHelperRunning() bool {
	ok, _, _ := hc.SendCommand("ping", nil)
	return ok
}

// StartSingBoxViaHelper sends a config JSON to the helper to start sing-box.
func (hc *HelperClient) StartSingBoxViaHelper(configJSON string) error {
	ok, msg, err := hc.SendCommand("start-singbox", []string{configJSON})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("helper rejected start-singbox: %s", msg)
	}
	return nil
}

// StopSingBoxViaHelper tells the helper to stop sing-box.
func (hc *HelperClient) StopSingBoxViaHelper() error {
	ok, msg, err := hc.SendCommand("stop-singbox", nil)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("helper rejected stop-singbox: %s", msg)
	}
	return nil
}

// EngineConfig holds the parameters needed to configure sing-box.
type EngineConfig struct {
	ServerAddr string
	ServerPort int
	Password   string
	Method     string
	Tier       string
	UDPRelay   bool
	LocalPort  int // SOCKS5 local port (default: 1080)
}

// Manager controls the sing-box tunnel process.
// Supports both direct spawning and helper-mediated operation.
type Manager struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	configPath string
	binaryPath string
	config     *EngineConfig
	running    bool
	stopCh     chan struct{}
	helper     *HelperClient // non-nil when helper is available
}

// New creates a new Manager.
// It automatically detects if the TUN helper service is available
// and uses it for sing-box management when present.
func New(binaryPath string) (*Manager, error) {
	// Verify the sing-box binary exists
	if _, err := os.Stat(binaryPath); err != nil {
		return nil, fmt.Errorf("sing-box binary not found at %s: %w", binaryPath, err)
	}

	m := &Manager{
		binaryPath: binaryPath,
		stopCh:     make(chan struct{}),
	}

	// Check for helper service
	helper := NewHelperClient()
	if helper.IsHelperRunning() {
		m.helper = helper
	}

	return m, nil
}

// Start generates config and launches sing-box.
// When the TUN helper is available, it delegates to the helper.
// Otherwise, it spawns sing-box directly (may lack TUN capabilities).
func (m *Manager) Start(config *EngineConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("tunnel is already running")
	}

	m.config = config

	// Generate JSON config
	cfg, err := m.generateConfig(config)
	if err != nil {
		return fmt.Errorf("cannot generate config: %w", err)
	}

	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, cfgData, 0600); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("cannot write config: %w", err)
	}

	m.configPath = configPath

	// Try helper mode first (TUN helper has root for sing-box TUN mode)
	if m.helper != nil {
		if err := m.helper.StartSingBoxViaHelper(string(cfgData)); err != nil {
			// Helper failed — fall through to direct spawning
			m.helper = nil // Don't try again
		} else {
			m.running = true
			// In helper mode, we don't monitor directly.
			// The helper manages sing-box lifecycle.
			return nil
		}
	}

	// Direct mode: spawn sing-box from this process
	// Note: TUN mode requires root. If sing-box is configured for TUN,
	// this will fail without root. Use helper mode in production.
	args := []string{"run", "-c", configPath, "-D", tmpDir}
	cmd := exec.Command(m.binaryPath, args...)

	// Set process group so we can kill children
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture stdout/stderr for diagnostics
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("cannot start sing-box: %w", err)
	}

	m.cmd = cmd
	m.running = true

	// Monitor in background
	go m.monitor(cmd, tmpDir)

	return nil
}

// Stop terminates the sing-box process gracefully.
// Uses helper when available, otherwise kills the direct process.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	// Try helper mode first
	if m.helper != nil {
		if err := m.helper.StopSingBoxViaHelper(); err == nil {
			m.running = false
			return nil
		}
		// Helper failed — fall through to direct kill
	}

	// Direct mode: kill the process we spawned
	if m.cmd != nil && m.cmd.Process != nil {
		// Try SIGTERM first
		syscall.Kill(-m.cmd.Process.Pid, syscall.SIGTERM)

		// Wait with timeout
		done := make(chan struct{})
		go func() {
			m.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Process exited cleanly
		case <-time.After(5 * time.Second):
			// Force kill
			syscall.Kill(-m.cmd.Process.Pid, syscall.SIGKILL)
		}
	}

	// Cleanup temp directory
	if m.configPath != "" {
		os.RemoveAll(filepath.Dir(m.configPath))
	}

	m.running = false
	m.configPath = ""

	return nil
}

// IsRunning returns whether the tunnel process is active.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// monitor watches the process and cleans up on exit.
func (m *Manager) monitor(cmd *exec.Cmd, tmpDir string) {
	cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Only cleanup if this is still our current process
	if m.cmd == cmd {
		m.running = false
		os.RemoveAll(tmpDir)
		m.configPath = ""
	}
}

// generateConfig creates a sing-box JSON configuration.
func (m *Manager) generateConfig(config *EngineConfig) (*SingBoxConfig, error) {
	if config.LocalPort == 0 {
		config.LocalPort = 1080
	}

	outbound := Outbound{
		Type: "shadowsocks",
		Tag:  config.Tier,
		Server: config.ServerAddr,
		ServerPort: config.ServerPort,
		Method:     config.Method,
		Password:   config.Password,
	}

	if config.UDPRelay {
		outbound.UDPOverTCP = &UDPOverTCPConfig{
			Enabled: true,
			Version: 2,
		}
	}

	cfg := &SingBoxConfig{
		Log: &LogConfig{
			Level: "warn",
			Output: "var/log/sing-box.log",
		},
		DNS: &DNSConfig{
			Final: "dns-direct",
			Rules: []DNSRule{
				{
					Rule:    "geosite:category-ads-all",
					Action:  "route",
					Server:  "block",
				},
				{
					Rule:   "geosite:cn",
					Action: "route",
					Server: "dns-direct",
				},
			},
			Servers: map[string]DNSServer{
				"dns-direct": {
					Address:    "https://1.1.1.1/dns-query",
					Detour:     "direct",
				},
				"dns-remote": {
					Address:    "https://1.1.1.1/dns-query",
				},
				"block": {
					Address:    "rcode://success",
				},
			},
		},
		Inbounds: []Inbound{
			{
				Type:       "socks",
				Tag:        "socks-in",
				Listen:     "127.0.0.1",
				ListenPort: config.LocalPort,
			},
			{
				Type:       "mixed",
				Tag:        "mixed-in",
				Listen:     "127.0.0.1",
				ListenPort: config.LocalPort + 1,
			},
		},
		Outbounds: []Outbound{
			outbound,
			{
				Type: "direct",
				Tag:  "direct",
			},
			{
				Type: "block",
				Tag:  "block",
			},
		},
		Route: &RouteConfig{
			Rules: []RouteRule{
				{
					Rule:        "geosite:category-ads-all",
					OutboundTag: "block",
				},
			},
			AutoDetectInterface: true,
			Final:               config.Tier,
		},
	}

	return cfg, nil
}

// SingBoxConfig represents a sing-box configuration file.
// See https://sing-box.sagernet.org/configuration/
type SingBoxConfig struct {
	Log       *LogConfig       `json:"log,omitempty"`
	DNS       *DNSConfig       `json:"dns,omitempty"`
	Inbounds  []Inbound        `json:"inbounds"`
	Outbounds []Outbound       `json:"outbounds"`
	Route     *RouteConfig     `json:"route,omitempty"`
}

type LogConfig struct {
	Level  string `json:"level"`
	Output string `json:"output,omitempty"`
}

type DNSConfig struct {
	Final   string              `json:"final"`
	Rules   []DNSRule           `json:"rules,omitempty"`
	Servers map[string]DNSServer `json:"servers"`
}

type DNSRule struct {
	Rule   string `json:"rule"`
	Action string `json:"action"`
	Server string `json:"server,omitempty"`
}

type DNSServer struct {
	Address    string `json:"address"`
	Detour     string `json:"detour,omitempty"`
}

type Inbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag,omitempty"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type Outbound struct {
	Type        string              `json:"type"`
	Tag         string              `json:"tag,omitempty"`
	Server      string              `json:"server,omitempty"`
	ServerPort  int                 `json:"server_port,omitempty"`
	Method      string              `json:"method,omitempty"`
	Password    string              `json:"password,omitempty"`
	UDPOverTCP  *UDPOverTCPConfig  `json:"udp_over_tcp,omitempty"`
}

type UDPOverTCPConfig struct {
	Enabled bool `json:"enabled"`
	Version int  `json:"version,omitempty"`
}

type RouteConfig struct {
	Rules               []RouteRule `json:"rules,omitempty"`
	AutoDetectInterface bool        `json:"auto_detect_interface"`
	Final               string      `json:"final"`
}

type RouteRule struct {
	Rule        string `json:"rule"`
	OutboundTag string `json:"outbound_tag"`
}
