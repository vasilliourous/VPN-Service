# V4 Implementation Plan — Sing-Box Unified Client

> Target: ~1,600 lines of Go across ~25 files. Build time: 1-2 weekends.
> Server: Blank Ubuntu 22.04 VPS. Sing-box replaces sslocal + tun2socks on the client.

---

## Prerequisites

- Go 1.22+ with Fyne v2 deps (CGO required)
- Blank Ubuntu 22.04 VPS ($6-12/mo from Hetzner, DigitalOcean, Vultr)
- Domain name pointing to VPS IP
- **sing-box** binary for each target platform (download from GitHub releases)
- shadowsocks-rust `ssserver` binary for the server (same as V3)

---

## Phase 1: Project Scaffolding

```bash
mkdir -p v4/cmd/myvpn
cd v4
go mod init myvpn

mkdir -p internal/{storage,activation,manager,tunnel,gui,updater,heartbeat}
mkdir -p server/pb_hooks
mkdir -p scripts
```

---

## Phase 2: Server Setup

### Step 2.1 — VPS Base Setup

```bash
ssh root@your-vps
apt update && apt upgrade -y
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
```

### Step 2.2 — Install Shadowsocks Server + BBR

```bash
# Download shadowsocks-rust ssserver
cd /tmp
wget https://github.com/shadowsocks/shadowsocks-rust/releases/download/v1.23.0/shadowsocks-v1.23.0.x86_64-unknown-linux-gnu.tar.xz
tar -xf shadowsocks-*.tar.xz
cp ssserver /usr/local/bin/

# Generate three passwords
ECO_PASS=$(openssl rand -hex 16)
STEALTH_PASS=$(openssl rand -hex 16)
STRIKE_PASS=$(openssl rand -hex 16)

cat > /root/.tier_passwords <<EOF
ECO_PASS=$ECO_PASS
STEALTH_PASS=$STEALTH_PASS
STRIKE_PASS=$STRIKE_PASS
EOF
chmod 600 /root/.tier_passwords

# Enable BBR as system default
modprobe tcp_bbr
echo "tcp_bbr" > /etc/modules-load.d/tcp_bbr.conf
sysctl -w net.core.default_qdisc=fq
sysctl -w net.ipv4.tcp_congestion_control=bbr
cat > /etc/sysctl.d/90-bbr.conf <<EOF
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
```

### Step 2.3 — Eco Instance (BBR, tc 5 Mbps)

**`/etc/shadowsocks/eco.json`:**
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

**`/etc/systemd/system/shadowsocks-eco.service`:**
```ini
[Unit]
Description=Shadowsocks Server (Eco — BBR + tc 5 Mbps)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/eco.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

**tc traffic shaping (5 Mbps shared cap):**
```bash
IFACE=$(ip -4 route show default | awk '{print $5}')
tc qdisc add dev $IFACE root handle 1: htb default 30
tc class add dev $IFACE parent 1: classid 1:10 htb rate 5mbit ceil 5mbit
tc filter add dev $IFACE protocol ip parent 1: prio 1 u32 \
  match ip sport 8443 0xffff flowid 1:10
tc qdisc add dev $IFACE parent 1:10 handle 10: sfq perturb 10

# Persist
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

### Step 2.4 — Install Brutal + Stealth Instance

**Build Brutal kernel module:**
```bash
apt install -y linux-headers-$(uname -r) build-essential
cd /tmp
git clone https://github.com/apernet/tcp-brutal-ng.git
cd tcp-brutal-ng
make
insmod tcp_brutal.ko
echo "tcp_brutal" > /etc/modules-load.d/tcp_brutal.conf
cp tcp_brutal.ko /lib/modules/$(uname -r)/kernel/net/ipv4/
depmod -a

# Set Brutal target rate to 48 Mbps
if sysctl -w net.ipv4.tcp_brutal.target_rate=48000000 2>/dev/null; then
  echo "net.ipv4.tcp_brutal.target_rate=48000000" > /etc/sysctl.d/90-brutal.conf
else
  echo 48 > /sys/module/tcp_brutal/parameters/target_rate
  echo "options tcp_brutal target_rate=48" > /etc/modprobe.d/tcp_brutal.conf
fi
```

