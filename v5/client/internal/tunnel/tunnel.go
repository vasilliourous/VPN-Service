// Package tunnel provides direct TUN interface management as a fallback.
//
// Primary tunnel management is handled by the manager package (sing-box).
// This package provides:
//   - A fallback TUN implementation for platforms/edge cases where sing-box can't be used
//   - Kill switch (block non-VPN traffic)
//   - DNS configuration (force all DNS through tunnel)
//
// The TUN helper service (internal/helper) handles privileged operations
// that require root/admin access.
//
// Hardening: interface validation, cleanup on error, platform-specific safety checks.
package tunnel

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
)

// Config holds TUN interface parameters.
type Config struct {
	// Name of the TUN interface
	Name string

	// MTU for the TUN interface
	MTU int

	// Virtual IP assigned to the TUN interface
	VirtualIP string

	// DNS servers to push through the tunnel
	DNSServers []string

	// Whether to enable the kill switch
	KillSwitch bool
}

// DefaultConfig returns a default TUN configuration.
func DefaultConfig() Config {
	return Config{
		Name:       "myvpn0",
		MTU:        1500,
		VirtualIP:  "10.0.0.2",
		DNSServers: []string{"1.1.1.1", "1.0.0.1"},
		KillSwitch: true,
	}
}

// Validate checks that the TUN config is valid.
func (c Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("tunnel: interface name is required")
	}
	if len(c.Name) > 16 {
		return fmt.Errorf("tunnel: interface name too long: %s", c.Name)
	}
	if c.MTU <= 0 || c.MTU > 65535 {
		return fmt.Errorf("tunnel: invalid MTU: %d", c.MTU)
	}
	if c.VirtualIP != "" {
		if net.ParseIP(c.VirtualIP) == nil {
			return fmt.Errorf("tunnel: invalid virtual IP: %s", c.VirtualIP)
		}
	}
	return nil
}

// Interface represents a TUN network interface.
type Interface interface {
	// Name returns the interface name.
	Name() string

	// Start brings up the TUN interface.
	Start() error

	// Stop tears down the TUN interface.
	Stop() error

	// IsUp returns whether the interface is active.
	IsUp() bool
}

// NewInterface creates a platform-appropriate TUN interface.
func NewInterface(cfg Config) (Interface, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	switch runtime.GOOS {
	case "linux":
		return &linuxTUN{cfg: cfg}, nil
	case "darwin":
		return &darwinTUN{cfg: cfg}, nil
	case "windows":
		return &windowsTUN{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// ── Kill Switch ──

// KillSwitch enables or disables the network kill switch.
// When enabled, all non-VPN traffic is blocked.
func KillSwitch(enable bool, tunInterfaceName string) error {
	switch runtime.GOOS {
	case "linux":
		return killSwitchLinux(enable, tunInterfaceName)
	case "darwin":
		return killSwitchDarwin(enable, tunInterfaceName)
	case "windows":
		return killSwitchWindows(enable, tunInterfaceName)
	default:
		return fmt.Errorf("kill switch not supported on %s", runtime.GOOS)
	}
}

func killSwitchLinux(enable bool, iface string) error {
	if enable {
		// Allow traffic through tunnel
		if err := exec.Command("iptables", "-A", "OUTPUT", "-o", iface, "-j", "ACCEPT").Run(); err != nil {
			return fmt.Errorf("kill switch enable failed (allow tunnel): %w", err)
		}
		// Allow established connections
		if err := exec.Command("iptables", "-A", "OUTPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT").Run(); err != nil {
			return fmt.Errorf("kill switch enable failed (allow established): %w", err)
		}
		// Allow loopback
		if err := exec.Command("iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT").Run(); err != nil {
			return fmt.Errorf("kill switch enable failed (allow loopback): %w", err)
		}
		// Block everything else
		if err := exec.Command("iptables", "-A", "OUTPUT", "-j", "DROP").Run(); err != nil {
			return fmt.Errorf("kill switch enable failed (block): %w", err)
		}
	} else {
		// Remove rules (best effort, ignore errors)
		_ = exec.Command("iptables", "-F", "OUTPUT").Run()
	}
	return nil
}

func killSwitchDarwin(enable bool, iface string) error {
	if enable {
		// pf-based kill switch for macOS
		rules := fmt.Sprintf(`
block drop all
pass on lo0
pass on %s
pass out proto udp from any to any port 53
`, iface)
		cmd := exec.Command("pfctl", "-E")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cannot enable pf: %w", err)
		}
		cmd = exec.Command("bash", "-c", fmt.Sprintf("echo '%s' | pfctl -f -", rules))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cannot set pf rules: %w", err)
		}
	} else {
		_ = exec.Command("pfctl", "-F", "all").Run()
		_ = exec.Command("pfctl", "-d").Run()
	}
	return nil
}

func killSwitchWindows(enable bool, iface string) error {
	if enable {
		// Use Windows Filtering Platform via netsh
		if err := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
			"name=MyVPN_KillSwitch", "dir=out", "action=block",
			"interfacetype=!\"Tunnel\"", "enable=yes",
		).Run(); err != nil {
			return fmt.Errorf("kill switch enable failed: %w", err)
		}
	} else {
		exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
			"name=MyVPN_KillSwitch").Run()
	}
	return nil
}

