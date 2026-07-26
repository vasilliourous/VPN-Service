// Package activation handles code validation, device activation, and server communication.
//
// Hardening: context propagation for cancellation, retry with exponential backoff,
// input sanitization, rate-limit awareness, comprehensive error wrapping.
package activation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Common errors.
var (
	ErrInvalidCharacter = errors.New("code contains invalid character")
	ErrInvalidCode      = errors.New("invalid activation code format")
	ErrChecksumFailed   = errors.New("activation code checksum failed")
	ErrServerError      = errors.New("server returned an error")
	ErrCodeBound        = errors.New("code already bound to another device")
	ErrCodeSuspended    = errors.New("code is suspended")
	ErrCodeExpired      = errors.New("code has expired")
	ErrRateLimited      = errors.New("too many activation attempts")
	ErrTimeout          = errors.New("activation request timed out")
	ErrServerUnreachable = errors.New("server is unreachable")
)

// ActivateResponse is the response from the activation server.
type ActivateResponse struct {
	Code       int           `json:"code"`
	Message    string        `json:"message"`
	Tier       string        `json:"tier,omitempty"`
	DeviceFP   string        `json:"device_fingerprint,omitempty"`
	ServerCfg  *ServerConfig `json:"server_config,omitempty"`
	UDPRelay   bool          `json:"udp_relay,omitempty"`
}

// ServerConfig holds Shadowsocks connection parameters from the server.
type ServerConfig struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
}

// ActivateRequest is sent to the activation endpoint.
type ActivateRequest struct {
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
}

// Client handles activation with the remote hub server.
type Client struct {
	hubURL     string
	httpClient *http.Client
	timeout    time.Duration
	maxRetries int
}

// ValidateHubURL checks that a hub URL is well-formed.
// It doesn't verify connectivity — just basic structure.
func ValidateHubURL(url string) error {
	if url == "" {
		return fmt.Errorf("hub URL must not be empty")
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("hub URL must start with http:// or https://")
	}
	if strings.Count(url, "/") < 2 {
		return fmt.Errorf("hub URL must include a hostname")
	}
	return nil
}

// ClientOption configures the activation client.
type ClientOption func(*Client)

// WithTimeout sets the HTTP request timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.timeout = d
	}
}

// WithMaxRetries sets the number of retry attempts for transient failures.
func WithMaxRetries(n int) ClientOption {
	return func(c *Client) {
		c.maxRetries = n
	}
}

// NewClient creates a new activation client.
func NewClient(hubURL string, opts ...ClientOption) *Client {
	c := &Client{
		hubURL:     strings.TrimRight(hubURL, "/"),
		timeout:    30 * time.Second,
		maxRetries: 2,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        2,
				IdleConnTimeout:     30 * time.Second,
				DisableCompression:  false,
			},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	// Ensure httpClient.Timeout matches if not overridden
	if c.httpClient.Timeout == 0 {
		c.httpClient.Timeout = c.timeout
	}
	return c
}

// ValidateCode checks the client-side Luhn-mod-N checksum only.
// Returns nil if the code passes validation.
func ValidateCode(code string) error {
	cleaned := stripFormatting(code)
	if len(cleaned) != CodeTotalLen {
		return fmt.Errorf("%w: expected %d characters, got %d", ErrInvalidCode, CodeTotalLen, len(cleaned))
	}
	if !luhnModNCheck(cleaned) {
		return ErrChecksumFailed
	}
	return nil
}

// Activate sends an activation request to the hub server with context support.
// It first validates the code client-side (Luhn-mod-N), then sends to server.
// Retries on transient failures with exponential backoff.
func (c *Client) Activate(ctx context.Context, code, fingerprint string) (*ActivateResponse, error) {
	// 1. Client-side Luhn-mod-N validation
	cleaned := stripFormatting(code)
	if len(cleaned) != CodeTotalLen {
		return nil, ErrInvalidCode
	}

	if !luhnModNCheck(cleaned) {
		return nil, ErrChecksumFailed
	}

	// 2. Validate fingerprint
	if !ValidateFingerprint(fingerprint) {
		return nil, fmt.Errorf("invalid device fingerprint: too short")
	}

	// 3. Format the code properly for transmission
	formattedCode := FormatCode(cleaned)

	// 4. Send to server with retry
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, ...
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, fmt.Errorf("activation cancelled: %w", ctx.Err())
			}
		}

		resp, err := c.attemptActivate(ctx, formattedCode, fingerprint)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't retry on client errors (invalid code, bound, suspended, etc.)
		if errors.Is(err, ErrInvalidCode) || errors.Is(err, ErrChecksumFailed) ||
			errors.Is(err, ErrCodeBound) || errors.Is(err, ErrCodeSuspended) ||
			errors.Is(err, ErrCodeExpired) || errors.Is(err, ErrRateLimited) {
			return nil, err
		}

		// Don't retry on context cancellation
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("activation failed after %d retries: %w", c.maxRetries, lastErr)
}

// attemptActivate performs a single activation request.
func (c *Client) attemptActivate(ctx context.Context, code, fingerprint string) (*ActivateResponse, error) {
	req := ActivateRequest{
		Code:        code,
		Fingerprint: fingerprint,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.hubURL+"/api/activate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "MyVPN-Client/2.0")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Check if the error is a timeout or connection error
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrServerUnreachable, err)
	}
	defer resp.Body.Close()

	// Read response body fully
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, fmt.Errorf("cannot read response body: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}

	var activateResp ActivateResponse
	if err := json.Unmarshal(respBody, &activateResp); err != nil {
		return nil, fmt.Errorf("cannot decode server response: %w", err)
	}

	// Map server status codes to errors
	switch activateResp.Code {
	case 200:
		return &activateResp, nil
	case 400:
		return nil, fmt.Errorf("%w: %s", ErrInvalidCode, activateResp.Message)
	case 403:
		if strings.Contains(activateResp.Message, "suspended") {
			return nil, ErrCodeSuspended
		}
		return nil, ErrCodeBound
	case 404:
		return nil, fmt.Errorf("%w: %s", ErrInvalidCode, activateResp.Message)
	case 410:
		return nil, ErrCodeExpired
	case 429:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("%w (%d): %s", ErrServerError, activateResp.Code, activateResp.Message)
	}
}