**Brutal LD_PRELOAD wrapper:**
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

**`/etc/shadowsocks/stealth.json`:**
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

**`/etc/systemd/system/shadowsocks-stealth.service`:**
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

### Step 2.5 — Strike Instance (BBR, Unlimited, UDP)

**`/etc/shadowsocks/strike.json`:**
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

**`/etc/systemd/system/shadowsocks-strike.service`:**
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

### Step 2.6 — Enable All Services

```bash
systemctl daemon-reload
systemctl enable --now shadowsocks-eco shadowsocks-stealth shadowsocks-strike
ufw allow 8443/tcp
ufw allow 8444/tcp
ufw allow 8445/tcp
```

### Step 2.7 — Caddy + PocketBase

**Install Caddy:**
```bash
apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor \
  -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  > /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install caddy
```

**`/etc/caddy/Caddyfile`:**
```
api.yourdomain.com {
    reverse_proxy 127.0.0.1:8090
    rate_limit {
        zone activation {
            key {remote_host}
            events 5
            window 10m
        }
        zone general {
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

**Install PocketBase:**
```bash
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
ExecStart=/opt/pocketbase/pocketbase serve --http=127.0.0.1:8090 \
  --dir=/opt/pocketbase/pb_data --hooksDir=/opt/pocketbase/pb_hooks
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now pocketbase
caddy fmt --overwrite /etc/caddy/Caddyfile
systemctl reload caddy
```

### Step 2.8 — PocketBase Collections

Open `https://api.yourdomain.com/_/` and create admin account. Create these collections:

**`codes`:**
| Field | Type | Notes |
|-------|------|-------|
| `code` | text (unique) | `MYVPN-XXXX-XXXX-XXXX-C` with Luhn check digit |
| `tier` | select | `eco`, `stealth`, `strike` |
| `used` | bool | default false |
| `suspended` | bool | default false |
| `fingerprint` | text (optional) | SHA256, set by app on activation |
| `middleman` | text (optional) | Reference to middleman |
| `notes` | text (optional) | Admin notes (e.g., "replaced device 2026-08-01") |

**`tier_configs`:**
| Field | Type | Notes |
|-------|------|-------|
| `tier` | text (unique) | `eco`, `stealth`, `strike` |
| `config` | json | Sing-box Shadowsocks outbound config |
| `speed_limit_bps` | number | 0 = unlimited (server enforces caps) |
| `udp_relay` | bool | Enables `--socks5-udp` equivalent in sing-box |
| `active` | bool | Must be true |

**`activation_attempts`** (rate limit tracking):
| Field | Type | Notes |
|-------|------|-------|
| `ip` | text | Client IP |
| `created` | autodate | Auto-set on create |

### Step 2.9 — Seed Tier Configs

Enter these in PocketBase `tier_configs`. Match passwords from `/root/.tier_passwords`.

**Eco:**
```json
{
  "tier": "eco",
  "config": {
    "server": "api.yourdomain.com",
    "server_port": 8443,
    "password": "<ECO_PASS>",
    "method": "aes-256-gcm"
  },
  "speed_limit_bps": 0,
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
    "method": "aes-256-gcm"
  },
  "speed_limit_bps": 0,
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
    "method": "aes-256-gcm"
  },
  "speed_limit_bps": 0,
  "udp_relay": true,
  "active": true
}
```

### Step 2.10 — JS Hooks

**`server/pb_hooks/activation.pb.js`** — validates code (including Luhn check), binds device, returns config:

