// Package-level watchdog and self-healing for the sing-box tunnel.
//
// For a distributed student client the tunnel must not silently degrade into
// the "reports Connected but passes no traffic" state that plagued earlier
// builds. The watchdog periodically probes the live tunnel; when it stops
// passing traffic it walks an escalation ladder automatically:
//
//	1. restart sing-box in place,
//	2. if that does not help, kill leftover sing-box processes and clear a
//	   stale myvpn0 TUN, then start a fresh engine,
//	3. if still broken, report a clear degraded state instead of claiming
//	   Connected.
//
// Connect() also auto-cleans a leftover sing-box / stale TUN instead of
// hard-failing with a "close it in Task Manager" dead end for students.

package manager

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"
)

// WatchdogProbeInterval is how often the tunnel is probed while connected.
const WatchdogProbeInterval = 10 * time.Second

// Watchdog stage labels exposed for UI/diagnostic reporting.
type ProbeStage string

const (
	StageHealthy      ProbeStage = "healthy"
	StageChecking     ProbeStage = "checking"
	StageRestarting   ProbeStage = "restart"
	StageFullReset    ProbeStage = "full-reset"
	StageDegraded     ProbeStage = "degraded"
	StageDisconnected ProbeStage = "stopped"
)

// ProbeCallback is invoked with the result of each watchdog probe. healthy is
// true when the tunnel is passing traffic; stage names the last action taken;
// err carries the underlying error (nil on a healthy probe).
type ProbeCallback func(healthy bool, stage string, err error)

// SetProbeCallback installs a callback invoked per watchdog probe result.
// Pass nil to disable. Safe to call at any time.
func (m *Manager) SetProbeCallback(cb ProbeCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watchdogOnProbe = cb
}

// TunnelHealthy reports whether the last probe found the tunnel passing
// traffic. Before the first probe it returns true (assume healthy until proven
// otherwise) so the UI does not flap right after Connect.
func (m *Manager) TunnelHealthy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tunnelHealthy
}

// StartWatchdog launches the periodic tunnel-health watchdog. It runs until
// the tunnel is stopped. Safe to call repeatedly — a running watchdog is left
// untouched.
func (m *Manager) StartWatchdog() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watchdogStop != nil {
		return
	}
	m.tunnelHealthy = true
	stop := make(chan struct{})
	m.watchdogStop = stop
	go m.watchdogLoop(stop)
}

// StopWatchdog halts the periodic probe loop (idempotent).
func (m *Manager) StopWatchdog() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watchdogStop == nil {
		return
	}
	close(m.watchdogStop)
	m.watchdogStop = nil
	m.tunnelHealthy = false
}

// ProbeTunnel performs a best-effort, dependency-free check that the tunnel is
// actually passing traffic. It layers several signals:
//
//  1. the sing-box process is alive and tracked,
//  2. the myvpn0 TUN interface is present and up (best effort),
//  3. an in-band TCP dial out through the tunnel completes quickly.
//
// The dial targets the configured VPN server endpoint: when the tunnel is
// healthy the server responds; when the whole tunnel stack is broken the dial
// fails. This cannot distinguish "tunnel broken" from "server down", but the
// recovery ladder only escalates to a full reset, which is safe in either case
// and restores the dominant failure mode autonomously.
func (m *Manager) ProbeTunnel() error {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("sing-box process not running")
	}
	if !m.processAlive() {
		return fmt.Errorf("sing-box process exited")
	}

	if up, ok := tunInterfaceUp(); ok && !up {
		return fmt.Errorf("TUN interface myvpn0 is down")
	}

	cfg, err := m.currentConfig()
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.Server, fmt.Sprintf("%d", cfg.ServerPort))
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("tunnel cannot reach server %s: %w", addr, err)
	}
	_ = conn.Close()
	return nil
}

// currentConfig returns the retained tunnel config used to (re)start the tunnel.
func (m *Manager) currentConfig() (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tunCfg.Server == "" {
		return Config{}, fmt.Errorf("no tunnel config retained")
	}
	return m.tunCfg, nil
}

