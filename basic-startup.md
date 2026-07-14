# Basic Startup — Beta-ASAP Plan (usque/Warp Only)

> For the networking/devops person who needs to ship.
> Skip the frills. Get revenue. Improve later.

---

## The Core Idea

You don't need to build a full VPN client. **usque** is already a working Cloudflare Warp client — it handles the tunnel. You just need:

1. A **backend** (VPS) that issues activation codes and tracks who's paid
2. A **thin client** that calls your backend, launches usque, and shows the user a status icon
3. A way to **update** that client later

That's it. No TUN driver, no Windows SYSTEM service, no kill switch, no multi-protocol engine manager, no Ed25519 update signing.

---

## Architecture (Simplified)

```
Student's PC                          Your VPS ($6/mo)
┌──────────────────┐                  ┌─────────────────────────┐
│  Your Client App │  ──HTTPS──►      │  Caddy (reverse proxy)  │
│  (systray icon)  │                  │  └─► PocketBase (API)   │
│       │          │                  │                         │
│       ▼          │                  │  Collections:           │
│  usque ──MASQUE──┼──► Cloudflare   │  - activations (codes)  │
│  (SOCKS5 proxy)  │     Network      │  - devices (binding)    │
└──────────────────┘                  │  - updates (file host)  │
                                      └─────────────────────────┘
```

- usque connects directly to Cloudflare — traffic **does not** go through your VPS
- Your VPS only handles activation, heartbeat, and file hosting
- No bandwidth costs, no scaling worries

---

## Phase 0: What You Need (Before Writing Code)

| Item | Cost | Notes |
|---|---|---|
| A domain | ~$10/yr | e.g. `vpn-yourbrand.com` |
| A $6 VPS | $6/mo | Hetzner CX11 or similar, Ubuntu 22.04 |
| Cloudflare account | Free | DNS + CDN for your domain |
| A Windows laptop/VM | Already have one | For testing the client |
| Go installed locally | Free | For building the client binary |
| usque binary | Free | Download from GitHub releases |

That's it. No code signing cert (skip for beta), no macOS dev environment (skip for beta), no Hysteria 2 binary.

---

## Phase 1: Backend — Your Territory (Weekend Project)

You know this stuff. Here's the checklist:

### 1. Set up the VPS

```
ssh root@your-vps
apt update && apt upgrade -y
ufw allow 22,80,443/tcp
ufw --force enable
```

### 2. Install Caddy + PocketBase

Follow the Caddy install + PocketBase install from `ACTION-PLAN.md` Phase 1.1 (steps 3-5). It's copy-paste work. Takes 30 minutes.

### 3. Create the activation schema in PocketBase

Create one collection called `activations`:

| Field | Type | Notes |
|---|---|---|
| `code` | text (unique) | e.g. `MYVPN-XXXX-XXXX-XXXX` |
| `plan` | text | hardcode to `"beta"` for now |
| `max_devices` | number | `3` |
| `used_devices` | number | starts at `0` |
| `active` | boolean | `true` |

Create a second collection called `devices`:

| Field | Type | Notes |
|---|---|---|
| `activation` | relation → activations | |
| `device_id` | text | HMAC of something unique |
| `last_seen` | datetime | |

### 4. Create one API endpoint

In PocketBase, create a custom endpoint (or use the SDK/API directly from the client):

```
POST /api/activate
  Body: { "code": "MYVPN-XXXX-XXXX-XXXX", "device_id": "..." }
  Response: { "token": "jwt...", "config": { ... usque config ... } }
```

**Logic:** Check code exists, check device limit not reached, return a session token + usque config.

### 5. Create a download endpoint

Put your client binary at a simple URL:

```
https://api.yourdomain.com/download/beta/latest.exe
```

That's the backend. **No heartbeat, no telemetry, no grace period, no token rotation.** Add those later.

---

## Phase 2: The Client — The Part You Need to Vibe-Code

This is the part you said you'd vibe-code. Let me be specific about what you need.

### The Minimum Viable Client

A single Go binary that does three things:

1. **Shows a window or systray icon** where the user enters their activation code
2. **Calls your backend** to activate, gets back usque config
3. **Launches usque** as a subprocess with that config

### What to ask an AI to build

Give this exact spec to Claude/GPT/Cursor:

> "Write a Go program for Windows that:
> - Shows a systray icon (use github.com/getlantern/systray)
> - On first launch, shows a dialog asking for an activation code
> - POSTs the code + a device ID to https://api.mydomain.com/api/activate
> - On success, saves the returned config locally and launches `usque.exe` as a subprocess with those config flags
> - Systray menu shows: Connected/Disconnected status, a Disconnect button, and a Quit button
> - On subsequent launches, skip the activation dialog and just connect
> - Single .exe output, no installer needed"

**That's the whole app.** No TUN, no kill switch, no multi-protocol, no update verification.

### How usque works (so you know what the client needs to run)

usque is a CLI binary. To start it in SOCKS5 proxy mode:

```
usque socks --listen :1080
```

Or in tunnel mode (Windows):

```
usque tunnel --listen :1080
```

Your client just needs to:
1. Download usque.exe (you bundle it or download at runtime)
2. Launch it as a subprocess with the right flags
3. Kill it when the user clicks Disconnect

### File layout on the user's PC

