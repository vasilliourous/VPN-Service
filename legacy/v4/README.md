# MyVPN V4 — Client Application (Go + Fyne)

> The V4 desktop client replaces V3's sslocal+tun2socks with **sing-box**,
> restores the two-phase update rollback safety from V1, and adds business-critical
> features like heartbeat health monitoring, offsite backups, and Luhn-mod-N
> activation codes.

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│              STUDENT'S LAPTOP                     │
│                                                   │
│  ┌───────────────────────────────────────────┐   │
│  │         MyVPN Desktop App (Go+Fyne)        │   │
│  │                                             │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  │   │
│  │  │Activation│  │ Heartbeat │  │ Updater   │  │   │
│  │  │ Client   │  │ 5min→2h   │  │ 2-phase   │  │   │
│  │  └────┬─────┘  └────┬─────┘  └─────┬────┘  │   │
│  │       │              │              │        │   │
│  │  ┌────┴──────────────┴──────────────┴────┐   │   │
│  │  │         Manager (sing-box)            │   │   │
│  │  │   Generates config, spawns process    │   │   │
│  │  └─────────────────┬────────────────────┘   │   │
│  │                    │                         │   │
│  │  ┌─────────────────┴────────────────────┐   │   │
│  │  │      TUN Helper (privileged)         │   │   │
│  │  │   IPC socket: /var/run/myvpn-helper  │   │   │
│  │  │   Creates TUN interfaces, firewall   │   │   │
│  │  └──────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────┘   │
│                                                    │
│  sing-box (SOCKS5 proxy on 127.0.0.1:1080)         │
│         │                                           │
│         ▼ Shadowsocks TCP                          │
│    ═══════ INTERNET ════════                        │
└─────────────────────────────────────────────────────┘
```

---

## Source Code Structure

```
v4/
├── cmd/myvpn/
│   └── main.go                 ← Application entry point
│                                  • --hub flag: hub server URL
│                                  • --revert flag: manual rollback
│                                  • --version flag: print version
│
├── internal/
│   ├── storage/
│   │   └── storage.go          ← Local JSON persistence
│   │                              Thread-safe reads/writes
│   │                              Atomic save (write-tmp + rename)
│   │                              Stores: code, tier, fingerprint,
│   │                                      server config, update state,
│   │                                      heartbeat history, crash flags
│   │                              Location: ~/.config/myvpn/storage.json
│   │
│   ├── activation/
│   │   ├── luhn.go             ← Luhn-mod-N checksum
│   │   │                          Character set: ABCDEFGHJKLMNPQRSTUVWXYZ23456789
│   │   │                          (No I/O/0/1 — avoids ambiguity)
│   │   │                          Format: MYVPN-XXXX-XXXX-XXXX-C
│   │   │                          Functions: GenerateCheckChar, ValidateCode,
│   │   │                                     FormatCode, luhnModNCheck
│   │   │
│   │   ├── activation.go       ← Activation client
│   │   │                          POST /api/activate with {code, fingerprint}
│   │   │                          Client-side Luhn validation before send
│   │   │                          Error mapping: 403→suspended, 404→not found,
│   │   │                          410→expired, 429→rate limited
│   │   │
│   │   ├── fingerprint.go      ← Device fingerprint (cross-platform)
│   │   │                          SHA256 hash of hardware identifiers
│   │   │                          4-tier fallback chain:
│   │   │                            1. MAC + disk_serial + motherboard UUID
│   │   │                            2. MAC + motherboard UUID
│   │   │                            3. MAC + hostname + machine_id
│   │   │                            4. Random UUID v4 (persistent per install)
│   │   │                          Cached for process lifetime
│   │   │
│   │   ├── fingerprint_linux.go   ← Linux: /sys/class/net/*, /sys/block/*/serial,
│   │   │                                      /sys/class/dmi/id/product_uuid,
│   │   │                                      /etc/machine-id
│   │   ├── fingerprint_darwin.go  ← macOS: ioreg for serial/UUID,
│   │   │                                      networksetup for MAC
│   │   └── fingerprint_windows.go ← Windows: PowerShell WMI queries
│   │
│   ├── manager/
│   │   └── process.go          ← Sing-box manager
│   │                              Generates sing-box JSON config from tier params
│   │                              Two modes:
│   │                                1. Helper mode: sends config to TUN helper via IPC
│   │                                2. Direct mode: spawns sing-box directly (fallback)
│   │                              Auto-detects helper availability
│   │                              Config generation:
│   │                                • SOCKS5 inbound on 127.0.0.1:1080
│   │                                • Mixed inbound on :1081
│   │                                • Shadowsocks outbound with tier params
│   │                                • DNS via 1.1.1.1 (direct + remote resolvers)
│   │                                • Route: ads→block, everything else→tunnel
│   │
│   ├── heartbeat/
│   │   └── heartbeat.go        ← Heartbeat loop
│   │                              GET /api/heartbeat?code=XXX&fp=XXX
│   │                              Interval: 5min → doubles on failure → max 2h
│   │                              Grace period: 7 days without successful heartbeat
│   │                              Update signal: update_available/URL/SHA256 from server
│   │                              Staged rollout: client independently gates via
│   │                                hash(fingerprint) % 100 < rollout_percent
│   │
│   ├── updater/
│   │   ├── updater.go          ← Update engine
│   │   │                          Platform-specific asset selection (Windows/Mac/Linux)
│   │   │                          Download + SHA256 verification
│   │   │                          Staged rollout gating (double-gate with server)
│   │   │                          Two-phase sentinel:
│   │   │                            1. Write .update-pending
│   │   │                            2. Swap binary
│   │   │                            3. Fork new process
│   │   │                            4. New process writes .update-confirmed
│   │   │                            5. If crash before confirmation → auto-revert
│   │   │
│   │   ├── recover.go          ← Crash recovery
│   │   │                          Checks for orphaned .update-pending
│   │   │                          Auto-revert: restores backup from .myvpn-backups/
│   │   │                          Manual revert: --revert flag
│   │   │                          Stale update detection (>24h old markers)
│   │   │                          Heartbeat-based crash detection:
│   │   │                            If update was applied but no heartbeat since → assume crash
│   │   │
│   │   ├── update_unix.go      ← Unix: atomic rename + fork
│   │   └── update_windows.go   ← Windows: .old rename trick + CreateProcess
│   │
│   ├── tunnel/
│   │   └── tunnel.go           ← Fallback TUN interface
│   │                              Only used when sing-box can't be run directly.
│   │                              Provides:
│   │                                • TUN interface creation (Linux ip tuntap,
│   │                                  macOS ifconfig, Windows requires helper)
│   │                                • DNS configuration (resolvectl, networksetup, netsh)
│   │                                • Kill switch (iptables, pfctl, netsh advfirewall)
│   │
│   ├── gui/
│   │   ├── app.go              ← Fyne v2 desktop GUI
│   │   │                          • Activation screen (code input + Luhn validation)
│   │   │                          • Main screen (status icon, tier badge, connect/disconnect)
│   │   │                          • System tray (background operation)
│   │   │                          • Settings/About dialogs
│   │   │                          • Update notification from heartbeat
│   │   │                          • Grace/crash recovery handling
│   │   │
│   │   └── diagnostics.go      ← Support report
│   │                              Exports: version, OS, tier, connection state,
│   │                              sentinel status, sing-box availability
│   │                              No personal data (safe to share)
│   │
│   └── helper/
│       ├── main.go             ← Privileged TUN helper (standalone binary)
│       │                          Runs as root/admin
│       │                          IPC via Unix socket (Linux/Mac) or named pipe (Windows)
│       │                          Commands:
│       │                            • ping — health check
│       │                            • create-tun / delete-tun — TUN interface management
│       │                            • set-dns — DNS configuration
│       │                            • killswitch — firewall enable/disable
│       │                            • start-singbox / stop-singbox — sing-box lifecycle
│       │                            • singbox-status — process health check
│       │
│       └── install.go          ← Service installation
│                                   systemd (Linux), launchd (macOS), Windows Service
│                                   Install/uninstall/status check
│
├── Makefile                    ← Build system
│                                  • make build — current platform
│                                  • make build-all — all platforms
│                                  • make bundle-all — build + bundle sing-box
│                                  • make clean
│
├── go.mod                      ← Module definition (Go 1.22, Fyne v2)
│
├── engines/                    ← Place sing-box binaries here
└── dist/                       ← Build artifacts
```

---

## The Three Tiers

| Tier | Price | Port | Server CC | Bandwidth Limit | UDP | Experience |
|------|-------|:----:|:---------:|:---------------:|:---:|------------|
| **Eco** | $2 | 8443 | BBR | 5 Mbps (server tc) | ❌ | Text loads slow, video buffers |
| **Stealth** | $4 | 8444 | **Brutal** (LD_PRELOAD) | 48 Mbps (kernel target) | ❌ | Fast streaming, jitter makes gaming bad |
| **Strike** | $8 | 8445 | BBR | 200 Mbps (server tc) | ✅ | Gaming (33-44ms), 4K streaming |

---

## Key Technical Details

### Sing-Box Configuration

The manager generates sing-box JSON config with:
- **SOCKS5 inbound** on `127.0.0.1:1080` (applications connect here)
- **Mixed inbound** on `127.0.0.1:1081` (HTTP/SOCKS auto-detect)
- **Shadowsocks outbound** with server address, port, password, method from tier config
- **UDP-over-TCP** for Strike tier (wraps UDP in TCP to bypass N4L's UDP block)
- **DNS**: `1.1.1.1` via HTTPS (direct) for main resolution, fallback remote
- **Route**: ad blocking via geosite, everything else through tunnel
- Auto-detects default network interface

### Activation Code Format

```
MYVPN-XXXX-XXXX-XXXX-C
│           └─ Luhn-mod-N checksum character
└─ 16 data characters (A-Z, 2-9, no I/O/0/1)
```

The Luhn-mod-N checksum is validated **client-side** before any HTTP request,
catching typos instantly without a server round-trip.

### Device Fingerprinting

The fingerprint is generated from hardware identifiers with a 4-tier fallback:

1. **Strong**: `SHA256(MAC + disk_serial + motherboard_UUID)` — most reliable
2. **Medium**: `SHA256(MAC + motherboard_UUID)` — when disk serial unavailable
3. **Weak**: `SHA256(MAC + hostname + machine_id)` — in VMs without DMI info
4. **Fallback**: Random UUID v4 — persistent for the lifetime of the install

The fingerprint is cached for the process lifetime. The storage layer persists
the first generated fingerprint so it remains stable across app restarts.

### Manager Dual-Mode Operation

The manager can operate in two modes:

1. **Helper mode** (preferred): Sends the sing-box config JSON to the TUN helper
   via IPC Unix socket. The helper, running as root, spawns sing-box which can
   create TUN interfaces. This is the production mode.

2. **Direct mode** (fallback): Spawns sing-box directly from the app process.
   Works for SOCKS5 proxy mode but cannot create TUN interfaces without root.
   Used when the helper isn't installed.

The manager auto-detects helper availability on startup.

### Update Safety

The two-phase update system prevents bricked installations:

```
Normal flow:
  Download → .update-pending → swap binary → fork → new process
  → .update-confirmed → success ✓

