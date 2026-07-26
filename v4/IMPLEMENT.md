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
Requires=tc-eco-cap.service

[Service]
Type=simple
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/eco.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

**tc traffic shaping (5 Mbps shared cap) — with systemd oneshot for reboot persistence:**
```bash
IFACE=$(ip -4 route show default | awk '{print $5}')
tc qdisc add dev $IFACE root handle 1: htb default 30
tc class add dev $IFACE parent 1: classid 1:10 htb rate 5mbit ceil 5mbit
tc filter add dev $IFACE protocol ip parent 1: prio 1 u32 \
  match ip sport 8443 0xffff flowid 1:10
tc qdisc add dev $IFACE parent 1:10 handle 10: sfq perturb 10

# Persist via systemd oneshot service (more reliable than rc.local)
cat > /etc/systemd/system/tc-eco-cap.service <<'UNIT'
[Unit]
Description=Apply tc bandwidth cap for Eco tier (port 8443)
After=network.target
Before=shadowsocks-eco.service

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'IFACE=$(ip -4 route show default | awk "{print \$5}"); \
  tc qdisc add dev $IFACE root handle 1: htb default 30 2>/dev/null; \
  tc class add dev $IFACE parent 1: classid 1:10 htb rate 5mbit ceil 5mbit 2>/dev/null; \
  tc filter add dev $IFACE protocol ip parent 1: prio 1 u32 match ip sport 8443 0xffff flowid 1:10 2>/dev/null; \
  tc qdisc add dev $IFACE parent 1:10 handle 10: sfq perturb 10 2>/dev/null'
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now tc-eco-cap
```

### Step 2.4 — Install Brutal + Stealth Instance

**Build Brutal kernel module** — with DKMS so it auto-rebuilds on kernel updates:
```bash
apt install -y linux-headers-$(uname -r) build-essential dkms
cd /tmp
git clone https://github.com/apernet/tcp-brutal-ng.git
cd tcp-brutal-ng

# Install as DKMS module so it survives kernel updates
DKMS_PKG="tcp-brutal-ng"
DKMS_VER="1.0"
mkdir -p /usr/src/${DKMS_PKG}-${DKMS_VER}
cp -r * /usr/src/${DKMS_PKG}-${DKMS_VER}/
cat > /usr/src/${DKMS_PKG}-${DKMS_VER}/dkms.conf <<DKMS
PACKAGE_NAME="${DKMS_PKG}"
PACKAGE_VERSION="${DKMS_VER}"
BUILT_MODULE_NAME="tcp_brutal"
DEST_MODULE_LOCATION="/kernel/net/ipv4"
AUTOINSTALL="yes"
MAKE="make -C \$dkms_tree/\$PACKAGE_NAME/\$PACKAGE_VERSION/build"
CLEAN="make -C \$dkms_tree/\$PACKAGE_NAME/\$PACKAGE_VERSION/build clean"
DKMS
dkms add ${DKMS_PKG}/${DKMS_VER}
dkms build ${DKMS_PKG}/${DKMS_VER}
dkms install ${DKMS_PKG}/${DKMS_VER}

# Load module now
modprobe tcp_brutal
echo "tcp_brutal" > /etc/modules-load.d/tcp_brutal.conf

# Set Brutal target rate to 48 Mbps
if sysctl -w net.ipv4.tcp_brutal.target_rate=48000000 2>/dev/null; then
  echo "net.ipv4.tcp_brutal.target_rate=48000000" > /etc/sysctl.d/90-brutal.conf
else
  # Fallback for older Brutal versions that expose sysfs instead
  echo 48 > /sys/module/tcp_brutal/parameters/target_rate 2>/dev/null || true
  echo "options tcp_brutal target_rate=48" > /etc/modprobe.d/tcp_brutal.conf
fi

# Verify it's loaded
lsmod | grep tcp_brutal || echo "ERROR: tcp_brutal module not loaded"
```

