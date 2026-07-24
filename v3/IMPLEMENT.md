# V3 Implementation Plan — Shadowsocks Architecture

> Target: ~1,300 lines of Go across ~22 files. One weekend build.
> Server: Blank Ubuntu 22.04 VPS. Nothing pre-configured.

## What Changed From V2 (simplified)

| Change | Reason |
|--------|--------|
| **Engine swap** Hysteria 2 → Shadowsocks-rust | N4L blocks UDP, Hysteria 2 won't connect |
| **Added tun2socks** as second binary | Shadowsocks is SOCKS5-only, no built-in TUN |
| **Removed bandwidth probe** | TCP doesn't need it — CC handles it |
| **Removed Brutal CC as client config** | TCP Brutal is server-side kernel module, not client config |
| **Three ssserver instances** | Eco (8443), Stealth (8444), Strike (8445) — one per tier |
| **tc traffic shaping for Eco** | Replaces non-functional `speed` field — enforces 5 Mbps cap |
| **Brutal target rate config** | Stealth capped at 48 Mbps via Brutal sysctl |
| **BBR system default** | Strike gets BBR natively, Eco/Stealth overridden via LD_PRELOAD |
| **VPS setup from scratch** | No existing server — setup script generates everything |

## Prerequisites

- Go 1.22+ with Fyne v2 deps (CGO required)
- Blank Ubuntu 22.04 VPS ($6-12/mo from Hetzner, DigitalOcean, Vultr)
- Domain name pointing to VPS IP
- shadowsocks-rust binaries for target platforms (download from GitHub releases)
- tun2socks binary per platform (badvpn-tun2socks or similar)

## Phase 1: Project Scaffolding

```bash
mkdir -p v3/cmd/myvpn
cd v3
go mod init myvpn

mkdir -p internal/{storage,activation,manager,tunnel,gui,updater}
mkdir -p server/pb_hooks
mkdir -p scripts
```

## Phase 2: Server (From Scratch)

### Step 2.1 — VPS Base Setup

```bash
# Fresh Ubuntu 22.04 — nothing installed
ssh root@your-vps

apt update && apt upgrade -y
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
```

### Step 2.2 — Install Shadowsocks Server

```bash
# Download shadowsocks-rust
cd /tmp
wget https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.23.0/shadowsocks-v1.23.0.x86_64-unknown-linux-gnu.tar.xz
tar -xf shadowsocks-*.tar.xz
cp ssserver /usr/local/bin/

# Generate three random passwords (one per tier)
ECO_PASS=$(openssl rand -hex 16)
STEALTH_PASS=$(openssl rand -hex 16)
STRIKE_PASS=$(openssl rand -hex 16)

echo "ECO_PASS=$ECO_PASS" >> /root/.tier_passwords
echo "STEALTH_PASS=$STEALTH_PASS" >> /root/.tier_passwords
echo "STRIKE_PASS=$STRIKE_PASS" >> /root/.tier_passwords
chmod 600 /root/.tier_passwords
```

### Step 2.3 — Enable BBR as System Default

BBR gives the Strike tier its characteristic low-latency throughput. Eco and Stealth
override this per-instance via LD_PRELOAD wrappers.

```bash
# Load BBR module and set as default CC
modprobe tcp_bbr
echo "tcp_bbr" > /etc/modules-load.d/tcp_bbr.conf
sysctl -w net.core.default_qdisc=fq
sysctl -w net.ipv4.tcp_congestion_control=bbr

# Persist
cat > /etc/sysctl.d/90-bbr.conf <<EOF
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
```

Verify: `sysctl net.ipv4.tcp_congestion_control` should show `bbr`.

### Step 2.4 — Configure Eco Instance (CUBIC + tc Cap)

**Config file** (`/etc/shadowsocks/eco.json`):
```json
{
  "server": "0.0.0.0",
  "server_port": 8443,
  "password": "${ECO_PASS}",
  "method": "aes-256-gcm",
  "mode": "tcp_only",
  "fast_open": true,
  "no_delay": true
}
```