// watchdogLoop runs the periodic probe and recovery until the stop channel is
// closed.
func (m *Manager) watchdogLoop(stop chan struct{}) {
	ticker := time.NewTicker(WatchdogProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.runProbeCycle()
		}
	}
}

// runProbeCycle runs one probe and, if it fails, one recovery escalation.
func (m *Manager) runProbeCycle() {
	if err := m.ProbeTunnel(); err == nil {
		m.markHealthy()
	} else {
		m.escalate(err)
	}
}

// escalate walks the recovery ladder: restart in place, then full reset, then
// degraded.
func (m *Manager) escalate(probeErr error) {
	m.mu.Lock()
	m.tunnelHealthy = false
	m.mu.Unlock()

	m.notifyProbe(StageRestarting, "restart", probeErr)
	if err := m.restartEngine(); err != nil {
		log.Printf("watchdog: restart failed: %v", err)
	}

	time.Sleep(2 * time.Second)
	if err := m.ProbeTunnel(); err == nil {
		m.markHealthy()
		return
	}

	m.notifyProbe(StageFullReset, "full-reset", probeErr)
	if err := m.fullReset(); err != nil {
		log.Printf("watchdog: full reset failed: %v", err)
		m.notifyProbe(StageDegraded, "degraded", err)
		return
	}

	time.Sleep(2 * time.Second)
	if err := m.ProbeTunnel(); err == nil {
		m.markHealthy()
		return
	}
	m.notifyProbe(StageDegraded, "degraded", fmt.Errorf("tunnel still not passing traffic after recovery"))
}

// markHealthy records a healthy tunnel and notifies the callback.
func (m *Manager) markHealthy() {
	m.mu.Lock()
	m.tunnelHealthy = true
	m.mu.Unlock()
	m.notifyProbe(StageHealthy, "healthy", nil)
}

// notifyProbe invokes the registered callback with the outcome, if any.
func (m *Manager) notifyProbe(stage ProbeStage, humanStage string, err error) {
	m.mu.Lock()
	cb := m.watchdogOnProbe
	healthy := m.tunnelHealthy
	m.mu.Unlock()
	if cb == nil {
		return
	}
	if humanStage == "" {
		humanStage = string(stage)
	}
	cb(healthy, humanStage, err)
}

// restartEngine regenerates the config and starts a fresh sing-box process
// without tearing down the interface.
func (m *Manager) restartEngine() error {
	cfg, err := m.currentConfig()
	if err != nil {
		return err
	}
	if err := m.killProcess(); err != nil {
		log.Printf("watchdog: stopping engine for restart: %v", err)
	}
	b, err := generateConfig(cfg)
	if err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if err := m.startDirect(context.Background(), b); err == nil || err == errEngineAlreadyRunning {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("sing-box would not restart after 3 attempts")
}

// fullReset kills leftover sing-box processes, clears the myvpn0 TUN, and
// starts a clean engine.
func (m *Manager) fullReset() error {
	if err := m.killProcess(); err != nil {
		log.Printf("watchdog: stopping engine for full reset: %v", err)
	}
	killForeignEngines()
	_ = removeStaleTUN()

	cfg, err := m.currentConfig()
	if err != nil {
		return err
	}
	b, err := generateConfig(cfg)
	if err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if err := m.startDirect(context.Background(), b); err == nil || err == errEngineAlreadyRunning {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("sing-box would not start after full reset")
}

// killProcess stops the currently tracked engine process, waiting for it to
// exit. No-op when nothing is tracked.
func (m *Manager) killProcess() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	if err := m.cmd.Process.Kill(); err != nil {
		return err
	}
	if m.exited != nil {
		select {
		case <-m.exited:
		case <-time.After(shutdownTimeout):
		}
	}
	m.cmd = nil
	m.exited = nil
	return nil
}
