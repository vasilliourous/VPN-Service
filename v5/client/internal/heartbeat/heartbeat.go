// Package heartbeat handles periodic communication with the hub server.
//
// The heartbeat serves three purposes:
//  1. Suspension check — server can mark a code as suspended at any time
//  2. Staged rollout — receive update signals gated by fingerprint hash
//  3. Grace period — tracks how long the VPN works without a heartbeat
//
// Heartbeat interval starts at 5 minutes and doubles on failure up to 2 hours.
// A successful heartbeat resets the interval to 5 minutes.
//
// Hardening: context propagation, jitter to prevent thundering herd, callback timeout,
// comprehensive error tracking.
package heartbeat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Default intervals.
const (
	MinInterval      = 5 * time.Minute
	MaxInterval      = 2 * time.Hour
	GracePeriod      = 7 * 24 * time.Hour // 7 days
	CallbackTimeout  = 5 * time.Second
	JitterPercent    = 0.1 // 10% jitter added to intervals
)

// Error types for heartbeat failures.
type HeartbeatError struct {
	Code    int
	Message string
	Wrapped error
}

func (e *HeartbeatError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("heartbeat error (%d): %s: %v", e.Code, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("heartbeat error (%d): %s", e.Code, e.Message)
}

func (e *HeartbeatError) Unwrap() error {
	return e.Wrapped
}

// Response from the heartbeat endpoint.
type Response struct {
	Status    string `json:"status"`
	ServerTime string `json:"server_time"`
	Tier      string `json:"tier,omitempty"`

	// Update signal (staged rollout)
	UpdateAvailable string `json:"update_available,omitempty"`
	UpdateURL       string `json:"update_url,omitempty"`
	UpdateSHA256    string `json:"update_sha256,omitempty"`
	UpdateLinux     string `json:"update_linux,omitempty"`
	UpdateWindows   string `json:"update_windows,omitempty"`
	UpdateMacOSIntel string `json:"update_macos_intel,omitempty"`
	UpdateMacOSARM  string `json:"update_macos_arm,omitempty"`

	// Config refresh
	ServerConfig *ServerConfig `json:"server_config,omitempty"`
	UDPRelay     bool          `json:"udp_relay,omitempty"`
}

// ServerConfig holds Shadowsocks connection parameters.
type ServerConfig struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
}

// Validate checks that the heartbeat response is well-formed.
func (r *Response) Validate() error {
	if r.Status == "" {
		return fmt.Errorf("empty heartbeat status")
	}
	if r.ServerConfig != nil {
		if r.ServerConfig.Server == "" {
			return fmt.Errorf("heartbeat response has empty server address")
		}
		if r.ServerConfig.ServerPort <= 0 || r.ServerConfig.ServerPort > 65535 {
			return fmt.Errorf("heartbeat response has invalid port: %d", r.ServerConfig.ServerPort)
		}
		if r.ServerConfig.Password == "" {
			return fmt.Errorf("heartbeat response has empty password")
		}
	}
	return nil
}

// Result contains the heartbeat outcome.
type Result struct {
	Success  bool
	Error    error
	Resp     *Response
	Latency  time.Duration
	Attempts int
}

// Callback is invoked after each heartbeat attempt.
type Callback func(Result)

// Heartbeat manages the periodic heartbeat loop.
type Heartbeat struct {
	hubURL      string
	code        string
	fingerprint string
	interval    time.Duration
	callback    Callback

	mu          sync.Mutex
	running     bool
	stopCh      chan struct{}
	failures    int
	totalBeats  int64
	successBeats int64
	client      *http.Client
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
			Transport: &http.Transport{
				MaxIdleConns:    2,
				IdleConnTimeout: 30 * time.Second,
			},
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

// Stats returns heartbeat statistics.
func (h *Heartbeat) Stats() (total, successes, failures int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return int(h.totalBeats), int(h.successBeats), h.failures
}

// loop is the main heartbeat goroutine.
func (h *Heartbeat) loop() {
	timer := time.NewTimer(h.intervalWithJitter())
	defer timer.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-timer.C:
			start := time.Now()
			result := h.beat()
			result.Latency = time.Since(start)

			// Update stats
			h.mu.Lock()
			h.totalBeats++
			if result.Success {
				h.successBeats++
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
			result.Attempts = h.failures + 1
			interval := h.interval
			h.mu.Unlock()

			// Notify callback with timeout safety
			if h.callback != nil {
				h.safeCallback(result)
			}

			timer.Reset(interval)
		}
	}
}

// safeCallback invokes the callback with a timeout guard to prevent
// a slow callback from blocking the heartbeat loop.
func (h *Heartbeat) safeCallback(result Result) {
	done := make(chan struct{}, 1)
	go func() {
		h.callback(result)
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(CallbackTimeout):
		// Callback timed out — don't block the loop
	}
}

// beat sends a single heartbeat request.
// Uses POST with JSON body to avoid leaking the activation code in server access logs.
func (h *Heartbeat) beat() Result {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	body := fmt.Sprintf(`{"code":%q,"fingerprint":%q}`, h.code, h.fingerprint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.hubURL+"/api/heartbeat", strings.NewReader(body))
	if err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat request creation failed: %w", err),
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat request failed: %w", err),
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat read failed: %w", err),
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat returned status %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var hbResp Response
	if err := json.Unmarshal(respBody, &hbResp); err != nil {
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

	// Validate response
	if err := hbResp.Validate(); err != nil {
		return Result{
			Success: false,
			Error:   fmt.Errorf("heartbeat validation failed: %w", err),
		}
	}

	return Result{
		Success: true,
		Resp:    &hbResp,
	}
}

// intervalWithJitter returns the current interval with ±10% random jitter
// to prevent thundering herd if multiple clients restart simultaneously.
func (h *Heartbeat) intervalWithJitter() time.Duration {
	h.mu.Lock()
	base := h.interval
	h.mu.Unlock()

	jitter := time.Duration(float64(base) * JitterPercent * (2*rand.Float64() - 1))
	return base + jitter
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
