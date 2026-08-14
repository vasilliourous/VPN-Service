//go:build !windows

package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLifecycle verifies process liveness tracking (the exited channel).
// Guards against regressions to signal-based checks — Process.Signal(0) is
// not supported on Windows and previously made Connect() hang in cmd.Wait().
func TestLifecycle(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fakesingbox")
	script := "#!/bin/sh\nsleep 3\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	m := NewManager(fakeBin, configPath, "")
	m.SetHelperMode(false)

	cfg := Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !m.IsRunning() {
		t.Fatal("IsRunning = false right after Start")
	}
	if st := m.State(); st != "running" {
		t.Fatalf("State = %q, want running", st)
	}

	// The fake sing-box exits on its own after ~3s — the exited channel must
	// flip IsRunning/State without any signal polling.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if !m.IsRunning() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if m.IsRunning() {
		t.Fatal("IsRunning still true after the process exited")
	}
	if st := m.State(); st != "crashed" {
		t.Fatalf("State after exit = %q, want crashed", st)
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := m.State(); st != "stopped" {
		t.Fatalf("State after Stop = %q, want stopped", st)
	}
}

// TestImmediateExit verifies the startup probe surfaces sing-box stderr.
func TestImmediateExit(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "failsingbox")
	script := "#!/bin/sh\necho 'configure tun interface: Access is denied.' >&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(fakeBin, filepath.Join(dir, "config.json"), "")
	m.SetHelperMode(false)

	cfg := Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := m.Start(ctx, cfg)
	if err == nil {
		t.Fatal("Start succeeded, want error")
	}
	if !strings.Contains(err.Error(), "Access is denied") {
		t.Fatalf("error %q does not surface sing-box stderr", err.Error())
	}
}

// TestGeneratedConfig locks in the hard-won config decisions:
//   - DNS must go through the tunnel (final = dns-tunnel) — a "direct" detour
//     loops back into the TUN and kills DNS resolution;
//   - strict_route must be OFF — its WFP filters block sing-box's own
//     outbound on some Windows machines and kill all connectivity.
func TestGeneratedConfig(t *testing.T) {
	cfg := Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm"}
	data, err := generateConfig(cfg)
	if err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, `"final": "dns-tunnel"`) {
		t.Errorf("dns.final must be dns-tunnel, got:\n%s", s)
	}
	if !strings.Contains(s, `"detour": "proxy"`) {
		t.Errorf("dns-tunnel must have an explicit detour to the proxy outbound:\n%s", s)
	}
	if strings.Contains(s, `"strict_route": true`) {
		t.Errorf("strict_route must not be enabled:\n%s", s)
	}
	if !strings.Contains(s, `"auto_route": true`) {
		t.Errorf("auto_route must be enabled:\n%s", s)
	}
	if !strings.Contains(s, `"auto_detect_interface": true`) {
		t.Errorf("auto_detect_interface must be enabled:\n%s", s)
	}
	if strings.Contains(s, `"sniff": true`) {
		t.Errorf("tun inbound must NOT set legacy sniff:true (removed in sing-box 1.13, breaks routing); use the sniff rule action instead:\n%s", s)
	}
	if !strings.Contains(s, `"action": "sniff"`) {
		t.Errorf("route rules must sniff via the modern sniff rule action:\n%s", s)
	}
	if !strings.Contains(s, `"server": "dns-direct"`) || !strings.Contains(s, `"default_domain_resolver"`) {
		t.Errorf("route.default_domain_resolver -> dns-direct must be present (prevents the server-domain resolution loop, sing-box #2207):\n%s", s)
	}
	if !strings.Contains(s, `"connect_timeout"`) {
		t.Errorf("direct outbound must carry a non-empty dialer option (1.12 rejects DNS detours to empty direct outbounds):\n%s", s)
	}
	if !strings.Contains(s, `"hijack-dns"`) {
		t.Errorf("route rules must use the hijack-dns action for DNS (sing-box 1.11+ format):\n%s", s)
	}
	if !strings.Contains(s, `"action": "route"`) {
		t.Errorf("rules that route to an outbound must use the modern action form (action: route + outbound):\n%s", s)
	}
	if !strings.Contains(s, `"ip_cidr"`) {
		t.Errorf("route rule excluding the VPN server IP (direct) must be present:\n%s", s)
	}
}

// TestGeneratedConfigUOT verifies the UDP-over-TCP branch:
//   - raw-UDP tiers (no advertised uot_port) must produce config byte-identical
//     in behaviour to before — no udp_over_tcp, no proxy-uot outbound;
//   - Strike with an advertised uot_port gets a dedicated UoT outbound
//     (udp_over_tcp: true) + a network:udp route rule, while TCP stays on the
//     standard port/outbound.
func TestGeneratedConfigUOT(t *testing.T) {
	base := Config{Server: "example.com", ServerPort: 8445, Password: "x", Method: "aes-256-gcm"}

	// 1. No UDP tier: nothing changes.
	raw, err := generateConfig(base)
	if err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	rawS := string(raw)
	if strings.Contains(rawS, `"udp_over_tcp"`) || strings.Contains(rawS, "proxy-uot") {
		t.Errorf("non-UDP tier must NOT emit UoT config:\n%s", rawS)
	}

	// 2. UDP tier but NO advertised uot_port: still raw UDP (server can't UoT).
	udpOnly := base
	udpOnly.UDPRelay = true
	raw2, err := generateConfig(udpOnly)
	if err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	if strings.Contains(string(raw2), `"udp_over_tcp"`) || strings.Contains(string(raw2), "proxy-uot") {
		t.Errorf("UDPRelay without advertised uot_port must NOT emit UoT config (shadowsocks-rust RSTs it):\n%s", raw2)
	}

	// 3. UDP tier + advertised uot_port: UoT outbound + UDP route rule.
	uot := base
	uot.UDPRelay = true
	uot.ServerPortUOT = 8446
	uotS, err := generateConfig(uot)
	if err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	s := string(uotS)
	if !strings.Contains(s, `"tag": "proxy-uot"`) {
		t.Errorf("UoT tier must add a proxy-uot outbound:\n%s", s)
	}
	if !strings.Contains(s, `"udp_over_tcp": true`) {
		t.Errorf("proxy-uot outbound must set udp_over_tcp: true:\n%s", s)
	}
	if !strings.Contains(s, `"server_port": 8446`) {
		t.Errorf("proxy-uot must point at the advertised uot_port (8446):\n%s", s)
	}
	if !strings.Contains(s, `"network": "udp"`) || !strings.Contains(s, `"outbound": "proxy-uot"`) || !strings.Contains(s, `"action": "route"`) {
		t.Errorf("a network:udp route rule must pin UDP to proxy-uot using the modern action form:\n%s", s)
	}
	if !strings.Contains(s, `"tag": "proxy"`) || !strings.Contains(s, `"server_port": 8445`) {
		t.Errorf("the TCP proxy outbound must stay on the standard port (8445):\n%s", s)
	}
	if !strings.Contains(s, `"final": "proxy"`) {
		t.Errorf("route final must remain proxy (TCP unchanged):\n%s", s)
	}
}
