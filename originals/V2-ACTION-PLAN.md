# V2 — Simplified Action Plan

> **replaces:** Actionable-Plan.md (v3 Hardened — 4771 lines)
> **principle:** Artificial bottlenecking via Hysteria 2 config profiles. Adaptive only for Gaming Max.
> **v2 total:** ~1,700 lines of Go

---

## What Changed From v1

| v1 Feature | v2 Decision |
|------------|-------------|
| 5-stage ramp probe (all tiers) | **Single 1MB probe for Gaming Max only. Others use static caps.** |
| EWMA bandwidth monitor (all tiers, 210 lines) | **Replaced by health check RTT monitoring for Gaming Max (~50 lines).** |
| Separate `internal/monitor/` package | **Removed. Adaptation is a goroutine in gui/app.go.** |
| Bufferbloat detection with multipliers | **Kept the same concept, simplified logic, no EWMA.** |
| Helper service + sandbox + disguise | **All removed.** |
| Two-phase update sentinel | **Background download + auto-revert on crash.** |
| Activation returns just token + plan | **Now returns Hysteria 2 configs too.** |
| Server config only via heartbeat | **Now delivered on activation + refreshed on heartbeat.** |

---

## Phase 0: Server-Side Setup

### Step 0.1 — VPS + Caddy + PocketBase

Same as v1. No changes needed.

### Step 0.2 — PocketBase Collections

**`codes`** — Activation codes:
```
code, tier, used, fingerprint, suspended, middleman, created_at
```

**`tier_configs`** — Hysteria 2 bottleneck profiles per tier:
```
tier (unique), configs (json), active (bool), updated_at
```

**`heartbeats`** — Telemetry log (optional)

### Step 0.3 — Define the Four Bottleneck Profiles

Insert these into `tier_configs`:

**Warp Lite:**
```json
{
    "tier": "warp_lite",
    "configs": [{
        "id": "usque",
        "display_name": "Lite Mode",
        "binary_name": "litemode",
        "config_json": { "wg": { "endpoint": "engage.cloudflareclient.com:2408" } }
    }]
}
```

**Stealth Browse:**
```json
{
    "tier": "stealth_browse",
    "configs": [{
        "id": "hysteria2",
        "display_name": "Speed Mode",
        "binary_name": "speedmode",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "stealth-pool-1",
            "tls": { "sni": "www.cloudflare.com", "insecure": false },
            "obfs": "salamander",
            "obfs-password": "stealth-obfs-key",
            "brutal": { "enabled": false },
            "recv_window_conn": 8388608,
            "recv_window": 33554432,
            "hop-interval": 15
        }
    }]
}
```

**Gaming Mid:**
```json
{
    "tier": "gaming_mid",
    "configs": [{
        "id": "hysteria2",
        "display_name": "Speed Mode",
        "binary_name": "speedmode",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "gaming-mid-pool-1",
            "tls": { "sni": "www.cloudflare.com", "insecure": false },
            "obfs": "salamander",
            "obfs-password": "mid-obfs-key",
            "brutal": { "enabled": true, "up_mbps": 15, "down_mbps": 15 },
            "recv_window_conn": 1048576,
            "recv_window": 4194304,
            "hop-interval": 30
        }
    }]
}
```

**Gaming Max:**
```json
{
    "tier": "gaming_max",
    "configs": [{
        "id": "hysteria2",
        "display_name": "Speed Mode",
        "binary_name": "speedmode",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "gaming-max-pool-1",
            "tls": { "sni": "www.cloudflare.com", "insecure": false },
            "obfs": "salamander",
            "obfs-password": "max-obfs-key",
            "brutal": { "enabled": true, "up_mbps": 0, "down_mbps": 0 },
            "recv_window_conn": 16777216,
            "recv_window": 67108864,
            "hop-interval": 0
        }
    }]
}
```

### Step 0.4 — JS Hooks

**`activation.pb.js`** — Validates code, binds device, returns configs from `tier_configs`.

**`heartbeat.pb.js`** — Checks suspension, refreshes token, returns current configs + optional update command.

---

## Phase 1: Create Caps Package

```go
// internal/caps/caps.go — 45 lines
package caps

func Get(tier string) int {
    switch tier {
    case "warp_lite":      return   8_000_000
    case "stealth_browse": return  48_000_000
    case "gaming_mid":     return  50_000_000
    case "gaming_max":     return           0  // 0 = probe on connect
    default:               return           0
    }
}

func Floor(tier string) int  { return 3_000_000 }
func IsAdaptive(tier string) bool { return tier == "gaming_max" }
```