**CUBIC wrapper** — forces CUBIC CC on every connection (overriding the system BBR default):

```bash
cat > /usr/local/src/cubic-wrap.c <<'WRAP'
#define _GNU_SOURCE
#include <dlfcn.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <string.h>

static int (*real_setsockopt)(int, int, int, const void *, socklen_t) = NULL;

int setsockopt(int sockfd, int level, int optname,
               const void *optval, socklen_t optlen) {
    if (!real_setsockopt)
        real_setsockopt = dlsym(RTLD_NEXT, "setsockopt");
    if (level == IPPROTO_TCP && optname == TCP_NODELAY) {
        const char *cc = "cubic";
        real_setsockopt(sockfd, IPPROTO_TCP, TCP_CONGESTION, cc, strlen(cc));
    }
    return real_setsockopt(sockfd, level, optname, optval, optlen);
}
WRAP

gcc -shared -fPIC -o /usr/local/lib/cubic-wrap.so /usr/local/src/cubic-wrap.c -ldl
```

**systemd unit** (`/etc/systemd/system/shadowsocks-eco.service`):
```ini
[Unit]
Description=Shadowsocks Server (Eco — CUBIC + tc 5 Mbps)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/eco.json
Environment=LD_PRELOAD=/usr/local/lib/cubic-wrap.so
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

**tc traffic shaping** — limits ALL traffic on port 8443 to 5 Mbps shared:

```bash
# On the main network interface (usually eth0 or ens3)
IFACE=$(ip -4 route show default | awk '{print $5}')

tc qdisc add dev $IFACE root handle 1: htb default 30
tc class add dev $IFACE parent 1: classid 1:10 htb rate 5mbit ceil 5mbit
tc filter add dev $IFACE protocol ip parent 1: prio 1 u32 \
  match ip sport 8443 0xffff flowid 1:10
tc qdisc add dev $IFACE parent 1:10 handle 10: sfq perturb 10

# Persist (via networkd-dispatcher or rc.local)
cat > /etc/rc.local <<'EOF'
#!/bin/sh
IFACE=$(ip -4 route show default | awk '{print $5}')
tc qdisc add dev $IFACE root handle 1: htb default 30 2>/dev/null
tc class add dev $IFACE parent 1: classid 1:10 htb rate 5mbit ceil 5mbit 2>/dev/null
tc filter add dev $IFACE protocol ip parent 1: prio 1 u32 match ip sport 8443 0xffff flowid 1:10 2>/dev/null
tc qdisc add dev $IFACE parent 1:10 handle 10: sfq perturb 10 2>/dev/null
exit 0
EOF
chmod +x /etc/rc.local
```

### Step 2.5 — Install TCP Brutal for Stealth Tier

```bash
# Install kernel headers + build tcp-brutal kernel module
apt install -y linux-headers-$(uname -r) build-essential
cd /tmp
git clone https://github.com/apernet/tcp-brutal-ng.git
cd tcp-brutal-ng
make
insmod tcp_brutal.ko

# Persist across reboots
echo "tcp_brutal" > /etc/modules-load.d/tcp_brutal.conf
cp tcp_brutal.ko /lib/modules/$(uname -r)/kernel/net/ipv4/
depmod -a

# Set Brutal target rate to 48 Mbps
sysctl -w net.ipv4.tcp_brutal.target_rate=48000000
echo "net.ipv4.tcp_brutal.target_rate=48000000" > /etc/sysctl.d/90-brutal.conf
```

If `tcp_brutal.target_rate` is not available as a sysctl, the parameter may be under
`/sys/module/tcp_brutal/parameters/target_rate` instead. Adjust accordingly:

```bash
echo 48 > /sys/module/tcp_brutal/parameters/target_rate
# And persist via modprobe config:
echo "options tcp_brutal target_rate=48" > /etc/modprobe.d/tcp_brutal.conf
```

Build the Brutal LD_PRELOAD wrapper:

```bash
cat > /usr/local/src/brutal-wrap.c <<'WRAP'
#define _GNU_SOURCE
#include <dlfcn.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <string.h>

