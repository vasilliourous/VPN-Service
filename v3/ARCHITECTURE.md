# V3 Architecture — Shadowsocks + tun2socks

## System Overview

```
┌─────────────────────────────────────────────┐
│              STUDENT'S LAPTOP                 │
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │         MyVPN Desktop App (Go+Fyne)    │   │
│  │                                         │   │
│  │  Activation → Get Config → Connect      │   │
│  │                     ↓                   │   │
│  │  ┌─────────────┐  ┌──────────────┐     │   │
│  │  │  sslocal    │  │  tun2socks   │     │   │
│  │  │ (SOCKS5     │  │ (TUN →       │     │   │
│  │  │  proxy)     │  │  SOCKS5)     │     │   │
│  │  │ :1080       │  │  tun0        │     │   │
│  │  └──────┬──────┘  └──────┬───────┘     │   │
│  │         │                │              │   │
│  │         └── SOCKS5 ──────┘              │   │
│  │             127.0.0.1:1080               │   │
│  └───────────────────────────────────────┘   │
│                     │                         │
│        ┌────────────┴─────────────┐           │
│        │  :8443 │  :8444 │  :8445  │          │
│        │  Eco   │ Stealth│ Strike  │          │
│        │  CUBIC │ Brutal │ BBR     │          │
│        │  5 Mbps│ 48 Mbps│ Ulimited│          │
│        │  TCP   │ TCP    │ TCP+UDP │          │
│        └────────┴────────┴─────────┘          │
│              TCP encrypted streams             │
└─────────────────────┬─────────────────────────┘
                      │
                      ▼
              ┌───────────────┐
              │   INTERNET    │
              │  (school WiFi) │
              │  N4L Palo Alto │
              └───────┬───────┘
                      │
                      ▼
┌─────────────────────────────────────────────┐
│              YOUR VPS (Ubuntu 22.04)         │
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │   Caddy (reverse proxy, TLS, rate      │   │
│  │   limit, auto LE certificates)         │   │
│  └──────────────┬─────────────────────────┘  │
│                 │                              │
│  ┌──────────────┴─────────────────────────┐  │
│  │  PocketBase (Go-based backend/SQLite)   │  │
│  │  Collections: codes, tier_configs       │  │
│  │  JS Hooks: activation, suspension       │  │
│  └─────────────────────────────────────────┘  │
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │  ssserver (Eco)     :8443  CUBIC+TC   │   │
│  │  ssserver (Stealth) :8444  Brutal     │   │
│  │  ssserver (Strike)  :8445  BBR+UDP    │   │
│  └───────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

## Tier Architecture

Each tier gets its own shadowsocks-rust server instance on a dedicated port.
This allows per-tier congestion control, bandwidth limiting, and authentication.

| Tier | Port | Server CC | Client CC | BW Limit | Mode | UDP |
|------|:----:|:---------:|:---------:|:--------:|:----:|:---:|
| **Eco** | 8443 | CUBIC (forced) | CUBIC (client default) | 5 Mbps via tc | tcp_only | ❌ |
| **Stealth** | 8444 | Brutal (forced) | CUBIC (client default) | 48 Mbps via Brutal target | tcp_only | ❌ |
| **Strike** | 8445 | BBR (default) | CUBIC (client default) | Unlimited | tcp_and_udp | ✅ |

### How CC Differentiation Works

Congestion control affects the **server → client** direction (download), which dominates VPN use.

- **System default:** BBR (`tcp_bbr` module loaded, `net.ipv4.tcp_congestion_control = bbr`)
- **Eco (8443):** ssserver launched with `LD_PRELOAD=cubic-wrap.so` — forces CUBIC on every accepted connection
- **Stealth (8444):** ssserver launched with `LD_PRELOAD=brutal-wrap.so` — forces Brutal on every accepted connection
- **Strike (8445):** no wrapper — inherits system default BBR

The **client → server** direction always uses whatever CC the client OS defaults to (typically CUBIC on Windows/macOS/Linux). This is documented but not a practical issue since download dominates.

### How Bandwidth Caps Work

**Eco (5 Mbps):** Enforced server-side via `tc` (traffic control). All traffic on port 8443 is shaped to a 5 Mbps HTB class. All Eco users share this cap — natural contention that encourages upgrading.

**Stealth (48 Mbps):** Enforced by setting Brutal's target rate to 48 Mbps via sysctl. Brutal maintains this rate aggressively, filling the pipe up to the target.

**Strike:** No traffic shaping on port 8445. Unlimited throughput, subject to the client's WiFi speed and BBR's self-tuning.

### How UDP Relay Works (Strike Only)

Strike's ssserver runs in `tcp_and_udp` mode. The client's sslocal also runs in `tcp_and_udp` mode, enabling SOCKS5 UDP ASSOCIATE. When the Go app launches tun2socks with `--socks5-udp`, the TUN device forwards UDP packets through the SOCKS5 tunnel:

```
Game → UDP packet → TUN tun0 → tun2socks → SOCKS5 (127.0.0.1:1080)
  → sslocal → Shadowsocks encrypted UDP → ssserver → Internet