**Brutal LD_PRELOAD wrapper:** — fixed to intercept TCP_CONGESTION directly, correctly pass through TCP_NODELAY, and handle dlsym errors
```bash
cat > /usr/local/src/brutal-wrap.c <<'WRAP'
#define _GNU_SOURCE
#include <dlfcn.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <string.h>
#include <stdio.h>

/*
 * Forces the ssserver process to use the "brutal" congestion control
 * on every TCP socket it opens.
 *
 * Fixes vs earlier version:
 *  - Intercepts TCP_CONGESTION directly (not just TCP_NODELAY trigger)
 *  - Passes TCP_NODELAY call through properly so Nagle is disabled
 *  - Only overrides when the app sets a CC, doesn't affect other options
 *  - Avoids infinite recursion from calling setsockopt inside itself
 */

static int (*real_setsockopt)(int, int, int, const void *, socklen_t) = NULL;

int setsockopt(int sockfd, int level, int optname,
               const void *optval, socklen_t optlen) {
    if (!real_setsockopt)
        real_setsockopt = dlsym(RTLD_NEXT, "setsockopt");
    if (!real_setsockopt) {
        fprintf(stderr, "brutal-wrap: dlsym(RTLD_NEXT, setsockopt) failed\n");
        return -1;
    }

    /* Intercept any TCP_CONGESTION setsockopt and force "brutal" */
    if (level == IPPROTO_TCP && optname == TCP_CONGESTION) {
        const char *cc = "brutal";
        return real_setsockopt(sockfd, level, optname, cc, strlen(cc));
    }

    /* Pass through everything else (TCP_NODELAY, etc.) */
    return real_setsockopt(sockfd, level, optname, optval, optlen);
}
WRAP
gcc -shared -fPIC -o /usr/local/lib/brutal-wrap.so /usr/local/src/brutal-wrap.c -ldl

# Verify the wrapper compiles and exports the right symbol
nm -D /usr/local/lib/brutal-wrap.so | grep setsockopt
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
# Post-start: verify brutal CC is actually active on the stealth port
ExecStartPost=/bin/sh -c 'sleep 0.3 && ss -ti sport = :8444 2>/dev/null | head -3 | grep -q brutal && echo "brutal CC confirmed active on port 8444" || echo "WARNING: brutal CC not detected on port 8444 — kernel module may not be loaded" | logger -t brutal-check'

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
Description=Shadowsocks Server (Strike — BBR, 200 Mbps tc cap, UDP)
After=network.target
Requires=tc-strike-cap.service

[Service]
Type=simple
ExecStart=/usr/local/bin/ssserver -c /etc/shadowsocks/strike.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

**tc traffic shaping (200 Mbps shared cap) — with systemd oneshot for reboot persistence:**
This mirrors the Eco tc cap but with a much higher ceiling — high enough that gaming never touches it (Valorant ~5 Mbps, Fortnite ~20 Mbps), but prevents a single user from saturating the link.

```bash
IFACE=$(ip -4 route show default | awk '{print $5}')
tc class add dev $IFACE parent 1: classid 1:30 htb rate 200mbit ceil 200mbit 2>/dev/null
tc filter add dev $IFACE protocol ip parent 1: prio 3 u32 \
  match ip sport 8445 0xffff flowid 1:30 2>/dev/null

