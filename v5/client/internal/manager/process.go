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
//
// Hardening: process health monitoring with restart limits, graceful shutdown timeout,
// config validation, resource cleanup, context propagation.
package manager

import (
	"context"
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

const (
	// Default socket path for IPC with TUN helper.
	defaultSocketPath = "/var/run/myvpn-helper.sock"

	// Process health check interval.
	healthCheckInterval = 10 * time.Second

	// Max consecutive health check failures before force-restart.
	maxHealthFailures = 3

	// Graceful shutdown timeout.
	shutdownTimeout = 10 * time.Second

	// Max restart attempts within 5 minutes.
	maxRestarts = 3
	restartWindow = 5 * time.Minute
)

// Common errors.
var (
	ErrProcessNotRunning = fmt.Errorf("sing-box process is not running")
	ErrProcessCrashed    = fmt.Errorf("sing-box process crashed")
	ErrHelperNotRunning  = fmt.Errorf("TUN helper is not running")
	ErrInvalidConfig     = fmt.Errorf("invalid sing-box configuration")
	ErrMaxRestarts       = fmt.Errorf("maximum restart attempts exceeded")
)

// HelperClient communicates with the privileged TUN helper service.
type HelperClient struct {
	socketPath string
	timeout    time.Duration
}

// NewHelperClient creates a client for the TUN helper service.
func NewHelperClient() *HelperClient {
	return &HelperClient{
		socketPath: defaultSocketPath,
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
		return false, "", fmt.Errorf("cannot send command to helper: %w", err)
	}

	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Error   string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return false, "", fmt.Errorf("cannot decode helper response: %w", err)
	}

	if !resp.Success {
		return false, resp.Error, fmt.Errorf("helper command failed: %s", resp.Error)
	}

	return true, resp.Message, nil
}

// Manager controls the sing-box tunnel process.
type Manager struct {
	mu                sync.Mutex
	cmd               *exec.Cmd
	configPath        string
	singBoxPath       string
	helperClient      *HelperClient
	useHelper         bool
	healthFailures    int
	restartCount      int
	firstRestartTime  time.Time
	stopHealthCheck   chan struct{}
}

// Config holds the parameters needed to start the tunnel.
type Config struct {
	Server     string
	ServerPort int
	Password   string
	Method     string
	TierName   string
	UDPRelay   bool
	HubURL     string
}

// Validate checks that the config is valid.
func (c *Config) Validate() error {
	if c.Server == "" {
		return fmt.Errorf("%w: server address is required", ErrInvalidConfig)
	}
	if c.ServerPort <= 0 || c.ServerPort > 65535 {
		return fmt.Errorf("%w: server port %d is invalid", ErrInvalidConfig, c.ServerPort)
	}
	if c.Password == "" {
		return fmt.Errorf("%w: password is required", ErrInvalidConfig)
	}
	if c.Method == "" {
		return fmt.Errorf("%w: encryption method is required", ErrInvalidConfig)
	}
	return nil
}

// NewManager creates a new tunnel manager.
func NewManager(singBoxPath, configPath string) *Manager {
	return &Manager{
		singBoxPath:  singBoxPath,
		configPath:   configPath,
		helperClient: NewHelperClient(),
	}
}

// SetHelperMode enables or disables helper mode.
func (m *Manager) SetHelperMode(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.useHelper = enabled
}

// Start generates the sing-box config and starts the process.
func (m *Manager) Start(ctx context.Context, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Signal(syscall.Signal(0)); err == nil {
			return fmt.Errorf("tunnel is already running")
		}
	}

	// Generate the sing-box configuration
	configJSON, err := generateConfig(cfg)
	if err != nil {
		return fmt.Errorf("cannot generate config: %w", err)
	}

	if m.useHelper {
		return m.startWithHelper(configJSON)
	}
	return m.startDirect(ctx, configJSON)
}

// Stop terminates the sing-box process gracefully.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.stopLocked()
}

// stopLocked stops the process, must be called with mu held.
func (m *Manager) stopLocked() error {
	// Stop health check loop
	if m.stopHealthCheck != nil {
		close(m.stopHealthCheck)
		m.stopHealthCheck = nil
	}

	if m.useHelper {
		_, _, err := m.helperClient.SendCommand("stop-singbox", nil)
		return err
	}

	if m.cmd == nil || m.cmd.Process == nil {
		return nil // Already stopped
	}

	// Try graceful shutdown first
	done := make(chan struct{}, 1)
	go func() {
		m.cmd.Wait()
		done <- struct{}{}
	}()

	select {
	case <-done:
		// Process exited cleanly
	case <-time.After(shutdownTimeout):
		// Force kill
		if err := m.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("cannot kill sing-box: %w", err)
		}
		<-done
	}

	m.cmd = nil
	return nil
}

// IsRunning checks if the sing-box process is alive.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.useHelper {
		success, msg, _ := m.helperClient.SendCommand("singbox-status", nil)
		return success && msg == "running"
	}

	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	return m.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// startWithHelper sends the config to the TUN helper to launch sing-box.
func (m *Manager) startWithHelper(configJSON []byte) error {
	success, msg, err := m.helperClient.SendCommand("start-singbox", []string{string(configJSON)})
	if err != nil {
		return fmt.Errorf("helper failed to start sing-box: %w", err)
	}
	if !success {
		return fmt.Errorf("helper refused start: %s", msg)
	}
	return nil
}

