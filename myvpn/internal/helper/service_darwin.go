//go:build darwin

package helper

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
)

func RunService() error {
	secret := fmt.Sprintf("myvpn-helper-secret-%d", os.Getpid())
	os.WriteFile("/var/run/myvpn-helper.secret", []byte(secret), 0600)

	os.Remove("/var/run/myvpn-helper.sock")
	listener, err := net.Listen("unix", "/var/run/myvpn-helper.sock")
	if err != nil {
		return fmt.Errorf("helper service listen failed: %w", err)
	}
	defer listener.Close()
	os.Chmod("/var/run/myvpn-helper.sock", 0660)

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
		// Create utun device with IP
	case "destroy_tun":
		// Remove utun device
	case "block_all":
		// Apply pf rule
	case "unblock_all":
		// Remove pf rule
	case "add_route":
		// Add route
	case "remove_route":
		// Remove route
	}
}