# Persist via systemd oneshot service
cat > /etc/systemd/system/tc-strike-cap.service <<'UNIT'
[Unit]
Description=Apply tc bandwidth cap for Strike tier (port 8445)
After=network.target
Before=shadowsocks-strike.service

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'IFACE=$(ip -4 route show default | awk "{print \$5}"); \
  tc class add dev $IFACE parent 1: classid 1:30 htb rate 200mbit ceil 200mbit 2>/dev/null; \
  tc filter add dev $IFACE protocol ip parent 1: prio 3 u32 match ip sport 8445 0xffff flowid 1:30 2>/dev/null'
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now tc-strike-cap
```

### Step 2.6 — Enable All Services

```bash
systemctl daemon-reload
systemctl enable --now shadowsocks-eco shadowsocks-stealth shadowsocks-strike tc-eco-cap tc-strike-cap
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
jeanette-qh9zbe.cloudserver.nz {
    reverse_proxy 127.0.0.1:8090

    # Coarse IP-based throttle (DDoS protection only).
    # Fine-grained per-fingerprint rate limiting is handled by
    # the PocketBase JS hook (activation.pb.js) — this Caddy layer
    # is a safety net against IP-level floods.
    # NOTE: CGNAT means all school students share one public IP,
    # so per-IP limits must be generous enough for the whole school.
    rate_limit {
        zone activation {
            key {remote_host}
            events 100      # Per IP (shared behind CGNAT) — generous
            window 10m       # Real per-user limit is in PB hook
        }
        zone general {
            key {remote_host}
            events 500
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

# Create dedicated system user for PocketBase
useradd --system --no-create-home --shell /usr/sbin/nologin pocketbase
chown -R pocketbase:pocketbase /opt/pocketbase

cat > /etc/systemd/system/pocketbase.service <<EOF
[Unit]
Description=PocketBase
After=network.target

[Service]
Type=simple
User=pocketbase
Group=pocketbase
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

Open `https://jeanette-qh9zbe.cloudserver.nz/_/` and create admin account. Create these collections:

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
| `rate_key` | text (indexed) | Fingerprint hash prefix (or fallback to IP) — keys rate limit behind CGNAT |
| `ip` | text | Client IP (logged for audit) |
| `created` | autodate | Auto-set on create |

**`admin_activity`** (optional — audit trail):
| Field | Type | Notes |
|-------|------|-------|
| `action` | text | e.g. `unbind_code`, `suspend` |
| `code` | text | Which code was acted on |
| `old_fingerprint` | text (optional) | Previous fingerprint if cleared |
| `admin_ip` | text | Admin's remote address |
| `created` | autodate | Auto-set on create |

### Step 2.9 — Seed Tier Configs

Enter these in PocketBase `tier_configs`. Match passwords from `/root/.tier_passwords`.

**Eco:**
```json
{
  "tier": "eco",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
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
    "server": "jeanette-qh9zbe.cloudserver.nz",
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
    "server": "jeanette-qh9zbe.cloudserver.nz",
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

// ─────────────────────────────────────────────────────────────────
// Utility: sanitize a string for use in PocketBase filter expressions.
// PocketBase filters use single-quoted string literals. An embedded
// single quote would break parsing. This function escapes quotes and
// strips characters that are unsafe in PB filter contexts.
// ─────────────────────────────────────────────────────────────────
function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    // Strip any backslash (PB filter escape char) and single quotes
    return val.replace(/\\/g, "").replace(/'/g, "");
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

    // Luhn check (server-side) — gates out malformed codes before any DB access
    const checkChar = clean[16];
    const dataPart = clean.slice(0, 16);
    const expectedCheck = CHARSET[luhnModN(dataPart)];
    if (checkChar !== expectedCheck) {
        return c.json(400, { code: 400, message: "Invalid code — check for typos" });
    }

    // Prune old rate-limit records (>24h) to prevent unbounded table growth
    $app.dao().db().newQuery(
        "DELETE FROM activation_attempts WHERE created <= datetime('now','-24 hours')"
    ).execute();

    // ── Rate limit check ──
    // Key by fingerprint hash when available (works behind CGNAT where
    // all students share the same IP). Fall back to IP only if no fingerprint.
    const ip = $apis.requestInfo(c).remoteAddress;
    const rateKey = fingerprint ? fingerprint.substring(0, 16) : ip;
    const recent = $app.dao().findRecordsByFilter(
        "activation_attempts",
        "rate_key={:key} && created >= datetime('now','-10 minutes')",
        "", 0, 0,
        { key: rateKey }
    );
    if (recent.length >= 5) {
        return c.json(429, { code: 429, message: "Too many attempts — try again later" });
    }

    // ── Look up code (parameterized to prevent filter injection) ──
    // Sanitize the code to strip any filter-breaking characters
    const safeCode = sanitizeFilter(rawCode);
    const records = $app.dao().findRecordsByFilter(
        "codes",
        "code={:code}",
        "", 0, 1,
        { code: safeCode }
    );
    if (records.length === 0) {
        // Log the failed attempt with the rate key
        const attempt = $app.dao().createRecord("activation_attempts");
        attempt.set("rate_key", rateKey);
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

    // ── Fetch tier config (parameterized) ──
    const tier = record.getString("tier");
    const safeTier = sanitizeFilter(tier);
    const configs = $app.dao().findRecordsByFilter(
        "tier_configs",
        "tier={:tier} && active={:active}",
        "", 0, 1,
        { tier: safeTier, active: true }
    );
    if (configs.length === 0) {
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
// ─────────────────────────────────────────────────────────────────
// Simple hash function: returns an integer 0..modulus-1 from a string.
// Used for deterministic canary rollout: same device always gets the
// same bucket, so it stays in its rollout stage across heartbeats.
// ─────────────────────────────────────────────────────────────────
function hashMod(str, modulus) {
    let hash = 0;
    for (let i = 0; i < str.length; i++) {
        const char = str.charCodeAt(i);
        hash = ((hash << 5) - hash) + char;
        hash = hash & hash; // Convert to 32-bit int
    }
    return Math.abs(hash) % modulus;
}

function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    return val.replace(/\\/g, "").replace(/'/g, "");
}

routerAdd("GET", "/api/heartbeat", (c) => {
    const code = c.query().get("code");
    const fingerprint = c.query().get("fp") || "";
    if (!code) {
        return c.json(400, { code: 400, message: "Missing code" });
    }

    const safeCode = sanitizeFilter(code);
    const records = $app.dao().findRecordsByFilter(
        "codes",
        "code={:code}",
        "", 0, 1,
        { code: safeCode }
    );
    if (records.length === 0) {
        return c.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];
    if (record.getBool("suspended")) {
        return c.json(403, { code: 403, message: "Account suspended" });
    }

    const response = { status: "ok" };

    // ── Staged rollout: only signal update if device is in the rollout bucket ──
    try {
        const fs = require("fs");
        const updatePath = "/var/www/html/update.json";
        if (fs.existsSync(updatePath)) {
            const updateData = JSON.parse(fs.readFileSync(updatePath, "utf8"));
            const rolloutPct = parseInt(updateData.rollout_percent || "0", 10);

            // Determine eligibility: use fingerprint if available, otherwise code hash
            const eligibilityKey = fingerprint || code;
            const bucket = hashMod(eligibilityKey, 100);

            if (rolloutPct > 0 && bucket < rolloutPct) {
                response.update_available = updateData.version;
            }
        }
    } catch(e) {
        // Invalid update.json — skip update signal
    }

    // Return any config changes (parameterized query)
    const tier = record.getString("tier");
    const safeTier = sanitizeFilter(tier);
    const configs = $app.dao().findRecordsByFilter(
        "tier_configs",
        "tier={:tier} && active={:active}",
        "", 0, 1,
        { tier: safeTier, active: true }
    );
    if (configs.length > 0) {
        response.config = configs[0].get("config");
        response.udp_relay = configs[0].get("udp_relay");
    }

    return c.json(200, response);
});
```

**`server/pb_hooks/admin_unbind.pb.js`** — allows admin to clear a device fingerprint for code recovery:

```javascript
function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    return val.replace(/\\/g, "").replace(/'/g, "");
}

routerAdd("POST", "/api/admin/unbind-code", (c) => {
    const data = $apis.requestInfo(c).data;
    const adminToken = data.admin_token || "";
    const code = data.code || "";

    // Simple admin token check (set via PB admin settings or env)
    const validToken = $os.getenv("ADMIN_API_TOKEN") || "change-me-in-production";
    if (adminToken !== validToken) {
        return c.json(403, { code: 403, message: "Invalid admin token" });
    }

    // Parameterized query to prevent filter injection
    const safeCode = sanitizeFilter(code);
    const records = $app.dao().findRecordsByFilter(
        "codes",
        "code={:code}",
        "", 0, 1,
        { code: safeCode }
    );
    if (records.length === 0) {
        return c.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];
    const oldFingerprint = record.getString("fingerprint") || "(none)";
    record.set("fingerprint", "");   // Clear binding
    record.set("notes", (record.getString("notes") || "") +
        ` [unbound by admin at ${new Date().toISOString()}; was: ${oldFingerprint}]`);
    $app.dao().saveRecord(record);

    // Audit log to admin activity collection
    try {
        const audit = $app.dao().createRecord("admin_activity");
        audit.set("action", "unbind_code");
        audit.set("code", safeCode);
        audit.set("old_fingerprint", oldFingerprint);
        audit.set("admin_ip", $apis.requestInfo(c).remoteAddress);
        $app.dao().saveRecord(audit);
    } catch(e) {
        // Admin activity collection may not exist yet — skip audit
    }

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
set -e
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
DB_PATH="/opt/pocketbase/pb_data/data.db"
TMP_FILE="/tmp/pb-backup-$TIMESTAMP.db"
DUMP_FILE="/tmp/pb-dump-$TIMESTAMP.sql.gz"
LOG_FILE="/var/log/pocketbase-backup.log"

# Step 1: Consistent snapshot via sqlite3 .backup (safe even during writes)
sqlite3 "$DB_PATH" ".backup $TMP_FILE"

# Step 2: Verify the backup is valid before uploading
sqlite3 "$TMP_FILE" "SELECT COUNT(*) FROM codes;" > /dev/null 2>&1 || {
    echo "[$TIMESTAMP] ERROR: backup integrity check failed — not uploading" >> "$LOG_FILE"
    rm -f "$TMP_FILE"
    exit 1
}

# Step 3: Upload to B2
if b2 upload-file my-vpn-backup-bucket "$TMP_FILE" "backups/data-$TIMESTAMP.db" > /dev/null 2>&1; then
    echo "[$TIMESTAMP] backup uploaded successfully" >> "$LOG_FILE"
else
    echo "[$TIMESTAMP] ERROR: B2 upload failed" >> "$LOG_FILE"
    rm -f "$TMP_FILE"
    exit 1
fi
rm -f "$TMP_FILE"

# Step 4: Also upload a SQL dump for extra safety
sqlite3 "$DB_PATH" .dump | gzip > "$DUMP_FILE"
b2 upload-file my-vpn-backup-bucket "$DUMP_FILE" "dumps/dump-$TIMESTAMP.sql.gz" > /dev/null 2>&1 || true
rm -f "$DUMP_FILE"

# Step 5: Prune old backups (keep last 7 days)
b2 ls my-vpn-backup-bucket --prefix backups/ --json 2>/dev/null | \
  python3 -c "
import sys, json
from datetime import datetime, timezone
now = datetime.now(timezone.utc)
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try:
        obj = json.loads(line)
        age = now - datetime.fromisoformat(obj['uploadTimestamp'].replace('Z', '+00:00'))
        if age.days > 7 and obj['fileName'].startswith('backups/'):
            print(obj['fileName'])
    except: pass
" | while read fname; do b2 delete-file-version my-vpn-backup-bucket "$fname" >/dev/null 2>&1; done

echo "[$TIMESTAMP] backup completed" >> "$LOG_FILE"
EOF

chmod +x /opt/pocketbase/backup.sh
echo "0 * * * * root /opt/pocketbase/backup.sh" > /etc/cron.d/pocketbase-backup
```

### Step 2.12 — Privileged Helper Setup

The helper is installed once (requires admin) and persists across reboots.
It creates the TUN device on behalf of the user-tier app.

**Helper binary:** Built from `internal/helper/` using Go, cross-compiled alongside
the main app. The helper shares a `go.mod` with the main app.

```go
// internal/helper/main.go — standalone binary, built separately
package main

import (
    "flag"
    "fmt"
    "net"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "syscall"
)

var installFlag = flag.Bool("install", false, "Install the helper service")

func main() {
    flag.Parse()

    if *installFlag {
        installService()
        return
    }

    // Unix socket listener
    socketPath := "/var/run/myvpn-helper.sock"
    os.Remove(socketPath)
    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
        os.Exit(1)
    }
    os.Chmod(socketPath, 0666)
    defer listener.Close()

    for {
        conn, err := listener.Accept()
        if err != nil {
            continue
        }
        go handleConnection(conn)
    }
}

func handleConnection(conn net.Conn) {
    defer conn.Close()
    // 1. Read config JSON from conn
    // 2. Write to temp file
    // 3. Kill any existing sing-box process
    // 4. Spawn sing-box -c <config> run
    // 5. Monitor, report status back over conn
    // 6. On conn close, kill sing-box
}
```

**Install commands, per platform:**

```bash
# macOS installer script
sudo cp myvpn-helper-darwin /Library/PrivilegedHelperTools/com.myvpn.helper
sudo chmod 755 /Library/PrivilegedHelperTools/com.myvpn.helper
sudo cat > /Library/LaunchDaemons/com.myvpn.helper.plist <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>com.myvpn.helper</string>
    <key>ProgramArguments</key>
    <array><string>/Library/PrivilegedHelperTools/com.myvpn.helper</string></array>
    <key>KeepAlive</key><true/>
    <key>RunAtLoad</key><true/>
    <key>SocketListeners</key>
    <dict>
        <key>SockPathName</key><string>/var/run/myvpn-helper.sock</string>
    </dict>
</dict>
</plist>
PLIST
sudo launchctl load /Library/LaunchDaemons/com.myvpn.helper.plist

# Windows installer script (run as admin)
copy myvpn-helper.exe "%ProgramFiles%\MyVPN\myvpn-helper.exe"
sc create MyVPNHelper binPath="%ProgramFiles%\MyVPN\myvpn-helper.exe" start=auto
sc start MyVPNHelper

# Linux installer script (run as root)
cp myvpn-helper-linux /opt/myvpn-helper/myvpn-helper
chmod 755 /opt/myvpn-helper/myvpn-helper
cat > /etc/systemd/system/myvpn-helper.service <<UNIT
[Unit]
Description=MyVPN TUN Helper
After=network.target

[Service]
Type=simple
ExecStart=/opt/myvpn-helper/myvpn-helper
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now myvpn-helper
```

The Go app connects to the helper via the IPC path for its platform:
- macOS: `/var/run/myvpn-helper.sock`
- Windows: `\\.\pipe\MyVPNHelper`
- Linux: `/run/myvpn-helper.sock`

If the helper isn't running, the app alerts the user: "Helper service not found. Reinstall the app or run installer again."

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

Generates a deterministic device identifier from hardware properties.

**Primary fingerprint** (used when all sources available):
```
fingerprint = SHA256(MAC_address + disk_serial + motherboard_UUID)
```

**Fallback chain** (for VMs, USB adapters, and platforms where some IDs are unavailable):
1. Try MAC + disk serial + motherboard UUID (strongest)
2. If any source is empty/missing, try MAC + motherboard UUID (medium)
3. If motherboard UUID also missing, try MAC + hostname + machine_id (weak but stable)
4. If no sources work, generate a random UUID and store it in the app data dir (persistent per install)

The fingerprint never changes on the same hardware. If it degrades to a lower tier due to
missing data, it won't fluctuate — the same sources are consistently available or not.

**Platform-specific sources:**

| Source | Windows | macOS | Linux |
|--------|---------|-------|-------|
| MAC address | `GetAdaptersAddresses` (first physical) | `en0/en1` interface | First non-loopback physical interface |
| Disk serial | WMI `Win32_DiskDrive.SerialNumber` | `IORegistryEntryCreateCFProperty` (`Serial`) | `udevadm` or `/sys/block/*/device/serial` |
| Motherboard UUID | WMI `Win32_ComputerSystemProduct.UUID` | `IOPlatformExpertDevice` (`IOPlatformUUID`) | `/sys/class/dmi/id/product_uuid` or `/etc/machine-id` |
| Fallback | `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` | hostname + `ioreg -rd1 -c IOPlatformExpertDevice` | `/etc/machine-id` + hostname |

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

**Example generated sing-box config for Eco/Stealth (TCP only)** — with IPv6 leak prevention:

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
      "strict_route": true,
      "inet4_address": "10.0.0.1/24",
      "inet6_address": "",
      "sniff": true
    }
  ],
  "outbounds": [
    {
      "type": "shadowsocks",
      "tag": "ss-out",
      "server": "jeanette-qh9zbe.cloudserver.nz",
      "server_port": 8443,
      "method": "aes-256-gcm",
      "password": "eco-password"
    },
    {
      "type": "block",
      "tag": "block-ipv6"
    }
  ],
  "route": {
    "rules": [
      {
        "protocol": "ipv6",
        "outbound": "block-ipv6"
      }
    ],
    "final": "ss-out",
    "auto_detect_interface": true
  },
  "dns": {
    "final": "fake-ip",
    "rules": [
      { "outbound": "any", "server": "local" },
      { "query_type": "AAAA", "server": "local" }
    ],
    "servers": [
      { "tag": "local", "address": "local", "detour": "direct" },
      { "tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": "ss-out" }
    ],
    "strategy": "prefer_ipv4"
  }
}
```

For Strike (UDP enabled), add `"udp_over_tcp": false` or set `"network": "udp"` on the outbound and ensure the TUN inbound handles UDP.

Since the app generates this config dynamically from the PocketBase tier_config data, no per-tier static configs are needed.

### Step 3.5 — Heartbeat (`internal/heartbeat/heartbeat.go`)

```go
// Heartbeat loop with exponential backoff on failure.
// Base interval: 5 minutes. On failure, doubles up to max 2 hours.
// On success, resets to base interval.
//
//   GET {hub}/api/heartbeat?code=CODE&fp=FINGERPRINT_HASH
//     (fp = first 16 chars of SHA256 fingerprint, URL-encoded.
//      Used for staged rollout gating server-side.)
//
//   On success (200):
//     - Reset backoff to base interval
//     - Reset grace counter to 0
//     - Call updater.trackHeartbeatSuccess() for crash detection
//     - Check for update_available field → call updater.CheckAndApply()
//     - Apply any config changes returned
//
//   On failure (network error or non-200/403):
//     - Increment grace counter
//     - Double backoff interval (cap at 2 hours)
//     - If grace < 7 days: continue normally (connection keeps working)
//     - If grace >= 7 days: show "Re-activation required" in GUI
//
//   On 403 response:
//     - Immediately disconnect VPN
//     - Show "Account suspended — contact your middleman"
//     - Block reconnection
//
//   Backoff schedule:
//     Failure 1:  5 min
//     Failure 2: 10 min
//     Failure 3: 20 min
//     Failure 4: 40 min
//     Failure 5: 80 min
//     Failure 6+: 120 min (cap)
//     Success:    reset to 5 min
```

### Step 3.6 — Tunnel (`internal/tunnel/`)

Fallback package for platforms where sing-box TUN doesn't work. Same interface as V3. In practice, sing-box handles all platforms.

### Step 3.7 — Updater (`internal/updater/updater.go`)

**Two-phase sentinel auto-rollback system** with crash detection via heartbeat elapsed time.

```go
package updater

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "hash/fnv"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "runtime"
    "time"
)

