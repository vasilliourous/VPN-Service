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
	cfg := Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm", BindInterface: "eth0"}
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
	if !strings.Contains(s, `"sniff": true`) {
		t.Errorf("tun inbound must have sniff enabled (required for the protocol:dns rule):\n%s", s)
	}
	if !strings.Contains(s, `"server": "dns-direct"`) || !strings.Contains(s, `"any"`) {
		t.Errorf("DNS rule outbound:any -> dns-direct must be present (prevents the server-domain resolution loop, sing-box #2207):\n%s", s)
	}
	if !strings.Contains(s, `"bind_interface": "eth0"`) {
		t.Errorf("proxy outbound must carry bind_interface:\n%s", s)
	}
	if !strings.Contains(s, `"action"`) {
		t.Errorf("route rules must use rule actions (sing-box 1.11+ format):\n%s", s)
	}
	if !strings.Contains(s, `"ip_cidr"`) {
		t.Errorf("route rule excluding the VPN server IP (direct) must be present:\n%s", s)
	}
}