---

## Phase 2: Simplify Probe (Gaming Max Only)

Create a simplified probe — single 1MB download, no stages:

```go
// internal/probe/probe.go (simplified, Gaming Max only)
package probe

import (
    "io"
    "net/http"
    "time"
)

type Result struct {
    BandwidthBps int
    BaselineRTT  time.Duration
}

func Run(adminHubURL string) (*Result, error) {
    url := adminHubURL + "/speedtest/1mb.bin"
    start := time.Now()
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    // Time to first byte = baseline RTT
    baselineRTT := time.Since(start)

    n, _ := io.Copy(io.Discard, resp.Body)
    elapsed := time.Since(start)

    if elapsed < time.Millisecond {
        return nil, nil
    }

    bps := int(float64(n*8) / elapsed.Seconds())
    if bps > 100_000_000 {
        bps = 100_000_000
    }
    if bps < 3_000_000 {
        bps = 3_000_000
    }

    return &Result{BandwidthBps: bps, BaselineRTT: baselineRTT}, nil
}
```

This only runs for Gaming Max. Other tiers don't call it.

---

## Phase 3: Manager — Support Dynamic Cap

```go
// internal/manager/process.go additions

var dynamicCap int64  // 0 = use caps.Get()

func SetBandwidthCap(bps int) {
    atomic.StoreInt64(&dynamicCap, int64(bps))
}

func CurrentBandwidthCap() int {
    d := atomic.LoadInt64(&dynamicCap)
    if d > 0 {
        return int(d)
    }
    return 0
}

func StartEngine(cfg storage.ProtocolConfig, tier string) error {
    // ... resolve binary ...

    cap := CurrentBandwidthCap()
    if cap == 0 {
        cap = caps.Get(tier)
    }

    cmd := buildCommand(binaryPath, cfg.ID, cfg.ConfigJSON, tier, cap)
    // ... start ...
}

func RestartEngine(cfg storage.ProtocolConfig, tier string) error {
    StopEngine()
    return StartEngine(cfg, tier)
}
```

`buildCommand` now receives the resolved cap as an int (not looking it up internally), and passes `--tun --speed <cap>`.

For Gaming Max adaptive changes: `RestartEngine` stops and starts with a new config. The config JSON's Brutal CC target is updated to match the new cap:

```go
func updateBrutalTarget(configJSON json.RawMessage, capBps int) json.RawMessage {
    var cfg map[string]interface{}
    json.Unmarshal(configJSON, &cfg)
    if brutal, ok := cfg["brutal"].(map[string]interface{}); ok {
        mbps := float64(capBps) / 1_000_000
        brutal["up_mbps"] = mbps
        brutal["down_mbps"] = mbps
    }
    updated, _ := json.Marshal(cfg)
    return updated
}
```

---

## Phase 4: GUI — Connect Flow + Adaptation Loop

### Connect (simplified)

```go
func connect() {
    state.ConnectBtn.SetText("Connecting...")
    go func() {
        planTier := storage.LoadPlanTier()
        protos := storage.GetProtocols()
        if len(protos) == 0 {
            state.StatusLabel.SetText("No server configs — re-activate?")
            state.ConnectBtn.SetText("Connect")
            return
        }

        // Gaming Max: probe bandwidth
        var probeResult *probe.Result
        if caps.IsAdaptive(planTier) {
            probeResult, _ = probe.Run(state.adminHubURL)
            if probeResult != nil {
                manager.SetBandwidthCap(probeResult.BandwidthBps)
            }
        }

        // Setup tunnel
        tunnel.Setup()
        tunnel.Engage()
        tunnel.Guard()

        // Start engine
        if err := manager.StartEngine(protos[0], planTier); err != nil {
            state.StatusLabel.SetText(fmt.Sprintf("Engine failed: %v", err))
            disconnect()
            return
        }

        // Start health check
        state.healthChecker = health.New(state.adminHubURL)
        state.healthChecker.OnDead(func() { disconnect() })
        state.healthChecker.Start()

        // Gaming Max: launch adaptation loop
        if caps.IsAdaptive(planTier) && probeResult != nil {
            go adaptBandwidth(state.adminHubURL, planTier,
                probeResult.BandwidthBps, probeResult.BaselineRTT)
        }

        state.connected = true
        state.startTime = time.Now()
        state.ConnectBtn.SetText("Disconnect")
        state.StatusLabel.SetText("Connected")
        setDotColor(green)
    }()
}
```