static int (*real_setsockopt)(int, int, int, const void *, socklen_t) = NULL;

int setsockopt(int sockfd, int level, int optname,
               const void *optval, socklen_t optlen) {
    if (!real_setsockopt)
        real_setsockopt = dlsym(RTLD_NEXT, "setsockopt");
    if (level == IPPROTO_TCP && optname == TCP_NODELAY) {
        const char *cc = "brutal";
        real_setsockopt(sockfd, IPPROTO_TCP, TCP_CONGESTION, cc, strlen(cc));
    }
    return real_setsockopt(sockfd, level, optname, optval, optlen);
}
WRAP

gcc -shared -fPIC -o /usr/local/lib/brutal-wrap.so /usr/local/src/brutal-wrap.c -ldl
```

### Step 2.6 — Configure Stealth Instance (Brutal, 48 Mbps target)

**Config file** (`/etc/shadowsocks/stealth.json`):
```json
{
  "server": "0.0.0.0",
  "server_port": 8444,
  "password": "${STEALTH_PASS}",
  "method": "aes-256-gcm",
  "mode": "tcp_only",
  "fast_open": true,
  "no_delay": true
}
```

**systemd unit** (`/etc/systemd/system/shadowsocks-stealth.service`):
```ini
[Unit]
Description=Shadowsocks Server (Stealth — Brutal, 48 Mbps target)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/stealth.json
Environment=LD_PRELOAD=/usr/local/lib/brutal-wrap.so
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### Step 2.7 — Configure Strike Instance (BBR, Unlimited, UDP)

**Config file** (`/etc/shadowsocks/strike.json`):
```json
{
  "server": "0.0.0.0",
  "server_port": 8445,
  "password": "${STRIKE_PASS}",
  "method": "aes-256-gcm",
  "mode": "tcp_and_udp",
  "fast_open": true,
  "no_delay": true
}
```

No LD_PRELOAD needed — inherits the system default BBR.

**systemd unit** (`/etc/systemd/system/shadowsocks-strike.service`):
```ini
[Unit]
Description=Shadowsocks Server (Strike — BBR, unlimited, UDP)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/strike.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### Step 2.8 — Enable All Instances

```bash
systemctl daemon-reload
systemctl enable --now shadowsocks-eco shadowsocks-stealth shadowsocks-strike
ufw allow 8443/tcp
ufw allow 8444/tcp
ufw allow 8445/tcp  # Note: 8445 is UDP too — ufw allows both by default
```

### Step 2.9 — Install Caddy + PocketBase

Same as V2 setup. Caddy reverse-proxies PocketBase. PocketBase stores codes and tier_configs.

```bash
# Install Caddy
apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install caddy

# Install PocketBase
cd /opt
wget https://github.com/pocketbase/pocketbase/releases/download/v0.25.0/pocketbase_0.25.0_linux_amd64.zip
unzip pocketbase_*.zip
mkdir -p pocketbase/pb_hooks

cat > /etc/systemd/system/pocketbase.service <<EOF
[Unit]
Description=PocketBase
After=network.target

[Service]
Type=simple
User=root
ExecStart=/opt/pocketbase/pocketbase serve --http=127.0.0.1:8090 --dir=/opt/pocketbase/pb_data --hooksDir=/opt/pocketbase/pb_hooks
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now pocketbase
```

### Step 2.10 — Caddy Reverse Proxy

**Caddyfile** (`/etc/caddy/Caddyfile`):
```
api.yourdomain.com {
    reverse_proxy 127.0.0.1:8090
    rate_limit {
        zone dynamic {
            key {remote_host}
            events 100
            window 10s
        }
    }
    header {
        -Server
    }
}
```

```bash
caddy fmt --overwrite /etc/caddy/Caddyfile
systemctl reload caddy
```

### Step 2.11 — PocketBase Collections

Open `https://api.yourdomain.com/_/` in a browser and create admin account.

