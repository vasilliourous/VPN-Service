package heartbeat

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"myvpn/internal/manager"
	"myvpn/internal/storage"
	"sync"
	"time"
)

type HeartbeatRequest struct {
	Token          string `json:"token"`
	DeviceID       string `json:"device_id"`
	AppVersion     string `json:"app_version"`
	OSPlatform     string `json:"os_platform"`
	ProtocolID     string `json:"protocol_id"`
	Connected      bool   `json:"connected"`
	HealthStatus   string `json:"health_status"`
	EngineExitCode int    `json:"engine_exit_code"`
	LastError      string `json:"last_error"`
	UptimeSec      int    `json:"uptime_seconds"`
	BytesUp        int64  `json:"bytes_up"`
	BytesDown      int64  `json:"bytes_down"`
}

type HeartbeatResponse struct {
	Status    string                 `json:"status"`
	Plan      string                 `json:"plan"`
	Token     string                 `json:"token"`
	Protocols []storage.ProtocolConfig `json:"protocols"`
	Commands  []RemoteCommand        `json:"commands"`
	Message   string                 `json:"message"`
}

type RemoteCommand struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

var (
	hubCertHash     string
	apiBase         string
	consecutiveFails int
	graceDeadline   time.Time
	graceMu         sync.Mutex
	startTime       = time.Now()
	lastBytesUp     int64
	lastBytesDown   int64
	lastError       string
)

const (
	maxConsecutiveFails = 3
	graceDuration       = 7 * 24 * time.Hour
	heartbeatInterval   = 60
)

func SetCertPin(certSHA256 string) {
	hubCertHash = certSHA256
}

func Start(baseURL string) {
	apiBase = baseURL
	graceDeadline = time.Now().Add(graceDuration)

	ticker := time.NewTicker(heartbeatInterval * time.Second)
	for range ticker.C {
		doHeartbeat()
	}
}

func SetLastError(errMsg string) {
	graceMu.Lock()
	defer graceMu.Unlock()
	lastError = errMsg
}

func GraceRemaining() int {
	graceMu.Lock()
	defer graceMu.Unlock()
	if time.Now().After(graceDeadline) {
		return 0
	}
	return int(time.Until(graceDeadline).Hours() / 24)
}

func MustReauthenticate() bool {
	graceMu.Lock()
	defer graceMu.Unlock()
	return consecutiveFails >= maxConsecutiveFails && time.Now().After(graceDeadline)
}

func pinnedHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: hubCertHash == "",
				VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
					if hubCertHash == "" {
						return nil
					}
					for _, raw := range rawCerts {
						cert, _ := x509.ParseCertificate(raw)
						if cert == nil {
							continue
						}
						hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
						got := fmt.Sprintf("%x", hash)
						if got == hubCertHash {
							return nil
						}
					}
					return fmt.Errorf("certificate pin mismatch")
				},
			},
		},
	}
}

func doHeartbeat() {
	token := storage.LoadToken()
	if token == "" {
		return
	}

	healthStatus := "healthy"
	if !manager.IsRunning() {
		healthStatus = "dead"
	}

	exitCode := 0
	if !manager.IsRunning() && manager.LastExitCode() != 0 {
		exitCode = manager.LastExitCode()
	}

	graceMu.Lock()
	errMsg := lastError
	lastError = ""
	graceMu.Unlock()

	uptime := int(time.Since(startTime).Seconds())

	reqBody := HeartbeatRequest{
		Token:          token,
		DeviceID:       storage.LoadDeviceFingerprint(),
		AppVersion:     "1.0.0",
		OSPlatform:     "windows_amd64",
		ProtocolID:     manager.ActiveProtocolID(),
		Connected:      manager.IsRunning(),
		HealthStatus:   healthStatus,
		EngineExitCode: exitCode,
		LastError:      errMsg,
		UptimeSec:      uptime,
		BytesUp:        0,
		BytesDown:      0,
	}

	payload, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", apiBase+"/api/heartbeat", bytes.NewReader(payload))
	if err != nil {
		recordHeartbeatFailure(err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Device-Fingerprint", storage.LoadDeviceFingerprint())

	client := pinnedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		recordHeartbeatFailure(err.Error())
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		recordHeartbeatFailure(fmt.Sprintf("heartbeat returned %d", resp.StatusCode))
		return
	}

	var hbResp HeartbeatResponse
	if err := json.Unmarshal(bodyBytes, &hbResp); err != nil {
		recordHeartbeatFailure("invalid heartbeat response")
		return
	}

	// Success — reset fail counter and grace timer
	graceMu.Lock()
	consecutiveFails = 0
	graceDeadline = time.Now().Add(graceDuration)
	graceMu.Unlock()

	// Check suspension
	if hbResp.Status == "suspended" {
		manager.StopEngine()
		msg := hbResp.Message
		if msg == "" {
			msg = "Your account has been suspended."
		}
		fmt.Printf("HEARTBEAT: %s\n", msg)
		return
	}

	// Rotate token
	if hbResp.Token != "" {
		storage.SaveToken(hbResp.Token)
	}

	// Update protocol list
	if len(hbResp.Protocols) > 0 {
		storage.SetProtocols(hbResp.Protocols)
	}

	// Process remote commands
	for _, cmd := range hbResp.Commands {
		processCommand(cmd)
	}
}

func recordHeartbeatFailure(reason string) {
	graceMu.Lock()
	defer graceMu.Unlock()
	consecutiveFails++
	fmt.Printf("HEARTBEAT fail %d/%d: %s\n", consecutiveFails, maxConsecutiveFails, reason)

	if consecutiveFails >= maxConsecutiveFails {
		fmt.Printf("HEARTBEAT: Entering grace period until %s\n", graceDeadline.Format(time.RFC3339))
	}
}

func processCommand(cmd RemoteCommand) {
	switch cmd.Action {
	case "disable":
		manager.StopEngine()
		fmt.Println("HEARTBEAT: Account disabled by admin")
	case "update":
		var payload struct {
			URL      string `json:"url"`
			SHA256   string `json:"sha256"`
			Version  string `json:"version"`
			Platform string `json:"platform"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			fmt.Printf("HEARTBEAT: invalid update command: %v\n", err)
			return
		}
		// Trigger update — updater package handles this
		fmt.Printf("HEARTBEAT: Update available: %s\n", payload.Version)
	case "reconfigure":
		fmt.Println("HEARTBEAT: Reconfiguring...")
	default:
		fmt.Printf("HEARTBEAT: Unknown command: %s\n", cmd.Action)
	}
}

func ResetGrace() {
	graceMu.Lock()
	defer graceMu.Unlock()
	consecutiveFails = 0
	graceDeadline = time.Now().Add(graceDuration)
}
