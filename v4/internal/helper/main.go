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

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[myvpn-helper] ")

	log.Println("Starting MyVPN TUN Helper...")

	// Verify we're running as root/admin
	if os.Geteuid() != 0 {
		log.Fatal("ERROR: Must be run as root/administrator")
	}

	// Listen on IPC socket
	listener, err := listenIPC()
	if err != nil {
		log.Fatalf("Failed to listen on IPC: %v", err)
	}
	defer listener.Close()

	log.Printf("Listening on %s", listener.Addr())

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		listener.Close()
		os.Exit(0)
	}()

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

// listenIPC creates the IPC listener.
func listenIPC() (net.Listener, error) {
	switch runtime.GOOS {
	case "windows":
		return listenWindowsPipe()
	default:
		return listenUnixSocket()
	}
}

func listenUnixSocket() (net.Listener, error) {
	// Remove existing socket file
	os.Remove(SocketPath)

	listener, err := net.Listen("unix", SocketPath)
	if err != nil {
		return nil, fmt.Errorf("unix socket listen failed: %w", err)
	}

	// Set permissions so the unprivileged app can connect
	if err := os.Chmod(SocketPath, 0666); err != nil {
		listener.Close()
		return nil, fmt.Errorf("socket chmod failed: %w", err)
	}

	return listener, nil
}

func listenWindowsPipe() (net.Listener, error) {
	return net.Listen("pipe", PipeName)
}

// handleConnection processes a single IPC connection.
func handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	var cmd Command

	if err := decoder.Decode(&cmd); err != nil {
		sendError(conn, fmt.Sprintf("Invalid command: %v", err))
		return
	}

	log.Printf("Command: %s %v", cmd.Action, cmd.Args)

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
		resp = handleStopSingBox(cmd.Args)
	case "singbox-status":
		resp = handleSingBoxStatus()
	default:
		resp = Response{Success: false, Error: fmt.Sprintf("Unknown action: %s", cmd.Action)}
	}

	sendResponse(conn, resp)
}

func sendResponse(conn net.Conn, resp Response) {
	encoder := json.NewEncoder(conn)
	encoder.Encode(resp)
}

func sendError(conn net.Conn, msg string) {
	sendResponse(conn, Response{Success: false, Error: msg})
}

// ── Command handlers ──

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
			{"ip", "link", "set", "dev", name, "mtu", mtu, "up"},
		})
	case "darwin":
		return execCommands([][]string{
			{"ifconfig", name, "inet", ip, ip, "up"},
			{"route", "add", "-net", "0.0.0.0/1", "-interface", name},
			{"route", "add", "-net", "128.0.0.0/1", "-interface", name},
		})
	default:
		return Response{Success: false, Error: "Unsupported platform for TUN creation"}
	}
}

func handleDeleteTUN(args []string) Response {
	if len(args) < 1 {
		return Response{Success: false, Error: "Usage: delete-tun <name>"}
	}

	name := args[0]

	switch runtime.GOOS {
	case "linux":
		exec.Command("ip", "link", "set", "dev", name, "down").Run()
		return execCommands([][]string{
			{"ip", "tuntap", "del", "dev", name, "mode", "tun"},
		})
	case "darwin":
		return execCommands([][]string{
			{"ifconfig", name, "down"},
		})
	default:
		return Response{Success: false, Error: "Unsupported platform"}
	}
}

func handleSetDNS(args []string) Response {
	if len(args) < 2 {
		return Response{Success: false, Error: "Usage: set-dns <interface> <dns1> [dns2...]"}
	}

	iface := args[0]
	servers := args[1:]

	switch runtime.GOOS {
	case "linux":
		cmds := [][]string{}
		for _, s := range servers {
			cmds = append(cmds, []string{"resolvectl", "dns", iface, s})
		}
		return execCommands(cmds)
	case "darwin":
		cmds := [][]string{}
		for _, s := range servers {
			cmds = append(cmds, []string{"networksetup", "-setdnsservers", iface, s})
		}
		return execCommands(cmds)
	default:
		return Response{Success: false, Error: "Unsupported platform for DNS"}
	}
}

