package helper

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

type Command struct {
	Action  string `json:"action"`
	TUNIP   string `json:"tun_ip,omitempty"`
	Dest    string `json:"dest,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Mask    string `json:"mask,omitempty"`
	Servers string `json:"servers,omitempty"`
	Auth    string `json:"auth,omitempty"`
}

func SocketPath() string {
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\MyVPNHelper`
	case "darwin":
		return "/var/run/myvpn-helper.sock"
	default:
		return "/var/run/myvpn-helper.sock"
	}
}

func SecretFile() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("PROGRAMDATA"), "MyVPN", "helper.secret")
	default:
		return "/var/run/myvpn-helper.secret"
	}
}

func SendCommand(cmd Command) error {
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		conn, err = net.Dial("tcp", SocketPath())
		if err != nil {
			return fmt.Errorf("helper connection failed: %w", err)
		}
	}
	defer conn.Close()

	secret, err := os.ReadFile(SecretFile())
	if err == nil {
		cmd.Auth = string(secret)
	}

	payload, _ := json.Marshal(cmd)
	_, err = conn.Write(payload)
	if err != nil {
		return fmt.Errorf("helper write failed: %w", err)
	}

	return nil
}
