# VPN Monetised Service — Configuration Templates (VPS-Only)

This directory contains sanitized config templates. Replace all `REPLACE_ME` placeholders before deploying.

All configs are for a single VPS running all protocols and NATing directly to the internet. No WireGuard tunnel, no enterprise server in the traffic path.

## Files

| File | Purpose |
|------|---------|
| `hysteria2-premium.yaml` | Hysteria2 config for Premium tier (gaming) — Brutal CC, Salamander obfs |
| `xray-vless-reality.json` | Xray VLESS-REALITY config for Premium fallback |
| `tuic-lite.json` | TUIC V5 config for Lite tier (web browsing only) |
| `amneziawg-standard.conf` | AmneziaWG config for Standard tier |
| `nginx-fallback.conf` | Nginx fallback for probe absorption (REALITY dest) |
| `nginx-api.conf` | Nginx reverse proxy for activation API |
| `marzban-docker-compose.yml` | Marzban panel Docker Compose |
| `marzban.env` | Marzban environment variables |
| `activation-api.env` | Environment file for activation API |