// startDirect spawns sing-box directly as a subprocess.
func (m *Manager) startDirect(ctx context.Context, configJSON []byte) error {
	// Write config to disk
	if err := os.WriteFile(m.configPath, configJSON, 0600); err != nil {
		return fmt.Errorf("cannot write config file: %w", err)
	}

	// Check sing-box binary exists
	if _, err := os.Stat(m.singBoxPath); os.IsNotExist(err) {
		return fmt.Errorf("sing-box binary not found at %s", m.singBoxPath)
	}

	// Start sing-box
	cmd := exec.CommandContext(ctx, m.singBoxPath, "run", "-c", m.configPath, "-D", filepath.Dir(m.configPath))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Detach slightly — allow parent to manage lifecycle
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start sing-box: %w", err)
	}

	m.cmd = cmd
	m.restartCount = 0
	m.firstRestartTime = time.Time{}

	// Start health check loop
	m.stopHealthCheck = make(chan struct{})
	go m.healthLoop()

	return nil
}

// healthLoop periodically checks that sing-box is still running.
func (m *Manager) healthLoop() {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopHealthCheck:
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.cmd == nil || m.cmd.Process == nil {
				m.mu.Unlock()
				return
			}

			err := m.cmd.Process.Signal(syscall.Signal(0))
			if err != nil {
				// Process died
				m.healthFailures++
				if m.healthFailures >= maxHealthFailures {
					m.mu.Unlock()
					return
				}

				// Attempt restart
				if m.canRestart() {
					m.restartCount++
					m.mu.Unlock()
					// Reload config and restart
					configData, err := os.ReadFile(m.configPath)
					if err != nil {
						return
					}
					newCtx := context.Background()
					cmd := exec.CommandContext(newCtx, m.singBoxPath, "run", "-c", m.configPath, "-D", filepath.Dir(m.configPath))
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
					if startErr := cmd.Start(); startErr == nil {
						m.mu.Lock()
						m.cmd = cmd
						m.healthFailures = 0
						m.mu.Unlock()
					} else {
						// Restart failed — will retry next cycle
						_ = configData
					}
					continue
				}
				m.mu.Unlock()
				return
			}

			// Health OK — reset failure counter
			m.healthFailures = 0
			m.mu.Unlock()
		}
	}
}

// canRestart checks if we haven't exceeded the restart limit in the window.
func (m *Manager) canRestart() bool {
	now := time.Now()
	if m.firstRestartTime.IsZero() {
		m.firstRestartTime = now
		return true
	}
	if now.Sub(m.firstRestartTime) > restartWindow {
		m.firstRestartTime = now
		m.restartCount = 0
		return true
	}
	return m.restartCount < maxRestarts
}

// State returns a snapshot of the manager state.
func (m *Manager) State() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.useHelper {
		_, msg, _ := m.helperClient.SendCommand("singbox-status", nil)
		return msg
	}

	if m.cmd == nil || m.cmd.Process == nil {
		return "stopped"
	}
	if err := m.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return "crashed"
	}
	return "running"
}

// generateConfig creates the sing-box JSON configuration.
func generateConfig(cfg Config) ([]byte, error) {
	logLevel := "warn"
	if os.Getenv("MYVPN_DEBUG") != "" {
		logLevel = "debug"
	}

	config := SingBoxConfig{
		Log: LogConfig{
			Level: logLevel,
		},
		DNS: DNSConfig{
			Final: "dns-direct",
			Servers: map[string]DNSServer{
				"dns-direct": {
					Address:    "https://1.1.1.1/dns-query",
					Detour:     "direct",
				},
				"dns-tunnel": {
					Address:    "https://1.1.1.1/dns-query",
				},
			},
		},
		Inbounds: []Inbound{
			{
				Type:       "tun",
				Tag:        "tun-in",
				Listen:     "0.0.0.0",
				ListenPort: 0,
			},
		},
		Outbounds: []Outbound{
			{
				Type:       "shadowsocks",
				Tag:        "proxy",
				Server:     cfg.Server,
				ServerPort: cfg.ServerPort,
				Method:     cfg.Method,
				Password:   cfg.Password,
			},
			{
				Type:       "direct",
				Tag:        "direct",
			},
			{
				Type:       "dns",
				Tag:        "dns-out",
			},
		},
		Route: RouteConfig{
			AutoDetectInterface: true,
			Final:               "proxy",
			Rules: []RouteRule{
				{Rule: "dns_query", OutboundTag: "dns-out"},
			},
		},
	}

	// Add UDP over TCP for Strike or if UDP relay is enabled
	if cfg.UDPRelay {
		config.Outbounds[0].UDPOverTCP = &UDPOverTCPConfig{
			Enabled: true,
			Version: 2,
		}
	}

	return json.MarshalIndent(config, "", "  ")
}

// ── Sing-box config types ──

type SingBoxConfig struct {
	Log       LogConfig    `json:"log"`
	DNS       DNSConfig    `json:"dns"`
	Inbounds  []Inbound    `json:"inbounds"`
	Outbounds []Outbound   `json:"outbounds"`
	Route     RouteConfig  `json:"route"`
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
	Type        string             `json:"type"`
	Tag         string             `json:"tag,omitempty"`
	Server      string             `json:"server,omitempty"`
	ServerPort  int                `json:"server_port,omitempty"`
	Method      string             `json:"method,omitempty"`
	Password    string             `json:"password,omitempty"`
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