// ── DNS Configuration ──

// SetDNS configures the system DNS servers through the tunnel.
func SetDNS(servers []string) error {
	switch runtime.GOOS {
	case "linux":
		return setDNSLinux(servers)
	case "darwin":
		return setDNSDarwin(servers)
	case "windows":
		return setDNSWindows(servers)
	default:
		return fmt.Errorf("DNS configuration not supported on %s", runtime.GOOS)
	}
}

func setDNSLinux(servers []string) error {
	// Write resolv.conf (best-effort, may be managed by systemd-resolved)
	resolvContent := ""
	for _, s := range servers {
		resolvContent += fmt.Sprintf("nameserver %s\n", s)
	}
	if err := exec.Command("bash", "-c", fmt.Sprintf("echo '%s' > /etc/resolv.conf", resolvContent)).Run(); err != nil {
		return fmt.Errorf("cannot set DNS: %w", err)
	}
	return nil
}

func setDNSDarwin(servers []string) error {
	for i, s := range servers {
		if err := exec.Command("networksetup", "-setdnsservers", "Wi-Fi", s).Run(); err != nil {
			// Try Ethernet if Wi-Fi fails
			if i == 0 {
				exec.Command("networksetup", "-setdnsservers", "Ethernet", s).Run()
			}
		}
	}
	return nil
}

func setDNSWindows(servers []string) error {
	// Use netsh to set DNS
	for _, s := range servers {
		if err := exec.Command("netsh", "interface", "ip", "set", "dns",
			"name=MyVPN", "source=static", fmt.Sprintf("addr=%s", s),
		).Run(); err != nil {
			return fmt.Errorf("cannot set Windows DNS: %w", err)
		}
	}
	return nil
}

// ── Platform TUN implementations ──

type linuxTUN struct {
	cfg Config
	up  bool
}

func (t *linuxTUN) Name() string { return t.cfg.Name }

func (t *linuxTUN) Start() error {
	cmds := [][]string{
		{"ip", "tuntap", "add", "dev", t.cfg.Name, "mode", "tun"},
		{"ip", "addr", "add", t.cfg.VirtualIP + "/24", "dev", t.cfg.Name},
		{"ip", "link", "set", "dev", t.cfg.Name, "mtu", fmt.Sprintf("%d", t.cfg.MTU)},
		{"ip", "link", "set", "dev", t.cfg.Name, "up"},
		{"ip", "route", "add", "0.0.0.0/1", "dev", t.cfg.Name},
		{"ip", "route", "add", "128.0.0.0/1", "dev", t.cfg.Name},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			_ = t.Stop() // Rollback on failure
			return fmt.Errorf("TUN setup failed at %s: %w", cmd[0], err)
		}
	}
	t.up = true
	return nil
}

func (t *linuxTUN) Stop() error {
	cmds := [][]string{
		{"ip", "link", "set", "dev", t.cfg.Name, "down"},
		{"ip", "tuntap", "del", "dev", t.cfg.Name, "mode", "tun"},
	}
	for _, cmd := range cmds {
		_ = exec.Command(cmd[0], cmd[1:]...).Run() // Best effort cleanup
	}
	t.up = false
	return nil
}

func (t *linuxTUN) IsUp() bool { return t.up }

type darwinTUN struct {
	cfg Config
	up  bool
}

func (t *darwinTUN) Name() string { return t.cfg.Name }

func (t *darwinTUN) Start() error {
	cmds := [][]string{
		{"ifconfig", t.cfg.Name, "inet", t.cfg.VirtualIP, t.cfg.VirtualIP, "up"},
		{"route", "add", "-net", "0.0.0.0/1", "-interface", t.cfg.Name},
		{"route", "add", "-net", "128.0.0.0/1", "-interface", t.cfg.Name},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			_ = t.Stop()
			return fmt.Errorf("TUN setup failed at %s: %w", cmd[0], err)
		}
	}
	t.up = true
	return nil
}

func (t *darwinTUN) Stop() error {
	exec.Command("ifconfig", t.cfg.Name, "down").Run()
	t.up = false
	return nil
}

func (t *darwinTUN) IsUp() bool { return t.up }

type windowsTUN struct {
	cfg Config
	up  bool
}

func (t *windowsTUN) Name() string { return t.cfg.Name }

func (t *windowsTUN) Start() error {
	// Windows TUN setup requires the TUN helper service
	return fmt.Errorf("windows TUN requires myvpn-helper service")
}

func (t *windowsTUN) Stop() error {
	t.up = false
	return nil
}

func (t *windowsTUN) IsUp() bool { return t.up }

// Ensure we implement the interface
var _ Interface = (*linuxTUN)(nil)
var _ Interface = (*darwinTUN)(nil)
var _ Interface = (*windowsTUN)(nil)
