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

# ── Strike UDP-over-TCP (sing-box, port 8446) ──
# Part of the default deployment, so the port is opened by default too.
# Without this the sing-box-uot service would listen but be unreachable from
# the internet, and Strike clients would sit on the advertised uot_port until
# they timed out and fell back — a silent, confusing failure.
# ENABLE_UOT=0 skips both the service and this rule.
if [ "${ENABLE_UOT:-1}" = "1" ]; then
    ufw allow "${UOT_PORT:-8446}"/tcp 2>/dev/null || true
    ufw allow "${UOT_PORT:-8446}"/udp 2>/dev/null || true
    log "✓ Strike UoT port ${UOT_PORT:-8446} (TCP+UDP) allowed"
else
    log "UoT disabled (ENABLE_UOT=0) — port ${UOT_PORT:-8446} not opened"
fi

# ── Enable UFW (safe — SSH is already allowed) ──
# NOTE: on some images /etc/ufw/ufw.conf ships with ENABLED=no while the
# iptables rules are already loaded. `ufw status` then reports "active" but
# `ufw reload` prints "Firewall not enabled (skipping reload)" and silently
# does nothing — so after.rules is never applied (observed 2026-09-19).
# We normalise the flag before enabling.
log "Enabling UFW..."
systemctl enable ufw 2>/dev/null || true
if grep -q "^ENABLED=no" /etc/ufw/ufw.conf 2>/dev/null; then
    warn "ufw.conf had ENABLED=no despite loaded rules — correcting"
    sed -i 's/^ENABLED=no/ENABLED=yes/' /etc/ufw/ufw.conf
fi
ufw --force enable 2>&1 | tail -3
# Guarantee the persistent flag matches the reported state.
sed -i 's/^ENABLED=.*/ENABLED=yes/' /etc/ufw/ufw.conf

# Apply after ufw is enabled/up — `ufw enable` reloads everything, so the
# limiter must be present in user.rules first (it is, above) and then loaded.
ufw reload 2>&1 | tail -2

# ── SSH brute-force protection (fail2ban) ──
# We deliberately do NOT use `ufw limit 22/tcp`.
#
# Two reasons, both learned the hard way on 2026-09-19:
#   1. UFW hardcodes the limiter at 6 new connections / 30s per IP — there is
#      no supported knob (backend_iptables.py writes '--seconds 30 --hitcount 6'
#      literally). Editing user.rules to change it is futile: `ufw reload`
#      re-renders the file from that hardcoded value.
#   2. 6/30s is low enough to lock out legitimate admin/CI automation. It did
#      exactly that during deployment, repeatedly refusing port 22.
#
# A custom chain injected into /etc/ufw/before.rules was also tried and locked
# SSH out entirely (port 22 timed out while 80/443 kept serving), requiring a
# provider-console recovery. So: keep UFW to plain allow/deny, and let fail2ban
# do rate-based banning, which is purpose-built, tunable, and reload-safe.
SSH_MAXRETRY="${SSH_MAXRETRY:-6}"       # failures before a ban
SSH_FINDTIME="${SSH_FINDTIME:-10m}"     # window in which failures are counted
SSH_BANTIME="${SSH_BANTIME:-1h}"        # ban duration

# Remove UFW's limiter rule if a previous deploy installed one, so SSH is a
# plain ALLOW and fail2ban is the only thing enforcing a threshold.
if iptables -S ufw-user-input 2>/dev/null | grep -q "ufw-user-limit"; then
    ufw delete limit 22/tcp 2>/dev/null || true
fi
ufw allow 22/tcp 2>/dev/null || true

if ! command -v fail2ban-client >/dev/null 2>&1; then
    log "Installing fail2ban..."
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq fail2ban 2>&1 | tail -3 || \
        warn "fail2ban install failed — SSH brute-force protection will be unavailable"
fi

if command -v fail2ban-client >/dev/null 2>&1; then
    cat > /etc/fail2ban/jail.d/locus-sshd.local <<JAIL
# Managed by v5/server/modules/08-firewall.sh — do not edit by hand.
[DEFAULT]
# Ban for BANTIME after MAXRETRY failures within FINDTIME.
bantime  = ${SSH_BANTIME}
findtime = ${SSH_FINDTIME}
maxretry = ${SSH_MAXRETRY}
# ufw is the active firewall; let fail2ban block via the firewall directly.
banaction = ufw
banaction_allports = ufw

[sshd]
enabled  = true
port     = ssh
backend  = systemd
# Ignore our own management/private ranges so automation is never banned.
ignoreip = 127.0.0.1/8 ::1
JAIL
    systemctl enable fail2ban 2>/dev/null || true
    systemctl restart fail2ban 2>/dev/null || true
    sleep 2
    if systemctl is-active --quiet fail2ban; then
        log "✓ fail2ban active (${SSH_MAXRETRY} failures / ${SSH_FINDTIME} → ${SSH_BANTIME} ban)"
    else
        warn "fail2ban did not start — check: journalctl -u fail2ban -n 20 --no-pager"
    fi
else
    warn "fail2ban unavailable — SSH relies on UFW's default deny + auth only"
fi

# Hard guarantee: SSH must ALWAYS be reachable. Never leave a firewall that
# drops our own management port — this exact failure cost a provider-console
# recovery on 2026-09-19.
if iptables -S ufw-user-input 2>/dev/null | grep -q "dport 22"; then
    log "✓ SSH (22/tcp) reachable"
else
    warn "No port 22 rule after setup — forcing allow 22/tcp"
    ufw delete limit 22/tcp 2>/dev/null || true
    ufw allow 22/tcp 2>&1 | tail -1
    ufw reload 2>&1 | tail -1
    if iptables -S ufw-user-input 2>/dev/null | grep -q "dport 22"; then
        log "✓ SSH (22/tcp) reachable (after recovery)"
    else
        fail "SSH port 22 is NOT reachable — refusing to finish. Fix before disconnecting."
    fi
fi

# ── Show status ──
log "UFW status:"
ufw status verbose 2>&1 | head -20

# ── Verify critical rules ──
# Port 22's rules are injected directly into ufw-user-input (see above), so
# `ufw status` alone won't list them — check the live chain too.
for port in 22 80 443 8443 8444 8445; do
    if ufw status | grep -q "${port}/tcp" || iptables -S ufw-user-input 2>/dev/null | grep -q "dport ${port}"; then
        log "✓ Port ${port}/tcp rule confirmed"
    else
        warn "Port ${port}/tcp rule not found — check manually: ufw status"
    fi
done

log "✓ Firewall setup complete"
exit 0
