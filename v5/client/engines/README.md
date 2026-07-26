# MyVPN Engine Binaries

Place sing-box binaries here for local development builds.

Download the appropriate version from:
https://github.com/SagerNet/sing-box/releases

For production builds, the Makefile and CI/CD pipeline will download
the correct binary automatically.

Required files:
- `sing-box` (Linux/macOS) or `sing-box.exe` (Windows)

**Engine Version:** 1.10.0 (tested)
**Protocol:** Shadowsocks AEAD-256-GCM over TCP
**TUN mode:** Required for system-wide VPN routing