```javascript
const CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const BASE = CHARSET.length;

function luhnModN(code) {
    let sum = 0;
    let double = false;
    for (let i = code.length - 1; i >= 0; i--) {
        let val = CHARSET.indexOf(code[i].toUpperCase());
        if (val === -1) return -1;
        if (double) {
            val *= 2;
            if (val >= BASE) val = Math.floor(val / BASE) + (val % BASE);
        }
        sum += val;
        double = !double;
    }
    return (BASE - (sum % BASE)) % BASE;
}

routerAdd("POST", "/api/activate", (c) => {
    const data = $apis.requestInfo(c).data;
    const rawCode = data.code || "";
    const fingerprint = data.fingerprint || "";

    // Strip prefix and dashes
    const clean = rawCode.replace(/^MYVPN-/, "").replace(/-/g, "").toUpperCase();
    if (clean.length !== 17) {
        return c.json(400, { code: 400, message: "Invalid code format" });
    }

    // Luhn check (server-side)
    const checkChar = clean[16];
    const dataPart = clean.slice(0, 16);
    const expectedCheck = CHARSET[luhnModN(dataPart)];
    if (checkChar !== expectedCheck) {
        return c.json(400, { code: 400, message: "Invalid code — check for typos" });
    }

    // Rate limit check
    const ip = $apis.requestInfo(c).remoteAddress;
    const recent = $app.dao().findRecordsByFilter(
        "activation_attempts",
        `ip='${ip}' && created >= datetime('now','-10 minutes')`,
        "", 0, 0
    );
    if (recent.length >= 5) {
        return c.json(429, { code: 429, message: "Too many attempts — try again later" });
    }

    // Look up code
    const records = $app.dao().findRecordsByFilter("codes", `code='${rawCode}'`, "", 0, 1);
    if (records.length === 0) {
        // Log the failed attempt
        const attempt = $app.dao().createRecord("activation_attempts");
        attempt.set("ip", ip);
        $app.dao().saveRecord(attempt);
        return c.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];
    if (record.getBool("suspended")) {
        return c.json(403, { code: 403, message: "Account suspended" });
    }

    // Device binding
    const existingFp = record.getString("fingerprint");
    if (existingFp) {
        if (existingFp !== fingerprint) {
            return c.json(403, { code: 403, message: "Code already bound to another device" });
        }
        // Same device re-activating — still return config
    } else {
        // First activation — bind
        record.set("fingerprint", fingerprint);
    }

    record.set("used", true);
    $app.dao().saveRecord(record);

    // Fetch tier config
    const tier = record.getString("tier");
    const configs = $app.dao().findRecordsByFilter("tier_configs", `tier='${tier}'`, "", 0, 1);
    if (configs.length === 0 || !configs[0].getBool("active")) {
        return c.json(500, { code: 500, message: "Tier configuration unavailable" });
    }

    const tc = configs[0];
    return c.json(200, {
        tier: tier,
        config: tc.get("config"),
        udp_relay: tc.get("udp_relay")
    });
});
```

**`server/pb_hooks/heartbeat.pb.js`** — suspension check + update notification + config refresh:

```javascript
routerAdd("GET", "/api/heartbeat", (c) => {
    const code = c.query().get("code");
    if (!code) {
        return c.json(400, { code: 400, message: "Missing code" });
    }

    const records = $app.dao().findRecordsByFilter("codes", `code='${code}'`, "", 0, 1);
    if (records.length === 0) {
        return c.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];
    if (record.getBool("suspended")) {
        return c.json(403, { code: 403, message: "Account suspended" });
    }

    // Check for available update
    const response = { status: "ok" };

    // If there's a pending update, include version info
    try {
        const fs = require("fs");
        if (fs.existsSync("/var/www/html/update.json")) {
            const updateData = JSON.parse(fs.readFileSync("/var/www/html/update.json", "utf8"));
            response.update_available = updateData.version;
        }
    } catch(e) {
        // Update file missing or invalid — skip
    }

    // Return any config changes (e.g., password rotation)
    const tier = record.getString("tier");
    const configs = $app.dao().findRecordsByFilter("tier_configs", `tier='${tier}'`, "", 0, 1);
    if (configs.length > 0 && configs[0].getBool("active")) {
        response.config = configs[0].get("config");
        response.udp_relay = configs[0].get("udp_relay");
    }

    return c.json(200, response);
});
```

**`server/pb_hooks/admin_unbind.pb.js`** — allows admin to clear a device fingerprint for code recovery:

