package activation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const luhnCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type ActivationRequest struct {
	Code string `json:"code"`
}

type ActivationResponse struct {
	Token string `json:"token"`
	Plan  string `json:"plan"`
}

func luhnModN(code string) bool {
	clean := strings.ToUpper(code)
	clean = strings.ReplaceAll(clean, "MYVPN-", "")
	clean = strings.ReplaceAll(clean, "-", "")

	if len(clean) != 17 {
		return false
	}

	data := clean[:16]
	checkChar := clean[16]

	sum := 0
	double := false
	for i := len(data) - 1; i >= 0; i-- {
		val := strings.IndexByte(luhnCharset, data[i])
		if val == -1 {
			return false
		}
		if double {
			val *= 2
			if val >= len(luhnCharset) {
				val = val/len(luhnCharset) + (val % len(luhnCharset))
			}
		}
		sum += val
		double = !double
	}

	expectedIdx := (len(luhnCharset) - (sum % len(luhnCharset))) % len(luhnCharset)
	expectedCheck := luhnCharset[expectedIdx]
	return checkChar == expectedCheck
}

func Validate(apiBase, code string) (*ActivationResponse, error) {
	code = strings.TrimSpace(code)

	if !luhnModN(code) {
		return nil, fmt.Errorf("invalid activation code format — check for typos")
	}

	fingerprint := CollectFingerprint()

	body := ActivationRequest{Code: code}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", apiBase+"/api/collections/activations/records",
		bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("activation request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Fingerprint", fingerprint)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("activation request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case 200:
		var result ActivationResponse
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("invalid activation response: %w", err)
		}
		if result.Token == "" {
			return nil, fmt.Errorf("activation rejected — invalid response")
		}
		return &result, nil
	case 403:
		var msg struct {
			Message string `json:"message"`
		}
		json.Unmarshal(bodyBytes, &msg)
		if msg.Message != "" {
			return nil, fmt.Errorf("%s", msg.Message)
		}
		return nil, fmt.Errorf("activation rejected (403)")
	case 404, 410:
		return nil, fmt.Errorf("invalid or expired activation code")
	case 429:
		return nil, fmt.Errorf("too many activation attempts — wait a few minutes")
	default:
		return nil, fmt.Errorf("activation failed (status %d)", resp.StatusCode)
	}
}
