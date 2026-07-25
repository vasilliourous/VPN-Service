#!/usr/bin/env bash
# MyVPN Modular VPS Setup — Orchestrator
# Usage:
#   1. scp -r modular-vps root@your-vps:/root/
#   2. ssh root@your-vps "DOMAIN=api.yourdomain.com /root/modular-vps/setup.sh"
#
# Each module is idempotent and can be re-run independently.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULES_DIR="${SCRIPT_DIR}/modules"
LOGFILE="/var/log/myvpn-setup.log"

: "${DOMAIN:?DOMAIN is required (e.g. api.yourdomain.com)}"
: "${FORCE:=}"  # Set to 1 to continue on module failure

# ── Colors ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

log()  { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*" | tee -a "$LOGFILE"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*" | tee -a "$LOGFILE"; }
fail() { echo -e "${RED}[FAIL]${NC} $*" | tee -a "$LOGFILE"; exit 1; }

# ── Detect if running interactively ──
if [ -t 0 ]; then
    LOCAL_MODE=1
else
    LOCAL_MODE=0
fi

# ── Run a single module ──
# Returns the module exit code. Orchestrator handles failure logic.
run_module() {
    local module="$1"
    local name
    name="$(basename "$module" .sh)"

    log "──────────────────────────────────────────"
    log "▶ Module: ${name}"
    log "──────────────────────────────────────────"

    if [ ! -f "$module" ]; then
        warn "Module not found: ${module}. Skipping."
        return 0
    fi

    local exitcode=0
    # Use a temp file to capture exit code (more portable than PIPESTATUS in all shells)
    local rc_file
    rc_file=$(mktemp /tmp/myvpn-rc-XXXXXX)

    # Run module, tee output, capture exit code
    DOMAIN="$DOMAIN" FORCE="$FORCE" bash "$module" 2>&1 | tee -a "$LOGFILE"
    # shellcheck disable=SC2320
    exitcode=${PIPESTATUS[0]:-$?}

    rm -f "$rc_file"

    if [ "$exitcode" -ne 0 ]; then
        if [ "$FORCE" = "1" ]; then
            warn "Module ${name} failed (exit ${exitcode}) but --force is set. Continuing."
        else
            fail "Module ${name} failed (exit ${exitcode}). Aborting. Run with FORCE=1 to ignore."
        fi
    fi
    log "✓ Module ${name} completed successfully."
    return "$exitcode"
}

# ── Show header ──
cat << EOF
╔═══════════════════════════════════════════╗
║     MyVPN VPS — Modular Setup             ║
║     Domain: ${DOMAIN}                      ║
╚═══════════════════════════════════════════╝
EOF

log "Starting modular VPS setup..."
log "OS: $(uname -a)"
log "Mode: $([ "$LOCAL_MODE" = 1 ] && echo 'interactive' || echo 'non-interactive/SSH pipe')"

# ── Verify modules directory ──
if [ ! -d "$MODULES_DIR" ]; then
    fail "Modules directory not found: ${MODULES_DIR}"
fi

# ── Ordered module list ──
MODULES=(
    "${MODULES_DIR}/00-env.sh"
    "${MODULES_DIR}/01-bbr.sh"
    "${MODULES_DIR}/02-shadowsocks.sh"
    "${MODULES_DIR}/03-brutal.sh"
    "${MODULES_DIR}/04-tc.sh"
    "${MODULES_DIR}/05-caddy.sh"
    "${MODULES_DIR}/06-pocketbase.sh"
    "${MODULES_DIR}/07-backups.sh"
    "${MODULES_DIR}/08-firewall.sh"
)

# Verify all modules exist before starting
for mod in "${MODULES[@]}"; do
    if [ ! -f "$mod" ]; then
        fail "Required module not found: ${mod}. Aborting."
    fi
done
log "✓ All ${#MODULES[@]} modules found"

# ── Run modules sequentially ──
for mod in "${MODULES[@]}"; do
    run_module "$mod"
done

# ── Start Shadowsocks services (all modules done, Brutal LD_PRELOAD exists) ──
log "Starting Shadowsocks services..."
systemctl daemon-reload
for svc in shadowsocks-eco shadowsocks-stealth shadowsocks-strike; do
    if systemctl enable "$svc" 2>/dev/null; then
        log "  Enabled ${svc}"
    fi
done

# Start services and verify
for svc in shadowsocks-eco shadowsocks-stealth shadowsocks-strike; do
    if systemctl start "$svc" 2>/dev/null; then
        log "  Started ${svc}"
    else
        warn "${svc} failed to start — check: journalctl -u ${svc} -n 20 --no-pager"
    fi
done

# ── Verify services ──
sleep 2
ALL_OK=true
for svc in shadowsocks-eco shadowsocks-stealth shadowsocks-strike; do
    if systemctl is-active --quiet "$svc"; then
        log "✓ ${svc} running"
    else
        warn "${svc} not running — check: journalctl -u ${svc} -n 20 --no-pager"
        ALL_OK=false
    fi
done

# Verify Caddy + PocketBase
for svc in caddy pocketbase; do
    if systemctl is-active --quiet "$svc"; then
        log "✓ ${svc} running"
    else
        warn "${svc} not running — check: journalctl -u ${svc} -n 20 --no-pager"
        ALL_OK=false
    fi
done

# ── Summary ──
log "══════════════════════════════════════════"
if [ "$ALL_OK" = true ]; then
    log "✅ Setup complete! All services running."
else
    log "⚠️  Setup finished with some issues. Review warnings above."
fi
log ""
log "   Domain:        ${DOMAIN}"
log "   Eco port:      8443 (BBR, 5 Mbps tc)"
log "   Stealth port:  8444 (Brutal CC)"
log "   Strike port:   8445 (BBR+UDP, 200 Mbps tc)"
log "   PocketBase:    https://${DOMAIN}/_/"
log "   Log file:      ${LOGFILE}"
log ""
log "   Passwords:     /root/.tier_passwords (chmod 600)"
log "══════════════════════════════════════════"