### Adaptation Loop (~50 lines)

```go
func adaptBandwidth(apiBase, tier string, maxBps int, baselineRTT time.Duration) {
    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()

    currentCap := maxBps
    floor := caps.Floor(tier)
    slowCount := 0
    goodCount := 0

    for range ticker.C {
        if !manager.IsRunning() {
            return
        }

        start := time.Now()
        resp, err := http.Get(apiBase + "/_/health")
        if err != nil || resp == nil || resp.StatusCode != 200 {
            continue
        }
        resp.Body.Close()
        rtt := time.Since(start)

        switch {
        case rtt > baselineRTT*3:
            slowCount++
            goodCount = 0
            if slowCount >= 2 {
                newCap := currentCap / 2
                if newCap < floor {
                    newCap = floor
                }
                if newCap != currentCap {
                    currentCap = newCap
                    manager.SetBandwidthCap(newCap)
                    go manager.RestartEngine(storage.GetProtocols()[0], tier)
                }
                slowCount = 0
            }

        case rtt < baselineRTT*1.5:
            goodCount++
            slowCount = 0
            if goodCount >= 4 {
                newCap := currentCap * 5 / 4
                if newCap > maxBps {
                    newCap = maxBps
                }
                if newCap != currentCap {
                    currentCap = newCap
                    manager.SetBandwidthCap(newCap)
                    go manager.RestartEngine(storage.GetProtocols()[0], tier)
                }
                goodCount = 0
            }

        default:
            slowCount = 0
            goodCount = 0
        }
    }
}
```

No EWMA. No background downloads. No separate package. ~45 lines that sit in `gui/app.go`.

---

## Phase 5: Simplify Heartbeat

Response now carries configs + optional update:

```go
type HeartbeatResponse struct {
    Status    string                `json:"status"`
    Token     string                `json:"token"`
    Configs   []ProtocolConfig      `json:"configs,omitempty"`
    Update    *UpdateCommand        `json:"update,omitempty"`
    Message   string                `json:"message,omitempty"`
}
```

Heartbeat loop: suspension check → token rotate → save configs if present → spawn update download if present → confirm update on first success.

---

## Phase 6: Background Updater

Unchanged from previous — background download to app data dir → apply on next launch → auto-revert on crash.

---

## Phase 7: Testing

### Gaming Max Adaptation Tests

| Test | Expected |
|------|----------|
| Gaming Max connect on fast network (80 Mbps) | Probe measures 80 Mbps → Brutal target 80 Mbps → smooth gaming |
| Simulate QoS throttle (traffic shape to 5 Mbps mid-session) | Health check RTT spikes → cap halves → engine restarts → settles near 5 Mbps |
| Simulate QoS release (back to 80 Mbps) | RTT normal for 4 checks → cap increases by 25% → climbs back toward 80 Mbps |
| Gaming Max connect on already-throttled network (10 Mbps) | Probe measures 10 Mbps → starts there → no spike, no adaptation needed |
| Non-Gaming-Max tiers connect | No probe runs, no adaptation loop, hardcoded cap applied |

### Other Tiers Bottleneck Tests

| Test | Expected |
|------|----------|
| Stealth Browse streaming | High throughput, but latency spikes make gaming unplayable |
| Gaming Mid gaming | Periodic rubber-banding, stutter from small recv_window |
| Warp Lite browsing | Slow and inconsistent (usque/Warp behavior) |

---

## Estimated Effort

| Task | Time |
|------|------|
| VPS + PocketBase + collections | 30 min |
| Define 4 bottleneck profiles in `tier_configs` | 15 min |
| Write JS hooks (activation + heartbeat) | 45 min |
| Create `internal/caps/caps.go` | 5 min |
| Simplify `internal/probe/probe.go` (Gaming Max only) | 15 min |
| Update `manager/process.go` (dynamic cap, restart, updateBrutalTarget) | 30 min |
| Add adaptation loop in `gui/app.go` | 20 min |
| Simplify `gui/app.go` connect flow | 15 min |
| Simplify `heartbeat/heartbeat.go` | 20 min |
| Build background updater | 1 hour |
| Test Gaming Max adaptation | 30 min |
| **Total** | **~4.5 hours** |
