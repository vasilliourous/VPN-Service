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
//     Legacy — the myvpn-helper binary is no longer shipped (removed in the
//     Wails migration; pre-migration client retained in v4/).
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
	// errEngineAlreadyRunning guards against concurrent double-spawn from the
	// health loop and the watchdog sharing myvpn0.
	errEngineAlreadyRunning = fmt.Errorf("sing-box engine is already running")
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

	// tunCfg is the last successful tunnel config, retained so the watchdog
	// can restart/recover the exact same tunnel (config file is deleted on
	// Stop, so recovery must regenerate it).
	tunCfg Config

	// Watchdog state.
	watchdogStop    chan struct{}
	watchdogOnProbe ProbeCallback // notified on each probe outcome (nil = no-op)
	tunnelHealthy   bool
}

// Config holds the parameters needed to start the tunnel.
type Config struct {
	Server     string
	ServerPort int
	Password   string
	Method     string
	TierName   string
	// UDPRelay is true when the tier supports UDP (Strike). It does NOT by
	// itself enable UDP-over-TCP: UoT is only used when ServerPortUOT > 0
	// (server advertises a UoT-capable sing-box endpoint). Otherwise UDP
	// flows raw (standard ss UDP) — see generateConfig note and
	// docs/FIXES.md Follow-up 9.
	UDPRelay bool
	// ServerPortUOT is the optional UDP-over-TCP endpoint (sing-box server).
	// When > 0 and UDPRelay is true, UDP is routed to a dedicated UoT
	// outbound on this port; TCP stays on ServerPort.
	ServerPortUOT int
	HubURL        string
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

	// Auto-clean before connecting. Previously this hard-failed with "close it
	// in Task Manager" — a manual dead-end for students. A leftover sing-box
	// (orphaned after a crash) or a stale myvpn0 TUN will corrupt routing if we
	// stack a fresh engine on top, so we clear both first, then start clean.
	if foreignSingBoxRunning() {
		log.Printf("Leftover sing-box detected before connect; killing it and clearing stale TUN")
		killForeignEngines()
		_ = removeStaleTUN()
	}

	// Retain the tunnel config so the watchdog can restart/recover it later
	// (the config file is deleted on Stop).
	m.tunCfg = cfg

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
	// Guard against concurrent double-spawn (e.g. the health loop and the
	// watchdog both trying to recover at once): two sing-box instances sharing
	// myvpn0 corrupt routing. Prefer the running instance over spawning a second.
	if m.processAlive() {
		return errEngineAlreadyRunning
	}

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

				// Attempt restart. Route through startDirect so the shared
				// double-spawn guard applies — otherwise this loop could race
				// the watchdog's recovery and spawn a second sing-box on the
				// same TUN (which corrupts routing).
				if m.canRestart() {
					m.restartCount++
					m.mu.Unlock()
					configJSON, err := os.ReadFile(m.configPath)
					if err != nil {
						// Config gone — nothing to restart with; give up the loop.
						m.mu.Lock()
						m.healthFailures = maxHealthFailures
						m.mu.Unlock()
						return
					}
					if startErr := m.startDirect(context.Background(), configJSON); startErr == nil {
						m.mu.Lock()
						m.healthFailures = 0
						m.mu.Unlock()
					}
					// Restart failed or engine already up — will retry next cycle.
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
//
// All route rules use the MODERN rule-action format ("action": "route", ...)
// and the tun inbound NO LONGER sets the legacy "sniff": true field: those
// legacy 1.10-era fields are deprecated (1.11) and removed (1.13), and make
// the config fail to start or break routing on newer engines. Validated to
// `sing-box check` clean on both 1.12.1 and 1.13.x.
func generateConfig(cfg Config) ([]byte, error) {
	// Debug is the DEFAULT level while the tunnel data path is under active
	// investigation (2026-08-01): with it, myvpn.log shows sing-box's dial
	// lines (target IP/port, error) which are required to diagnose
	// "connects but no internet". Override with MYVPN_LOG_LEVEL=warn or
	// MYVPN_DEBUG=0 for quieter logs in production builds.
	logLevel := "debug"
	if lvl := os.Getenv("MYVPN_LOG_LEVEL"); lvl != "" {
		logLevel = lvl
	} else if os.Getenv("MYVPN_DEBUG") == "0" {
		logLevel = "warn"
	}
	log.Printf("sing-box log level: %s", logLevel)

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
				// NOTE: sniff is intentionally NOT set here. The legacy
				// `"sniff": true` tun-inbound field was deprecated in sing-box
				// 1.11 and REMOVED in 1.13 (legacy inbound fields → rule
				// actions migration). Leaving it in makes the whole config
				// fail to start on any sing-box ≥ 1.13 ("removed in 1.13.0")
				// and was a source of DNS/routing breakage. DNS sniffing is
				// handled in the modern way by the `sniff` route rule action
				// below (see Route.Rules).
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
				Type: "direct",
				Tag:  "direct",
				// connect_timeout makes the direct outbound NON-EMPTY — sing-box
				// 1.12 rejects DNS detours to an empty direct outbound — without
				// needing bind_interface (which broke the Windows dial).
				ConnectTimeout: "10s",
			},
		},
		Route: RouteConfig{
			AutoDetectInterface: true,
			Final:               "proxy",
			// The Shadowsocks server is a DOMAIN — this resolver sends ALL
			// outbound-initiated resolution (e.g. the proxy dialing
			// networkingguides.duckdns.org) DIRECT, preventing the DNS
			// loopback (sing-box issue #2207; the 1.10-era outbound DNS rule
			// is deprecated in 1.12).
			DefaultDomainResolver: &DomainResolveOptions{Server: "dns-direct"},
			Rules: []RouteRule{
				// Sniff connections first (modern equivalent of the removed
				// legacy tun-inbound `"sniff": true`) so the protocol:dns rule
				// below can match DNS by sniffed protocol. First rule so the
				// metadata is available to every subsequent rule.
				{Action: "sniff"},
				// DNS traffic is handled by the hijack-dns rule action (1.11+).
				{Protocol: "dns", Action: "hijack-dns"},
			},
		},
	}

	// Exclude the VPN server itself from the tunnel: if auto_route ever
	// captures sing-box's own Shadowsocks connection, this rule sends it
	// DIRECT (out via the physical NIC) instead of looping it back into the
	// proxy. Best-effort — resolution happens before the TUN exists.
	//
	// This rule (and the UoT UDP rule below) is APPENDED after the base
	// [sniff, hijack-dns] rules, NEVER prepended: the DNS query must be
	// handled by hijack-dns before any `network:`/`ip_cidr:` matcher can
	// capture it. If a network-based rule were first it would swallow DNS
	// (e.g. UDP DNS hitting the proxy-uot route) and nothing would resolve —
	// the classic "TUN up but no traffic passes" symptom.
	if serverIPs, err := net.LookupIP(cfg.Server); err == nil {
		for _, ip := range serverIPs {
			if ip4 := ip.To4(); ip4 != nil {
				config.Route.Rules = append(config.Route.Rules, RouteRule{
					// Canonical action form — the top-level `outbound` field
					// was deprecated in 1.11 and is silently problematic; the
					// `"action": "route"` form works on 1.12 and 1.13+.
					IPCIDR:   []string{ip4.String() + "/32"},
					Action:   "route",
					Outbound: "direct",
				})
				break
			}
		}
	}

	// UDP-over-TCP (UoT) — used ONLY when the server advertises a UoT
	// endpoint (ServerPortUOT > 0, sing-box server). sing-box's UoT is a
	// proprietary SagerNet protocol (magic domains sp.udp-over-tcp.arpa /
	// sp.v2.udp-over-tcp.arpa), NOT the Shadowsocks standard — the default
	// server (shadowsocks-rust) does not implement it and RSTs every such
	// connection (observed 2026-08-01: "forcibly closed" ~300ms after each
	// UoT dial). So unless the tier advertises uot_port, UDP stays RAW
	// (standard ss UDP; Strike server mode tcp_and_udp) and works wherever
	// the network allows UDP; on UDP-blocking networks (N4L school WiFi)
	// clients fall back to TCP.
	//
	// When UoT IS advertised: UDP is pinned to a dedicated UoT outbound on
	// the sing-box server port, while TCP stays on the standard port — the
	// school firewall only sees TCP, and game UDP rides inside it.
	uotEnabled := cfg.UDPRelay && cfg.ServerPortUOT > 0
	if uotEnabled {
		config.Outbounds = append(config.Outbounds, Outbound{
			Type:       "shadowsocks",
			Tag:        "proxy-uot",
			Server:     cfg.Server,
			ServerPort: cfg.ServerPortUOT,
			Method:     cfg.Method,
			Password:   cfg.Password,
			UDPOverTCP: true,
		})
		// Route UDP flows to the UoT outbound; TCP continues via "proxy".
		// Appended AFTER the base sniff/hijack-dns rules so DNS over UDP is
		// hijacked before it can match this network:udp matcher (see the
		// server-IP exclusion comment above).
		config.Route.Rules = append(config.Route.Rules, RouteRule{
			Network:  "udp",
			Action:   "route",
			Outbound: "proxy-uot",
		})
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
	Type           string `json:"type"`
	Tag            string `json:"tag,omitempty"`
	Server         string `json:"server,omitempty"`
	ServerPort     int    `json:"server_port,omitempty"`
	Method         string `json:"method,omitempty"`
	Password       string `json:"password,omitempty"`
	ConnectTimeout string `json:"connect_timeout,omitempty"`
	// UDPOverTCP enables SagerNet UDP-over-TCP on this outbound (only valid
	// for shadowsocks outbounds; requires a server that implements UoT, e.g.
	// sing-box server).
	UDPOverTCP bool `json:"udp_over_tcp,omitempty"`
}

type RouteConfig struct {
	Rules                 []RouteRule           `json:"rules,omitempty"`
	AutoDetectInterface   bool                  `json:"auto_detect_interface"`
	Final                 string                `json:"final"`
	DefaultDomainResolver *DomainResolveOptions `json:"default_domain_resolver,omitempty"`
}

type DomainResolveOptions struct {
	Server string `json:"server"`
}

type RouteRule struct {
	Protocol string   `json:"protocol,omitempty"`
	Network  string   `json:"network,omitempty"`
	IPCIDR   []string `json:"ip_cidr,omitempty"`
	// Outbound is the target outbound for the "route" action. This must be
	// paired with Action == "route" (sing-box 1.11+): the bare top-level
	// `outbound` field is the deprecated legacy form and is troublesome on
	// sing-box 1.12+/1.13. See generateConfig.
	Outbound string `json:"outbound,omitempty"`
	// Action names the rule action: "route", "hijack-dns", "sniff", etc.
	// Required for all rules on sing-box 1.11+.
	Action string `json:"action,omitempty"`
}