func handleKillSwitch(args []string) Response {
	if len(args) < 1 {
		return Response{Success: false, Error: "Usage: killswitch <on|off> [interface]"}
	}

	action := args[0]

	switch action {
	case "on":
		if len(args) < 2 {
			return Response{Success: false, Error: "Usage: killswitch on <interface>"}
		}
		iface := args[1]

		switch runtime.GOOS {
		case "linux":
			return execCommands([][]string{
				{"iptables", "-P", "INPUT", "DROP"},
				{"iptables", "-P", "FORWARD", "DROP"},
				{"iptables", "-P", "OUTPUT", "DROP"},
				{"iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
				{"iptables", "-A", "OUTPUT", "-o", iface, "-j", "ACCEPT"},
				{"iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
				{"iptables", "-A", "INPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
			})
		case "darwin":
			return execCommands([][]string{
				{"pfctl", "-e"},
			})
		default:
			return Response{Success: false, Error: "Kill switch not supported on this platform"}
		}

	case "off":
		switch runtime.GOOS {
		case "linux":
			return execCommands([][]string{
				{"iptables", "-P", "INPUT", "ACCEPT"},
				{"iptables", "-P", "FORWARD", "ACCEPT"},
				{"iptables", "-P", "OUTPUT", "ACCEPT"},
				{"iptables", "-F"},
			})
		case "darwin":
			return execCommands([][]string{
				{"pfctl", "-d"},
			})
		default:
			return Response{Success: false, Error: "Kill switch not supported on this platform"}
		}

	default:
		return Response{Success: false, Error: fmt.Sprintf("Unknown killswitch action: %s", action)}
	}
}

// ── Sing-box process management ──

var (
	singBoxCmd     *exec.Cmd
	singBoxMu      sync.Mutex
	singBoxTempDir string
)

func handleStartSingBox(args []string) Response {
	if len(args) < 1 {
		return Response{Success: false, Error: "Usage: start-singbox <config-json>"}
	}

	singBoxMu.Lock()
	defer singBoxMu.Unlock()

	// Kill any existing sing-box instance
	if singBoxCmd != nil && singBoxCmd.Process != nil {
		syscall.Kill(-singBoxCmd.Process.Pid, syscall.SIGTERM)
		singBoxCmd.Wait()
	}

	// Create temp directory for config
	tmpDir, err := os.MkdirTemp("", "myvpn-singbox-*")
	if err != nil {
		return Response{Success: false, Error: fmt.Sprintf("Cannot create temp dir: %v", err)}
	}

	// Write config file
	configPath := tmpDir + "/config.json"
	configJSON := args[0]
	if err := os.WriteFile(configPath, []byte(configJSON), 0600); err != nil {
		os.RemoveAll(tmpDir)
		return Response{Success: false, Error: fmt.Sprintf("Cannot write config: %v", err)}
	}

	// Find sing-box binary
	singBoxBinary := findSingBoxBinary()
	if singBoxBinary == "" {
		os.RemoveAll(tmpDir)
		return Response{Success: false, Error: "sing-box binary not found. Place it in /usr/local/bin or /opt/myvpn/"}
	}

	// Launch sing-box
	cmd := exec.Command(singBoxBinary, "run", "-c", configPath, "-D", tmpDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		os.RemoveAll(tmpDir)
		return Response{Success: false, Error: fmt.Sprintf("Cannot start sing-box: %v", err)}
	}

	// Clean up previous temp dir
	if singBoxTempDir != "" {
		os.RemoveAll(singBoxTempDir)
	}

	singBoxCmd = cmd
	singBoxTempDir = tmpDir

	return Response{Success: true, Message: "sing-box started"}
}

func handleStopSingBox(args []string) Response {
	singBoxMu.Lock()
	defer singBoxMu.Unlock()

	if singBoxCmd == nil || singBoxCmd.Process == nil {
		return Response{Success: true, Message: "sing-box not running"}
	}

	// SIGTERM with timeout, then SIGKILL
	syscall.Kill(-singBoxCmd.Process.Pid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		singBoxCmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-singBoxCmd.Process.Pid, syscall.SIGKILL)
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
	if err := singBoxCmd.Process.Signal(syscall.Signal(0)); err != nil {
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
