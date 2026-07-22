# V2 — Simplified Architecture with Artificial Bottlenecking

> **replaces:** Architectural-Plan.md (over-engineered v1)
> **principle:** Plans are bandwidth profiles. Codes unlock servers. Updates are the lifeline.

---

## 0. Design Priorities

1. **Survive & adapt** — background updater to push new binaries when protocols get blocked
2. **Connect reliably** — Hysteria 2 with configs delivered per-tier via activation + heartbeat
3. **Bottleneck artificially** — each tier gets a different experience profile via engine config tuning
4. **Stay invisible** — minimal GUI, fake display names

---

## 1. The Four Bottleneck Profiles

Each tier uses Hysteria 2's own config parameters to create a distinct artificial experience. Bandwidth caps are just the first dimension.

| Tier | Speed Cap | Brutal CC | Recv Window | Hop Interval | Experience |
|------|-----------|-----------|-------------|--------------|------------|
| **Warp Lite** | 1 MB/s (8 Mbps) | N/A (usque) | N/A | N/A | Inherently slow and inconsistent — usque/Warp over free Cloudflare |
| **Stealth Browse** | 6 MB/s (48 Mbps) | ❌ Disabled | 32 MB (large) | 15s | High throughput for streaming, but **standard CC causes bufferbloat + latency spikes** → terrible for gaming |
| **Gaming Mid** | 6.25 MB/s (50 Mbps) | ✅ Enabled but **target throttled to 15 Mbps** | 4 MB (small) | 30s | Brutal tries to hit 15 Mbps on a 50 Mbps pipe → **constant congestion signals → rubber-banding + jitter** |
| **Gaming Max** | Adapts (see §2) | ✅ Enabled, **target = current probed bandwidth** | 64 MB (large) | 0 (off) | Smooth, fast, dynamically adapts to QoS changes |

All of this is delivered as JSON in the `tier_configs` PocketBase collection — no client code changes needed to tweak the profiles.

---

## 2. ★ Gaming Max Adaptive Bottlenecking

This is the only tier that needs dynamic behavior. The rest are static configs.

### The Problem a Hardcoded Cap Can't Solve

If Gaming Max has a 100 Mbps hard cap and school QoS throttles to 5 Mbps:
- Brutal CC target (100 Mbps) >> actual pipe (5 Mbps)
- Brutal sends harder → router buffers fill → **RTT spikes to 800ms+**
- Brutal interprets packet loss as "link is lossy" → **sends even harder** → catastrophic bufferbloat
- Game becomes unplayable even though 5 Mbps is enough

### The Solution: Probe on Connect + Health Check RTT Monitoring

Two lightweight mechanisms, ~80 lines total, no separate package:

```
CONNECT FLOW (Gaming Max only):
  1. Download 1MB from hub → measure throughput + baseline RTT
  2. Set Brutal CC target = measured throughput (clamped to 100 Mbps)
  3. Start engine with that config
  4. Start health check (every 15s, already exists for all tiers)
        ↓
MID-SESSION ADAPTATION (Gaming Max only):
  Health check measures HTTP response time through the tunnel
    ↓
  RTT > 3× baseline for 2 consecutive checks?
    → QoS has throttled the pipe
    → Halve Brutal CC target
    → Restart engine with new config (~1s blip)
    ↓
  RTT < 1.5× baseline for 4 consecutive checks?
    → QoS has released
    → Increase Brutal CC target by 25% (up to original probed max)
    → Restart engine with new config
```

### Why This Isn't the v1 EWMA Monitor

| v1 Monitor | v2 Adaptation |
|-----------|--------------|
| 100KB background downloads every 15s | No extra downloads — reuses health check timing |
| EWMA smoothing with alpha parameter | No smoothing — simple comparison against baseline |
| Separate `internal/monitor/` package (210 lines) | Single goroutine in `gui/app.go` (~50 lines) |
| Bufferbloat detection + dynamic cap adjustment | Same, but simpler logic |
| Ran for ALL tiers | Gaming Max only |

### Code

```go
// In gui/app.go, launched after engine starts for Gaming Max:

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

That's it. ~45 lines. No new package. No EWMA. No extra downloads.

---

## 3. The Full Pipeline: Paper Code → Hysteria 2 Connection

```
┌──────────┐   ┌──────────┐   ┌──────────────────┐   ┌──────────────────┐
│ 1. Admin │   │ 2. Paper │   │ 3. User enters   │   │ 4. Server        │
│    gen   │──→│    distro│──→│    code in app   │──→│    validates     │
│    codes │   │  (cash)  │   │                   │   │                  │
│          │   │          │   │ + fingerprint     │   │ • Luhn-mod-N     │
│ Pocket   │   │ Middleman│   │                   │   │ • Code exists?   │
│ Base     │   │ hands    │   │                   │   │ • Rate limit     │
│ batch    │   │ card     │   │                   │   │ • Bind device    │
└──────────┘   └──────────┘   └──────────────────┘   └────────┬─────────┘
                                                              │
                                                              ▼
                                                  ┌──────────────────────┐
                                                  │ 5. Server responds  │
                                                  │                     │
                                                  │ • JWT token         │
                                                  │ • Plan tier         │
                                                  │ • ★ Hysteria 2 cfg  │
                                                  │   (profile for tier)│
                                                  └────────┬─────────────┘
                                                           │
                                                           ▼
                                                  ┌──────────────────────┐
                                                  │ 6. Client connects  │
                                                  │                     │
                                                  │ hysteria2 client    │
                                                  │   -c config.json    │
                                                  │   --tun             │
                                                  │   --speed <tier_cap>│
                                                  │                     │
                                                  │ If Gaming Max:      │
                                                  │   probe → set cap   │
                                                  │   adapt loop starts │
                                                  └──────────────────────┘
