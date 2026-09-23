# Threat Model: Implementation Risks & Mediations for the VPN Service

> **Classification:** Internal Security Architecture — Defensive Companion to `threat-model-rookie-provider.md`
> **Context:** This document inverts the perspective of the red-team implementation guide. Where that document describes *how to weaponize* each component, this one examines what those same attack surfaces mean for a defender operating this service, and prescribes concrete mitigations.
> **Purpose:** Ensure that when we design, build, and operate this VPN service, we understand the risks inherent in its architecture and have a plan to mediate each one — **before** an adversary finds them.
> **Audience:** Developers, infrastructure engineers, security reviewers, and operators of the service.

---

## Table of Contents

1. [Threat Model Methodology](#1-threat-model-methodology)
2. [Critical Asset Inventory](#2-critical-asset-inventory)
3. [Trust Model & Boundaries](#3-trust-model--boundaries)
4. [Threat 1 — Privileged Helper Service Abuse](#4-threat-1--privileged-helper-service-abuse)
5. [Threat 2 — Update Mechanism Subversion](#5-threat-2--update-mechanism-subversion)
6. [Threat 3 — Heartbeat & Telemetry as Covert Channel](#6-threat-3--heartbeat--telemetry-as-covert-channel)
7. [Threat 4 — Named Pipe ACL & Local EoP](#7-threat-4--named-pipe-acl--local-eop)
8. [Threat 5 — CA Trust Store Poisoning](#8-threat-5--ca-trust-store-poisoning)
9. [Threat 6 — PocketBase / Admin Hub as C2 Infrastructure](#9-threat-6--pocketbase--admin-hub-as-c2-infrastructure)
10. [Threat 7 — Build Pipeline & Supply Chain](#10-threat-7--build-pipeline--supply-chain)
11. [Threat 8 — Device Fingerprinting & Targeting](#11-threat-8--device-fingerprinting--targeting)
12. [Threat 9 — Lateral Movement from VPN Client](#12-threat-9--lateral-movement-from-vpn-client)
13. [Threat 10 — Persistence Beyond Uninstall](#13-threat-10--persistence-beyond-uninstall)
14. [Defense-in-Depth Summary](#14-defense-in-depth-summary)
15. [Implementation Roadmap](#15-implementation-roadmap)

---

## 1. Threat Model Methodology

We use a lightweight STRIDE-per-component approach, adapted for this service architecture:

| Mnemonic | Property | What we ask |
|----------|----------|-------------|
| **S**poofing | Authenticity | Can an attacker impersonate a legitimate component? |
| **T**ampering | Integrity | Can data or code be modified in transit or at rest? |
| **R**epudiation | Non-repudiability | Can actions be denied after the fact? |
| **I**nformation Disclosure | Confidentiality | Can sensitive data leak? |
| **D**enial of Service | Availability | Can the service be disrupted? |
| **E**levation of Privilege | Authorization | Can a low-privilege actor gain higher privilege? |

For each component we rate: **Severity** (Critical / High / Medium / Low), **Likelihood** (Certain / Likely / Possible / Rare), and **Priority** (P0–P4).

---

## 2. Critical Asset Inventory

Before enumerating threats, we must know what we are protecting.

| Asset | Description | Sensitivity | Who Has Access |
|-------|-------------|-------------|----------------|
| **Hub VPS (Caddy + PocketBase)** | Admin API server, database, update file server | **Critical** — total compromise = full C2 | Operators (SSH key + MFA); public HTTPS |
| **PocketBase Admin UI** | `/_/` web admin panel | **Critical** — full control over all devices | Authorized admins only |
| **PocketBase Database** | Device fingerprints, configs, harvested credential data | **Critical** — PII + secrets | Backend processes; admins |
| **Update Server** (`/var/www/updates/`) | Signed binaries and SHA256 hashes | **Critical** — code execution on every client | Web server process; ops (write) |
| **Signing Key (ed25519)** | Key used to sign update payloads | **Critical** — if stolen, attacker can sign malicious updates | Restricted to build pipeline; HSM or offline |
| **Helper Service** (SYSTEM/root) | Named-pipe IPC for TUN, DNS, firewall | **High** — runs as root on every client | Local user (via pipe ACL) |
| **Client Database** (storage) | Local config, exfil cache, metrics | **Medium** — contains fingerprints, metadata | Local user; app process |
| **Heartbeat Channel** | TLS connection to hub | **Medium** — carries operational telemetry | Hub TLS endpoint |
| **Device Fingerprint** | Hash of machine identity | **Medium** — used for device binding & targeting | Client; hub database |
| **Logs** (client + server) | Operational logs | **Low–Medium** — may contain metadata | Ops team; log rotation service |

---

## 3. Trust Model & Boundaries

### 3.1 Trust Zones

```
Zone 0 (Hub VPS — TRUSTED)
  ├── PocketBase (API + Admin UI)
  ├── Caddy (TLS termination, reverse proxy)
  └── Update Server (binary serving)

Zone 1 (Operator Workstation — TRUSTED but limited)
  ├── SSH to VPS
  └── PocketBase Admin UI access

Zone 2 (Build Environment — TRUSTED ephemeral)
  ├── Source code + signing key access
  └── CI/CD pipeline

Zone 3 (Client Device — UNTRUSTED)
  ├── GUI (user context)
  ├── Helper Service (SYSTEM/root — partial trust)
  ├── Heartbeat (network-facing)
  └── Local Storage
```

### 3.2 Trust Boundaries (Critical)

```
Hub VPS (Zone 0) ↔ Client (Zone 3)
    ↓                    ↓
  HTTPS/TLS           TLS with cert pinning
  Auth: bearer token derived from device fingerprint
  ↓                    ↓
  ❌ The client trusts the hub for:
     - Version/update info
     - Protocol config
     - Heartbeat response
  ❌ The hub trusts the client for:
     - Device authentication token
     - Telemetry data (untrusted input!)
```

**Key insight:** The trust boundary between hub and client is asymmetric. The client places *far more trust* in the hub than vice versa. A compromised hub can push malicious updates, CA certificates, and commands to every connected client. A compromised client can only poison its own data.

### 3.3 Principles Applied

1. **Least privilege** — Each component gets only the permissions it needs.
2. **Defense in depth** — No single security measure is relied upon alone.
3. **Fail secure** — When a component fails, it should default to the safe state.
4. **Auditability** — All security-relevant actions are logged and reviewable.
5. **Separation of duties** — Build, sign, and deploy actions require different credentials.

---

## 4. Threat 1 — Privileged Helper Service Abuse

### 4.1 Component Overview

The helper service (`internal/helper/`) runs as **SYSTEM (Windows)** or **root (macOS/Linux)** and exposes a JSON-over-named-pipe interface for the GUI/manager process to create TUN devices, set DNS, and configure firewall rules.

### 4.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T1-A** Pipe command injection | EoP | A low-privilege process on the device sends crafted commands to the named pipe to execute arbitrary OS commands as SYSTEM/root | **Critical** |
| **T1-B** Unauthenticated pipe access | Spoofing | Any local user or sandboxed process can write to the pipe because the ACL is too permissive | **High** |
| **T1-C** Command handler logic bugs | Tampering | A command handler has a buffer overflow, path traversal, or injection vulnerability allowing unexpected behavior | **Critical** |
| **T1-D** Red-team build tag leakage | Info Disclosure | An instrumented build (`-tags=redteam`) is accidentally shipped or an attacker extracts command handlers | **High** |
| **T1-E** Helper crash → fail-open | DoS | Helper crashes and leaves TUN device, firewall, or DNS in an insecure state | **Medium** |

### 4.3 Mediations

#### T1-A / T1-B: Pipe ACL & Authentication

**Requirement:** The named pipe MUST authenticate the caller before processing privileged commands.

```go
// internal/helper/service_windows.go — ACL enforcement

func createPipeWithACL() (net.Listener, error) {
    // SDDL: Only allow the specific user SID that launched the VPN client.
    // NEVER use a wide ACL that allows ALL authenticated users.
    //
    // CORRECT:
    //   "D:(A;;GA;;;<user-sid>)"    — only the owning user
    //   "(A;;GA;;;SY)"              — SYSTEM (for service management)
    //
    // WRONG:
    //   "(A;;GA;;;AU)"              — ALL Authenticated Users
    //   "(A;;GA;;;WD)"              — EVERYONE (WORLD)
    //
    sddl := fmt.Sprintf("D:(A;;GA;;;%s)(A;;GA;;;SY)",
        escapeSDDL(sid.String()))

    config := &pipe.Config{
        SecurityDescriptor: sddl,
        MessageMode:        true,
        InputBufferSize:    65536,
        OutputBufferSize:   65536,
    }
    return pipe.Listen(`\\.\pipe\MyVPNHelper`, config)
}
```

**Unix equivalent:** Use **AF_UNIX** sockets with `SO_PEERCRED` / `getpeereid()` to verify the connecting process UID matches the expected user.

```go
// internal/helper/service_unix.go — Peer credential verification

func verifyCaller(conn *net.UnixConn) error {
    file, err := conn.File()
    if err != nil {
        return err
    }
    defer file.Close()

    // Retrieve the UID of the peer process
    credentials, err := syscall.GetsockoptUcred(
        int(file.Fd()), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
    if err != nil {
        return err
    }

    // The helper is running as root. Only allow the specific user
    // who launched the VPN GUI (stored at service start).
    if credentials.Uid != expectedUID {
        return fmt.Errorf("caller UID %d does not match expected UID %d",
            credentials.Uid, expectedUID)
    }
    return nil
}
```

**Implementation checklist:**
- [ ] Windows: Derive pipe SDDL from the user SID at service start
- [ ] macOS/Linux: Verify `SO_PEERCRED` on every connection
- [ ] Reject connection immediately if verification fails — do not read any data
- [ ] Log failed authentication attempts (rate-limited to avoid log flood)

#### T1-C: Command Handler Hardening

**Requirement:** Every command handler must validate inputs, use safe APIs, and never pass user-controlled data to OS shell execution.

```go
// RULE 1: No shell invocation. Use exec.Command with a fixed argument list.
// WRONG:
//   exec.Command("cmd", "/c", userInput).Run()
// RIGHT:
//   exec.Command("netsh", "interface", "ip", "set", "address",
//       sanitizeInterfaceName(tunName), "static", sanitizeIP(ip), ...).Run()

// RULE 2: Validate all parameters against an allowlist.
func validateTUNParams(params map[string]string) error {
    allowedParams := map[string]func(string) bool{
        "ip":  isValidIP,
        "dns": isValidIP,
        "mtu": isValidMTU,
    }
    for key, value := range params {
        validator, ok := allowedParams[key]
        if !ok {
            return fmt.Errorf("unknown parameter: %s", key)
        }
        if !validator(value) {
            return fmt.Errorf("invalid value for %s: %q", key, value)
        }
    }
    return nil
}

// RULE 3: Bounded buffers for all pipe reads.
func readCommand(conn net.Conn) ([]byte, error) {
    const maxCommandSize = 1 << 16 // 64 KB
    buf := make([]byte, maxCommandSize+1)
    n, err := conn.Read(buf)
    if err != nil {
        return nil, err
    }
    if n > maxCommandSize {
        return nil, fmt.Errorf("command too large: %d bytes", n)
    }
    return buf[:n], nil
}
```

**Fuzz testing requirement:** The named pipe command parser MUST be fuzz-tested:

```go
// internal/helper/fuzz_test.go
func FuzzParseCommand(f *testing.F) {
    f.Fuzz(func(t *testing.T, data []byte) {
        var cmd Command
        json.Unmarshal(data, &cmd) // Must not panic or crash
        if cmd.Action != "" {
            // Ensure handler doesn't panic on any valid-format input
            resp := dispatchCommand(cmd)
            _ = resp
        }
    })
}
```

#### T1-D: Build Tag Governance

**Requirement:** The `redteam` build tag must NEVER appear in release builds. Enforce with CI gates.

```makefile
# Makefile — build gate
.PHONY: check-no-redteam
check-no-redteam:
	@echo "Checking for redteam build tags..."
	@! grep -rn '//go:build.*redteam' --include='*.go' cmd/ internal/ || \
		(echo "ERROR: redteam build tag found in source!"; exit 1)

.PHONY: build-release
build-release: check-no-redteam
	go build -tags="" -ldflags="-s -w" -o build/myvpn ./cmd/myvpn
```

**Strict separation:** The `redteam` instrumented files (`*_redteam.go`) should live in a separate directory or branch that is NEVER included in release branches:

```yaml
# .github/workflows/release.yml
jobs:
  build:
    steps:
      - uses: actions/checkout@v4
      - run: make check-no-redteam
      - run: make build-release
```

#### T1-E: Fail-Safe Helper

**Requirement:** On crash, the helper must clean up resources and leave the system in a safe state.

```go
// internal/helper/service.go — Graceful shutdown

func (s *Service) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) {
    defer func() {
        // Clean up ALL resources on exit
        s.removeAllTUNs()
        s.restoreDNS()
        s.restoreFirewallRules()
        s.logger.Info("helper service stopped — all resources cleaned")
    }()

    // ... main service loop ...
}

func (s *Service) removeAllTUNs() {
    // Enumerate and remove any TUN devices created by this helper
    interfaces, err := s.tunManager.List()
    if err != nil {
        s.logger.Error("failed to list TUNs during cleanup", "err", err)
        return
    }
    for _, iface := range interfaces {
        if err := s.tunManager.Delete(iface.Name); err != nil {
            s.logger.Warn("failed to remove TUN during cleanup", "name", iface.Name)
        }
    }
}
```

### 4.4 Residual Risks

1. **Local admin can always bypass pipe ACL** — If the attacker has admin rights, they can Terminate or replace the helper service. This is accepted (local admin is equivalent to owning the device).
2. **Kernel-level attack** — A rootkit can intercept pipe communication. Mitigation: Code signing + TPM-based integrity measurement (future enhancement).

---

## 5. Threat 2 — Update Mechanism Subversion

### 5.1 Component Overview

The update mechanism (`internal/updater/update.go`) polls `GET /api/v1/version` for the latest version, downloads the binary, verifies SHA256, and replaces the running executable.

### 5.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T2-A** Hub compromise pushes malicious update | Tampering | Attacker gains access to hub VPS and modifies version endpoint or binary server to serve trojaned binaries | **Critical** |
| **T2-B** MITM on update download | Tampering | Attacker intercepts HTTPS to update server (despite cert pinning — see T3) | **Critical** |
| **T2-C** SHA256-only verification bypass | Tampering | Without code signing, anyone who controls the version API can serve any binary with a matching SHA256 | **Critical** |
| **T2-D** Rollback attack | Tampering | Attacker serves an older version with known vulnerabilities | **High** |
| **T2-E** Targeted payload delivery (as documented in red-team guide) | Tampering | Server gating that delivers different binaries to different devices — used legitimately for staged rollouts, but abusable | **High** |

### 5.3 Mediations

#### T2-A / T2-C: Code Signing (Ed25519)

**Requirement:** Updates MUST be signed with an Ed25519 key. SHA256 verification alone is insufficient — the hub controls the hash.

```go
// internal/updater/verify.go — Code signing verification

import (
    "crypto/ed25519"
    "encoding/hex"
    "io"
    "os"
)

// Embedded public key (compiled into binary, not fetched from server)
var publicKey, _ = hex.DecodeString("e1b2c3d4...") // 64 hex chars = 32 bytes

func VerifyUpdate(binaryPath string, signatureHex string) error {
    sig, err := hex.DecodeString(signatureHex)
    if err != nil {
        return fmt.Errorf("invalid signature hex: %w", err)
    }

    f, err := os.Open(binaryPath)
    if err != nil {
        return err
    }
    defer f.Close()

    hash := sha256.New()
    if _, err := io.Copy(hash, f); err != nil {
        return err
    }

    if !ed25519.Verify(publicKey, hash.Sum(nil), sig) {
        return fmt.Errorf("signature verification failed — binary may be tampered")
    }
    return nil
}
```

**Key management requirements:**

```bash
# 1. Generate key on an OFFLINE, air-gapped machine
#    Never on a networked system.
openssl genpkey -algorithm ed25519 -out /media/usb/update-signing-key.pem

# 2. Extract public key for embedding
openssl pkey -in /media/usb/update-signing-key.pem -pubout -out public-key.pem

# 3. Embed public key into source:
#    internal/updater/signing_key.go
#    var PublicKey = [...] (HEX-ENCODED)

# 4. Signing happens in a SEPARATE step from building:
#    ./scripts/sign-update.sh build/myvpn-1.3.5.tar.gz /media/usb/update-signing-key.pem
```

**CI/CD integration:**

```yaml
# .github/workflows/sign-release.yml
jobs:
  sign:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with:
          name: release-package
      - name: Sign with HSM-backed key
        run: |
          # Key never leaves the HSM. Signing is done via PKCS#11 or KMS.
          ./scripts/sign-with-hsm.sh myvpn-*.tar.gz
      - name: Upload signature
        run: |
          # Upload both binary and .sig file to releases/
          aws s3 cp *.tar.gz s3://updates/releases/
          aws s3 cp *.tar.gz.sig s3://updates/releases/
```

**Update verification flow (client-side):**

```go
// internal/updater/update.go — Full verification chain

func (u *Updater) ApplyUpdate(version Response) error {
    // Step 1: Download binary
    binaryPath, err := u.downloadBinary(version.URL)
    if err != nil {
        return fmt.Errorf("download failed: %w", err)
    }

    // Step 2: Verify SHA256 (first line of defense)
    if err := verifySHA256(binaryPath, version.SHA256); err != nil {
        return fmt.Errorf("SHA256 mismatch: %w", err)
    }

    // Step 3: Verify Ed25519 signature (second line of defense)
    // The signature is served alongside the binary, e.g. binary.tar.gz.sig
    sigURL := version.URL + ".sig"
    sigData, err := u.fetchSignature(sigURL)
    if err != nil {
        return fmt.Errorf("signature fetch failed: %w", err)
    }

    if err := VerifyUpdate(binaryPath, string(sigData)); err != nil {
        // CRITICAL: Do NOT proceed even if SHA256 matched
        // If signature verification fails, the update is compromised
        u.alertSecurityEvent("update_signature_failure", map[string]interface{}{
            "version": version.Latest,
            "error":   err.Error(),
        })
        return fmt.Errorf("signature verification failed: %w", err)
    }

    // Step 4: Apply update
    return u.applyBinary(binaryPath)
}
```

#### T2-B: Cert Pinning for Update Server

**Requirement:** The update download URL MUST use a separate, pinned TLS certificate from the heartbeat/API hub. This way, compromise of the API server does not automatically enable forging updates.

```go
// internal/updater/transport.go — Dedicated TLS config for update downloads

func updateTLSConfig() *tls.Config {
    return &tls.Config{
        // Pin the specific update server certificate (NOT the API hub cert)
        VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
            for _, raw := range rawCerts {
                cert, _ := x509.ParseCertificate(raw)
                if cert == nil {
                    continue
                }
                actualHash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
                expected := "d4e5f6a7..." // Update server SPKI hash
                if hex.EncodeToString(actualHash[:]) == expected {
                    return nil
                }
            }
            return fmt.Errorf("no matching update server certificate pin")
        },
    }
}
```

#### T2-D: Minimum Version Enforcement

**Requirement:** The client must refuse to install a version older than a compiled-in minimum.

```go
// internal/updater/version.go

// MinVersion is the oldest version the client will accept.
// Updated with each release.
var MinVersion = semver.MustParse("1.3.0")

func (u *Updater) AcceptableVersion(v string) bool {
    parsed, err := semver.Parse(v)
    if err != nil {
        return false
    }
    if parsed.LT(MinVersion) {
        u.logger.Warn("rejecting rollback attempt", "version", v, "minimum", MinVersion)
        return false
    }
    return true
}
```

#### T2-E: Staged Rollout Transparency

If you need to serve different binaries to different devices (e.g., staged rollout), do so **without** making it a covert targeting mechanism:

```go
// internal/updater/rollout.go — Honest staged rollout

type RolloutGroup struct {
    // Percentage of devices that should receive this version.
    // Logged and auditable.
    Percentage int    `json:"pct"`
    Version    string `json:"version"`
    Reason     string `json:"reason"` // e.g., "gradual rollout of 1.4.0"
}

// The version response MUST include the rollout reason in logs.
// The client MUST log which rollout group it falls into.
func (u *Updater) determineRolloutGroup(fp string, groups []RolloutGroup) *RolloutGroup {
    // Deterministic hash-based assignment for reproducibility
    hash := sha256.Sum256([]byte(fp + u.config.rolloutSalt))
    bucket := int(binary.BigEndian.Uint64(hash[:8]) % 100)
    cumulative := 0
    for _, g := range groups {
        cumulative += g.Percentage
        if bucket < cumulative {
            u.logger.Info("rollout group assignment", "version", g.Version, "reason", g.Reason)
            return &g
        }
    }
    return nil
}
```

**Prohibited:** Serving a *different* binary to a specific device without the device being aware that it is in a targeted group. All device-specific version responses must be logged on both server and client.

### 5.4 Residual Risks

1. **Zero-day in updater code itself** — Rare but possible. Mitigation: Minimal attack surface (no shell exec, no dynamic loading).
2. **Compromised signing key** — Catastrophic. Mitigation: HSM-backed key; offline root; key rotation plan; monitoring for unexpected signatures.

---

## 6. Threat 3 — Heartbeat & Telemetry as Covert Channel

### 6.1 Component Overview

The heartbeat (`internal/heartbeat/heartbeat.go`) sends periodic telemetry to the hub. The red-team document shows how to abuse this for command delivery and exfiltration.

### 6.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T3-A** Heartbeat response carries hidden commands | Tampering | Hub returns commands in heartbeat response that enable malicious behavior | **High** |
| **T3-B** Telemetry payload carries exfiltrated data | Info Disclosure | Client encodes stolen data in telemetry fields that appear legitimate | **High** |
| **T3-C** Cert pinning prevents detection of rogue hub | Repudiation | The client trusts the pinned cert, making a malicious hub indistinguishable from a legitimate one | **Medium** |
| **T3-D** Heartbeat frequency enables beaconing | Info Disclosure | 60-second heartbeat interval provides a high-resolution beacon for C2 | **Low** |

### 6.3 Mediations

#### T3-A / T3-B: Strict Schema Validation

**Requirement:** The heartbeat request and response schemas must be strictly defined, with no free-form "telemetry blob" fields that can carry arbitrary data.

```go
// internal/heartbeat/types.go — Strict heartbeat types

// HeartbeatRequest — NO generic blob/extra fields allowed.
// Every field must have a specific purpose and be validated.
type HeartbeatRequest struct {
    Version   string `json:"version"`          // semver, validated
    OS        string `json:"os"`               // enum: windows/darwin/linux
    UptimeSec int    `json:"uptime_seconds"`   // 0–max uptime
    BytesUp   int64  `json:"bytes_up"`         // >= 0
    BytesDown int64  `json:"bytes_down"`       // >= 0

    // NO catch-all fields.
    // NO "telemetry_blob", "extra", "metadata", or "custom" fields.
}

// HeartbeatResponse — Strict response with NO command injection vector.
type HeartbeatResponse struct {
    Status    string `json:"status"`    // "active" | "maintenance" | "deactivated"
    ServerTime string `json:"server_time"`

    // Version check — structured, not command-like
    VersionInfo *VersionInfo `json:"version_info,omitempty"`

    // Protocol config — structured, not command-like
    Protocols []ProtocolConfig `json:"protocols,omitempty"`

    // NO "commands" array.
    // NO "actions" array.
    // NO freeform JSON payload.
}
```

**Server-side validation:**

```javascript
// pb_hooks/heartbeat.pb.js — Validate incoming heartbeat structure

routerAdd("POST", "/api/heartbeat", (c) => {
    const body = c.requestInfo().body;

    // Reject heartbeats with unexpected fields
    const allowedFields = ["version", "os", "uptime_seconds", "bytes_up", "bytes_down"];
    for (const field of Object.keys(body)) {
        if (!allowedFields.includes(field)) {
            // Log and reject — this is suspicious
            $app.logger().warn("heartbeat with unexpected field", "field", field, "device_fp", deviceFP);
            return c.json(400, { error: "unexpected field: " + field });
        }
    }

    // Validate types
    if (typeof body.version !== "string" || !semver.valid(body.version)) {
        return c.json(400, { error: "invalid version" });
    }
    if (!["windows", "darwin", "linux"].includes(body.os)) {
        return c.json(400, { error: "invalid os" });
    }
    // ... additional validation ...

    // Process heartbeat — NEVER include commands in response
    return c.json(200, {
        status: "active",
        server_time: new Date().toISOString(),
        version_info: getVersionInfo(body.os),
        protocols: getProtocolsForDevice(deviceFP),
    });
});
```

#### T3-C: Dual Pinning / Break-Glass

**Requirement:** While cert pinning is necessary, we need a mechanism to detect and respond to hub compromise.

```go
// internal/heartbeat/pinning.go — Trust-aware cert pinning

// TrustAnchors holds multiple acceptable SPKI hashes.
// The primary hash is the current hub certificate.
// A secondary (emergency) hash can be updated via an out-of-band update.
var TrustAnchors = []string{
    "a1b2c3d4...", // Primary hub cert (current)
    "e5f6a7b8...", // Secondary (emergency backup, updated via signed config)
}

func verifyHubCertificate(rawCerts [][]byte, _ [][]*x509.Certificate) error {
    for _, raw := range rawCerts {
        cert, err := x509.ParseCertificate(raw)
        if err != nil {
            continue
        }
        hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
        hashHex := hex.EncodeToString(hash[:])
        for _, anchor := range TrustAnchors {
            if hashHex == anchor {
                return nil // Trusted
            }
        }
    }
    return fmt.Errorf("hub certificate not in trust anchors")
}

// ReportTrustAnomaly — If a pinning failure occurs, log it locally
// and refuse to connect until the user intervenes.
func (h *Heartbeat) validateTrust() error {
    err := verifyHubCertificate(h.rawCerts, nil)
    if err != nil {
        // Write a persistent alert that survives reboot
        h.storage.WriteSecurityEvent("cert_pinning_failure", map[string]string{
            "observed_certs": fmt.Sprintf("%x", h.rawCerts),
            "timestamp":      time.Now().UTC().Format(time.RFC3339),
        })
        // DO NOT fall back to unpinned HTTPS
        // DO NOT ignore the error
        return err
    }
    return nil
}
```

#### T3-D: Heartbeat Jitter

Add random jitter to heartbeat intervals to prevent predictable beaconing:

```go
// internal/heartbeat/heartbeat.go — Jittered heartbeat

func (h *Heartbeat) nextInterval() time.Duration {
    base := 60 * time.Second
    jitter := time.Duration(rand.Int63n(30000)) * time.Millisecond // ±15s
    return base + jitter
}

func (h *Heartbeat) loop() {
    for {
        select {
        case <-time.After(h.nextInterval()):
            h.send()
        case <-h.stopCh:
            return
        }
    }
}
```

### 6.4 Residual Risks

1. **Heartbeat is still a side channel** — Even with strict schemas, the mere existence and timing of heartbeats conveys information. Acceptable for a VPN service.
2. **Future protocol field abuse** — If a new field is added to the heartbeat, it could be abused. Mitigation: Mandatory security review for any heartbeat schema changes.

---

## 7. Threat 4 — Named Pipe ACL & Local EoP

### 7.1 Component Overview

The helper service communicates with the GUI/manager via a named pipe (Windows) or Unix domain socket (macOS/Linux). The ACL on this pipe determines who can issue privileged commands.

### 7.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T4-A** Overly permissive pipe ACL allows any user to send commands | EoP | Default named pipe ACLs may grant "Everyone" write access | **Critical** |
| **T4-B** Pipe path predictable → symlink attack (Unix) | EoP | Attacker places a symlink at the expected socket path to intercept commands | **High** |
| **T4-C** No rate limiting → brute-force command injection | Tampering | Attacker can rapidly send commands to the pipe to exploit race conditions or timing bugs | **Medium** |
| **T4-D** Pipe name known → third-party process connects | Spoofing | Malware on the device knows the pipe name and sends crafted commands | **High** |

### 7.3 Mediations

#### T4-A: Pipe ACL — Only the VPN user can connect

Already covered in [Threat 1, T1-A/B](#41-component-overview) above. Key points:

- **Windows:** Create pipe with SDDL that grants access ONLY to the specific user SID and LOCAL_SYSTEM.
- **macOS/Linux:** Use `AF_UNIX` with `SO_PEERCRED` to verify the caller's UID.

**Audit requirement:** Log every pipe connection attempt, including the caller's SID/UID and whether it was allowed or denied:

```go
func (s *Service) handleConnection(conn net.Conn) {
    defer conn.Close()

    caller := s.getCallerIdentity(conn)
    s.logger.Info("pipe connection", "caller", caller)

    if !s.isAuthorized(caller) {
        s.logger.Warn("unauthorized pipe connection attempt", "caller", caller)
        // Send a generic error — don't reveal why access was denied
        json.NewEncoder(conn).Encode(Response{
            Error: "access denied",
        })
        return
    }

    s.processCommands(conn)
}
```

#### T4-B: Unix Socket Path Protection

```go
// internal/helper/service_unix.go — Socket creation

func (s *Service) createSocket() (net.Listener, error) {
    socketPath := "/var/run/myvpn/helper.sock"

    // Ensure directory exists with correct permissions
    if err := os.MkdirAll(filepath.Dir(socketPath), 0750); err != nil {
        return nil, err
    }

    // Remove any existing socket (must be clean shutdown)
    os.Remove(socketPath)

    // Create socket
    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        return nil, err
    }

    // Set permissions so only the expected user can connect
    if err := os.Chmod(socketPath, 0660); err != nil {
        listener.Close()
        return nil, err
    }

    // Also set ownership to the expected user
    if err := os.Chown(socketPath, expectedUID, -1); err != nil {
        listener.Close()
        return nil, err
    }

    return listener, nil
}
```

**Symlink attack prevention:** Always delete and recreate the socket path on service start. Never follow symlinks:

```go
// Before creating, verify the path is NOT a symlink
func checkNotSymlink(path string) error {
    info, err := os.Lstat(path)
    if err == nil {
        if info.Mode()&os.ModeSymlink != 0 {
            return fmt.Errorf("refusing to overwrite symlink at %s", path)
        }
        // It exists as a regular file/socket — that's fine, we'll remove it
    } else if !os.IsNotExist(err) {
        return err
    }
    return nil
}
```

#### T4-C: Rate Limiting & Backpressure

```go
// internal/helper/ratelimit.go — Per-connection rate limiting

type RateLimiter struct {
    mu         sync.Mutex
    timestamps []time.Time
    maxBurst   int
    window     time.Duration
}

func NewRateLimiter(maxBurst int, window time.Duration) *RateLimiter {
    return &RateLimiter{
        maxBurst: maxBurst,
        window:   window,
    }
}

func (rl *RateLimiter) Allow() bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    now := time.Now()
    cutoff := now.Add(-rl.window)

    // Prune old timestamps
    var active []time.Time
    for _, t := range rl.timestamps {
        if t.After(cutoff) {
            active = append(active, t)
        }
    }
    rl.timestamps = active

    if len(active) >= rl.maxBurst {
        return false
    }

    rl.timestamps = append(rl.timestamps, now)
    return true
}

// Usage in command dispatch:
func (s *Service) processCommands(conn net.Conn) {
    limiter := NewRateLimiter(10, 1*time.Second) // 10 commands/second max
    decoder := json.NewDecoder(conn)

    for {
        if !limiter.Allow() {
            json.NewEncoder(conn).Encode(Response{
                Error: "rate limited — try again later",
            })
            time.Sleep(100 * time.Millisecond)
            continue
        }

        var cmd Command
        if err := decoder.Decode(&cmd); err != nil {
            return // Connection closed or invalid
        }

        resp := dispatchCommand(cmd)
        json.NewEncoder(conn).Encode(resp)
    }
}
```

### 7.4 Residual Risks

1. **Local admin can always bypass** — As with any endpoint protection, a determined local admin can disable or replace the helper. This is an accepted risk.
2. **Pipe eavesdropping** — On Windows, other processes with sufficient privileges may eavesdrop on pipe communications. Mitigation: Consider encrypting sensitive pipe payloads with a per-session key.

---

## 8. Threat 5 — CA Trust Store Poisoning

### 8.1 Component Overview

The red-team document shows how to install a rogue CA certificate into the system trust store, enabling TLS MITM on the device.

### 8.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T5-A** Rogue CA installed via helper command | Tampering | Helper installs attacker CA into system store, enabling MITM on all TLS traffic | **Critical** |
| **T5-B** CA installed into browser-specific stores (Chrome, Firefox) | Tampering | Even if system store is protected, browser stores may be writable | **High** |
| **T5-C** CA persists after VPN uninstall | Repudiation | User uninstalls VPN but rogue CA remains in trust store | **High** |

### 8.3 Mediations

#### T5-A: Remove CA Installation from Helper Commands

**Requirement:** The helper MUST NOT have any "install CA" command — not even in debug builds. CA management belongs to the OS or a dedicated, audited tool.

```go
// internal/helper/commands.go — NO install_ca handler

func dispatchCommand(cmd Command) Response {
    switch cmd.Action {
    case "create_tun":
        return handleCreateTUN(cmd)
    case "delete_tun":
        return handleDeleteTUN(cmd)
    case "set_dns":
        return handleSetDNS(cmd)
    case "get_status":
        return handleGetStatus(cmd)
    // NO: install_ca, run_script, exfil_*, keylog_*, persist, scan_network, reverse_tunnel
    default:
        return Response{Error: "unknown action"}
    }
}
```

**CI gate:** grep for "install_ca" in any release build source.

```bash
# scripts/check-forbidden-commands.sh
#!/bin/bash
# Fail the build if any forbidden command handler exists
FORBIDDEN=("install_ca" "run_script" "exfil_password" "keylog_start" "persist" "scan_network")
for cmd in "${FORBIDDEN[@]}"; do
    if grep -r "case \"$cmd\"" internal/helper/ --include='*.go' > /dev/null 2>&1; then
        echo "ERROR: Found forbidden command handler: $cmd"
        exit 1
    fi
done
echo "OK: No forbidden command handlers found"
```

#### T5-B: Trust Store Integrity Monitoring

**Requirement:** The client should detect unauthorized changes to the system trust store and alert the user.

```go
// internal/security/truststore.go — Trust store monitoring

type TrustStoreMonitor struct {
    // Known-good state: SHA256 hash of the system trust store
    knownHash string
    interval  time.Duration
}

func (m *TrustStoreMonitor) Start(ctx context.Context) {
    ticker := time.NewTicker(m.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            currentHash, err := m.computeTrustStoreHash()
            if err != nil {
                // Trust store not readable — suspicious
                m.alert("trust_store_unreadable")
                continue
            }
            if currentHash != m.knownHash {
                m.alert("trust_store_modified")
            }
        }
    }
}

func (m *TrustStoreMonitor) computeTrustStoreHash() (string, error) {
    switch runtime.GOOS {
    case "windows":
        // Use certutil -store Root to enumerate certificates
        out, err := exec.Command("certutil", "-store", "Root").Output()
        if err != nil {
            return "", err
        }
        hash := sha256.Sum256(out)
        return hex.EncodeToString(hash[:]), nil
    case "darwin":
        out, err := exec.Command("security", "find-certificate", "-a",
            "-p", "/Library/Keychains/System.keychain").Output()
        if err != nil {
            return "", err
        }
        hash := sha256.Sum256(out)
        return hex.EncodeToString(hash[:]), nil
    case "linux":
        // /etc/ssl/certs/ca-certificates.crt or hash directory
        data, err := os.ReadFile("/etc/ssl/certs/ca-certificates.crt")
        if err != nil {
            return "", err
        }
        hash := sha256.Sum256(data)
        return hex.EncodeToString(hash[:]), nil
    }
    return "", fmt.Errorf("unsupported platform")
}
```

#### T5-C: Uninstall Cleanup

**Requirement:** The uninstaller MUST remove any certificates installed by the VPN.

```go
// internal/uninstaller/cleanup.go — Thorough cleanup

func CleanupSystem() error {
    // 1. Remove any CA certificates installed by this product
    switch runtime.GOOS {
    case "windows":
        // Find and delete certificates with our issuer name
        certs, _ := findOurCertificates()
        for _, cert := range certs {
            removeCertificate(cert)
        }
    case "darwin":
        exec.Command("security", "remove-trusted-cert", "-d",
            "/Library/Keychains/System.keychain",
            "/Applications/MyVPN.app/Contents/Resources/ca.pem").Run()
    case "linux":
        os.Remove("/usr/local/share/ca-certificates/vpn-ca.crt")
        exec.Command("update-ca-certificates", "--fresh").Run()
    }

    // 2. Remove any firewall rules
    cleanupFirewallRules()

    // 3. Remove any TUN devices
    cleanupTUNs()

    // 4. Remove any launch agents / services
    cleanupServices()

    // 5. Log what was cleaned
    logCleanup()

    return nil
}
```

### 8.4 Residual Risks

1. **Monitor not running** — If the trust store monitor fails to start, changes go undetected. Mitigation: Monitor health as part of heartbeat.
2. **Race condition** — An attacker could install a CA, use it, and remove it between monitor checks. Mitigation: Shorter check intervals or event-driven monitoring (e.g., ETW on Windows).

---

## 9. Threat 6 — PocketBase / Admin Hub as C2 Infrastructure

### 9.1 Component Overview

PocketBase serves as the admin hub — it handles device authentication, API endpoints, and the admin UI. The red-team document uses it as a C2 server.

### 9.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T6-A** PocketBase admin UI exposed to internet | Spoofing | Admin panel at `/_/` is accessible without VPN or IP restriction | **Critical** |
| **T6-B** Weak admin credentials | Spoofing | Default or weak PocketBase admin credentials allow full system takeover | **Critical** |
| **T6-C** Webhook/API endpoint injection | Tampering | Unvalidated input in API endpoints leads to NoSQL injection or code injection | **High** |
| **T6-D** Harvest data stored unencrypted | Info Disclosure | Exfiltrated credentials stored in plaintext in PocketBase DB | **Critical** |
| **T6-E** Custom hooks introduce vulnerabilities | Tampering | `pb_hooks/` JavaScript hooks run in the PocketBase process and can access all data | **High** |

### 9.3 Mediations

#### T6-A: Admin UI Access Restriction

**Requirement:** The PocketBase admin UI must NEVER be directly exposed to the internet. It must be accessible only through an authenticated tunnel or VPN.

```caddy
# Caddyfile — Admin UI protection

# Public API — accessible from clients
api.getfastvpn.com {
    reverse_proxy localhost:8090

    # Only the specific API routes are public
    @public_routes {
        path /api/heartbeat /api/v1/version /api/device/bind
    }
    handle @public_routes {
        reverse_proxy localhost:8090
    }

    # Everything else (including /_/ admin) is blocked
    handle {
        respond 403 "Forbidden"
    }
}

# Admin access — behind VPN or SSH tunnel only
# This hostname resolves ONLY on the office VPN
admin.getfastvpn.com.internal {
    tls internal
    reverse_proxy localhost:8090
}
```

**SSH tunnel for admin access:**

```bash
# Operators must tunnel in:
ssh -L 8090:localhost:8090 admin-vps
# Then open http://localhost:8090/_/ in browser
```

#### T6-B: Strong Authentication

**Requirement:** Enforce strong passwords, MFA, and session timeouts.

```javascript
// pb_hooks/auth_harden.pb.js

// Enforce password complexity
routerAdd("POST", "/api/admins/request-password-reset", (c) => {
    const body = c.requestInfo().body;
    if (body.password && body.password.length < 16) {
        return c.json(400, { error: "password must be at least 16 characters" });
    }
    if (body.password && !/(?=.*[a-z])(?=.*[A-Z])(?=.*\d)(?=.*[^a-zA-Z\d])/.test(body.password)) {
        return c.json(400, { error: "password must contain uppercase, lowercase, number, and special character" });
    }
});

// Auto-logout after 15 minutes of inactivity
routerAdd("GET", "/api/admins/auth-refresh", (c) => {
    const admin = c.get("admin");
    if (!admin) return c.json(401);

    const lastActivity = admin.get("last_activity") || new Date(0);
    const inactiveMinutes = (new Date() - new Date(lastActivity)) / 60000;

    if (inactiveMinutes > 15) {
        // Force re-authentication
        throw new ClientException(401, "session expired due to inactivity");
    }

    admin.set("last_activity", new Date().toISOString());
    $app.dao().saveRecord(admin);
});
```

#### T6-C: Input Validation & Rate Limiting

```javascript
// pb_hooks/validate_input.pb.js

// Strip unexpected fields from ALL API requests
function sanitizeRequestBody(body, allowedFields) {
    const sanitized = {};
    for (const field of allowedFields) {
        if (body[field] !== undefined) {
            sanitized[field] = body[field];
        }
    }
    return sanitized;
}

// Apply to heartbeat endpoint
routerAdd("POST", "/api/heartbeat", (c) => {
    const allowed = ["version", "os", "uptime_seconds", "bytes_up", "bytes_down"];
    c.requestInfo().body = sanitizeRequestBody(c.requestInfo().body, allowed);
    // ... continue processing ...
});

// Rate limiting per IP
const rateLimitMap = new Map();
const RATE_LIMIT = 100; // requests per minute
const RATE_WINDOW = 60000; // 1 minute

function checkRateLimit(ip) {
    const now = Date.now();
    const entry = rateLimitMap.get(ip) || { count: 0, windowStart: now };
    if (now - entry.windowStart > RATE_WINDOW) {
        entry.count = 0;
        entry.windowStart = now;
    }
    entry.count++;
    if (entry.count > RATE_LIMIT) {
        return false;
    }
    rateLimitMap.set(ip, entry);
    return true;
}
```

#### T6-D: Encrypted Storage for Sensitive Data

**Requirement:** Any potentially sensitive data stored in PocketBase must be encrypted at the application level (database-level encryption is not sufficient — PocketBase admin has access to the raw DB).

```javascript
// pb_hooks/crypto_helper.pb.js — Application-level encryption

const crypto = require('crypto');

// The encryption key is NOT stored in PocketBase.
// It is provided via environment variable, loaded at startup.
const ENCRYPTION_KEY = process.env.PB_ENCRYPTION_KEY;
if (!ENCRYPTION_KEY || ENCRYPTION_KEY.length < 32) {
    throw new Error("PB_ENCRYPTION_KEY must be at least 32 characters");
}

function encrypt(plaintext) {
    const iv = crypto.randomBytes(16);
    const cipher = crypto.createCipheriv('aes-256-gcm',
        Buffer.from(ENCRYPTION_KEY.padEnd(32).slice(0, 32)), iv);
    let encrypted = cipher.update(plaintext, 'utf8', 'hex');
    encrypted += cipher.final('hex');
    const tag = cipher.getAuthTag().toString('hex');
    return JSON.stringify({ iv: iv.toString('hex'), tag, data: encrypted });
}

function decrypt(ciphertextObj) {
    const { iv, tag, data } = JSON.parse(ciphertextObj);
    const decipher = crypto.createDecipheriv('aes-256-gcm',
        Buffer.from(ENCRYPTION_KEY.padEnd(32).slice(0, 32)),
        Buffer.from(iv, 'hex'));
    decipher.setAuthTag(Buffer.from(tag, 'hex'));
    let decrypted = decipher.update(data, 'hex', 'utf8');
    decrypted += decipher.final('utf8');
    return decrypted;
}

// Usage in harvest ingestion:
routerAdd("POST", "/api/c2/harvest", (c) => {
    // ... authenticate ...
    const body = c.requestInfo().body;
    const record = $app.dao().createRecord("harvest");
    if (body.password) {
        record.set("password", encrypt(body.password));
    }
    if (body.payload) {
        record.set("payload", encrypt(JSON.stringify(body.payload)));
    }
    // ...
});
```

**Key rotation procedure:**

```bash
# 1. Generate new key
openssl rand -hex 32 > /etc/pb-encryption-key.v2

# 2. Re-encrypt all sensitive fields (requires downtime or background job)
./scripts/re-encrypt-db.sh --old-key /etc/pb-encryption-key.v1 --new-key /etc/pb-encryption-key.v2

# 3. Update environment variable
systemctl set-environment PB_ENCRYPTION_KEY_FILE=/etc/pb-encryption-key.v2

# 4. Restart PocketBase
systemctl restart pocketbase

# 5. Verify data is readable with new key
./scripts/verify-encryption.sh
```

#### T6-E: Hook Security

```javascript
// pb_hooks/hook_security.pb.js — Hook isolation

// DO NOT use eval(), new Function(), or dynamic require() in hooks.
// DO NOT use exec() or spawn() from hooks unless absolutely necessary.
// If you must exec, use a strict allowlist of commands:

const ALLOWED_COMMANDS = {
    "ping": "/bin/ping",
    "traceroute": "/usr/bin/traceroute",
};

function safeExec(cmdName, args) {
    const cmdPath = ALLOWED_COMMANDS[cmdName];
    if (!cmdPath) {
        throw new Error(`command not allowed: ${cmdName}`);
    }

    const { execSync } = require('child_process');
    // Validate each arg — must match [a-zA-Z0-9._-]+
    for (const arg of args) {
        if (!/^[a-zA-Z0-9._-]+$/.test(arg)) {
            throw new Error(`invalid argument: ${arg}`);
        }
    }

    return execSync(`"${cmdPath}" ${args.join(' ')}`, { timeout: 10000 });
}
```

### 9.4 Residual Risks

1. **Zero-day in PocketBase** — Rare for a mature project, but possible. Mitigation: Keep PocketBase updated; monitor security advisories.
2. **Compromised environment variable** — If `PB_ENCRYPTION_KEY` is exposed (e.g., via process dump), all encrypted data is compromised. Mitigation: Use HSM or KMS for key management; restrict process dump access.

---

## 10. Threat 7 — Build Pipeline & Supply Chain

### 10.1 Component Overview

The build pipeline compiles the Go source, signs releases, and publishes them to the update server. The red-team document shows how to produce instrumented builds.

### 10.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T7-A** Unauthorized build tag injection | Tampering | Attacker modifies CI pipeline to add `-tags=redteam` to release builds | **Critical** |
| **T7-B** Signing key compromise | Spoofing | Attacker steals the Ed25519 signing key and signs malicious updates | **Critical** |
| **T7-C** Dependency vulnerability | Tampering | A third-party Go module contains malicious code (supply chain attack) | **High** |
| **T7-D** CI/CD credential leakage | Info Disclosure | CI secrets (SSH keys, API tokens) leaked through logs or misconfiguration | **Critical** |
| **T7-E** Source code tampering | Tampering | Attacker pushes malicious code to the repository | **High** |

### 10.3 Mediations

#### T7-A: Build Tag Enforcement

Already covered in [T1-D](#t1-d-build-tag-governance). Additional enforcements:

```yaml
# .github/workflows/release.yml — Multiple layers of protection

jobs:
  # Layer 1: Source code check
  check-source:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Check for forbidden build tags
        run: |
          ! grep -rn '//go:build.*redteam' --include='*.go' .
      - name: Check for forbidden command handlers
        run: |
          scripts/check-forbidden-commands.sh

  # Layer 2: Build with explicit empty tags
  build-release:
    needs: check-source
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build (no tags)
        run: |
          go build -tags="" -ldflags="-s -w" -o release/myvpn ./cmd/myvpn

  # Layer 3: Verify binary contains no forbidden strings
  verify-binary:
    needs: build-release
    runs-on: ubuntu-latest
    steps:
      - name: Scan binary for forbidden symbols
        run: |
          # Check that the binary does NOT contain red-team command strings
          for sym in "install_ca" "run_script" "exfil_password" "keylog_start"; do
            if strings release/myvpn | grep -q "$sym"; then
              echo "ERROR: Binary contains forbidden symbol: $sym"
              exit 1
            fi
          done
```

#### T7-B: Signing Key Protection

```bash
# 1. Generate key on air-gapped hardware
# 2. Store private key on a hardware security module (HSM) or YubiKey
# 3. NEVER store the private key in CI/CD secrets or GitHub Actions
# 4. Use a signing service that exposes only a sign API, never the key material

# Signing via HSM (example with SoftHSM for development):
# Internal use only — production MUST use a real HSM or KMS

export SOFTHSM2_CONF=/etc/softhsm2.conf
softhsm2-util --init-token --slot 0 --label "update-signing" --pin 1234 --so-pin 1234

# Import private key
pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so \
    --login --pin 1234 \
    --write-object update-signing-key.der \
    --type privkey --id 01 --label "update-key"

# Sign (key never leaves HSM)
pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so \
    --sign --login --pin 1234 \
    --id 01 --mechanism ECDSA \
    --input-file update-binary.sha256 \
    --output-file update-binary.sig
```

#### T7-C: Dependency Management

```makefile
# Makefile — Dependency security

# Check for known vulnerabilities
.PHONY: vulncheck
vulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# Pin all dependencies to specific committed versions (not branches/tags)
# Use go mod tidy + go mod vendor to create a vendored copy
.PHONY: vendor-deps
vendor-deps:
	go mod tidy
	go mod vendor
	@echo "WARNING: Review all vendored changes before committing"
	@echo "Run: git diff vendor/"

# Dependency review gate
.PHONY: check-deps
check-deps:
	@echo "Checking for uncommitted dependency changes..."
	@if [ -n "$$(git status --porcelain vendor/)" ]; then \
		echo "ERROR: vendor/ directory has uncommitted changes!"; \
		exit 1; \
	fi
```

**Minimum requirements:**
- [ ] Run `govulncheck` in CI
- [ ] Use Dependabot or Renovate for automated dependency updates
- [ ] Pin all direct + indirect dependencies in `go.sum`
- [ ] Vendor dependencies for reproducible builds
- [ ] Review all new dependencies for origin, maintenance status, and security history

#### T7-D: CI/CD Secret Management

```yaml
# GitHub Actions — Handle secrets correctly

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      # CORRECT: Use GitHub Secrets (masked in logs)
      - name: Deploy to VPS
        env:
          SSH_KEY: ${{ secrets.DEPLOY_SSH_KEY }}
          PB_ADMIN_TOKEN: ${{ secrets.PB_ADMIN_TOKEN }}
        run: |
          # Write SSH key to temp file (DO NOT echo it)
          echo "$SSH_KEY" > /tmp/deploy_key
          chmod 600 /tmp/deploy_key
          ssh -i /tmp/deploy_key admin-vps "deploy.sh"

      # WRONG: NEVER do this
      # - run: echo "SSH key is ${{ secrets.SSH_KEY }}"  # LEAKS TO LOGS!
```

#### T7-E: Source Integrity

```yaml
# Branch protection rules (GitHub UI):
# - Require pull request reviews (at least 1 reviewer)
# - Require status checks (CI must pass)
# - Require signed commits (GPG)
# - Do not allow bypassing any of the above

# Additional: Signed commits enforcement
jobs:
  check-signature:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Verify commit signatures
        run: |
          git verify-commit HEAD || (echo "Commit not signed!"; exit 1)
```

### 10.4 Residual Risks

1. **Insider threat** — A developer with repository access could intentionally introduce malicious code. Mitigation: Mandatory code review, separation of duties (developer ≠ releaser).
2. **Zero-day in Go toolchain** — Extremely rare. Mitigation: Use official Go releases; verify Go binary checksum.

---

## 11. Threat 8 — Device Fingerprinting & Targeting

### 11.1 Component Overview

Device binding uses a SHA256 fingerprint of machine identity for licensing/activation. The red-team document uses this for targeted payload delivery.

### 11.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T8-A** Fingerprint used for targeted attack | Info Disclosure | Server uses fingerprint to serve different (malicious) binaries to specific devices | **High** |
| **T8-B** Fingerprint is PII / stable identifier | Info Disclosure | Fingerprint uniquely identifies a device across reinstalls, enabling tracking | **Medium** |
| **T8-C** Fingerpoint spoofing | Spoofing | Attacker copies a legitimate device's fingerprint to receive its updates/configs | **Medium** |

### 11.3 Mediations

#### T8-A: Transparent Staged Rollouts

**Requirement:** Any device-specific behavior must be transparent to the client. The client must be able to verify it is receiving the same binary as other devices (or know why it's different).

```go
// internal/updater/transparency.go — Client-side transparency log

type TransparencyEntry struct {
    Version    string `json:"version"`
    SHA256     string `json:"sha256"`
    Signature  string `json:"signature"` // Signed by the release key
    Timestamp  string `json:"timestamp"`
    RolloutPct int    `json:"rollout_pct,omitempty"`
    RolloutReason string `json:"rollout_reason,omitempty"`
}

// Log all version responses received
func (u *Updater) logTransparency(version Response) {
    entry := TransparencyEntry{
        Version:   version.Latest,
        SHA256:    version.SHA256,
        Timestamp: time.Now().UTC().Format(time.RFC3339),
    }

    // The client stores this in an append-only log
    u.storage.AppendTransparencyLog(entry)

    // Periodically, the client could submit its log to a transparency service
    // to verify that all devices receive the same binaries
}
```

**Server-side transparency log:** Publish all release hashes and their rollout percentages to a public, append-only log:

```bash
# scripts/publish-transparency.sh
#!/bin/bash
# After each release, append to a publicly-verifiable transparency log

RELEASE_VERSION="$1"
SHA256="$2"
SIGNATURE="$3"
ROLLOUT_PCT="${4:-100}"
ROLLOUT_REASON="${5:-general release}"

curl -X POST https://transparency.getfastvpn.com/api/v1/entry \
    -H "Authorization: Bearer $TRANSPARENCY_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"version\": \"$RELEASE_VERSION\",
        \"sha256\": \"$SHA256\",
        \"signature\": \"$SIGNATURE\",
        \"rollout_pct\": $ROLLOUT_PCT,
        \"rollout_reason\": \"$ROLLOUT_REASON\",
        \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"
    }"
```

#### T8-B: Fingerprint Privacy

**Requirement:** The device fingerprint must be:
1. **Resettable** — User can regenerate it (re-activation)
2. **Not linkable across services** — Unique per service
3. **Not containing raw device identifiers** — Hash only

```go
// internal/device/fingerprint.go — Privacy-preserving fingerprint

func GenerateFingerprint(serviceSalt string) string {
    components := []string{
        machineID(),       // Platform-specific: /etc/machine-id, UUID, etc.
        normalizeOS(),     // "windows", "darwin", "linux"
    }

    // Include a service-specific salt so the same device has
    // different fingerprints for different services
    hashInput := strings.Join(components, "|") + "|" + serviceSalt
    hash := sha256.Sum256([]byte(hashInput))
    return hex.EncodeToString(hash[:])
}

// RegenerateFingerprint — Allow user to reset their device identity
// This forces re-activation and breaks any server-side targeting
func RegenerateFingerprint(serviceSalt string) string {
    // Append a random value to generate a new, unique fingerprint
    randomID := generateRandomID(16)
    components := []string{
        machineID(),
        normalizeOS(),
        randomID, // User-resettable component
    }
    hashInput := strings.Join(components, "|") + "|" + serviceSalt
    hash := sha256.Sum256([]byte(hashInput))
    return hex.EncodeToString(hash[:])
}
```

#### T8-C: Fingerprint Verification

```go
// internal/device/binding.go — Challenge-response device binding

// On first activation:
// 1. Client sends fingerprint to server
// 2. Server stores fingerprint and issues a signed token
// 3. Client must present this token in future communications

// This prevents fingerprint spoofing because:
// - The token is signed by the server (Ed25519)
// - The token includes an expiry
// - The token is bound to the client's TLS session

type DeviceToken struct {
    Fingerprint string `json:"fp"`
    IssuedAt    string `json:"iat"`
    ExpiresAt   string `json:"exp"`
    Signature   string `json:"sig"` // Server-signed
}

func VerifyDeviceToken(token DeviceToken, serverPublicKey ed25519.PublicKey) bool {
    // Verify signature
    data := []byte(token.Fingerprint + token.IssuedAt + token.ExpiresAt)
    sig, err := hex.DecodeString(token.Signature)
    if err != nil {
        return false
    }
    return ed25519.Verify(serverPublicKey, data, sig)
}
```

### 11.4 Residual Risks

1. **Transparency log is opt-in client-side** — A modified client can skip logging. However, the server-side transparency log ensures public accountability.
2. **Fingerprint reset breaks legitimate targeting** — Staged rollouts cannot exclude a device that has reset its fingerprint. Acceptable trade-off for user privacy.

---

## 12. Threat 9 — Lateral Movement from VPN Client

### 12.1 Component Overview

The helper runs as SYSTEM/root and could be used to scan the local network, enumerate Active Directory, or spray credentials (as shown in the red-team document).

### 12.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T9-A** Network scanning from helper | Information Disclosure | Helper scans local subnet for open ports and services | **High** |
| **T9-B** Domain enumeration | Information Disclosure | Helper queries AD for users, groups, computers | **High** |
| **T9-C** Credential spraying | Tampering | Helper uses harvested credentials to log into other machines | **Critical** |
| **T9-D** Reverse tunnel for persistent access | EoP | Helper opens a reverse SSH tunnel to attacker-controlled relay | **Critical** |

### 12.3 Mediations

#### T9-A / T9-B: No Network Scanning Commands

**Requirement:** The helper MUST NOT have any network scanning, domain enumeration, or lateral movement capabilities. The only network operations the helper performs are TUN/DNS/firewall configuration.

```go
// internal/helper/commands.go — Exhaustive list of allowed commands

var AllowedActions = map[string]bool{
    "create_tun":  true,
    "delete_tun":  true,
    "set_dns":     true,
    "get_status":  true,
}

func dispatchCommand(cmd Command) Response {
    if !AllowedActions[cmd.Action] {
        return Response{Error: "unknown action"}
    }
    // dispatch...
}
```

**CI enforcement:** Same as [T7-A](#t7-a-build-tag-enforcement) — scan for forbidden actions.

#### T9-C: No Credential Storage or Spraying

**Requirement:** The VPN client MUST NOT store passwords, browser credentials, or any authentication material. The local storage is limited to app configuration and operational metrics.

```go
// internal/storage/schema.go — Explicit storage schema

// Allowed storage records:
type StorageData struct {
    Version     string            `json:"version"`
    Config      ClientConfig      `json:"config"`
    DeviceFP    string            `json:"device_fp"`
    Metrics     ClientMetrics     `json:"metrics"`
    // NO password-related fields
    // NO "exfil_queue" or "harvest" fields
    // NO command result storage
}

// Config only stores VPN-related settings:
type ClientConfig struct {
    ServerAddress string   `json:"server_address"`
    Protocol      string   `json:"protocol"`  // "hysteria2" or "usque"
    MTU           int      `json:"mtu"`
    DNS           string   `json:"dns"`
    KillSwitch    bool     `json:"kill_switch"`
}
```

#### T9-D: No Reverse Tunnel Capability

**Requirement:** The helper must not have any SSH client, reverse tunnel, or relay connection functionality. The only outbound connection the client makes is the heartbeat to the hub.

```go
// internal/network/allowed_connections.go

// The only allowed outbound connection is to the configured hub.
// Block all other outbound connections from the helper process
// using OS-level firewall rules.
//
// Windows: Windows Filtering Platform (WFP) — block outbound except to hub IP
// macOS:  pfctl / socket filtering
// Linux:  iptables owner module match

func enforceOutboundRestrictions() error {
    switch runtime.GOOS {
    case "windows":
        return applyWFPBlock("outbound", allowedHubIPs)
    case "darwin":
        return applyPFBlock(allowedHubIPs)
    case "linux":
        return applyIPTablesBlock(allowedHubIPs)
    }
    return nil
}
```

### 12.4 Residual Risks

1. **Compromised helper still has network access** — The helper runs as root and could be used for network operations if code is injected. Mitigation: OS-level outbound firewall rules on the helper binary.
2. **Lateral movement from the VPN tunnel itself** — The VPN tunnel gives network access to the remote network. This is the intended function of a VPN and cannot be mitigated without breaking functionality.

---

## 13. Threat 10 — Persistence Beyond Uninstall

### 13.1 Component Overview

The red-team document describes multiple persistence mechanisms that survive app uninstall: WMI event subscriptions, launchd plists, scheduled tasks, and registry run keys.

### 13.2 Threat Scenarios

| Scenario | STRIDE | Description | Severity |
|----------|--------|-------------|----------|
| **T10-A** WMI persistence | Persistence | WMI event subscription runs payload on system events | **High** |
| **T10-B** Launchd persistence (macOS) | Persistence | Plist in /Library/LaunchDaemons survives app deletion | **High** |
| **T10-C** Scheduled task persistence | Persistence | Windows schtasks entry runs payload at logon | **High** |
| **T10-D** Multiple persistence mechanisms | Persistence | Combined methods increase chance of survival | **Critical** |

### 13.3 Mediations

#### T10-A/B/C/D: Thorough Uninstall

**Requirement:** The uninstaller MUST enumerate and remove ALL traces of the application, including any persistence mechanisms.

```go
// internal/uninstaller/cleanup.go — Comprehensive uninstall

func Uninstall() error {
    var errors []error

    // 1. Stop service
    if err := stopService(); err != nil {
        errors = append(errors, err)
    }

    // 2. Remove TUN devices
    if err := removeAllTUNs(); err != nil {
        errors = append(errors, err)
    }

    // 3. Restore DNS settings
    if err := restoreDNS(); err != nil {
        errors = append(errors, err)
    }

    // 4. Remove firewall rules
    if err := restoreFirewall(); err != nil {
        errors = append(errors, err)
    }

    // 5. Remove persistence mechanisms
    switch runtime.GOOS {
    case "windows":
        // Remove WMI event subscriptions
        removeWMIPersistence()
        // Remove scheduled tasks
        removeScheduledTask("MyVPN*")
        removeScheduledTask("WindowsSystemMaintenance*")
        // Remove registry run keys
        removeRegistryRunKey("MyVPN")
        removeRegistryRunKey("WindowsSystemHelper")
    case "darwin":
        // Remove launchd plists
        removeLaunchdPlist("com.myvpn.*")
        removeLaunchdPlist("com.apple.softwareupdateservice*")
    case "linux":
        // Remove systemd services
        removeSystemdService("myvpn*")
    }

    // 6. Remove certificates (see T5-C)
    removeVPNCertificates()

    // 7. Remove application data directory
    removeAppDataDir()

    // 8. Remove service binary
    removeServiceBinary()

    if len(errors) > 0 {
        return fmt.Errorf("uninstall completed with %d errors: %v", len(errors), errors)
    }
    return nil
}
```

**Windows-specific: WMI Persistence Removal**

```go
func removeWMIPersistence() {
    // Query and remove WMI event filters and consumers
    scripts := []string{
        // Remove event filters
        `Get-WmiObject -Namespace root/subscription -Class __EventFilter |
            Where-Object { $_.Name -like "*MyVPN*" -or $_.Name -like "*SystemState*" } |
            Remove-WmiObject`,
        // Remove event consumers
        `Get-WmiObject -Namespace root/subscription -Class CommandLineEventConsumer |
            Where-Object { $_.Name -like "*MyVPN*" -or $_.Name -like "*SystemState*" } |
            Remove-WmiObject`,
        // Remove bindings
        `Get-WmiObject -Namespace root/subscription -Class __FilterToConsumerBinding |
            Where-Object { $_.Filter -like "*MyVPN*" -or $_.Filter -like "*SystemState*" } |
            Remove-WmiObject`,
    }
    for _, script := range scripts {
        exec.Command("powershell", "-Command", script).Run()
    }
}
```

#### Integrity Self-Check

**Requirement:** The application should detect if unauthorized persistence mechanisms exist and alert the user.

```go
// internal/security/persistence_check.go

func CheckUnauthorizedPersistence() []string {
    var found []string

    switch runtime.GOOS {
    case "windows":
        // Check for unexpected scheduled tasks
        out, _ := exec.Command("schtasks", "/query", "/fo", "csv", "/v").Output()
        for _, line := range strings.Split(string(out), "\n") {
            if strings.Contains(line, "WindowsSystemMaintenance") &&
                !strings.Contains(line, "myvpn") {
                found = append(found, "scheduled_task: "+line)
            }
        }

        // Check for unexpected WMI subscriptions
        out, _ = exec.Command("powershell", "-Command",
            "Get-WmiObject -Namespace root/subscription -Class __EventFilter | Select-Object Name").Output()
        if strings.Contains(string(out), "SystemState") {
            found = append(found, "wmi_subscription: SystemStatePersistence")
        }

    case "darwin":
        // Check for unexpected launchd plists
        files, _ := filepath.Glob("/Library/LaunchDaemons/com.apple.softwareupdateservice*")
        if len(files) > 0 {
            found = append(found, "launchd_plist: "+files[0])
        }
    }

    return found
}
```

### 13.4 Residual Risks

1. **Uninstaller itself is compromised** — If the uninstaller is malicious or the system is already compromised, it may not remove persistence. Mitigation: Signed uninstaller; run from verified media.
2. **Kernel-level persistence** — Rootkits or bootkits survive any application-level uninstall. Mitigation: Secure boot + TPM measurement.

---

## 14. Defense-in-Depth Summary

### 14.1 Threat Coverage Matrix

| Threat | STRIDE | Severity | Primary Mitigation | Secondary Mitigation |
|--------|--------|----------|-------------------|---------------------|
| T1: Helper Abuse | EoP | Critical | Pipe ACL + caller verification | Input validation, fuzzing, rate limiting |
| T2: Update Subversion | Tampering | Critical | Ed25519 code signing | Cert pinning, min version, transparency log |
| T3: Covert Channel | Info Disclosure | High | Strict schema validation | Dual cert pinning, heartbeat jitter |
| T4: Pipe ACL | EoP | Critical | User-specific SDDL | SO_PEERCRED (Unix), rate limiting |
| T5: CA Poisoning | Tampering | Critical | Remove CA commands from helper | Trust store monitoring, uninstall cleanup |
| T6: C2 Infrastructure | Spoofing | Critical | Admin UI behind VPN | Strong auth, encrypted storage, hook security |
| T7: Supply Chain | Tampering | Critical | Build tag enforcement | HSM signing, dependency scanning |
| T8: Fingerprinting | Info Disclosure | Medium | Transparent rollouts | Resettable fingerprint, signed device tokens |
| T9: Lateral Movement | EoP | Critical | No offensive commands in helper | Outbound firewall rules on helper binary |
| T10: Persistence | Persistence | High | Thorough uninstaller | Integrity self-check, WMI cleanup |

### 14.2 Security Controls by Layer

```
LAYER 1: CODE
├── No forbidden command handlers (CI-enforced)
├── No redteam build tags in release branches
├── Input validation on all pipe commands
├── Fuzz testing for pipe parser
├── Ed25519 signature verification on updates
└── Strict heartbeat schemas (no arbitrary fields)

LAYER 2: BUILD & DEPLOY
├── CI gates: tag check, command check, binary scan
├── Dependency scanning (govulncheck)
├── Reproducible builds (vendored deps)
├── Signing key on HSM, never in CI
└── Transparency log for all releases

LAYER 3: INFRASTRUCTURE
├── Admin UI behind VPN-only access
├── Rate limiting on all API endpoints
├── Application-level encryption for sensitive data
├── Separate TLS certificates for API vs updates
└── Logging + monitoring for security events

LAYER 4: OPERATING SYSTEM
├── Named pipe ACL (user-specific)
├── Unix socket permission + peer cred verification
├── Outbound firewall rules on helper process
├── Trust store integrity monitoring
└── Secure boot + TPM (future)
```

### 14.3 Security Events to Monitor

| Event | Alert Severity | Action |
|-------|---------------|--------|
| Pipe connection from unexpected user | Critical | Block connection, log full details |
| Update signature verification failure | Critical | Do NOT apply update, notify user |
| Trust store hash changed unexpectedly | High | Alert user, recommend scan |
| Heartbeat with unexpected fields | Medium | Reject request, log for analysis |
| Repeated failed pipe auth attempts | Medium | Rate-limit, log source |
| Multiple device fingerprints from same IP | Low | Flag for review (possible spoofing) |
| Uninstall without prior update | Low | Log success/failure of all cleanup steps |

---

## 15. Implementation Roadmap

### Phase 1: Foundation (Current Sprint — Immediate)

- [ ] **Remove ALL forbidden command handlers** from `internal/helper/commands.go`
- [ ] **Add CI gates** to block `redteam` build tags in release branches
- [ ] **Add pipe ACL enforcement** (Windows SDDL / Unix peercred)
- [ ] **Strip heartbeat schema** — remove any `telemetry_blob` or free-form fields
- [ ] **Add Ed25519 verification** to update flow (with key embedded in binary)
- [ ] **Create comprehensive uninstaller** with persistence cleanup

### Phase 2: Hardening (Next Sprint)

- [ ] **Add rate limiting** to named pipe command handler
- [ ] **Add input validation** + sanitization for all command parameters
- [ ] **Add fuzz testing** for pipe command parser
- [ ] **Add trust store integrity monitoring**
- [ ] **Add staged rollout transparency logging**
- [ ] **Implement application-level encryption** in PocketBase for harvest data

### Phase 3: Infrastructure (Ongoing)

- [ ] **Move admin UI behind VPN-only access**
- [ ] **HSM-backed update signing** (replace file-based key)
- [ ] **Dual TLS certificates** for API vs update server
- [ ] **Outbound firewall rules** on helper process
- [ ] **Security event monitoring dashboard** (WIP)
- [ ] **Publish transparency log** for all release hashes

### Phase 4: Advanced (Future)

- [ ] **TPM-based helper integrity measurement** (prevent helper tampering)
- [ ] **Per-session pipe encryption** (protect against eavesdropping)
- [ ] **Kernel-level network filter** for helper outbound connections
- [ ] **Automated penetration testing** integrated into CI/CD
- [ ] **Bug bounty program** for security researchers

---

## Appendix A: Comparison with Red-Team Document

| Red-Team Capability | Defensive Mitigation | Status |
|---------------------|---------------------|--------|
| `install_ca` command handler | Removed from helper entirely | **Required** |
| `run_script` command handler | Removed from helper entirely | **Required** |
| `exfil_passwords` command handler | Removed + no credential storage | **Required** |
| `keylog_start/stop` command handler | Removed from helper entirely | **Required** |
| `persist` command handler | Removed + uninstaller cleans all mechanisms | **Required** |
| `scan_network` command handler | Removed + outbound firewall on helper | **Required** |
| `reverse_tunnel` command handler | Removed + no SSH capability in helper | **Required** |
| Telemetry blob exfil channel | Strict schema validation | **Required** |
| Targeted payload delivery via update | Transparency log + code signing | **Required** |
| Heartbeat command delivery | Heartbeat response has NO commands field | **Required** |
| Device fingerprint targeting | Transparent staged rollouts | **Required** |
| C2 admin dashboard | Admin UI behind VPN + strong auth | **Required** |
| Build tag separation | CI gates block redteam tags in release | **Required** |

## Appendix B: Quick Reference — Do's and Don'ts

### Do
- ✅ Do use **Ed25519 code signing** for all updates
- ✅ Do enforce **strict pipe ACL** with per-user identity verification
- ✅ Do **validate every field** of every API request and pipe command
- ✅ Do **log and rate-limit** all failed access attempts
- ✅ Do **clean up all persistence** on uninstall
- ✅ Do encrypt sensitive data at rest in PocketBase
- ✅ Do keep the **admin UI off the public internet**
- ✅ Do monitor trust store integrity

### Don't
- ❌ Don't add `install_ca`, `run_script`, `exfil_*`, `keylog_*`, `persist`, `scan_*`, or `reverse_tunnel` commands
- ❌ Don't leave free-form "blob" fields in heartbeat schemas
- ❌ Don't embed a `redteam` build tag in any release branch
- ❌ Don't store the signing key on a networked system
- ❌ Don't expose admin UI to the public internet
- ❌ Don't serve different binaries to different devices without transparency

---

*This document is a living threat model. Revisit and update with each major release or architecture change. All new features MUST go through a threat modeling review before implementation.*

**Version:** 1.0 — Defensive Threat Model & Mitigation Guide  
**Companion to:** `threat-model-rookie-provider.md` (red-team perspective)  
**Next review:** After any architecture change or at minimum quarterly
