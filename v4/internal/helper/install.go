// Package helper provides installation and management of the MyVPN TUN helper service.
//
// The helper runs with elevated privileges and is installed as:
//   - Linux: systemd service
//   - macOS: launchd plist
//   - Windows: Windows Service
package helper

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Install installs the TUN helper service.
func Install(helperPath string) error {
	switch runtime.GOOS {
	case "linux":
		return installLinux(helperPath)
	case "darwin":
		return installDarwin(helperPath)
	case "windows":
		return installWindows(helperPath)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// Uninstall removes the TUN helper service.
func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		return uninstallLinux()
	case "darwin":
		return uninstallDarwin()
	case "windows":
		return uninstallWindows()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// IsInstalled checks if the helper service is installed and running.
func IsInstalled() bool {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("systemctl", "is-active", "--quiet", "myvpn-helper").Run() == nil
	case "darwin":
		return exec.Command("launchctl", "list", "com.myvpn.helper").Run() == nil
	case "windows":
		_, err := exec.Command("sc", "query", "MyVPNHelper").Output()
		return err == nil
	}
	return false
}

// ── Linux systemd ──

func installLinux(helperPath string) error {
	const serviceContent = `[Unit]
Description=MyVPN TUN Helper
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

	servicePath := "/etc/systemd/system/myvpn-helper.service"
	content := fmt.Sprintf(serviceContent, helperPath)

	if err := os.WriteFile(servicePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write service file: %w", err)
	}

	cmds := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "myvpn-helper"},
		{"systemctl", "start", "myvpn-helper"},
	}

	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("systemctl %s failed: %w", cmd[1], err)
		}
	}

	return nil
}

func uninstallLinux() error {
	cmds := [][]string{
		{"systemctl", "stop", "myvpn-helper"},
		{"systemctl", "disable", "myvpn-helper"},
	}

	for _, cmd := range cmds {
		exec.Command(cmd[0], cmd[1:]...).Run() // Best effort
	}

	os.Remove("/etc/systemd/system/myvpn-helper.service")
	exec.Command("systemctl", "daemon-reload").Run()

	return nil
}

// ── macOS launchd ──

func installDarwin(helperPath string) error {
	const plistContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.myvpn.helper</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/myvpn-helper.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/myvpn-helper.log</string>
</dict>
</plist>`

	plistPath := "/Library/LaunchDaemons/com.myvpn.helper.plist"
	content := fmt.Sprintf(plistContent, helperPath)

	if err := os.WriteFile(plistPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write plist: %w", err)
	}

	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}

	return nil
}

func uninstallDarwin() error {
	plistPath := "/Library/LaunchDaemons/com.myvpn.helper.plist"
	exec.Command("launchctl", "unload", plistPath).Run()
	os.Remove(plistPath)
	return nil
}

// ── Windows Service ──

func installWindows(helperPath string) error {
	// Copy helper to Program Files
	destDir := filepath.Join(os.Getenv("ProgramFiles"), "MyVPN")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	destPath := filepath.Join(destDir, "myvpn-helper.exe")
	if err := copyFile(helperPath, destPath); err != nil {
		return fmt.Errorf("cannot copy helper: %w", err)
	}

	// Create Windows service
	cmd := exec.Command("sc", "create", "MyVPNHelper",
		"binPath="+destPath,
		"start=auto",
		"displayname=MyVPN TUN Helper")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sc create failed: %w", err)
	}

	return exec.Command("sc", "start", "MyVPNHelper").Run()
}

func uninstallWindows() error {
	exec.Command("sc", "stop", "MyVPNHelper").Run()
	exec.Command("sc", "delete", "MyVPNHelper").Run()

	destPath := filepath.Join(os.Getenv("ProgramFiles"), "MyVPN", "myvpn-helper.exe")
	os.Remove(destPath)

	return nil
}

// copyFile copies a file (simple implementation).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0755)
}