**Collection: `codes`**
- `code` (text, required, unique) — format `XXXX-XXXX-XXXX`
- `tier` (select, required, options: `eco`, `stealth`, `strike`)
- `used` (bool, default: false)
- `suspended` (bool, default: false)
- `fingerprint` (text, optional) — SHA256, set by app
- `middleman` (text, optional)

**Collection: `tier_configs`** — stores Shadowsocks client configs + metadata:

| Field | Type | Description |
|-------|------|-------------|
| `tier` | text (unique) | `eco`, `stealth`, or `strike` |
| `config` | json | Shadowsocks sslocal config (server, port, password, method, mode) |
| `speed_limit_bps` | number | 0 = unlimited, >0 = client-side throttle if desired |
| `udp_relay` | bool | Whether tun2socks gets `--socks5-udp` flag |
| `active` | bool | Must be true for the tier to work |

### Step 2.12 — Seed Tier Configs

Enter these in the PocketBase `tier_configs` collection. Replace passwords with
the ones generated in step 2.2 (stored in `/root/.tier_passwords`).

**Eco:**
```json
{
  "tier": "eco",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8443,
    "password": "<ECO_PASS>",
    "method": "aes-256-gcm",
    "mode": "tcp_only",
    "fast_open": true,
    "no_delay": true
  },
  "speed_limit_bps": 5000000,
  "udp_relay": false,
  "active": true
}
```

**Stealth:**
```json
{
  "tier": "stealth",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8444,
    "password": "<STEALTH_PASS>",
    "method": "aes-256-gcm",
    "mode": "tcp_only",
    "fast_open": true,
    "no_delay": true
  },
  "speed_limit_bps": 48000000,
  "udp_relay": false,
  "active": true
}
```

**Strike:**
```json
{
  "tier": "strike",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8445,
    "password": "<STRIKE_PASS>",
    "method": "aes-256-gcm",
    "mode": "tcp_and_udp",
    "fast_open": true,
    "no_delay": true
  },
  "speed_limit_bps": 0,
  "udp_relay": true,
  "active": true
}
```

### Step 2.13 — JS Hooks

**`server/pb_hooks/activation.pb.js`:**

```javascript
routerAdd("POST", "/api/collections/codes/records", (c) => {
  const data = $apis.requestInfo(c).data;
  const code = data.code;
  const fingerprint = data.fingerprint;

  if (!code || typeof code !== 'string') {
    return c.json(400, { "code": 400, "message": "Invalid code format" });
  }

  // Look up the code
  const records = $app.dao().findRecordsByFilter("codes", `code='${code}'`, "", 0, 1);
  if (records.length === 0) {
    return c.json(404, { "code": 404, "message": "Code not found" });
  }

  const record = records[0];
  if (record.getBool("suspended")) {
    return c.json(403, { "code": 403, "message": "Account suspended" });
  }

  // Tier config
  const tier = record.getString("tier");
  const configs = $app.dao().findRecordsByFilter("tier_configs", `tier='${tier}'`, "", 0, 1);
  if (configs.length === 0 || !configs[0].getBool("active")) {
    return c.json(500, { "code": 500, "message": "Tier configuration not available" });
  }

  // Device binding (first time = bind, subsequent = verify)
  const existingFp = record.getString("fingerprint");
  if (existingFp && existingFp !== fingerprint) {
    return c.json(403, { "code": 403, "message": "Code already bound to another device" });
  }

  // Mark used + store fingerprint
  record.set("used", true);
  record.set("fingerprint", fingerprint);
  $app.dao().saveRecord(record);

  // Return config
  const tc = configs[0];
  return c.json(200, {
    "tier": tier,
    "config": tc.get("config"),
    "speed_limit_bps": tc.get("speed_limit_bps"),
    "udp_relay": tc.get("udp_relay")
  });
});
```

