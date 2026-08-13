# MyVPN Engine Binaries

Place sing-box binaries here for local development builds.

Download the appropriate version from:
https://github.com/SagerNet/sing-box/releases

For production builds, the Makefile and CI/CD pipeline will download
the correct binary automatically.

Required files:
- `sing-box` (Linux) or `sing-box.exe` (Windows)

**Engine Version:** 1.12.1 (tested — the 1.10 TUN config had Windows routing issues; 1.12 with rule actions + refactored DNS is validated end-to-end)
**Protocol:** Shadowsocks AEAD-256-GCM over TCP
**TUN mode:** Required for system-wide VPN routing
