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
    fail "DOMAIN is not set. Usage: DOMAIN=networkingguides.duckdns.org bash $0"
fi
log "✓ Domain: ${DOMAIN}"

# ── Network: Check DNS resolves (HARD FAIL) ──
# Caddy needs DNS to provision Let's Encrypt TLS certificates.
# Without DNS working, the entire deployment is unusable.
DNS_RESOLVES=false
if command -v host &>/dev/null; then
    if host "$DOMAIN" &>/dev/null; then
        DOMAIN_IP=$(host "$DOMAIN" | awk '/has address/ {print $4; exit}')
        log "✓ DNS resolves: ${DOMAIN} → ${DOMAIN_IP}"
        DNS_RESOLVES=true
    fi
elif command -v nslookup &>/dev/null; then
    if nslookup "$DOMAIN" &>/dev/null 2>&1; then
        DOMAIN_IP=$(nslookup "$DOMAIN" 2>/dev/null | awk '/^Address: / {print $2; exit}')
        log "✓ DNS resolves: ${DOMAIN} → ${DOMAIN_IP:-unknown}"
        DNS_RESOLVES=true
    fi
elif command -v dig &>/dev/null; then
    if dig +short "$DOMAIN" &>/dev/null; then
        DOMAIN_IP=$(dig +short "$DOMAIN" | head -1)
        log "✓ DNS resolves: ${DOMAIN} → ${DOMAIN_IP}"
        DNS_RESOLVES=true
    fi
fi

if [ "$DNS_RESOLVES" = false ]; then
    cat >&2 << 'EOF'

╔══════════════════════════════════════════════════════════════╗
║  FATAL: Domain DNS does not resolve.                        ║
║                                                            ║
║  Caddy requires DNS to provision Let's Encrypt TLS          ║
║  certificates. Without TLS, the API won't work over HTTPS.  ║
║                                                            ║
║  Fix: Add an A record for your domain pointing to this      ║
║  VPS IP, wait for propagation, then re-run setup.           ║
║                                                            ║
║  To force setup without DNS (not recommended), set:        ║
║    SKIP_DNS_CHECK=1 DOMAIN=... ./setup.sh                   ║
╚══════════════════════════════════════════════════════════════╝
EOF
    if [ "${SKIP_DNS_CHECK:-0}" != "1" ]; then
        fail "DNS does not resolve for ${DOMAIN}. Aborting."
    else
        warn "SKIP_DNS_CHECK=1 — proceeding without DNS resolution."
    fi
fi

# ── Disk space ──
AVAIL_KB=$(df / | awk 'NR==2 {print $4}')
if [ "${AVAIL_KB}" -lt 2097152 ]; then
    fail "Less than 2GB free disk space. Need at least 2GB for PocketBase + backups."
fi
log "✓ Disk space: $((AVAIL_KB / 1024)) MB available"

# ── Memory ──
# ── Memory ──
# NOTE: a "512MB" droplet reports MemTotal well under 524288 KB, because the
# kernel reserves a slice for itself (464972 KB / 454 MB observed on a
# DigitalOcean 512MB droplet, 2026-09-19). Comparing against the nominal size
# made this check impossible to pass on the exact hardware it was written for,
# so deployments aborted before module 01 on every fresh 512MB box.
# We now compare against ~85% of the nominal figure, which still catches the
# genuine 256MB-class droplets while allowing 512MB ones through.
MIN_MEM_KB=380000   # ~371 MB — comfortably below a 512MB droplet, above a 256MB one
TOTAL_MEM_KB=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
if [ "${TOTAL_MEM_KB}" -lt "${MIN_MEM_KB}" ]; then
    fail "Less than $((${MIN_MEM_KB} / 1024))MB RAM (found $((TOTAL_MEM_KB / 1024))MB). Minimum 512MB-class VPS required."
fi
if [ "${TOTAL_MEM_KB}" -lt 1048576 ]; then
    warn "Low memory ($((TOTAL_MEM_KB / 1024))MB total): supported but tight for Caddy + PocketBase + 3x shadowsocks-rust."
    # Swap is a strong recommendation, not a hard requirement: without it a
    # transient spike (Caddy TLS issuance, PocketBase migrations) can OOM-kill
    # a service on a 512MB box. 08-firewall.sh does not create swap, so we
    # surface it here rather than silently proceeding.
    SWAP_KB=$(awk '/SwapTotal/ {print $2}' /proc/meminfo)
    if [ "${SWAP_KB:-0}" -lt 262144 ]; then
        warn "No/low swap detected ($((SWAP_KB / 1024))MB). Recommended: 1GB swap —"
        warn "  fallocate -l 1G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile"
        warn "  echo '/swapfile none swap sw 0 0' >> /etc/fstab"
    else
        log "✓ Swap: $((SWAP_KB / 1024)) MB"
    fi
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
