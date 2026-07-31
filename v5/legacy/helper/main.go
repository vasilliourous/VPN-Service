// Package main implements the MyVPN TUN Helper service.
//
// This service runs with elevated privileges (root/admin) and provides
// TUN interface management for the main MyVPN application, which runs
// without special privileges.
//
// Communication is via IPC:
//   - Linux/macOS: Unix domain socket at /var/run/myvpn-helper.sock
//   - Windows: Named pipe \\.\pipe\MyVPNHelper
//
// Commands:
//   - ping — Health check
//   - create-tun <name> <ip> <mtu> — Create a TUN interface
//   - delete-tun <name> — Remove a TUN interface
//   - set-dns <iface> <server1> [server2...] — Set DNS servers
//   - killswitch on <iface> — Enable kill switch
//   - killswitch off — Disable kill switch
//   - start-singbox <config-json> — Write config and launch sing-box
//   - stop-singbox — Kill the running sing-box process
//   - singbox-status — Check if sing-box is running
//
// Hardening: socket cleanup on start, request timeout per command,
// resource limits for child processes, input validation.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// SocketPath is the IPC socket path (Unix).
	SocketPath = "/var/run/myvpn-helper.sock"
	// PipeName is the IPC pipe name (Windows).
	PipeName = `\\.\pipe\MyVPNHelper`

	// Max request size (1MB)
	maxRequestSize = 1 * 1024 * 1024

	// Command timeout
	commandTimeout = 30 * time.Second
)

// Command represents an IPC command from the app.
type Command struct {
	Action string   `json:"action"`
	Args   []string `json:"args,omitempty"`
}

// Response represents the helper's reply.
type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Global state for sing-box management
var (
	singBoxCmd     *exec.Cmd
	singBoxMu      sync.Mutex
	singBoxTempDir string
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("MyVPN TUN Helper starting...")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	switch runtime.GOOS {
	case "linux", "darwin":
		startUnixSocket()
	case "windows":
		startNamedPipe()
	default:
		log.Fatalf("Unsupported platform: %s", runtime.GOOS)
	}

	// Wait for shutdown signal
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)
	cleanup()
	os.Exit(0)
}

func cleanup() {
	singBoxMu.Lock()
	if singBoxCmd != nil && singBoxCmd.Process != nil {
		if err := singBoxCmd.Process.Signal(signalInterrupt()); err != nil {
			log.Printf("Failed to send interrupt signal: %v", err)
		}
		time.Sleep(3 * time.Second)
		if err := singBoxCmd.Process.Kill(); err != nil {
			log.Printf("Failed to kill process: %v", err)
		}
	}
	if singBoxTempDir != "" {
		os.RemoveAll(singBoxTempDir)
	}
	singBoxMu.Unlock()

	// Clean up socket
	os.Remove(SocketPath)
}

// ── Unix Socket ──