**`server/pb_hooks/suspension.pb.js`** — checks suspension on every heartbeat:

```javascript
routerAdd("GET", "/api/health", (c) => {
  const code = c.query().get("code");
  if (!code) {
    return c.json(400, { "code": 400, "message": "Missing code" });
  }
  const records = $app.dao().findRecordsByFilter("codes", `code='${code}'`, "", 0, 1);
  if (records.length === 0) {
    return c.json(404, { "code": 404, "message": "Code not found" });
  }
  if (records[0].getBool("suspended")) {
    return c.json(403, { "code": 403, "message": "Account suspended" });
  }
  return c.json(200, { "status": "ok" });
});
```

## Phase 3: Client — Core Packages

### Step 3.1 — Storage (`internal/storage/storage.go`)

Identical to V2. Stores `{code, tier, device_fingerprint, config, activated}` in a JSON file.

The `config` object is the opaque JSON from PocketBase (server, port, password, method, mode, fast_open, no_delay).

### Step 3.2 — Activation (`internal/activation/activation.go`)

Same flow as V2: POST `{code, fingerprint}` → receive `{tier, config, speed_limit_bps, udp_relay}`.
Store everything in storage.

### Step 3.3 — Fingerprint (`internal/activation/fingerprint*.go`)

Identical to V2. SHA256(MAC + disk serial + motherboard UUID).

### Step 3.4 — Manager (`internal/manager/process.go`)

```go
package manager

// Types:
//   - EngineStatus struct { Running bool, Tier string }
//   - ShadowsocksConfig struct { Server, Port, Password, Method, Mode, FastOpen, NoDelay }
//
//   - AppConfig struct {
//       Shadowsocks   ShadowsocksConfig   // passed to sslocal
//       SpeedLimitBps int64               // 0 = unlimited (for client throttle)
//       UDPRelay      bool                // --socks5-udp flag for tun2socks
//     }
//
// Functions:
//   - StartEngine(cfg *AppConfig, engineDir string) error
//   - StopEngine() error
//   - Status() EngineStatus
//
// Flow:
//   StartEngine:
//     1. Write Shadowsocks config JSON to temp file
//     2. Start sslocal -c <tempfile> -b 127.0.0.1:1080
//     3. Wait for SOCKS5 ready (TCP dial 127.0.0.1:1080, retry 5× 200ms)
//     4. Start tun2socks:
//        tun2socks --tun tun0 --proxy socks5://127.0.0.1:1080
//          [--socks5-udp]           // only if cfg.UDPRelay == true
//          --netif-ip 10.0.0.2
//        Platform tuning:
//          Linux:   --tun "tun0"
//          macOS:   --tun "utunX"
//          Windows: --tun "wintun0"
//     5. Add default route through TUN
//     6. Store PIDs, update status
//
//   StopEngine:
//     1. Remove default route
//     2. Kill tun2socks (SIGTERM, then SIGKILL after 3s)
//     3. Kill sslocal
//     4. Clean up temp file
//     5. Update status
//
//   Bandwidth limiting:
//     The server enforces caps server-side (tc for Eco, Brutal target for Stealth).
//     The speed_limit_bps field is available for optional client-side throttle
//     (e.g., via OS traffic shaping API or rate-limited loopback proxy).
//     For MVP, client-side throttle is NOT implemented — server-side enforcement
//     is sufficient for the target audience.
//
//   Engine discovery:
//     Look for sslocal + tun2socks in:
//       1. Same directory as app binary
//       2. engines/ subdirectory
//       3. PATH
```

### Step 3.5 — Tunnel (`internal/tunnel/`)

Fallback package for platforms where tun2socks doesn't work. Same interface as V2 but now a fallback rather than primary. In practice, tun2socks handles all platforms.

### Step 3.6 — Updater (`internal/updater/updater.go`)

Identical to V2. Checks `update.json`, downloads zip, SHA256 verify, replace, restart.

### Step 3.7 — GUI (`internal/gui/app.go`)

