package probe

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

type Result struct {
	BandwidthBps int
	BaselineRTT  time.Duration
	IsConstrained bool
}

func Run(adminHubURL, tier string) (*Result, error) {
	if tier == "warp_lite" {
		return &Result{
			BandwidthBps: 8_000_000,
			BaselineRTT:  50 * time.Millisecond,
			IsConstrained: true,
		}, nil
	}

	url := adminHubURL + "/speedtest/1mb.bin"
	start := time.Now()

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("probe request failed: %w", err)
	}
	defer resp.Body.Close()

	// Measure time to first byte as baseline RTT
	baselineRTT := time.Since(start)

	// Read entire body to measure throughput
	n, readErr := io.Copy(io.Discard, resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("probe read failed: %w", readErr)
	}

	elapsed := time.Since(start)
	if elapsed < 10*time.Millisecond {
		return nil, fmt.Errorf("probe too fast")
	}

	throughputBps := int(float64(n*8) / elapsed.Seconds())

	// Is the network constrained?
	isConstrained := elapsed > 1500*time.Millisecond

	// Clamp to reasonable range
	switch tier {
	case "gaming_max":
		if throughputBps > 100_000_000 {
			throughputBps = 100_000_000
		}
	case "gaming_mid":
		if throughputBps > 50_000_000 {
			throughputBps = 50_000_000
		}
	case "stealth_browse":
		if throughputBps > 48_000_000 {
			throughputBps = 48_000_000
		}
	}

	if throughputBps < 3_000_000 {
		throughputBps = 3_000_000
	}

	if !isConstrained {
		throughputBps = 0 // 0 means no cap needed
	}

	return &Result{
		BandwidthBps:  throughputBps,
		BaselineRTT:   baselineRTT,
		IsConstrained: isConstrained,
	}, nil
}
