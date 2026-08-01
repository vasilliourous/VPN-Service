// Package manager manages the sing-box tunnel process lifecycle.
//
// It handles:
//   - Generating sing-box JSON configuration from server parameters
//   - Starting and stopping the sing-box process
//   - Monitoring process health
//   - Graceful shutdown
//
// The manager can operate in two modes:
//  1. Direct mode: spawns sing-box directly from the app process.
//     This is the mode used by the Wails app on ALL platforms — app.go calls
//     SetHelperMode(false) after construction.
//  2. Helper mode: sends config to the privileged TUN helper via IPC.
//     Legacy — the myvpn-helper binary is no longer shipped (see v5/legacy).
//     NewManager still defaults to helper mode on Windows, so the app must
//     explicitly disable it.
//
// Hardening: process health monitoring with restart limits, graceful shutdown timeout,
// config validation, resource cleanup, context propagation.
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// Default socket path for IPC with TUN helper.
	defaultSocketPath = "/var/run/myvpn-helper.sock"
	// Windows named pipe path for TUN helper IPC.
	windowsPipePath = `\\.\pipe\MyVPNHelper`

	// Process health check interval.
	healthCheckInterval = 10 * time.Second

	// Max consecutive health check failures before force-restart.
	maxHealthFailures = 3

	// Graceful shutdown timeout.
	shutdownTimeout = 10 * time.Second

	// Max restart attempts within 5 minutes.
	maxRestarts   = 3
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