```javascript
routerAdd("POST", "/api/admin/unbind-code", (c) => {
    const data = $apis.requestInfo(c).data;
    const adminToken = data.admin_token || "";
    const code = data.code || "";

    // Simple admin token check (set via PB admin settings or env)
    const validToken = $os.getenv("ADMIN_API_TOKEN") || "change-me-in-production";
    if (adminToken !== validToken) {
        return c.json(403, { code: 403, message: "Invalid admin token" });
    }

    const records = $app.dao().findRecordsByFilter("codes", `code='${code}'`, "", 0, 1);
    if (records.length === 0) {
        return c.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];
    record.set("fingerprint", "");   // Clear binding
    record.set("notes", (record.getString("notes") || "") +
        ` [unbound by admin at ${new Date().toISOString()}]`);
    $app.dao().saveRecord(record);

    return c.json(200, { status: "ok", message: "Code unbound. Customer can re-activate on new device." });
});
```

### Step 2.11 — Offsite Backups

```bash
# Install Backblaze B2 CLI
pip3 install b2
b2 authorize-account

cat > /opt/pocketbase/backup.sh <<'EOF'
#!/bin/sh
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
TMP_FILE="/tmp/pb-backup-$TIMESTAMP.db"
DUMP_FILE="/tmp/pb-dump-$TIMESTAMP.sql.gz"

sqlite3 /opt/pocketbase/pb_data/data.db ".backup $TMP_FILE"
b2 upload-file my-vpn-backup-bucket "$TMP_FILE" "backups/data-$TIMESTAMP.db" > /dev/null 2>&1
rm -f "$TMP_FILE"

sqlite3 /opt/pocketbase/pb_data/data.db .dump | gzip > "$DUMP_FILE"
b2 upload-file my-vpn-backup-bucket "$DUMP_FILE" "dumps/dump-$TIMESTAMP.sql.gz" > /dev/null 2>&1
rm -f "$DUMP_FILE"

echo "backup completed $TIMESTAMP" >> /var/log/pocketbase-backup.log
EOF

chmod +x /opt/pocketbase/backup.sh
echo "0 * * * * root /opt/pocketbase/backup.sh" > /etc/cron.d/pocketbase-backup
```

---

## Phase 3: Client — Core Packages

### Step 3.1 — Storage (`internal/storage/storage.go`)

Stores `{ code, tier, device_fingerprint, shadosocks_config, udp_relay, activated }` as JSON.

### Step 3.2 — Activation (`internal/activation/activation.go`)

- Code input: `MYVPN-XXXX-XXXX-XXXX-C` format
- **Client-side Luhn-mod-N validation** before sending to server
- POST to `{hub}/api/activate` with `{ code, fingerprint }`
- Receive `{ tier, config, udp_relay }`
- Store config + tier locally

### Step 3.3 — Fingerprint (`internal/activation/fingerprint*.go`)

SHA256(MAC + disk serial + motherboard UUID). Same as V3.

### Step 3.4 — Manager (`internal/manager/process.go`)

Manages sing-box process lifecycle.

```go
package manager

// SingBoxConfig represents the full sing-box configuration
// generated by the manager for the active tier.
type SingBoxConfig struct {
    Log      LogConfig      `json:"log"`
    Inbounds []Inbound      `json:"inbounds"`
    Outbounds []Outbound    `json:"outbounds"`
    Route    RouteConfig    `json:"route"`
    DNS      DNSConfig      `json:"dns"`
}

type AppConfig struct {
    Server   string // domain:port from activation
    Password string
    Method   string // aes-256-gcm
    UDPRelay bool
    Tier     string
}

// Functions:
//   - StartEngine(cfg *AppConfig, engineDir string) error
//   - StopEngine() error
//   - Status() EngineStatus
//
// Flow:
//   StartEngine:
//     1. Build sing-box JSON config:
//        - TUN inbound (tun0, 10.0.0.1/24, depending on platform)
//        - Shadowsocks outbound to configured server:port
//        - Route: default → shadowsocks outbound
//        - DNS: fake-dns or direct
//     2. Write config to temp file in app data dir
//     3. Find sing-box binary: ./sing-box, engines/sing-box, or PATH
//     4. Launch: sing-box -c <config> run
//     5. Wait for TUN device (poll with 5 retries at 200ms)
//     6. Add default route through TUN
//     7. Store PID, update status
//
//   StopEngine:
//     1. Remove default route
//     2. Kill sing-box (SIGTERM, then SIGKILL after 5s)
//     3. Clean up config temp file
//     4. Update status
//
//   The sing-box config varies by platform:
//     Linux:   tun platform "linux", tun device name "tun0"
//     macOS:   tun platform "darwin", tun device name "utun8"
//     Windows: tun platform "windows", tun device name "wintun0"
```