Crash recovery:
  Download → .update-pending → swap binary → fork → CRASH
  → next start detects orphaned .update-pending → auto-revert ✓

Manual revert:
  myvpn --revert → restores last backup from .myvpn-backups/ ✓
```

The updater also uses heartbeat timestamps as a second line of defense:
if an update was applied but no heartbeat was recorded since, it assumes
the new version crashed at runtime and auto-reverts.

### Staged Rollouts

Updates can be rolled out gradually to catch problems early:

1. Server sets `rollout_percent` (e.g., 5%) in PocketBase `update_config`
2. Server heartbeat hook checks: only return `update_available` to clients
   whose fingerprint falls within the rollout percentage
3. Client independently checks: `hash(fingerprint) % 100 < rollout_percent`
4. Both gates must pass for the client to download the update

This double-gating prevents a bad update from reaching all users at once.

---

## Build & Release

### Local Build

```bash
cd v4

# Build for current platform
make build

# Build for all platforms (requires cross-compilation toolchains)
make build-all

# Build + bundle with sing-box
make bundle-all

# Build TUN helper only
make build-helper
```

### GitHub Actions CI/CD

Push a tag `v*` to trigger the full pipeline:

```yaml
on:
  push:
    tags:
      - 'v*'
```

The workflow:
1. Lints and vets all code
2. Builds for Linux, macOS (Intel + ARM), Windows
3. Downloads the matching sing-box binary for each platform
4. Bundles into platform-specific ZIPs
5. Creates a GitHub Release with all artifacts + checksums

---

## Platform Support

| Feature | Linux | macOS | Windows |
|---------|:-----:|:-----:|:-------:|
| Fyne GUI | ✅ | ✅ | ✅ |
| Device fingerprint | ✅ | ✅ | ✅ |
| SOCKS5 proxy | ✅ | ✅ | ✅ |
| TUN helper | ✅ systemd | ✅ launchd | ✅ Windows Service |
| Kill switch | ✅ iptables | ✅ pfctl | ✅ netsh |
| Two-phase update | ✅ | ✅ | ✅ |
| System tray | ✅ | ✅ | ✅ |