// boundedBuffer is a thread-safe writer that keeps only the last max bytes
// written — used to capture sing-box stderr without unbounded memory growth.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= b.max {
		b.buf = append([]byte(nil), p[len(p)-b.max:]...)
		return len(p), nil
	}
	if len(b.buf)+len(p) > b.max {
		b.buf = append(b.buf, p...)
		b.buf = b.buf[len(b.buf)-b.max:]
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// HelperClient communicates with the privileged TUN helper service.
type HelperClient struct {
	socketPath string
	timeout    time.Duration
}

// NewHelperClient creates a client for the TUN helper service.
func NewHelperClient() *HelperClient {
	path := defaultSocketPath
	// On Windows, the helper uses a named pipe instead of a Unix socket.
	if runtime.GOOS == "windows" {
		path = windowsPipePath
	}
	return &HelperClient{
		socketPath: path,
		timeout:    30 * time.Second,
	}
}

// helperNetwork returns the network type for IPC based on the platform.
func helperNetwork() string {
	if runtime.GOOS == "windows" {
		return "pipe"
	}
	return "unix"
}

// SendCommand sends an IPC command to the helper and returns the response.
func (hc *HelperClient) SendCommand(action string, args []string) (bool, string, error) {
	conn, err := net.DialTimeout(helperNetwork(), hc.socketPath, hc.timeout)
	if err != nil {
		return false, "", fmt.Errorf("cannot connect to helper: %w", err)
	}
	defer func() { _ = conn.Close() }()

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
	mu               sync.Mutex
	cmd              *exec.Cmd
	exited           chan struct{} // closed by the cmd.Wait goroutine when the process exits
	configPath       string
	singBoxPath      string
	helperPath       string
	helperClient     *HelperClient
	useHelper        bool
	healthFailures   int
	restartCount     int
	firstRestartTime time.Time
	stopHealthCheck  chan struct{}
}

// Config holds the parameters needed to start the tunnel.
type Config struct {
	Server        string
	ServerPort    int
	Password      string
	Method        string
	TierName      string
	UDPRelay      bool
	HubURL        string
	BindInterface string // physical NIC for the proxy dial (Windows TUN capture fix)
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
func NewManager(singBoxPath, configPath, helperPath string) *Manager {
	m := &Manager{
		singBoxPath:  singBoxPath,
		configPath:   configPath,
		helperPath:   helperPath,
		helperClient: NewHelperClient(),
	}
	// On Windows, default to helper mode since TUN requires admin privileges.
	if runtime.GOOS == "windows" {
		m.useHelper = true
	}
	return m
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
	if m.processAlive() {
		return fmt.Errorf("tunnel is already running")
	}

	// Generate the sing-box configuration
	configJSON, err := generateConfig(cfg)
	if err != nil {
		return fmt.Errorf("cannot generate config: %w", err)
	}

	if m.useHelper {
		// Try to use the helper — if it's not running, auto-start it
		if _, _, err := m.helperClient.SendCommand("ping", nil); err != nil {
			log.Println("TUN helper not running, attempting to start it...")
			if startErr := m.autoStartHelper(); startErr != nil {
				log.Printf("Cannot auto-start helper (%v), falling back to direct mode...", startErr)
				// Fall back to direct mode — disable helper so stopLocked
				// doesn't try IPC that will never work.
				m.useHelper = false
				return m.startDirect(ctx, configJSON)
			}
			// Wait a moment for the helper to start listening
			time.Sleep(2 * time.Second)
		}
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
		// Only try helper IPC if the helper is actually reachable.
		if ok, _, _ := m.helperClient.SendCommand("ping", nil); ok {
			_, _, err := m.helperClient.SendCommand("stop-singbox", nil)
			return err
		}
		// Helper not reachable — fall through to clean up any direct-mode process
	}

	if m.cmd == nil || m.cmd.Process == nil {
		return nil // Already stopped
	}

	// Graceful shutdown: wait for the exit goroutine (started in startDirect)
	// to finish. cmd.Wait must only be called ONCE per process — spawning a
	// second Wait here would error out immediately and skip the kill.
	exited := m.exited
	if exited == nil {
		exited = make(chan struct{})
		go func() { _ = m.cmd.Wait(); close(exited) }()
	}

	select {
	case <-exited:
		// Process exited cleanly
	case <-time.After(shutdownTimeout):
		// Force kill
		if err := m.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("cannot kill sing-box: %w", err)
		}
		<-exited
	}

	m.cmd = nil
	m.exited = nil

	// Clean up config file from disk
	if m.configPath != "" {
		_ = os.Remove(m.configPath)
	}

	return nil
}

// processAlive reports whether the current sing-box process is still running.
// Cross-platform: uses the exited channel (closed when the cmd.Wait goroutine
// returns) instead of Unix signals — Process.Signal(signal 0) is NOT supported
// on Windows and always errors, which previously made every liveness check
// report "dead" and caused Connect() to hang in cmd.Wait().
func (m *Manager) processAlive() bool {
	if m.cmd == nil || m.cmd.Process == nil || m.exited == nil {
		return false
	}
	select {
	case <-m.exited:
		return false
	default:
		return true
	}
}

// autoStartHelper attempts to start the myvpn-helper as an elevated process.
// On Windows, this uses "runas" to trigger a UAC elevation prompt.
// On Unix, it tries to start the helper via sudo if available.
func (m *Manager) autoStartHelper() error {
	if m.helperPath == "" {
		// Try to find helper in likely locations
		log.Println("myvpn-helper path not set, searching...")
		execDir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
		singDir := filepath.Dir(m.singBoxPath)
		candidates := []string{
			filepath.Join(execDir, "myvpn-helper"),
			filepath.Join(execDir, "myvpn-helper.exe"),
			filepath.Join(singDir, "myvpn-helper"),
			filepath.Join(singDir, "myvpn-helper.exe"),
			"./myvpn-helper",
			"./myvpn-helper.exe",
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				m.helperPath = p
				break
			}
		}
	}
	if m.helperPath == "" {
		return fmt.Errorf("myvpn-helper binary not found alongside sing-box")
	}

	switch runtime.GOOS {
	case "windows":
		// On Windows, use PowerShell Start-Process with RunAs verb to trigger UAC
		// This shows a UAC elevation prompt and starts the helper as administrator.
		// The PowerShell window itself is hidden so only the UAC prompt appears.
		cmd := exec.Command("powershell", "-Command",
			"Start-Process", "-FilePath", m.helperPath,
			"-Verb", "RunAs", "-WindowStyle", "Hidden")
		cmd.SysProcAttr = newProcAttr()
		return cmd.Start()
	case "linux", "darwin":
		// On Unix, try pkexec (PolKit) or sudo for elevation
		cmds := [][]string{
			{"pkexec", m.helperPath},
			{"sudo", "-n", m.helperPath},
		}
		var lastErr error
		for _, args := range cmds {
			if _, err := exec.LookPath(args[0]); err != nil {
				lastErr = err
				continue
			}
			cmd := exec.Command(args[0], args[1:]...)
			if err := cmd.Start(); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		return fmt.Errorf("cannot elevate helper: %w", lastErr)
	default:
		return fmt.Errorf("unsupported platform for helper auto-start")
	}
}

func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.useHelper {
		success, msg, _ := m.helperClient.SendCommand("singbox-status", nil)
		return success && msg == "running"
	}

	return m.processAlive()
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

	// Start sing-box. stderr is mirrored to our log AND captured in a bounded
	// buffer so startup failures can be reported back to the UI with the real
	// sing-box error (e.g. TUN "Access is denied" on non-elevated Windows).
	stderrBuf := &boundedBuffer{max: 8192}
	cmd := exec.CommandContext(ctx, m.singBoxPath, "run", "-c", m.configPath, "-D", filepath.Dir(m.configPath))
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)

	// Detach — allow parent to manage lifecycle (platform-specific)
	cmd.SysProcAttr = newProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start sing-box: %w", err)
	}

	m.cmd = cmd
	m.exited = make(chan struct{})
	exited := m.exited
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	m.restartCount = 0
	m.firstRestartTime = time.Time{}

	// Brief startup probe: wait a moment then check if the process is still
	// alive. This catches cases where sing-box exits immediately due to a
	// config error or permission denial (e.g. "Access is denied" on Windows).
	// The liveness check uses the exited channel — Process.Signal(0) does not
	// work on Windows and would block forever in cmd.Wait() below.
	select {
	case <-exited:
		// Process already exited — the Wait goroutine has released resources
		m.cmd = nil
		m.exited = nil
		detail := strings.TrimSpace(stderrBuf.String())
		if detail == "" {
			detail = "no error output"
		}
		if strings.Contains(detail, "Access is denied") {
			return fmt.Errorf("TUN interface creation was denied — run MyVPN as administrator: %s", detail)
		}
		return fmt.Errorf("sing-box exited immediately: %s", detail)
	case <-time.After(500 * time.Millisecond):
		// Still running — startup probe passed
	}

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

			if !m.processAlive() {
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
					if _, err := os.ReadFile(m.configPath); err != nil {
						return
					}
					newCtx := context.Background()
					cmd := exec.CommandContext(newCtx, m.singBoxPath, "run", "-c", m.configPath, "-D", filepath.Dir(m.configPath))
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					cmd.SysProcAttr = newProcAttr()
					if startErr := cmd.Start(); startErr == nil {
						exited := make(chan struct{})
						m.mu.Lock()
						m.cmd = cmd
						m.exited = exited
						m.healthFailures = 0
						m.mu.Unlock()
						go func() {
							_ = cmd.Wait()
							close(exited)
						}()
					}
					// Restart failed — will retry next cycle
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
	if !m.processAlive() {
		return "crashed"
	}
	return "running"
}