**Example generated sing-box config for Eco/Stealth (TCP only):**

```json
{
  "log": { "level": "warn", "output": "" },
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "interface_name": "tun0",
      "address": "10.0.0.1/24",
      "mtu": 1500,
      "auto_route": true,
      "strict_route": true
    }
  ],
  "outbounds": [
    {
      "type": "shadowsocks",
      "tag": "ss-out",
      "server": "api.yourdomain.com",
      "server_port": 8443,
      "method": "aes-256-gcm",
      "password": "eco-password"
    }
  ],
  "route": {
    "rules": [
      { "outbound": "ss-out" }
    ],
    "final": "ss-out"
  },
  "dns": {
    "final": "fake-ip",
    "rules": [
      { "outbound": "any", "server": "local" }
    ],
    "servers": [
      { "tag": "local", "address": "local", "detour": "direct" },
      { "tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": "ss-out" }
    ]
  }
}
```

For Strike (UDP enabled), add `"udp_over_tcp": false` or set `"network": "udp"` on the outbound and ensure the TUN inbound handles UDP.

Since the app generates this config dynamically from the PocketBase tier_config data, no per-tier static configs are needed.

### Step 3.5 — Heartbeat (`internal/heartbeat/heartbeat.go`)

```go
// Heartbeat loop: runs every 5 minutes
//   GET {hub}/api/heartbeat?code=XXXX-XXXX-XXXX
//   On success:
//     - Reset grace counter
//     - Check for update_available field → trigger update check
//     - Apply any config changes returned
//   On failure (network error):
//     - Increment grace counter
//     - If grace < 7 days: continue normally (connection keeps working)
//     - If grace >= 7 days: show "Re-activation required" in GUI
//   On 403 response:
//     - Immediately disconnect VPN
//     - Show "Account suspended — contact your middleman"
//     - Block reconnection
```

### Step 3.6 — Tunnel (`internal/tunnel/`)

Fallback package for platforms where sing-box TUN doesn't work. Same interface as V3. In practice, sing-box handles all platforms.

### Step 3.7 — Updater (`internal/updater/updater.go`)

**Two-phase sentinel auto-rollback system.** Restored and adapted from V1.

```go
package updater

// Constants:
//   sentinelPending   = ".update-pending"
//   sentinelConfirmed = ".update-confirmed"

// CheckOnStartup() — MUST be called at the very top of main().
// Returns true if a rollback occurred.
// Logic:
//   1. Manual revert (--revert flag or Ctrl held):
//      - Restore .prev, rename current to .bad, clean sentinels
//   2. .update-confirmed EXISTS:
//      - Second clean launch → delete .prev + sentinels
//   3. .update-pending EXISTS + .prev EXISTS:
//      - First launch after update (or crash)
//      - Rename .pending → .confirmed (if we crash now, next launch reverts)
//   4. No sentinels:
//      - Normal startup, clean any stale .prev

// DownloadAndApply() — downloads update, verifies SHA256, applies swap:
//   1. Download zip from URL
//   2. SHA256 verify against expected hash
//   3. Extract binary from zip
//   4. Verify new binary runs (exec --version)
//   5. Save current as .prev
//   6. Write .update-pending sentinel
//   7. Replace current binary with new
//   8. Start new binary (fork)
//   9. Exit current process
```

### Step 3.8 — GUI (`internal/gui/app.go`)