// Sentinel / state file constants
const (
    sentinelPending    = ".update-pending"    // Exists while new binary is being tested
    sentinelConfirmed  = ".update-confirmed"  // Written after first successful startup
    stampUpdate        = ".update-timestamp"  // Unix timestamp when update was applied
    stampLastHeartbeat = ".last-heartbeat"    // Unix timestamp of last successful heartbeat
    flagCrashed        = ".crashed-on-update" // Set when crash is detected post-update
)

// UpdateInfo mirrors the server's update.json
type UpdateInfo struct {
    Version        string `json:"version"`
    RolloutPercent int    `json:"rollout_percent"`
    Windows        *Asset `json:"windows,omitempty"`
    MacOSIntel     *Asset `json:"macos_intel,omitempty"`
    MacOSARM       *Asset `json:"macos_arm,omitempty"`
}

type Asset struct {
    URL    string `json:"url"`
    SHA256 string `json:"sha256"`
}

// ─────────────────────────────────────────────────────────────
// Phase 1: Startup — Check sentinel state and detect crashes
// ─────────────────────────────────────────────────────────────

// CheckOnStartup() MUST be called at the very top of main().
// Returns (rolledBack, error).
//
// Sentinel state machine:
//
//   State A: --revert flag OR Ctrl held during launch
//     → Restore .prev as current, rename current to .bad
//     → Clean all sentinel files
//     → Return rolledBack=true
//
//   State B: .crashed-on-update EXISTS
//     → Previous update crashed at runtime (not just during startup)
//     → Auto-revert: restore .prev, clean sentinels
//     → Write rollback reason to log
//     → Return rolledBack=true
//
//   State C: .update-pending EXISTS + .prev EXISTS
//     → First launch after update (or crash during startup window)
//     → Rename .pending → .confirmed
//     → Write .update-timestamp
//     → If the process exits within 5 minutes, crashed-on-update is set
//       on next startup (see State B)
//     → Return rolledBack=false
//
//   State D: .update-confirmed EXISTS + .prev EXISTS
//     → Second clean startup → update is stable
//     → Delete .prev and all sentinels (update is permanent)
//     → Return rolledBack=false
//
//   State E: No sentinels
//     → Normal startup, clean any stale .prev
//     → Return rolledBack=false
//
func CheckOnStartup(revertRequested bool) (rolledBack bool, err error) {
    // ... (implementation follows the state machine above)
    return false, nil
}