```

Eco and Stealth use `tcp_only` mode in their client configs. Even though their servers could technically handle UDP, the client never requests it. Since the client is closed-source, there's no way for the user to enable it.

## Port Map

| Service | Port | Purpose |
|---------|:----:|---------|
| Eco ssserver | 8443 | Shadowsocks, CUBIC + tc 5 Mbps |
| Stealth ssserver | 8444 | Shadowsocks, Brutal at 48 Mbps target |
| Strike ssserver | 8445 | Shadowsocks, BBR, unlimited, UDP |
| Caddy | 443 | TLS termination for PocketBase + update server |
| Caddy | 80 | ACME HTTP-01 challenge only |
| PocketBase | 8090 (localhost) | Admin API + DB |
| SSH | 22 | Admin access |

## Server Configs

### Eco Instance (`/etc/shadowsocks/eco.json`)

```json
{
  "server": "0.0.0.0",
  "server_port": 8443,
  "password": "eco-random-password",
  "method": "aes-256-gcm",
  "mode": "tcp_only",
  "fast_open": true,
  "no_delay": true
}
```

Launched with `LD_PRELOAD=/usr/local/lib/cubic-wrap.so`.

Traffic shaped: `tc qdisc on eth0 port 8443 → 5 mbit HTB class`.

### Stealth Instance (`/etc/shadowsocks/stealth.json`)

```json
{
  "server": "0.0.0.0",
  "server_port": 8444,
  "password": "stealth-random-password",
  "method": "aes-256-gcm",
  "mode": "tcp_only",
  "fast_open": true,
  "no_delay": true
}
```

Launched with `LD_PRELOAD=/usr/local/lib/brutal-wrap.so`.

Brutal target rate set via sysctl: `net.ipv4.tcp_brutal.target_rate = 48000000`.

### Strike Instance (`/etc/shadowsocks/strike.json`)

```json
{
  "server": "0.0.0.0",
  "server_port": 8445,
  "password": "strike-random-password",
  "method": "aes-256-gcm",
  "mode": "tcp_and_udp",
  "fast_open": true,
  "no_delay": true
}
```

No LD_PRELOAD needed — inherits system default BBR.

## LD_PRELOAD Wrappers

Two small shared libraries force a specific TCP congestion control on every connection accepted by ssserver.

### cubic-wrap.c

```c
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
```

### brutal-wrap.c

Same structure, replaces `"cubic"` with `"brutal"`. Already defined in IMPLEMENT.md.

Build:
```bash
gcc -shared -fPIC -o /usr/local/lib/cubic-wrap.so cubic-wrap.c -ldl
gcc -shared -fPIC -o /usr/local/lib/brutal-wrap.so brutal-wrap.c -ldl
```

## Auto-Updater

Unchanged from simplified plan. Checks `update.json` every 6 hours, downloads zip, verifies SHA256, replaces binary, restarts.

## Client-Side Config (Stored in PocketBase tier_configs)

Each tier's config is stored in PocketBase and returned to the app on activation.
The `speed` and `udp_relay` fields are NOT shadowsocks config — they're consumed by the Go app's manager package for client-side behavior.

```json
{
  "tier": "eco",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
    "server_port": 8443,
    "password": "eco-random-password",
    "method": "aes-256-gcm",
    "mode": "tcp_only",
    "fast_open": true,
    "no_delay": true
  },
  "speed_limit_bps": 5000000,
  "udp_relay": false
}
```

```json
{
  "tier": "stealth",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
    "server_port": 8444,
    "password": "stealth-random-password",
    "method": "aes-256-gcm",
    "mode": "tcp_only",
    "fast_open": true,
    "no_delay": true
  },
  "speed_limit_bps": 48000000,
  "udp_relay": false
}
```

```json
{
  "tier": "strike",
  "config": {
    "server": "jeanette-qh9zbe.cloudserver.nz",
    "server_port": 8445,
    "password": "strike-random-password",
    "method": "aes-256-gcm",
    "mode": "tcp_and_udp",
    "fast_open": true,
    "no_delay": true
  },
  "speed_limit_bps": 0,
  "udp_relay": true
}
```

The Go app passes the `config` object directly to sslocal, uses `speed_limit_bps` for optional client-side rate limiting, and uses `udp_relay` to decide whether to pass `--socks5-udp` to tun2socks.

## What Gets Cut vs Simplified Plan

| Feature | Status | Reason |
|---------|--------|--------|
| Hysteria 2 engine | ❌ Removed | UDP blocked by N4L |
| Bandwidth probe | ❌ Removed | TCP doesn't need it |
| Hysteria 2 Brutal CC | ❌ Removed | Replaced by TCP Brutal (server-side) |
| tun2socks | ✅ Added | Required for TUN → SOCKS5 |
| Shadowsocks config format | ✅ Changed | Different fields than Hysteria 2 |
| Three server instances | ✅ Added | One per tier for proper isolation |
| Everything else | ✅ Same | GUI, storage, activation, updater, business model |
