//go:build windows

package helper

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

func RunService() error {
	secret := generateSecret()
	secretDir := filepath.Join(os.Getenv("PROGRAMDATA"), "MyVPN")
	os.MkdirAll(secretDir, 0700)
	os.WriteFile(filepath.Join(secretDir, "helper.secret"), []byte(secret), 0600)

	listener, err := net.Listen("tcp", `\\.\pipe\MyVPNHelper`)
	if err != nil {
		return fmt.Errorf("helper service listen failed: %w", err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn, secret)
	}
}

func handleConnection(conn net.Conn, secret string) {
	defer conn.Close()

	var cmd Command
	json.NewDecoder(conn).Decode(&cmd)

	if cmd.Auth != secret {
		return
	}

	switch cmd.Action {
	case "create_tun":
		// Create wintun device with IP
	case "destroy_tun":
		// Remove wintun device
	case "block_all":
		// Apply firewall block rule
	case "unblock_all":
		// Remove firewall block rule
	case "add_route":
		// Add route
	case "remove_route":
		// Remove route
	}
}

func generateSecret() string {
	return fmt.Sprintf("myvpn-helper-secret-%d", os.Getpid())
}