// trackHeartbeatSuccess() called by the heartbeat goroutine after
// every successful server response. Writes the current Unix timestamp
// to .last-heartbeat. This is used to detect crashes at runtime:
// if the app was updated recently but no heartbeat has been recorded
// for more than the check interval, it likely crashed.
func trackHeartbeatSuccess(appDir string) {
    stamp := fmt.Sprintf("%d", time.Now().Unix())
    os.WriteFile(filepath.Join(appDir, stampLastHeartbeat), []byte(stamp), 0644)
}

// ─────────────────────────────────────────────────────────────
// Phase 2: Check for and apply updates
// ─────────────────────────────────────────────────────────────

// CheckAndApply() is called by the heartbeat goroutine when
// the server reports update_available. Returns true if an update
// was applied (process will exit).
//
// Staged rollout logic:
//   The server already gates by rollout_percent in the heartbeat
//   response (see heartbeat.pb.js). But we also verify client-side:
//   the client hashes its own fingerprint and checks the percent,
//   so even if the server signals everyone, the client self-selects.
//
//   This double-gating means:
//     - Server controls who gets the signal
//     - Client independently verifies eligibility
//     - Deterministic: same device always in same bucket
//
func CheckAndApply(appDir, hubURL, currentVersion, fingerprint string) (updated bool, err error) {
    // 1. Fetch update.json
    resp, err := http.Get(hubURL + "/update.json")
    if err != nil {
        return false, fmt.Errorf("fetch update info: %w", err)
    }
    defer resp.Body.Close()

    var info UpdateInfo
    if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
        return false, fmt.Errorf("parse update info: %w", err)
    }

    // 2. Compare version
    if info.Version == "" || info.Version <= currentVersion {
        return false, nil // no newer version
    }

    // 3. Client-side rollout gating
    if info.RolloutPercent < 100 && fingerprint != "" {
        h := fnv.New32a()
        h.Write([]byte(fingerprint))
        bucket := int(h.Sum32() % 100)
        if bucket >= info.RolloutPercent {
            return false, nil // not in rollout bucket
        }
    }

    // 4. Select platform asset
    var asset *Asset
    switch runtime.GOOS + "/" + runtime.GOARCH {
    case "windows/amd64":
        asset = info.Windows
    case "darwin/amd64":
        asset = info.MacOSIntel
    case "darwin/arm64":
        asset = info.MacOSARM
    }
    if asset == nil || asset.URL == "" {
        return false, fmt.Errorf("no asset for platform %s/%s", runtime.GOOS, runtime.GOARCH)
    }

    // 5. Proceed to download and apply
    return downloadAndApply(appDir, asset)
}

