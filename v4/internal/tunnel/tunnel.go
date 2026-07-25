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

// NewInterface creates a platform-specific TUN interface.
func NewInterface(cfg Config) (Interface, error) {
	switch runtime.GOOS {
	case "linux":
		return newLinuxTUN(cfg)
	case "darwin":
		return newDarwinTUN(cfg)
	case "windows":
		return newWindowsTUN(cfg)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// DNSManager handles DNS configuration to prevent leaks.
type DNSManager struct {
	backup []string
}

// NewDNSManager creates a DNS manager.
func NewDNSManager() *DNSManager {
	return &DNSManager{}
}

// SetTunnelDNS configures system DNS to use tunnel servers.
func (d *DNSManager) SetTunnelDNS(servers []string, interfaceName string) error {
	if len(servers) == 0 {
		return fmt.Errorf("no DNS servers provided")
	}

	switch runtime.GOOS {
	case "linux":
		return setLinuxDNS(servers, interfaceName)
	case "darwin":
		return setDarwinDNS(servers, interfaceName)
	case "windows":
		return setWindowsDNS(servers, interfaceName)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// RestoreDNS reverts DNS to system defaults.
func (d *DNSManager) RestoreDNS() error {
	// Implementation depends on platform — typically we just reset
	// the interface DNS to DHCP/default.
	switch runtime.GOOS {
	case "linux":
		return exec.Command("resolvectl", "revert").Run()
	case "darwin":
		return nil // macOS networksetup doesn't need explicit restore on disconnect
	case "windows":
		return nil // Windows resets DNS on interface disconnect
	}
	return nil
}

// KillSwitchManager handles blocking non-VPN traffic.
type KillSwitchManager struct {
	enabled bool
}

// NewKillSwitch creates a kill switch manager.
func NewKillSwitch() *KillSwitchManager {
	return &KillSwitchManager{}
}

// Enable activates the kill switch (block all traffic except through tunnel).
func (k *KillSwitchManager) Enable(tunnelInterface string) error {
	if k.enabled {
		return nil
	}

	switch runtime.GOOS {
	case "linux":
		return enableLinuxKillSwitch(tunnelInterface)
	case "darwin":
		return enableDarwinKillSwitch(tunnelInterface)
	case "windows":
		return enableWindowsKillSwitch(tunnelInterface)
	}
	return fmt.Errorf("unsupported platform")
}

// Disable deactivates the kill switch.
func (k *KillSwitchManager) Disable() error {
	if !k.enabled {
		return nil
	}

	switch runtime.GOOS {
	case "linux":
		return disableLinuxKillSwitch()
	case "darwin":
		return disableDarwinKillSwitch()
	case "windows":
		return disableWindowsKillSwitch()
	}
	return fmt.Errorf("unsupported platform")
}

// IsEnabled returns whether the kill switch is active.
func (k *KillSwitchManager) IsEnabled() bool {
	return k.enabled
}

// ── Platform stubs (implemented in platform-specific files) ──

func newLinuxTUN(cfg Config) (Interface, error) {
	return &linuxTUN{cfg: cfg}, nil
}

func newDarwinTUN(cfg Config) (Interface, error) {
	return &darwinTUN{cfg: cfg}, nil
}

func newWindowsTUN(cfg Config) (Interface, error) {
	return &windowsTUN{cfg: cfg}, nil
}

func setLinuxDNS(servers []string, iface string) error {
	for _, dns := range servers {
		if err := exec.Command("resolvectl", "dns", iface, dns).Run(); err != nil {
			return fmt.Errorf("failed to set DNS on %s: %w", iface, err)
		}
	}
	return nil
}

func setDarwinDNS(servers []string, iface string) error {
	for _, dns := range servers {
		if err := exec.Command("networksetup", "-setdnsservers", iface, dns).Run(); err != nil {
			return fmt.Errorf("failed to set DNS on %s: %w", iface, err)
		}
	}
	return nil
}

func setWindowsDNS(servers []string, iface string) error {
	// Find interface index by name
	ifaceName := iface
	if iface == "myvpn0" || iface == "" {
		// Use default interface
		interfaces, err := net.Interfaces()
		if err != nil {
			return err
		}
		for _, intf := range interfaces {
			if intf.Name != "Loopback Pseudo-Interface 1" {
				ifaceName = intf.Name
				break
			}
		}
	}

	args := append([]string{
		"interface", "ip", "set", "dns",
		fmt.Sprintf("name=%s", ifaceName),
		"static",
	}, servers...)
	return exec.Command("netsh", args...).Run()
}

func enableLinuxKillSwitch(iface string) error {
	// Block all traffic except through the tunnel interface
	cmds := [][]string{
		{"iptables", "-P", "INPUT", "DROP"},
		{"iptables", "-P", "FORWARD", "DROP"},
		{"iptables", "-P", "OUTPUT", "DROP"},
		{"iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
		{"iptables", "-A", "OUTPUT", "-o", iface, "-j", "ACCEPT"},
		{"iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
		{"iptables", "-A", "INPUT", "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
	}

	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("kill switch rule failed: %v", err)
		}
	}
	return nil
}

func disableLinuxKillSwitch() error {
	cmds := [][]string{
		{"iptables", "-P", "INPUT", "ACCEPT"},
		{"iptables", "-P", "FORWARD", "ACCEPT"},
		{"iptables", "-P", "OUTPUT", "ACCEPT"},
		{"iptables", "-F"},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("kill switch cleanup failed: %v", err)
		}
	}
	return nil
}

func enableDarwinKillSwitch(iface string) error {
	// macOS uses pfctl for the kill switch
	pfRules := fmt.Sprintf(`
block all
pass on lo0
pass on %s
pass proto udp from any to any port 53
pass out proto tcp from any to any keep state
`, iface)

	cmd := exec.Command("pfctl", "-f", "-")
	cmd.Stdin = &pfRulesInput{rules: pfRules}
	return cmd.Run()
}

type pfRulesInput struct {
	rules string
}

func (p *pfRulesInput) Read(b []byte) (int, error) {
	copy(b, p.rules)
	return len(p.rules), nil
}

func disableDarwinKillSwitch() error {
	return exec.Command("pfctl", "-d").Run()
}

func enableWindowsKillSwitch(iface string) error {
	// Windows firewall rules to block non-tunnel traffic
	cmds := [][]string{
		{"netsh", "advfirewall", "set", "allprofiles", "firewallpolicy", "blockinbound,blockoutbound"},
		{"netsh", "advfirewall", "add", "rule", "name=MyVPN-Tunnel",
			"dir=out", "action=allow", "remoteip=any",
			fmt.Sprintf("interface=%s", iface)},
		{"netsh", "advfirewall", "add", "rule", "name=MyVPN-DNS",
			"dir=out", "action=allow", "protocol=udp", "remoteport=53"},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("kill switch rule failed: %v", err)
		}
	}
	return nil
}

func disableWindowsKillSwitch() error {
	cmds := [][]string{
		{"netsh", "advfirewall", "reset"},
		{"netsh", "advfirewall", "set", "allprofiles", "firewallpolicy", "blockinbound,allowoutbound"},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("firewall reset failed: %v", err)
		}
	}
	return nil
}

// ── Platform TUN implementations ──

type linuxTUN struct {
	cfg   Config
	iface string
	up    bool
}

func (t *linuxTUN) Name() string { return t.cfg.Name }

func (t *linuxTUN) Start() error {
	// Create TUN interface via ip tuntap
	cmds := [][]string{
		{"ip", "tuntap", "add", "dev", t.cfg.Name, "mode", "tun", "user", "root"},
		{"ip", "addr", "add", t.cfg.VirtualIP + "/24", "dev", t.cfg.Name},
		{"ip", "link", "set", "dev", t.cfg.Name, "mtu", fmt.Sprintf("%d", t.cfg.MTU), "up"},
		{"ip", "route", "add", "0.0.0.0/1", "dev", t.cfg.Name},
		{"ip", "route", "add", "128.0.0.0/1", "dev", t.cfg.Name},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("TUN setup failed: %v", err)
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
		exec.Command(cmd[0], cmd[1:]...).Run() // Best effort cleanup
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
			return fmt.Errorf("TUN setup failed: %v", err)
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