Same systray design as V3 with additions:
- Grace period indicator (yellow dot + "Server unreachable — X days remaining")
- Update notification ("Update available — restart to apply")
- Diagnostics: "Export logs" button (collects app logs + config without passwords)

---

## Phase 4: Main Entrypoint

```go
package main

import (
    "flag"
    "myvpn/internal/storage"
    "myvpn/internal/updater"
    "myvpn/internal/gui"
)

var (
    version     = "1.0.0"
    adminHubURL = flag.String("hub", "https://api.yourdomain.com", "Admin hub URL")
    revertFlag  = flag.Bool("revert", false, "Revert to previous version")
)

func main() {
    flag.Parse()

    // MUST run before anything else
    rolledBack, _ := updater.CheckOnStartup(*revertFlag)
    if rolledBack {
        // Log the rollback event
    }

    storage.Init()
    gui.Run(*adminHubURL, version)
}
```

---

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

bundle-windows: build-windows
	zip -j dist/myvpn-windows.zip myvpn.exe engines/sing-box-windows-amd64.exe

bundle-macos-intel: build-macos-intel
	zip -j dist/myvpn-macos-intel.zip myvpn-intel engines/sing-box-darwin-amd64

bundle-macos-arm: build-macos-arm
	zip -j dist/myvpn-macos-arm.zip myvpn-arm engines/sing-box-darwin-arm64

bundle-all: bundle-windows bundle-macos-intel bundle-macos-arm
```

---

## File-by-File Summary

| File | Est. Lines | New/Changed |
|------|-----------|-------------|
| `cmd/myvpn/main.go` | 40 | Added `--revert`, updated `CheckOnStartup` call |
| `internal/storage/storage.go` | 70 | Same as V3 |
| `internal/activation/activation.go` | 80 | **Added Luhn-mod-N client-side validation** |
| `internal/activation/luhn.go` | 40 | **New** — Luhn-mod-N checksum functions |
| `internal/activation/fingerprint.go` | 15 | Same as V3 |
| `internal/activation/fingerprint_windows.go` | 50 | Same as V3 |
| `internal/activation/fingerprint_darwin.go` | 55 | Same as V3 |
| `internal/manager/process.go` | 150 | **Rewritten** — sing-box config generation, process lifecycle |
| `internal/heartbeat/heartbeat.go` | 90 | **New** — 5-min heartbeat loop, grace counter, suspension detection |
| `internal/updater/updater.go` | 80 | **Rewritten** — download, verify, swap, fork |
| `internal/updater/recover.go` | 100 | **New** — sentinel handshake, auto-revert, manual revert |
| `internal/updater/update_windows.go` | 60 | **New** — Windows-specific swap + launcher |
| `internal/updater/update_unix.go` | 70 | **New** — Unix-specific swap + fork |
| `internal/tunnel/tunnel.go` | 10 | Same (fallback) |
| `internal/tunnel/tun_linux.go` | 30 | Same (fallback) |
| `internal/tunnel/tun_windows.go` | 40 | Same (fallback) |
| `internal/tunnel/tun_darwin.go` | 40 | Same (fallback) |
| `internal/tunnel/killswitch.go` | 25 | Same |
| `internal/tunnel/dns.go` | 20 | Same |
| `internal/gui/app.go` | 360 | Added grace indicator, update notification, diagnostics export |
| `server/Caddyfile` | 30 | Added rate_limit zones |
| `server/pb_hooks/activation.pb.js` | 90 | **Rewritten** — Luhn check, rate limiting, device binding |
| `server/pb_hooks/heartbeat.pb.js` | 60 | **New** — suspension check + update notification + config refresh |
| `server/pb_hooks/admin_unbind.pb.js` | 35 | **New** — code recovery endpoint |
| `scripts/setup_vps.sh` | 280 | Three instances + BBR + Brutal + tc + Caddy + PB + backups |
| `scripts/generate_codes.sh` | 60 | **Updated** — Luhn checksum generation |
| `scripts/print_codes.sh` | 60 | Same |
| **Total** | **~1,940** | |