// downloadAndApply() downloads, verifies, swaps, and restarts.
func downloadAndApply(appDir string, asset *Asset) (bool, error) {
    // 1. Download zip to temp file
    tmpZip := filepath.Join(appDir, ".download.tmp")
    // ... download from asset.URL ...

    // 2. SHA256 verify
    // ... compute hash, compare to asset.SHA256 ...

    // 3. Extract binary from zip
    // 4. Verify new binary runs: exec newBinary --version
    // 5. Save current binary as .prev
    // 6. Write .update-pending sentinel
    // 7. Replace current binary with new
    // 8. Start new binary (fork)
    // 9. Exit current process

    return true, nil
}
```

### Step 3.8 — GUI (`internal/gui/app.go`)

Same systray design as V3 with additions:
- Grace period indicator (yellow dot + "Server unreachable — X days remaining")
- Update notification ("Update available — restart to apply")
- Diagnostics: "Help → Export Diagnostics" menu item

### Step 3.9 — Diagnostics (`internal/gui/diagnostics.go`)

A "Help → Export Diagnostics" menu item in the GUI writes a plain-text report
to the user's desktop. This lets support diagnose issues without remote access.

```go
package gui

// ExportDiagnostics() collects non-sensitive state into a text file:
//
//   MyVPN Diagnostics
//   =================
//   App version:    1.0.0
//   OS:             macOS 14.5 (arm64)
//   Tier:           strike
//   Connection:     CONNECTED
//
//   Heartbeat (last 20):
//     200, 200, 200, 200, 200, 200, 200, 200, 200, 200
//     200, 200, 200, 200, 200, 200, 200, 200, 200, 200
//
//   Last 5 errors:
//     (none)
//
//   Sentinel files:
//     .update-pending:  false
//     .update-confirmed: false
//     .prev exists:      true
//
//   Engine:
//     sing-box running: true
//
// Returns the file path or an error.
```

Called from the Fyne menu:

```go
// In the main menu setup:
diagnosticsItem := fyne.NewMenuItem("Export Diagnostics", func() {
    path, err := diagnostics.ExportDiagnostics()
    if err != nil {
        // Show error dialog
        return
    }
    // Show success dialog with path
    dialog.ShowInformation("Diagnostics Exported",
        "Saved to:\n"+path, window)
})
```

The customer sends this file to their middleman who forwards it to you.

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
    adminHubURL = flag.String("hub", "https://jeanette-qh9zbe.cloudserver.nz", "Admin hub URL")
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

### Option A: Local Cross-Compilation (Quick, Requires Toolchains)

```makefile
APP_NAME := myvpn
VERSION := 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)