Same systray design as V2. The only change: tier badges reflect the new meanings:

```
Eco:     grey ○ "Eco · Basic"
Stealth: purple ◉ "Stealth · Stream"
Strike:  gold ⚡ "Strike · Gaming"
```

No reference to "Brutal" or "Shadowsocks" in the UI. The user sees tier names, not protocol names.

## Phase 4: Main Entrypoint

```go
package main

import (
    "flag"
    "myvpn/internal/storage"
    "myvpn/internal/gui"
)

var (
    version     = "1.0.0"
    adminHubURL = flag.String("hub", "https://api.yourdomain.com", "Admin hub URL")
)

func main() {
    flag.Parse()
    storage.Init()
    gui.Run(*adminHubURL, version)
}
```

## Phase 5: Build System

```makefile
APP_NAME := myvpn
VERSION := 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)

build-windows:
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		go build -ldflags="$(LDFLAGS) -H windowsgui" -o $(APP_NAME).exe ./cmd/$(APP_NAME)

build-macos-intel:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" -o $(APP_NAME)-intel ./cmd/$(APP_NAME)

build-macos-arm:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
		go build -ldflags="$(LDFLAGS)" -o $(APP_NAME)-arm ./cmd/$(APP_NAME)

# After building, zip with engines:
bundle-windows: build-windows
	zip -j dist/myvpn-windows.zip myvpn.exe engines/sslocal.exe engines/tun2socks.exe

bundle-macos-intel: build-macos-intel
	zip -j dist/myvpn-macos-intel.zip myvpn-intel engines/sslocal-darwin-amd64 engines/tun2socks-darwin-amd64

bundle-macos-arm: build-macos-arm
	zip -j dist/myvpn-macos-arm.zip myvpn-arm engines/sslocal-darwin-arm64 engines/tun2socks-darwin-arm64

bundle-all: bundle-windows bundle-macos-intel bundle-macos-arm
```

## Phase 6: Agent Operations

See OPS.md. The main change from V2: agent monitors three shadowsocks services, not one.

## Phase 7: Distribution

Same physical card model as V2. `print_codes.sh` generates codes in card format. Zip files on the VPS at `/var/www/html/`. Auto-updater handles the rest.

## File-by-File Summary

| File | Est. Lines | Change from V2 |
|------|-----------|----------------|
| `cmd/myvpn/main.go` | 30 | Same |
| `internal/storage/storage.go` | 70 | Same |
| `internal/activation/activation.go` | 60 | Same |
| `internal/activation/fingerprint.go` | 15 | Same |
| `internal/activation/fingerprint_windows.go` | 50 | Same |
| `internal/activation/fingerprint_darwin.go` | 55 | Same |
| `internal/manager/process.go` | 120 | Rewritten: sslocal + tun2socks instead of hysteria2; AppConfig struct |
| `internal/updater/updater.go` | 80 | Same |
| `internal/tunnel/tunnel.go` | 10 | Same (fallback) |
| `internal/tunnel/tun_linux.go` | 30 | Same |
| `internal/tunnel/tun_windows.go` | 40 | Same |
| `internal/tunnel/tun_darwin.go` | 40 | Same |
| `internal/tunnel/killswitch.go` | 25 | Same |
| `internal/tunnel/dns.go` | 20 | Same |
| `internal/gui/app.go` | 320 | Same (tier badges updated) |
| `server/Caddyfile` | 40 | Same |
| `server/pb_hooks/activation.pb.js` | 80 | Returns config + speed_limit_bps + udp_relay |
| `server/pb_hooks/suspension.pb.js` | 25 | Same |
| `server/pb_hooks/middleman_codes.pb.js` | 40 | Same |
| `scripts/setup_vps.sh` | 240 | Three instances + tc + BBR + Brutal target + wrappers |
| `scripts/generate_codes.sh` | 40 | Same |
| `scripts/print_codes.sh` | 60 | Same |
| **Total** | **~1,400** | |