// generateConfig creates the sing-box JSON configuration.
// NOTE: strict_route is DISABLED — on Windows it installs WFP filters that
// "strictly block all connections not from the TUN", which on some machines
// (incl. the school laptops this app targets) also blocks sing-box's own
// outbound + DNS and kills all connectivity. auto_route alone still routes
// all traffic through the TUN (see FIXES.md).
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
			// DNS goes THROUGH the tunnel (final = dns-tunnel, detour = proxy).
			// sing-box 1.12 DNS server format (type + server + port).
			// Safeguards against DNS loops (see FIXES.md):
			//  1. The rule below sends ALL resolution initiated by sing-box's
			//     own outbounds (e.g. resolving the Shadowsocks SERVER domain
			//     networkingguides.duckdns.org) DIRECT — without it, that
			//     resolution re-enters dns-tunnel and sing-dns reports
			//     "DNS query loopback in transport[dns-tunnel]" (issue #2207).
			//  2. dns-tunnel MUST have an explicit detour (empty detour dials
			//     through the router and loops back into the DNS handler).
			Final: "dns-tunnel",
			Rules: []DNSRule{
				{Outbound: []string{"any"}, Server: "dns-direct"},
			},
			Servers: []DNSServer{
				{
					Type:       "https",
					Tag:        "dns-tunnel",
					Server:     "1.1.1.1",
					ServerPort: 443,
					Detour:     "proxy",
				},
				{
					Type:       "https",
					Tag:        "dns-direct",
					Server:     "1.1.1.1",
					ServerPort: 443,
					Detour:     "direct",
				},
			},
		},
		Inbounds: []Inbound{
			{
				Type:          "tun",
				Tag:           "tun-in",
				InterfaceName: "myvpn0",
				Address:       []string{"10.0.0.1/30"},
				MTU:           1500,
				AutoRoute:     true,
				StrictRoute:   false,
				Sniff:         true,
			},
		},
		Outbounds: []Outbound{
			{
				Type:          "shadowsocks",
				Tag:           "proxy",
				Server:        cfg.Server,
				ServerPort:    cfg.ServerPort,
				Method:        cfg.Method,
				Password:      cfg.Password,
				BindInterface: cfg.BindInterface,
			},
			{
				Type: "direct",
				Tag:  "direct",
			},
		},
		Route: RouteConfig{
			AutoDetectInterface: true,
			Final:               "proxy",
			Rules: []RouteRule{
				// DNS traffic is handled by the dns rule action (1.11+ format).
				{Protocol: "dns", Action: &RuleAction{Type: "dns"}},
			},
		},
	}

	// Exclude the VPN server itself from the tunnel: if auto_route ever
	// captures sing-box's own Shadowsocks connection, this rule sends it
	// DIRECT (out via the physical NIC) instead of looping it back into the
	// proxy. Best-effort — resolution happens before the TUN exists.
	if serverIPs, err := net.LookupIP(cfg.Server); err == nil {
		for _, ip := range serverIPs {
			if ip4 := ip.To4(); ip4 != nil {
				config.Route.Rules = append([]RouteRule{
					{IPCIDR: []string{ip4.String() + "/32"}, Action: &RuleAction{Type: "route", Outbound: "direct"}},
				}, config.Route.Rules...)
				break
			}
		}
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
	Log       LogConfig   `json:"log"`
	DNS       DNSConfig   `json:"dns"`
	Inbounds  []Inbound   `json:"inbounds"`
	Outbounds []Outbound  `json:"outbounds"`
	Route     RouteConfig `json:"route"`
}