```
C:\Users\<user>\AppData\Local\MyVPN\
├── client.exe          (your binary)
├── usque.exe           (the usque binary, downloaded or bundled)
├── config.json         (saved activation + usque config)
└── logs\
    └── client.log
```

### Build command (for you to run)

```bash
# After the AI writes the code:
cd client
go mod init myvpn
go mod tidy
go build -ldflags="-s -w" -o myvpn.exe .
```

Upload `myvpn.exe` to `https://api.yourdomain.com/download/beta/latest.exe`.

---

## Phase 3: Updates — Without Locking Yourself Out

This is the part you specifically asked about. Here's the **simplest possible update system** that doesn't require crypto keys:

### How it works

```
1. You build a new myvpn.exe on your computer
2. You upload it to your VPS: /download/beta/latest.exe
3. The client checks https://api.yourdomain.com/version.txt on startup
4. If the version differs, it downloads the new .exe, replaces itself, and restarts
```

### No signing keys needed

For beta, skip Ed25519 signing entirely. You're serving the update over HTTPS from your own domain. The connection is already encrypted and authenticated by TLS. If someone compromises your VPS, signed updates wouldn't save you anyway (they'd just change the version.txt too).

**Add signing later** if you want, once you have paying users and care about supply-chain attacks.

### What to put in version.txt

```
1.0.2
```

Just a version string. The client compares it to its own hardcoded version.

### The update logic (about 20 lines of Go)

Give this to the AI alongside the client spec:

> "Add an update check: on startup, fetch https://api.mydomain.com/version.txt, compare to internal VERSION constant. If different, download https://api.mydomain.com/download/beta/latest.exe to a temp file, verify it's a valid PE (.exe) header, then replace the running binary and restart via exec."

**That's it.** No Ed25519, no key management, no lock-out risk. You just upload new files.

---

## Phase 4: Beta Launch Checklist

Before you hand out your first activation code:

- [ ] VPS: Caddy serving HTTPS, rate limiting enabled
- [ ] VPS: PocketBase running as a systemd service
- [ ] VPS: Uploaded usque.exe to /download/beta/usque.exe
- [ ] VPS: Uploaded myvpn.exe to /download/beta/latest.exe
- [ ] VPS: Created version.txt with the current version
- [ ] Client: Tested activation flow on a clean Windows machine
- [ ] Client: Tested that usque starts and provides internet access
- [ ] Client: Tested Disconnect/Quit
- [ ] Business: Generated 5-10 activation codes in PocketBase
- [ ] Business: Given codes to one middleman as a test

If all of that works, you can take money.

---

## What You're NOT Building (and Why That's Fine)

| Feature | Status | When to add |
|---|---|---|
| macOS support | ❌ Skip | When Windows beta is stable and you have macOS users asking for it |
| Hysteria 2 (gaming) | ❌ Skip | When Warp Lite users complain about gaming latency |
| Kill switch | ❌ Skip | When you have 50+ users and someone actually gets in trouble |
| Multi-tier pricing | ❌ Skip | One price, one product. Add tiers when you have too many users at one price. |
| Signed updates | ❌ Skip | When someone actually tries to MITM your update process (they won't) |
| Privileged helper service | ❌ Skip | Never needed if you stick with SOCKS5 mode |
| Telemetry dashboard | ❌ Skip | When you have hundreds of users and need data to make decisions |
| Split tunnel | ❌ Skip | When users ask for it (they probably won't for this use case) |

---

## Timeline

| Week | What | Who |
|---|---|---|
| **Week 1** | VPS setup, PocketBase, Caddy, activation schema, test activation call | **You** (this is your strong suit) |
| **Week 1-2** | Vibe-code the Windows client (systray + activation + usque launch) | AI-assisted |
| **Week 2** | Integration test — client talks to backend, usque connects | You + AI |
| **Week 2** | Give 2-3 test codes to trusted friends | You |
| **Week 3** | Fix bugs from real usage, launch to first middleman | You |
| **Post-launch** | Add features one at a time based on what users actually complain about | Ongoing |

---

## What "Make It Fully Fledged" Looks Like Later

After you have paying users, you add features in priority order:

1. **Better error handling** — users see "Disconnected" with no explanation? Fix that.
2. **Auto-reconnect** — usque crashes? Restart it.
3. **macOS version** — same codebase, different build target
4. **Hysteria 2** — add as a secondary engine for gamers
5. **Kill switch** — block non-VPN traffic if usque dies
6. **Signed updates** — add Ed25519 when you care about security hardening
7. **Admin dashboard** — web UI for viewing active users, generating codes

Each one is a separate project. You don't need any of them to start selling.

---

## The One Thing That Could Bite You

**usque stops working.** Cloudflare changes their API, or a school blocks Cloudflare IPs, or usque has a bug.

Mitigation: Test usque from the actual school network before you sell codes there. Have a backup plan (e.g. "if usque doesn't work, you get a refund"). Don't promise 99.9% uptime.

---

## Bottom Line

You can ship a beta with:
- **1 VPS** ($6/mo)
- **1 domain** ($10/yr)
- **1 Go binary** (~200 lines, AI-generated)
- **1 usque binary** (free, open source)

Backend in a weekend. Client in a week. Beta in week 3.

You will not lock yourself out of updates because there are no signing keys to lose — just upload new files to your VPS.

Ship it.
