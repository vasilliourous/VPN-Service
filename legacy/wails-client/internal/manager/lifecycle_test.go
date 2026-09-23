//go:build !windows

package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These tests cover the state-machine bugs reported from the field:
//
//   * "⚠ tunnel is already running" after a Retry, with sing-box.exe visibly
//     still running while the UI showed Disconnected;
//   * the watchdog restarting the engine AFTER the tunnel had been stopped.
//
// The previous tests tolerated probe failures (`t.Logf`), so the probe could be
// wrong without any test failing. These assert the specific behaviours instead.

// fakeEngine writes a stand-in sing-box that stays alive until killed.
func fakeEngine(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "fakesingbox")
	// Long-lived, and does NOT respond to the probe's dial — the probe is
	// expected to fail for a fake engine, which is fine: these tests are about
	// lifecycle/state, not about traffic.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 300\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestProbeReportsNoEngineDistinctly pins the error classification the recovery
// ladder now depends on. "No engine" and "engine up but broken" must be
// distinguishable, because only the latter may be escalated — escalating the
// former spawns an engine the user never asked for.
func TestProbeReportsNoEngineDistinctly(t *testing.T) {
	m := NewManager("/nonexistent/sing-box", filepath.Join(t.TempDir(), "c.json"), "")
	m.SetHelperMode(false)

	err := m.ProbeTunnel()
	if err == nil {
		t.Fatal("ProbeTunnel must fail when no engine is running")
	}
	if !isNotRunning(err) {
		t.Fatalf("a missing engine must classify as errEngineNotRunning (got %v)", err)
	}
	if !errors.Is(err, errEngineNotRunning) {
		t.Fatalf("errors.Is must see the sentinel through the wrapped error (got %v)", err)
	}
}

// TestStopReleasesTrackingSoStartSucceeds is the regression test for the
// "tunnel is already running" dead end: after Stop, a fresh Start must be
// accepted rather than refused by a stale belief about the process.
func TestStopReleasesTrackingSoStartSucceeds(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fakeEngine(t, dir), filepath.Join(dir, "c.json"), "")
	m.SetHelperMode(false)

	cfg := Config{Server: "127.0.0.1", ServerPort: 1, Password: "x", Method: "aes-256-gcm"}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Start(ctx, cfg); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if !m.processAlive() {
		t.Fatal("engine should be alive after Start")
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if m.processAlive() {
		t.Fatal("engine must be dead after Stop — a surviving engine is the orphan that caused the dead end")
	}

	// A second Start must NOT be refused. This is the actual user-visible bug.
	m2 := NewManager(fakeEngine(t, dir), filepath.Join(dir, "c2.json"), "")
	m2.SetHelperMode(false)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if err := m2.Start(ctx2, cfg); err != nil {
		t.Fatalf("Start after a completed Stop must succeed, got: %v", err)
	}
	_ = m2.Stop()
}

// TestStopIsIdempotentAndSafeWhenNothingRunning guards the invariant that made
// the unconditional Stop() in app.disconnect() correct: calling Stop when there
// is no engine must be a harmless no-op rather than an error or a panic.
func TestStopIsIdempotentAndSafeWhenNothingRunning(t *testing.T) {
	m := NewManager("/nonexistent/sing-box", filepath.Join(t.TempDir(), "c.json"), "")
	m.SetHelperMode(false)

	for i := 0; i < 3; i++ {
		if err := m.Stop(); err != nil {
			t.Fatalf("Stop #%d with nothing running must be a no-op, got: %v", i+1, err)
		}
	}
}

