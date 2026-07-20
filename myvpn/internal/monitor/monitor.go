package monitor

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Monitor struct {
	currentCapKBps int64
	ewmaKBps       float64
	tier           string
	baselineRTT    time.Duration
	probeURL       string
	lastAdjustment time.Time
	adjustmentMu   sync.Mutex
	stopCh         chan struct{}
	stopped        chan struct{}
}

const (
	monitorInterval     = 15 * time.Second
	monitorChunkSize    = 100 * 1024 // 100KB
	decreaseThreshold   = 0.7
	decreaseSamples     = 2
	increaseThreshold   = 1.2
	increaseWait        = 60 * time.Second
	bufferbloatRTTMult  = 2.0
	bufferbloatCapCut   = 0.5
	capDecreaseFactor   = 0.7
	capIncreaseFactor   = 1.2
	ewmaAlpha           = 0.3
	minAdjustmentGap    = 5 * time.Second
)

func New(adminHubURL, tier string, initialCapBps int, baselineRTT time.Duration) *Monitor {
	initialCapKBps := int(math.Ceil(float64(initialCapBps) / 1024))
	if initialCapKBps <= 0 {
		initialCapKBps = 0
	}

	return &Monitor{
		currentCapKBps: int64(initialCapKBps),
		tier:           tier,
		baselineRTT:    baselineRTT,
		probeURL:       adminHubURL + "/speedtest/100kb.bin",
		stopCh:         make(chan struct{}),
		stopped:        make(chan struct{}),
	}
}

func (m *Monitor) Start() {
	go m.loop()
}

func (m *Monitor) Stop() {
	close(m.stopCh)
	<-m.stopped
}

func (m *Monitor) CurrentCapKBps() int {
	return int(atomic.LoadInt64(&m.currentCapKBps))
}

func (m *Monitor) loop() {
	defer close(m.stopped)

	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	lowSamples := 0
	highSamples := 0

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			sampleKBps, rtt, err := m.sampleBandwidth()
			if err != nil {
				continue
			}

			// Update EWMA
			if m.ewmaKBps == 0 {
				m.ewmaKBps = float64(sampleKBps)
			} else {
				m.ewmaKBps = ewmaAlpha*float64(sampleKBps) + (1-ewmaAlpha)*m.ewmaKBps
			}

			currentCap := int(atomic.LoadInt64(&m.currentCapKBps))

			// Bufferbloat detection
			if m.baselineRTT > 0 && rtt > time.Duration(float64(m.baselineRTT)*bufferbloatRTTMult) {
				if currentCap > 0 {
					newCap := int(float64(currentCap) * bufferbloatCapCut)
					m.adjustCap(newCap)
					fmt.Printf("MONITOR: bufferbloat detected — cap cut to %d KB/s\n", newCap)
				}
				lowSamples = 0
				highSamples = 0
				continue
			}

			if currentCap > 0 {
				ewma := m.ewmaKBps
				if ewma < float64(currentCap)*decreaseThreshold {
					lowSamples++
					highSamples = 0
					if lowSamples >= decreaseSamples {
						newCap := int(float64(currentCap) * capDecreaseFactor)
						m.adjustCap(newCap)
						fmt.Printf("MONITOR: sustained low bandwidth — cap reduced to %d KB/s\n", newCap)
						lowSamples = 0
					}
				} else if ewma > float64(currentCap)*increaseThreshold {
					highSamples++
					lowSamples = 0
					if highSamples >= int(increaseWait/monitorInterval) {
						newCap := int(float64(currentCap) * capIncreaseFactor)
						m.adjustCap(newCap)
						fmt.Printf("MONITOR: headroom available — cap increased to %d KB/s\n", newCap)
						highSamples = 0
					}
				} else {
					lowSamples = 0
					highSamples = 0
				}
			} else if m.ewmaKBps > 0 {
				// Uncapped but network is constrained
				newCap := int(m.ewmaKBps * 0.9)
				m.adjustCap(newCap)
				fmt.Printf("MONITOR: applying initial cap of %d KB/s\n", newCap)
			}
		}
	}
}

func (m *Monitor) sampleBandwidth() (throughputKBps int, rtt time.Duration, err error) {
	start := time.Now()
	resp, err := http.Get(m.probeURL)
	if err != nil {
		return 0, 0, fmt.Errorf("monitor GET failed: %w", err)
	}
	defer resp.Body.Close()
	rtt = time.Since(start)

	limited := io.LimitReader(resp.Body, int64(monitorChunkSize))
	n, readErr := io.Copy(io.Discard, limited)
	if readErr != nil {
		return 0, 0, fmt.Errorf("monitor read failed: %w", readErr)
	}

	totalElapsed := time.Since(start)
	if totalElapsed < 1*time.Millisecond {
		return 0, rtt, fmt.Errorf("sample too fast")
	}

	throughputKBps = int(float64(n) / totalElapsed.Seconds() / 1024)
	return throughputKBps, rtt, nil
}

func (m *Monitor) adjustCap(newCapKBps int) {
	m.adjustmentMu.Lock()
	defer m.adjustmentMu.Unlock()

	newCapKBps = clampToTier(newCapKBps, m.tier)

	oldCap := int(atomic.LoadInt64(&m.currentCapKBps))
	if newCapKBps == oldCap {
		return
	}

	if time.Since(m.lastAdjustment) < minAdjustmentGap {
		return
	}

	atomic.StoreInt64(&m.currentCapKBps, int64(newCapKBps))
	m.lastAdjustment = time.Now()
}

func clampToTier(capKBps int, tier string) int {
	const floor = 3000 / 8 // 3 Mbps floor in KB/s ≈ 375

	var ceiling int
	switch tier {
	case "gaming_max":
		ceiling = 100_000_000 / 8 / 1024 // ≈ 12207 KB/s
	case "gaming_mid":
		ceiling = 50_000_000 / 8 / 1024 // ≈ 6104 KB/s
	case "stealth_browse":
		ceiling = 48_000_000 / 8 / 1024 // ≈ 5859 KB/s
	case "warp_lite":
		ceiling = 8_000_000 / 8 / 1024 // ≈ 977 KB/s
	default:
		ceiling = 100_000_000 / 8 / 1024
	}

	if capKBps < floor {
		return floor
	}
	if capKBps > ceiling {
		return ceiling
	}
	return capKBps
}
