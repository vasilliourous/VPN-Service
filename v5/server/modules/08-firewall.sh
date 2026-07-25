#!/usr/bin/env bash
# Module 08: Firewall (UFW)
# Opens required ports and sets default deny policy.
# Safe to re-run — UFW checks existing rules before adding.
set -euo pipefail

log()  { echo "[08-firewall] $*"; }
warn() { echo "[08-firewall][WARN] $*"; }
fail() { echo "[08-firewall][FAIL] $*"; exit 1; }

# ── Install UFW if missing ──
if ! command -v ufw &>/dev/null; then
    log "Installing UFW..."
    apt-get install -y -qq ufw
fi

# ── Ensure UFW is available ──
if ! command -v ufw &>/dev/null; then
    fail "UFW installation failed"
fi

log "Configuring firewall rules..."

# ── Default policies ──
ufw default deny incoming 2>/dev/null || true
ufw default allow outgoing 2>/dev/null || true

# ── SSH (rate-limited to prevent brute force) ──
ufw allow 22/tcp 2>/dev/null || true
log "✓ SSH (22/tcp) allowed"

# ── HTTP/HTTPS (Caddy / Let's Encrypt) ──
ufw allow 80/tcp 2>/dev/null || true
ufw allow 443/tcp 2>/dev/null || true
log "✓ HTTP (80/tcp) and HTTPS (443/tcp) allowed"

# ── Shadowsocks tiers ──
ufw allow 8443/tcp 2>/dev/null || true
ufw allow 8444/tcp 2>/dev/null || true
ufw allow 8445/tcp 2>/dev/null || true
log "✓ Shadowsocks ports 8443, 8444, 8445 (TCP) allowed"

# ── Allow UDP only on Strike port ──
ufw allow 8445/udp 2>/dev/null || true
log "✓ Strike UDP (8445/udp) allowed"

# ── Rate limiting on SSH ──
ufw limit 22/tcp 2>/dev/null || true
log "✓ SSH rate limiting enabled (6 connections per 30s per IP)"

# ── Enable UFW (safe — SSH is already allowed) ──
log "Enabling UFW..."
ufw --force enable 2>&1 | tail -3

# ── Show status ──
log "UFW status:"
ufw status verbose 2>&1 | head -20

# ── Verify critical rules ──
for port in 22 80 443 8443 8444 8445; do
    if ufw status | grep -q "${port}/tcp"; then
        log "✓ Port ${port}/tcp rule confirmed"
    else
        warn "Port ${port}/tcp rule not found — check manually: ufw status"
    fi
done

log "✓ Firewall setup complete"
exit 0