# Note: Fyne requires CGO. Cross-compilation for Windows from Linux needs MinGW.
# For macOS targets, Darwin cross-compilation requires osxcross or Xcode.
# If local cross-compilation is too painful, use Option B (GitHub Actions).

build-windows:
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		go build -ldflags="$(LDFLAGS) -H windowsgui" -o $(APP_NAME).exe ./cmd/$(APP_NAME)

build-macos-intel:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
		go build -ldflags="$(LDFLAGS)" -o $(APP_NAME)-intel ./cmd/$(APP_NAME)

build-macos-arm:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
		go build -ldflags="$(LDFLAGS)" -o $(APP_NAME)-arm ./cmd/$(APP_NAME)

build-helper:
	go build -o myvpn-helper ./internal/helper/

build-helper-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o myvpn-helper.exe ./internal/helper/

build-helper-macos-intel:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o myvpn-helper-darwin-amd64 ./internal/helper/

build-helper-macos-arm:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o myvpn-helper-darwin-arm64 ./internal/helper/

bundle-windows: build-windows build-helper-windows
	zip -j dist/myvpn-windows.zip myvpn.exe engines/sing-box-windows-amd64.exe myvpn-helper.exe

bundle-macos-intel: build-macos-intel build-helper-macos-intel
	zip -j dist/myvpn-macos-intel.zip myvpn-intel engines/sing-box-darwin-amd64 myvpn-helper-darwin-amd64