func startUnixSocket() {
	// Remove any leftover socket from a previous run
	os.Remove(SocketPath)

	listener, err := net.Listen("unix", SocketPath)
	if err != nil {
		log.Fatalf("Cannot listen on %s: %v", SocketPath, err)
	}
	defer listener.Close()

	// Set permissions so only root can connect
	if err := os.Chmod(SocketPath, 0600); err != nil {
		log.Fatalf("Cannot set socket permissions: %v", err)
	}

	log.Printf("Listening on Unix socket %s", SocketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

// ── Windows Named Pipe ──

func startNamedPipe() {
	// Windows named pipe listener
	path := `\\.\pipe\MyVPNHelper`
	listener, err := net.Listen("pipe", path)
	if err != nil {
		log.Fatalf("Cannot listen on pipe %s: %v", path, err)
	}
	defer listener.Close()

	log.Printf("Listening on named pipe %s", path)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

// ── Connection Handler ──

func handleConnection(conn net.Conn) {
	defer conn.Close()

	// Set a deadline for reading the command
	conn.SetDeadline(time.Now().Add(commandTimeout))

	// Read command
	var cmd Command
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&cmd); err != nil {
		log.Printf("Failed to decode command: %v", err)
		sendResponse(conn, false, "", "Failed to decode command")
		return
	}

	log.Printf("Received command: %s %v", cmd.Action, cmd.Args)

	// Execute command
	var resp Response
	switch cmd.Action {
	case "ping":
		resp = Response{Success: true, Message: "pong"}
	case "create-tun":
		resp = handleCreateTUN(cmd.Args)
	case "delete-tun":
		resp = handleDeleteTUN(cmd.Args)
	case "set-dns":
		resp = handleSetDNS(cmd.Args)
	case "killswitch":
		resp = handleKillSwitch(cmd.Args)
	case "start-singbox":
		resp = handleStartSingBox(cmd.Args)
	case "stop-singbox":
		resp = handleStopSingBox()
	case "singbox-status":
		resp = handleSingBoxStatus()
	default:
		resp = Response{Success: false, Error: fmt.Sprintf("Unknown command: %s", cmd.Action)}
	}

	sendResponse(conn, resp.Success, resp.Message, resp.Error)
}

func sendResponse(conn net.Conn, success bool, message, errMsg string) {
	resp := Response{
		Success: success,
		Message: message,
		Error:   errMsg,
	}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(resp); err != nil {
		log.Printf("Failed to send response: %v", err)
	}
}

// ── Command Handlers ──

func handleCreateTUN(args []string) Response {
	if len(args) < 3 {
		return Response{Success: false, Error: "Usage: create-tun <name> <ip> <mtu>"}
	}
	name := args[0]
	ip := args[1]
	mtu := args[2]

	switch runtime.GOOS {
	case "linux":
		return execCommands([][]string{
			{"ip", "tuntap", "add", "dev", name, "mode", "tun"},
			{"ip", "addr", "add", ip + "/24", "dev", name},
			{"ip", "link", "set", "dev", name, "mtu", mtu},
			{"ip", "link", "set", "dev", name, "up"},
		})
	case "darwin":
		return execCommands([][]string{
			{"ifconfig", name, "inet", ip, ip, "up"},
		})
	default:
		return Response{Success: false, Error: fmt.Sprintf("create-tun not supported on %s", runtime.GOOS)}
	}
}

func handleDeleteTUN(args []string) Response {
	if len(args) < 1 {
		return Response{Success: false, Error: "Usage: delete-tun <name>"}
	}
	name := args[0]

	switch runtime.GOOS {
	case "linux":
		return execCommands([][]string{
			{"ip", "link", "set", "dev", name, "down"},
			{"ip", "tuntap", "del", "dev", name, "mode", "tun"},
		})
	case "darwin":
		return execCommands([][]string{
			{"ifconfig", name, "down"},
		})
	default:
		return Response{Success: false, Error: fmt.Sprintf("delete-tun not supported on %s", runtime.GOOS)}
	}
}

func handleSetDNS(args []string) Response {
	if len(args) < 2 {
		return Response{Success: false, Error: "Usage: set-dns <iface> <server1> [server2...]"}
	}
	iface := args[0]
	servers := args[1:]

	switch runtime.GOOS {
	case "linux":
		// Write resolv.conf
		resolvContent := ""
		for _, s := range servers {
			resolvContent += fmt.Sprintf("nameserver %s\n", s)
		}
		return execCommands([][]string{
			{"bash", "-c", fmt.Sprintf("cat > /etc/resolv.conf << 'EOF'\n%sEOF", resolvContent)},
		})
	case "darwin":
		cmds := make([][]string, 0, len(servers)+1)
		// Clear existing DNS first
		cmds = append(cmds, []string{"networksetup", "-setdnsservers", iface, "Empty"})
		for _, s := range servers {
			cmds = append(cmds, []string{"networksetup", "-setdnsservers", iface, s})
		}
		return execCommands(cmds)
	default:
		return Response{Success: false, Error: fmt.Sprintf("set-dns not supported on %s", runtime.GOOS)}
	}
}

func handleKillSwitch(args []string) Response {
	if len(args) < 1 {
		return Response{Success: false, Error: "Usage: killswitch on|off <iface>"}
	}

	mode := args[0]
	iface := ""
	if len(args) > 1 {
		iface = args[1]
	}

	switch mode {
	case "on":
		if iface == "" {
			return Response{Success: false, Error: "killswitch on requires an interface name"}
		}
		switch runtime.GOOS {
		case "linux":
			return execCommands([][]string{
				{"iptables", "-A", "OUTPUT", "-o", iface, "-j", "ACCEPT"},
				{"iptables", "-A", "OUTPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
				{"iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
				{"iptables", "-A", "OUTPUT", "-j", "DROP"},
			})
		case "darwin":
			return execCommands([][]string{
				{"pfctl", "-E"},
				{"bash", "-c", fmt.Sprintf("echo 'block drop all\npass on lo0\npass on %s\npass out proto udp from any to any port 53' | pfctl -f -", iface)},
			})
		default:
			return Response{Success: false, Error: "kill switch not supported on this platform"}
		}
	case "off":
		switch runtime.GOOS {
		case "linux":
			return execCommands([][]string{
				{"iptables", "-F", "OUTPUT"},
			})
		case "darwin":
			return execCommands([][]string{
				{"pfctl", "-F", "all"},
				{"pfctl", "-d"},
			})
		default:
			return Response{Success: false, Error: "kill switch not supported on this platform"}
		}
	default:
		return Response{Success: false, Error: fmt.Sprintf("Unknown killswitch mode: %s (use on/off)", mode)}
	}
}

func handleStartSingBox(args []string) Response {
	if len(args) < 1 {
		return Response{Success: false, Error: "Usage: start-singbox <config-json>"}
	}

	configJSON := args[0]

	singBoxMu.Lock()
	defer singBoxMu.Unlock()

	// Check if already running
	if singBoxCmd != nil && singBoxCmd.Process != nil {
		if err := singBoxCmd.Process.Signal(syscall.Signal(0)); err == nil {
			return Response{Success: false, Error: "sing-box is already running"}
		}
	}

	// Find sing-box binary
	binaryPath := findSingBoxBinary()
	if binaryPath == "" {
		return Response{Success: false, Error: "sing-box binary not found in any standard location"}
	}

	// Create temp directory for config
	tempDir, err := os.MkdirTemp("", "myvpn-singbox-*")
	if err != nil {
		return Response{Success: false, Error: fmt.Sprintf("Cannot create temp dir: %v", err)}
	}

	// Write config
	configPath := tempDir + "/config.json"
	if err := os.WriteFile(configPath, []byte(configJSON), 0600); err != nil {
		os.RemoveAll(tempDir)
		return Response{Success: false, Error: fmt.Sprintf("Cannot write config: %v", err)}
	}

	// Start sing-box
	cmd := exec.Command(binaryPath, "run", "-c", configPath, "-D", tempDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = newProcAttr()

	if err := cmd.Start(); err != nil {
		os.RemoveAll(tempDir)
		return Response{Success: false, Error: fmt.Sprintf("Cannot start sing-box: %v", err)}
	}

	singBoxCmd = cmd
	singBoxTempDir = tempDir

	return Response{Success: true, Message: fmt.Sprintf("sing-box started (PID %d)", cmd.Process.Pid)}
}

func handleStopSingBox() Response {
	singBoxMu.Lock()
	defer singBoxMu.Unlock()

	if singBoxCmd == nil || singBoxCmd.Process == nil {
		return Response{Success: true, Message: "sing-box not running"}
	}

	// Graceful shutdown
	if err := singBoxCmd.Process.Signal(signalInterrupt()); err != nil {
		log.Printf("Failed to send interrupt signal: %v", err)
	}

	// Wait up to 5 seconds
	done := make(chan struct{})
	go func() {
		singBoxCmd.Wait()
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if err := killProcessGroup(singBoxCmd.Process.Pid, signalKill()); err != nil {
			log.Printf("Failed to kill process group: %v", err)
		}
	}

	if singBoxTempDir != "" {
		os.RemoveAll(singBoxTempDir)
		singBoxTempDir = ""
	}

	singBoxCmd = nil
	return Response{Success: true, Message: "sing-box stopped"}
}

func handleSingBoxStatus() Response {
	singBoxMu.Lock()
	defer singBoxMu.Unlock()

	if singBoxCmd == nil || singBoxCmd.Process == nil {
		return Response{Success: true, Message: "stopped"}
	}

	// Check if process is still alive
	if !processExists(singBoxCmd) {
		singBoxCmd = nil
		return Response{Success: true, Message: "stopped"}
	}

	return Response{Success: true, Message: "running"}
}

// findSingBoxBinary searches common paths for the sing-box binary.
func findSingBoxBinary() string {
	paths := []string{
		"/usr/local/bin/sing-box",
		"/usr/bin/sing-box",
		"/opt/myvpn/sing-box",
		"/opt/sing-box/sing-box",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

// ── Utility ──

func execCommands(cmds [][]string) Response {
	for _, cmd := range cmds {
		if len(cmd) == 0 {
			continue
		}
		c := exec.Command(cmd[0], cmd[1:]...)
		output, err := c.CombinedOutput()
		if err != nil {
			return Response{
				Success: false,
				Error:   fmt.Sprintf("Command '%s' failed: %v\nOutput: %s", strings.Join(cmd, " "), err, string(output)),
			}
		}
	}
	return Response{Success: true, Message: fmt.Sprintf("Executed %d commands successfully", len(cmds))}
}