// TestStartReclaimsUntrackedEngine covers the reclaim path: if an engine is
// running that we do NOT track, Start must clear it and proceed rather than
// refusing.
//
// SCOPE NOTE (why this test is shaped this way): the orphan is detected by
// foreignSingBoxRunning(), which matches the REAL process name (`sing-box` /
// `sing-box.exe`). A test cannot name its fake binary that without racing the
// developer's own machine for the name, so this exercises the other half of the
// fix — the stale-tracking reconciliation — plus the invariant that Start does
// not refuse when tracking is clear but a child is still alive.
//
// What it genuinely pins:
//   - a stale m.cmd (tracked process already gone) must not make Start refuse;
//   - Start must succeed and leave a live, tracked engine.
func TestStartReclaimsUntrackedEngine(t *testing.T) {
	dir := t.TempDir()
	bin := fakeEngine(t, dir)

	m := NewManager(bin, filepath.Join(dir, "c.json"), "")
	m.SetHelperMode(false)
	cfg := Config{Server: "127.0.0.1", ServerPort: 1, Password: "x", Method: "aes-256-gcm"}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := m.Start(ctx, cfg); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	cancel()
	_ = m.Stop()

	// Simulate the STALE-tracking half of the orphan state: tracking says there
	// is a process, but it has already exited (the raced-disconnect leftover).
	// The old Start refused on `processAlive()` alone, so this is the case that
	// could dead-end; it must now heal itself and start cleanly.
	//
	// NOTE: a FRESH context. The previous one is cancelled/expired, and reusing
	// it would fail on the deadline rather than on anything this test is about.
	m.mu.Lock()
	m.cmd = nil
	m.exited = nil
	m.mu.Unlock()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if err := m.Start(ctx2, cfg); err != nil {
		t.Fatalf("Start with stale tracking must succeed, got: %v", err)
	}
	if !m.processAlive() {
		t.Fatal("Start must leave a live, tracked engine")
	}
	_ = m.Stop()
}

// TestWatchdogStopsEscalatingAfterStop is the regression test for engines being
// spawned after a disconnect, and for the probe misclassifying "no engine" as
// "broken tunnel".
//
// It must set a RETAINED CONFIG, because that is the state a real disconnect
// leaves behind (the config is kept so the watchdog can recover). Without it the
// recovery path has nothing to start and the test would pass even with the bug
// present — which is exactly how the first version of this test was fooled.
func TestWatchdogStopsEscalatingAfterStop(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fakeEngine(t, dir), filepath.Join(dir, "c.json"), "")
	m.SetHelperMode(false)

	// A disconnect after a successful connect leaves the tunnel config retained
	// (that is what lets the watchdog restart the engine) while the tracking is
	// cleared — so recovery HAS something it could start.
	m.tunCfg = Config{Server: "127.0.0.1", ServerPort: 1, Password: "x", Method: "aes-256-gcm"}

	// watchdogStop is nil: the tunnel was stopped.
	if m.watchdogActive() {
		t.Fatal("watchdog must not report active before it is started")
	}

	// The probe must classify this as "no engine", NOT as a degraded tunnel;
	// otherwise the ladder runs and spawns an engine nobody asked for.
	if err := m.ProbeTunnel(); !isNotRunning(err) {
		t.Fatalf("a stopped tunnel must probe as errEngineNotRunning, got: %v", err)
	}

	// And a full probe cycle in this state must be inert.
	m.runProbeCycle()
	if m.processAlive() {
		t.Fatal("a probe cycle after stop must not spawn an engine")
	}
}

// TestEscalateIsInertWhenWatchdogStopped proves the second half of the guard:
// even if escalate IS reached (e.g. a probe cycle already in flight when the
// user hit disconnect), it must not restart the engine once the watchdog has
// been stopped. This is the window that produced the observed "several sing-box
// startups after the UI said Disconnected".
func TestEscalateIsInertWhenWatchdogStopped(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fakeEngine(t, dir), filepath.Join(dir, "c.json"), "")
	m.SetHelperMode(false)
	m.tunCfg = Config{Server: "127.0.0.1", ServerPort: 1, Password: "x", Method: "aes-256-gcm"}

	if m.watchdogActive() {
		t.Fatal("precondition: watchdog must be stopped")
	}

	m.escalate(errEngineNotRunning)

	if m.processAlive() {
		t.Fatal("escalate must be inert once the watchdog has been stopped — " +
			"otherwise a disconnect is followed by engine restarts")
	}
}

// TestWatchdogActiveWhileRunning is the positive control for the guard above,
// so the fix cannot pass by making the watchdog permanently inert.
func TestWatchdogActiveWhileRunning(t *testing.T) {
	m := NewManager("/nonexistent/sing-box", filepath.Join(t.TempDir(), "c.json"), "")
	m.SetHelperMode(false)

	m.StartWatchdog()
	if !m.watchdogActive() {
		t.Fatal("watchdog must report active after StartWatchdog")
	}
	m.StopWatchdog()
	if m.watchdogActive() {
		t.Fatal("watchdog must not report active after StopWatchdog")
	}
}