bundle-macos-arm: build-macos-arm build-helper-macos-arm
	zip -j dist/myvpn-macos-arm.zip myvpn-arm engines/sing-box-darwin-arm64 myvpn-helper-darwin-arm64

bundle-all: bundle-windows bundle-macos-intel bundle-macos-arm
```

### Option B: GitHub Actions (Recommended for Production)

GitHub Actions provides native macOS and Windows runners, eliminating cross-compilation pain. Fyne's CGO dependencies are natively available on each OS.

Create `.github/workflows/build.yml`:

```yaml
name: Build MyVPN
on:
  push:
    tags:
      - 'v*'

jobs:
  build:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Install Fyne deps (Linux)
        if: runner.os == 'Linux'
        run: sudo apt-get install -y libgl1-mesa-dev xorg-dev

      - name: Build app
        run: |
          go build -ldflags="-s -w -X main.version=${{ github.ref_name }}" \
            -o myvpn ./cmd/myvpn/

      - name: Build helper
        run: go build -o myvpn-helper ./internal/helper/

      - name: Download sing-box
        run: |
          # Determine platform-specific sing-box binary
          # … download from GitHub releases …

      - name: Bundle
        run: |
          mkdir -p dist
          zip -j dist/myvpn-${{ runner.os }}.zip myvpn* sing-box*

      - uses: actions/upload-artifact@v4
        with:
          name: myvpn-${{ runner.os }}
          path: dist/*.zip
```

This approach:
- Compiles on native hardware (no cross-compilation issues)
- macOS runner builds Intel and ARM binaries natively
- Windows runner handles Fyne's CGO deps without MinGW
- Produces consistent, reproducible builds
- Triggers on version tags for release management

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
| `internal/helper/main.go` | 120 | **New** — privileged TUN helper service |
| `internal/helper/install.go` | 80 | **New** — platform-specific install/uninstall logic |
| `internal/heartbeat/heartbeat.go` | 110 | **New** — 5-min heartbeat loop with exponential backoff, grace counter, crash telemetry (`fp` param + `.last-heartbeat` tracking) |
| `internal/updater/updater.go` | 130 | **Rewritten** — download, verify, swap, fork, staged rollout, client-side rollout gating (FNV hash of fingerprint) |
| `internal/updater/recover.go` | 130 | **New** — sentinel handshake, auto-revert, manual revert, crash detection via heartbeat timestamp comparison, `.crashed-on-update` flag, `.update-timestamp` tracking |
| `internal/updater/update_windows.go` | 60 | **New** — Windows-specific swap + launcher |
| `internal/updater/update_unix.go` | 70 | **New** — Unix-specific swap + fork |
| `internal/tunnel/tunnel.go` | 10 | Same (fallback) |
| `internal/tunnel/tun_linux.go` | 30 | Same (fallback) |
| `internal/tunnel/tun_windows.go` | 40 | Same (fallback) |
| `internal/tunnel/tun_darwin.go` | 40 | Same (fallback) |
| `internal/tunnel/killswitch.go` | 25 | Same |
| `internal/tunnel/dns.go` | 20 | Same |
| `internal/gui/app.go` | 360 | Added grace indicator, update notification, "Export Diagnostics" menu item |
| `internal/gui/diagnostics.go` | 60 | **New** — diagnostic report generation |
| `server/Caddyfile` | 30 | Added rate_limit zones (generous CGNAT-aware limits) |
| `server/pb_hooks/activation.pb.js` | 110 | **Rewritten** — Luhn check, parameterized queries (no injection), fingerprint-keyed rate limiting (CGNAT-safe), table cleanup |
| `server/pb_hooks/heartbeat.pb.js` | 80 | **New** — suspension check, staged rollout gating (fingerprint hash mod), config refresh, parameterized queries |
| `server/pb_hooks/admin_unbind.pb.js` | 50 | **New** — admin unbind with parameterized queries, audit logging |
| `server/pb_hooks/heartbeat.pb.js` | 60 | **New** — suspension check + update notification + config refresh |
| `server/pb_hooks/admin_unbind.pb.js` | 35 | **New** — code recovery endpoint |
| `scripts/setup_vps.sh` | 280 | Three instances + BBR + Brutal + tc + Caddy + PB + backups |
| `scripts/generate_codes.sh` | 60 | **Updated** — Luhn checksum generation |
| `scripts/print_codes.sh` | 60 | Same |
| **Total** | **~2,260** | |
