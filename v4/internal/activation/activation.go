package activation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Common errors.
var (
	ErrInvalidCharacter = errors.New("code contains invalid character")
	ErrInvalidCode     = errors.New("invalid activation code format")
	ErrChecksumFailed  = errors.New("activation code checksum failed")
	ErrServerError     = errors.New("server returned an error")
	ErrCodeBound       = errors.New("code already bound to another device")
	ErrCodeSuspended   = errors.New("code is suspended")
	ErrCodeExpired     = errors.New("code has expired")
	ErrRateLimited     = errors.New("too many activation attempts")
)

// ActivateResponse is the response from the activation server.
type ActivateResponse struct {
	Code       int            `json:"code"`
	Message    string         `json:"message"`
	Tier       string         `json:"tier,omitempty"`
	DeviceFP   string         `json:"device_fingerprint,omitempty"`
	ServerCfg  *ServerConfig  `json:"server_config,omitempty"`
	UDPRelay   bool           `json:"udp_relay,omitempty"`
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
	hubURL   string
	httpClient *http.Client
	timeout  time.Duration
}

// NewClient creates a new activation client.
func NewClient(hubURL string) *Client {
	return &Client{
		hubURL:   hubURL,
		timeout:  30 * time.Second,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Activate sends an activation request to the hub server.
// It first validates the code client-side (Luhn-mod-N), then sends to server.
func (c *Client) Activate(code, fingerprint string) (*ActivateResponse, error) {
	// 1. Client-side Luhn-mod-N validation
	cleaned := stripFormatting(code)
	if len(cleaned) != CodeTotalLen {
		return nil, ErrInvalidCode
	}

	if !luhnModNCheck(cleaned) {
		return nil, ErrChecksumFailed
	}

	// 2. Format the code properly for transmission
	formattedCode := FormatCode(cleaned)

	// 3. Send to server
	req := ActivateRequest{
		Code:        formattedCode,
		Fingerprint: fingerprint,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.hubURL+"/api/activate",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("cannot contact server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}

	var activateResp ActivateResponse
	if err := json.NewDecoder(resp.Body).Decode(&activateResp); err != nil {
		return nil, fmt.Errorf("cannot decode server response: %w", err)
	}

	// 4. Map server status codes to errors
	switch activateResp.Code {
	case 200:
		return &activateResp, nil
	case 400:
		return nil, fmt.Errorf("%w: %s", ErrInvalidCode, activateResp.Message)
	case 403:
		if activateResp.Message == "Code is suspended" {
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

// ValidateCode checks the client-side Luhn-mod-N checksum only.
// Returns nil if the code passes validation.
func ValidateCode(code string) error {
	cleaned := stripFormatting(code)
	if len(cleaned) != CodeTotalLen {
		return ErrInvalidCode
	}
	if !luhnModNCheck(cleaned) {
		return ErrChecksumFailed
	}
	return nil
}