type LogConfig struct {
	Level  string `json:"level"`
	Output string `json:"output,omitempty"`
}

type DNSConfig struct {
	Final   string      `json:"final"`
	Rules   []DNSRule   `json:"rules,omitempty"`
	Servers []DNSServer `json:"servers"`
}

type DNSRule struct {
	Rule     string   `json:"rule,omitempty"`
	Action   string   `json:"action,omitempty"`
	Server   string   `json:"server,omitempty"`
	Outbound []string `json:"outbound,omitempty"`
}

type DNSServer struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port,omitempty"`
	Detour     string `json:"detour,omitempty"`
}

type Inbound struct {
	Type          string   `json:"type"`
	Tag           string   `json:"tag,omitempty"`
	InterfaceName string   `json:"interface_name,omitempty"`
	Address       []string `json:"address,omitempty"`
	MTU           int      `json:"mtu,omitempty"`
	AutoRoute     bool     `json:"auto_route,omitempty"`
	StrictRoute   bool     `json:"strict_route,omitempty"`
	Sniff         bool     `json:"sniff,omitempty"`
}

type Outbound struct {
	Type          string            `json:"type"`
	Tag           string            `json:"tag,omitempty"`
	Server        string            `json:"server,omitempty"`
	ServerPort    int               `json:"server_port,omitempty"`
	Method        string            `json:"method,omitempty"`
	Password      string            `json:"password,omitempty"`
	BindInterface string            `json:"bind_interface,omitempty"`
	UDPOverTCP    *UDPOverTCPConfig `json:"udp_over_tcp,omitempty"`
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
	Protocol string      `json:"protocol,omitempty"`
	Network  string      `json:"network,omitempty"`
	IPCIDR   []string    `json:"ip_cidr,omitempty"`
	Action   *RuleAction `json:"action,omitempty"`
}

type RuleAction struct {
	Type     string `json:"type"`
	Outbound string `json:"outbound,omitempty"`
}
