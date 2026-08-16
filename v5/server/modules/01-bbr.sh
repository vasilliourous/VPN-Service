#!/usr/bin/env bash
# Module 01: BBR + Kernel Tuning
# Enables BBR as the system default congestion control algorithm.
set -euo pipefail

log()  { echo "[01-bbr] $*"; }
warn() { echo "[01-bbr][WARN] $*"; }
fail() { echo "[01-bbr][FAIL] $*"; exit 1; }

log "Configuring BBR congestion control..."

# ── Check if BBR module exists ──
if ! modinfo tcp_bbr &>/dev/null; then
    fail "tcp_bbr module not available in kernel. Expected Ubuntu 22.04+ with default kernel."
fi

# ── Load module now ──
modprobe tcp_bbr 2>/dev/null || warn "tcp_bbr already loaded or failed to load"
log "✓ tcp_bbr module loaded"

# ── Persist module load on boot ──
if [ ! -f /etc/modules-load.d/tcp_bbr.conf ]; then
    echo "tcp_bbr" > /etc/modules-load.d/tcp_bbr.conf
    log "Created /etc/modules-load.d/tcp_bbr.conf"
else
    log "tcp_bbr.conf already exists"
fi

# ── Apply sysctl settings now ──
sysctl -w net.core.default_qdisc=fq 2>/dev/null || warn "Could not set fq qdisc"
sysctl -w net.ipv4.tcp_congestion_control=bbr 2>/dev/null || warn "Could not set BBR immediately"

# ── Persist sysctl settings ──
if [ ! -f /etc/sysctl.d/90-bbr.conf ]; then
    cat > /etc/sysctl.d/90-bbr.conf << 'EOF'
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
    log "Created /etc/sysctl.d/90-bbr.conf"
    sysctl --system &>/dev/null
else
    log "90-bbr.conf already exists"
fi

# ── Verify ──
CURRENT_CC=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)
if [ "$CURRENT_CC" = "bbr" ]; then
    log "✓ BBR is active (current CC: ${CURRENT_CC})"
else
    warn "BBR should be active but current CC is: ${CURRENT_CC}"
fi

# ── Additional TCP optimizations ──
SYSCTL_TCP="/etc/sysctl.d/91-tcp-tune.conf"
if [ ! -f "$SYSCTL_TCP" ]; then
    cat > "$SYSCTL_TCP" << 'EOF'
# TCP buffer auto-tuning
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.ipv4.tcp_rmem = 4096 87380 134217728
net.ipv4.tcp_wmem = 4096 65536 134217728

# TCP fast open (client + server)
net.ipv4.tcp_fastopen = 3

# Reduce TIME_WAIT sockets
net.ipv4.tcp_fin_timeout = 15

# Increase backlog
net.core.netdev_max_backlog = 5000
net.core.somaxconn = 4096

# TCP keepalive
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_probes = 5
net.ipv4.tcp_keepalive_intvl = 15

# Gaming/latency (added 2026-08-14): keep cwnd after idle so games that
# alternate quiet/burst don't re-slow-start every round; MTU probing helps
# if the path fragments. See FIXES.md Strike gaming optimisation.
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_mtu_probing = 1
EOF
    sysctl --system &>/dev/null
    log "Created ${SYSCTL_TCP} with TCP optimizations"
else
    log "TCP tune file already exists"
fi

log "✓ BBR setup complete"
exit 0
