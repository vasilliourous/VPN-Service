# MyVPN

Cross-platform VPN desktop app for bypassing school/college WiFi restrictions.

## Architecture

```
┌──────────────────────────────────────────────────┐
│                MyVPN Desktop App                  │
│  Go + Fyne GUI + Hysteria 2 + usque/Warp         │
│  Windows & macOS                                  │
└──────────────────┬───────────────────────────────┘
                   │ HTTPS (TLS 1.3, cert-pinned)
                   ▼
┌──────────────────────────────────────────────────┐
│              Admin Hub (your VPS)                 │
│  PocketBase + Caddy + SQLite                      │
│  Rate limiting, device binding, heartbeat         │
└──────────────────────────────────────────────────┘
```

## Features

- **4 tiers:** Warp Lite, Stealth Browse, Gaming Mid, Gaming Max
- **Hardware fingerprint binding** — one code = one device permanently
- **Hysteria 2** primary engine (bundled in installer)
- **usque/Warp** fallback engine
- **Full TUN tunnel** with kill switch & DNS guard
- **Heartbeat telemetry** with 7-day grace period
- **SHA256-verified updates** with automatic rollback on crash
- **Minimal Fyne GUI** — no technical details exposed to users
- **Middleman distribution model** — physical codes for cash

## Build

### Prerequisites
- Go 1.22+
- Fyne requires CGO:
  - Windows: `mingw-w64`
  - macOS: Xcode Command Line Tools
  - Linux: `libgl1-mesa-dev xorg-dev`

### Commands
```bash
make build-windows      # Windows amd64
make build-macos-intel  # macOS Intel
make build-macos-arm    # macOS Apple Silicon
make build-native       # Build for current platform
make test               # Run tests
```

### GitHub Actions
Push to main/master triggers automatic builds for all platforms.
Create a tag (`v1.0.0`) to produce release artifacts.

## Server Deployment

```bash
# On a fresh Ubuntu 22.04 VPS:
bash scripts/setup_server.sh api.yourdomain.com
```

Then in PocketBase admin (`https://api.yourdomain.com/_/`):
1. Create `activation_codes` collection (fields: code, plan, bound_device_id, suspended, suspended_reason, created_by, expires, active)
2. Create `activation_attempts` collection (fields: ip, created)
3. Copy `server/pb_hooks/*.pb.js` to `/opt/pocketbase/pb_hooks/`
4. Set `TOKEN_SECRET` environment variable for PocketBase
5. Restart PocketBase

## Project Structure

```
myvpn/
├── main.go                    # Entry point
├── internal/
│   ├── storage/               # Persistent state (JSON file)
│   ├── activation/            # Device fingerprint + code validation
│   ├── branding/              # Fake protocol/plan names
│   ├── manager/               # Engine process lifecycle
│   ├── heartbeat/             # 60s heartbeat with grace period
│   ├── tunnel/                # TUN device + kill switch + DNS
│   ├── probe/                 # Bandwidth probe on connect
│   ├── monitor/               # Background EWMA bandwidth monitor
│   ├── health/                # 15s tunnel health checks
│   ├── helper/                # Privileged helper IPC
│   ├── updater/               # SHA256 updates + rollback
│   └── gui/                   # Fyne window
├── server/
│   ├── Caddyfile              # Reverse proxy config
│   └── pb_hooks/              # PocketBase JS hooks
├── scripts/
│   ├── setup_server.sh        # VPS deployment
│   └── publish_update.sh      # Update payload generation
└── .github/workflows/build.yml
```

## License

Unlicense — public domain.