```

---

## 4. Protocol Config Delivery

### PocketBase Collection: `tier_configs`

```json
{
    "tier": "gaming_mid",
    "configs": [{
        "id": "hysteria2",
        "display_name": "Speed Mode",
        "binary_name": "speedmode",
        "config_json": {
            "server": "sgp1.api.yourdomain.com:443",
            "auth": "pool-password",
            "tls": { "sni": "www.cloudflare.com", "insecure": false },
            "obfs": "salamander",
            "obfs-password": "pool-key",
            "brutal": {
                "enabled": true,
                "up_mbps": 15,
                "down_mbps": 15
            },
            "recv_window_conn": 1048576,
            "recv_window": 4194304,
            "hop-interval": 30
        }
    }]
}
```

The config JSON is the "bottleneck profile." Change these values in PocketBase → next heartbeat delivers them → client experience changes immediately.

### Per-Tier Profiles

**Warp Lite:**
```json
{ "engine": "usque", "config_json": { /* Warp WireGuard config */ } }
```
No Hysteria 2 at all. Hardcoded 1 MB/s client cap.

**Stealth Browse:**
```json
{
    "brutal": { "enabled": false },
    "recv_window_conn": 8388608,
    "recv_window": 33554432,
    "hop-interval": 15
}
```
Standard CC (AIMD) + large buffers + port hopping → high throughput for streaming, but standard CC causes bufferbloat under school WiFi congestion → latency spikes make gaming unplayable.

**Gaming Mid:**
```json
{
    "brutal": { "enabled": true, "up_mbps": 15, "down_mbps": 15 },
    "recv_window_conn": 1048576,
    "recv_window": 4194304,
    "hop-interval": 30
}
```
Brutal CC enabled but target (15 Mbps) is well below the cap (50 Mbps). Brutal constantly tries to push past the pipe's actual capacity → congestion signals → periodic rubber-banding. Small receive window adds stutter.

**Gaming Max:**
```json
{
    "brutal": { "enabled": true, "up_mbps": 0, "down_mbps": 0 },
    "recv_window_conn": 16777216,
    "recv_window": 67108864,
    "hop-interval": 0
}
```
Brutal targets start at 0 — set dynamically by the probe on connect. Large buffers, no port hopping. Adaptation loop adjusts target mid-session via health check RTT.

---

## 5. Caps Package

```go
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

// IsAdaptive returns true if this tier uses dynamic cap adjustment mid-session.
func IsAdaptive(tier string) bool { return tier == "gaming_max" }
```

---

## 6. Connection Flow

```
User taps Connect
        │
        ▼
  Load protocol configs from storage (from activation or heartbeat)
  Look up tier cap: caps.Get(tier)
        │
        ├── Gaming Max? → Probe (1MB download, measure BW + RTT)
        │                  → Set initial cap = result
        │                  → Start engine with Brutal target = result
        │
        ├── Other? → Start engine with hardcoded cap
        │
        ▼
  Setup TUN → Engage kill switch → Guard DNS
  Start engine with config + --speed <cap> + --tun
  Start health check (15s)
        │
        ├── Gaming Max? → Launch adaptBandwidth goroutine
        │
        ▼
  GUI: "Connected" + tier name + elapsed time
```

---

## 7. Background Updater

Unchanged from previous iteration — the lifeline for when protocols get blocked.

---

## 8. Heartbeat (Config Carrier + Update Carrier)

Returns suspension status, fresh token, updated protocol configs, and optional update command. Config refresh is how you change bottleneck profiles in the field.

---

## 9. v1 → v2 Summary

| Area | v1 | v2 |
|------|----|----|
| Speed limiting | 5-stage probe + EWMA + dynamic (all tiers) | **Static caps for 3 tiers, adaptive for Gaming Max only** |
| Bottleneck profiles | Implicit (just speed caps) | **Explicit per-tier engine config tuning** (Brutal targets, recv_window, hop-interval) |
| Adaptation | Separate monitor package, background 100KB downloads, EWMA smoothing | **Reuses health check timing, single goroutine, ~50 lines** |
| Probe | Staged ramp per tier | **Single 1MB download for Gaming Max only** |
| Helper service | IPC + install + verify | **Dropped** — engine --tun |
| Update | Two-phase sentinel | **Background download + auto-revert** |
| Server configs | Heartbeat only | **Activation + heartbeat dual path** |
| State machine | 6 states + GRACE | **3 states** |
