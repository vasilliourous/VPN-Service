#!/usr/bin/env bash
# Module 00: Environment validation
# Validates that all prerequisites are met before provisioning.
set -euo pipefail

log()  { echo "[00-env] $*"; }
warn() { echo "[00-env][WARN] $*"; }
fail() { echo "[00-env][FAIL] $*"; exit 1; }

log "Validating environment..."

# ── OS Check ──
if [ ! -f /etc/os-release ]; then
    fail "Cannot detect OS. Expected Ubuntu 22.04."
fi
. /etc/os-release
if [ "$ID" != "ubuntu" ] || [ "${VERSION_ID%%.*}" -lt 22 ]; then
    fail "Expected Ubuntu 22.04+, got ${ID} ${VERSION_ID}. Aborting."
fi
log "✓ OS: ${ID} ${VERSION_ID}"

# ── Architecture ──
ARCH=$(uname -m)
if [ "$ARCH" != "x86_64" ]; then
    fail "Expected x86_64, got ${ARCH}. shadowsocks-rust binaries are x86_64 only."
fi
log "✓ Architecture: ${ARCH}"

# ── Root check ──
if [ "$(id -u)" -ne 0 ]; then
    fail "Must be run as root."
fi
log "✓ Running as root"

# ── Required variables ──
if [ -z "${DOMAIN:-}" ]; then
    fail "DOMAIN is not set. Usage: DOMAIN=api.yourdomain.com bash $0"
fi
log "✓ Domain: ${DOMAIN}"

# ── Network: Check DNS resolves ──
if command -v host &>/dev/null; then
    if host "$DOMAIN" &>/dev/null; then
        DOMAIN_IP=$(host "$DOMAIN" | awk '/has address/ {print $4; exit}')
        log "✓ DNS resolves: ${DOMAIN} → ${DOMAIN_IP}"
    else
        warn "DNS does not resolve for ${DOMAIN}. Setup will work but clients can't connect until DNS is set."
    fi
elif command -v nslookup &>/dev/null; then
    if nslookup "$DOMAIN" &>/dev/null; then
        log "✓ DNS resolves for ${DOMAIN}"
    else
        warn "DNS does not resolve for ${DOMAIN}."
    fi
fi

# ── Disk space ──
AVAIL_KB=$(df / | awk 'NR==2 {print $4}')
if [ "${AVAIL_KB}" -lt 2097152 ]; then
    fail "Less than 2GB free disk space. Need at least 2GB for PocketBase + backups."
fi
log "✓ Disk space: $((AVAIL_KB / 1024)) MB available"

# ── Memory ──
TOTAL_MEM_KB=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
if [ "${TOTAL_MEM_KB}" -lt 1048576 ]; then
    fail "Less than 1GB RAM. Minimum 2GB recommended."
fi
log "✓ Memory: $((TOTAL_MEM_KB / 1024)) MB total"

# ── Existing installation check ──
EXISTING_INSTALL=0
if systemctl list-units --type=service --all 2>/dev/null | grep -q "shadowsocks-eco"; then
    warn "Existing Shadowsocks installation detected. Modules will handle idempotently."
    EXISTING_INSTALL=1
fi

log "Environment validation passed. Ready to provision."
exit 0
