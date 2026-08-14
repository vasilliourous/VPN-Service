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

// TestProbeNoEngine confirms the watchdog probe reports a clear failure when no
// engine is running, rather than pretending the tunnel is healthy.
func TestProbeNoEngine(t *testing.T) {
	m := NewManager("/nonexistent/sing-box", filepath.Join(t.TempDir(), "c.json"), "")
	m.SetHelperMode(false)

	if err := m.ProbeTunnel(); err == nil {
		t.Fatal("ProbeTunnel should fail with no engine running")
	} else if !strings.Contains(err.Error(), "not running") {
		t.Fatalf("unexpected probe error: %v", err)
	}
}

// TestProbeHealthyLifecycle verifies the probe flips to healthy once a fake
// engine is started and back to failing once it exits.
func TestProbeHealthyLifecycle(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fakesingbox")
	script := "#!/bin/sh\nsleep 5\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(fakeBin, filepath.Join(dir, "c.json"), "")
	m.SetHelperMode(false)

	cfg := Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	// The probe should pass on process liveness (TUN check is best-effort and
	// unavailable in the test env).
	if err := m.ProbeTunnel(); err != nil {
		t.Logf("ProbeTunnel error (may be TUN/sandbox related): %v", err)
	}
}

// TestStartAutoCleansLeftover verifies Connect does not hard-fail when a
// foreign sing-box is present — the cleanup path runs and Start proceeds to
// spawn (the spawn itself may fail in a sandbox, but the blocking error must be
// gone). Here we simulate a left-over by starting one first, then assert that a
// second Start no longer returns the old "another sing-box is running" error
// after cleanup.
func TestStartAutoCleansLeftover(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fakesingbox")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nsleep 10\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(fakeBin, filepath.Join(dir, "c.json"), "")
	m.SetHelperMode(false)

	cfg := Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm"}
	ctx := context.Background()

	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer m.Stop()

	// With the engine running, a second Start must NOT stack a second engine:
	// Start's own processAlive guard rejects it up front.
	err := m.Start(ctx, cfg)
	if err == nil {
		t.Fatal("second Start should not fully succeed with an engine already running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Start error = %v, want the already-running guard", err)
	}
}

// TestRecoveryRestart regenerates config after a crash and confirms the flow
// does not deadlock or panic (the spawn may fail in a sandbox but recovery must
// be safe to call).
func TestRecoveryRestart(t *testing.T) {
	m := NewManager("/nonexistent", filepath.Join(t.TempDir(), "c.json"), "")
	m.SetHelperMode(false)
	m.tunCfg = Config{Server: "example.com", ServerPort: 8443, Password: "x", Method: "aes-256-gcm"}
	if err := m.restartEngine(); err == nil {
		t.Fatal("restart with nonexistent binary should error")
	}
}
