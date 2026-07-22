# V2 — Quick Reference

---

## The Four Bottleneck Profiles

| Tier | Engine | Speed Cap | Brutal CC | Recv Window | Hop | Experience |
|------|--------|-----------|-----------|-------------|-----|------------|
| Warp Lite | usque/Warp | 1 MB/s | N/A | N/A | N/A | Slow + inconsistent |
| Stealth Browse | Hysteria 2 | 6 MB/s | **Disabled** | 32 MB | 15s | Good streaming, **terrible gaming** (bufferbloat) |
| Gaming Mid | Hysteria 2 | 6.25 MB/s | **Target 15 Mbps** (cap is 50) | 4 MB | 30s | **Rubber-banding + jitter** (Brutal fights itself) |
| Gaming Max | Hysteria 2 | **Adaptive** | **Target = probed BW** | 64 MB | 0 | Smooth, adapts to QoS |

The profiles are JSON in PocketBase `tier_configs`. Change them live → clients get them on next heartbeat.

---

## Gaming Max Adaptation

```
On connect: download 1MB from hub → measure bandwidth + baseline RTT
            → set Brutal CC target = measured bandwidth

Every 15s: health check measures HTTP response time through tunnel

  RTT > 3× baseline × 2 in a row?  → halve Brutal target → restart engine
  RTT < 1.5× baseline × 4 in a row? → +25% Brutal target → restart engine
```

~50 lines. No EWMA. No background downloads. No separate package.

---

## Code: caps.Get()

```go
func Get(tier string) int {
    switch tier {
    case "warp_lite":      return   8_000_000
    case "stealth_browse": return  48_000_000
    case "gaming_mid":     return  50_000_000
    case "gaming_max":     return           0  // 0 = probe on connect
    default:               return           0
    }
}
func IsAdaptive(tier string) bool { return tier == "gaming_max" }
```

---

## Pipeline

```
Paper code → app → hub validates + binds device → returns token + Hysteria 2 config
  → client saves config → connect: probe (Gaming Max) or static cap (others)
  → engine starts with --tun --speed <cap> -c config.json
  → health check pings every 15s
  → Gaming Max: adaptation loop monitors health check RTT, adjusts Brutal target
```

Heartbeat refreshes configs (change server IPs, bottleneck profiles) + triggers background updates.

---

## What to Delete From v1

```
rm -rf internal/monitor/         # Replaced by 50-line goroutine in gui/app.go
rm -rf internal/helper/          # Engine --tun flag
rm -f  internal/manager/disguise_windows.go
rm -f  internal/manager/process_windows.go
rm -f  internal/manager/process_darwin.go
```

Probe stays but simplified — only 1MB download, only for Gaming Max.

---

## What to Add/Change

| File | Change | Lines |
|------|--------|-------|
| `internal/caps/caps.go` | New — per-tier caps + IsAdaptive() | 45 |
| `internal/probe/probe.go` | Simplify to single 1MB download | 40 |
| `internal/manager/process.go` | Add dynamic cap, updateBrutalTarget(), RestartEngine() | +30 |
| `internal/gui/app.go` | Add adaptBandwidth() goroutine | +50 |
| `server/pb_hooks/activation.pb.js` | New — validate + bind + return configs | 80 |
| `server/pb_hooks/heartbeat.pb.js` | New — suspension + token rotate + config refresh | 70 |

---

## Key Principle

**Don't fight the engine. Tune it.**

Hysteria 2's Brutal CC, recv_window, and hop-interval are already powerful shaping tools. The four bottleneck profiles use these parameters directly — no external traffic shaping, no kernel modules, no helper service needed. The only "extra code" is the Gaming Max adaptation loop, which uses the health check HTTP timing that already exists.

No BBR. No EWMA. No staged ramps. Just config JSON + 50 lines of adaptation.
