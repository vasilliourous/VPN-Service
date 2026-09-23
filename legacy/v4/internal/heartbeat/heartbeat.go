// Package heartbeat handles periodic communication with the hub server.
//
// The heartbeat serves three purposes:
//  1. Suspension check — server can mark a code as suspended at any time
//  2. Staged rollout — receive update signals gated by fingerprint hash
//  3. Grace period — tracks how long the VPN works without a heartbeat
//
// Heartbeat interval starts at 5 minutes and doubles on failure up to 2 hours.
// A successful heartbeat resets the interval to 5 minutes.
package heartbeat

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"
)

// Default intervals.
const (
	MinInterval    = 5 * time.Minute
	MaxInterval    = 2 * time.Hour
	GracePeriod    = 7 * 24 * time.Hour // 7 days
)

// Response from the heartbeat endpoint.
type Response struct {
	Status    string `json:"status"`
	ServerTime string `json:"server_time"`
	Tier      string `json:"tier,omitempty"`

	// Update signal (staged rollout)
	UpdateAvailable string `json:"update_available,omitempty"`
	UpdateURL       string `json:"update_url,omitempty"`
	UpdateSHA256    string `json:"update_sha256,omitempty"`
	UpdateWindows   string `json:"update_windows,omitempty"`
	UpdateMacOSIntel string `json:"update_macos_intel,omitempty"`
	UpdateMacOSARM  string `json:"update_macos_arm,omitempty"`

	// Config refresh
	ServerConfig *ServerConfig `json:"server_config,omitempty"`
	UDPRelay     bool         `json:"udp_relay,omitempty"`
}

// ServerConfig holds Shadowsocks connection parameters.
type ServerConfig struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
}

// Result contains the heartbeat outcome.
type Result struct {
	Success bool
	Error   error
	Resp    *Response
}

// Callback is invoked after each heartbeat attempt.
type Callback func(Result)

// Heartbeat manages the periodic heartbeat loop.
type Heartbeat struct {
	hubURL     string
	code       string
	fingerprint string
	interval   time.Duration
	callback   Callback

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	failures int
	client   *http.Client
}

// New creates a new Heartbeat manager.
func New(hubURL, code, fingerprint string, callback Callback) *Heartbeat {
	return &Heartbeat{
		hubURL:      hubURL,
		code:        code,
		fingerprint: fingerprint,
		interval:    MinInterval,
		callback:    callback,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Start begins the heartbeat loop.
func (h *Heartbeat) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return
	}

	h.running = true
	h.stopCh = make(chan struct{})

	go h.loop()
}

// Stop terminates the heartbeat loop.
func (h *Heartbeat) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}

	close(h.stopCh)
	h.running = false
}

// IsRunning returns whether the heartbeat loop is active.
func (h *Heartbeat) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// loop is the main heartbeat goroutine.
func (h *Heartbeat) loop() {
	timer := time.NewTimer(h.getInterval())
	defer timer.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-timer.C:
			result := h.beat()

			// Notify callback
			if h.callback != nil {
				h.callback(result)
			}

			// Adjust interval based on result
			h.mu.Lock()
			if result.Success {
				h.failures = 0
				h.interval = MinInterval
			} else {
				h.failures++
				// Exponential backoff: 5min → 10min → 20min → ... → 2h max
				h.interval = time.Duration(math.Min(
					float64(MinInterval)*math.Pow(2, float64(h.failures)),
					float64(MaxInterval),
				))
			}
			interval := h.interval
			h.mu.Unlock()

			timer.Reset(interval)
		}
	}
}

// beat sends a single heartbeat request.
func (h *Heartbeat) beat() Result {
	url := fmt.Sprintf("%s/api/heartbeat?code=%s&fp=%s",
		h.hubURL, h.code, h.fingerprint)

	resp, err := h.client.Get(url)
	if err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat request failed: %w", err),
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat read failed: %w", err),
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var hbResp Response
	if err := json.Unmarshal(body, &hbResp); err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat decode failed: %w", err),
		}
	}

	if hbResp.Status != "ok" {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat status: %s", hbResp.Status),
		}
	}

	return Result{
		Success: true,
		Resp:    &hbResp,
	}
}

// getInterval returns the current interval safely.
func (h *Heartbeat) getInterval() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interval
}

// Failures returns the consecutive heartbeat failure count.
func (h *Heartbeat) Failures() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failures
}

// RemainingGracePeriod returns the estimated remaining grace period
// based on the last successful heartbeat and current failures.
func (h *Heartbeat) RemainingGracePeriod(lastHeartbeatOK int64) time.Duration {
	if lastHeartbeatOK == 0 {
		return GracePeriod // Full grace period from first launch
	}

	elapsed := time.Since(time.Unix(lastHeartbeatOK, 0))
	remaining := GracePeriod - elapsed
	if remaining < 0 {
		return 0
	}
	return remaining
}
