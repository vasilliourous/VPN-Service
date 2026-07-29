#!/usr/bin/env bash
# MyVPN Modular VPS Setup — Orchestrator
# Usage:
#   1. scp -r v5/server age-key.txt root@your-vps:/root/server/
#   2. ssh root@your-vps "/root/server/setup.sh"
#
# Secrets are auto-decrypted from secrets.env.age using age.
# See docs/SECRETS-MANAGEMENT.md for setup instructions.
#
# Each module is idempotent and can be re-run independently.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULES_DIR="${SCRIPT_DIR}/modules"
LOGFILE="/var/log/myvpn-setup.log"

# ── Colors ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

log()  { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*" | tee -a "$LOGFILE"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*" | tee -a "$LOGFILE"; }
fail() { echo -e "${RED}[FAIL]${NC} $*" | tee -a "$LOGFILE"; exit 1; }

# ── Age-encrypted secrets auto-decrypt ──
# Decrypts secrets.env.age at startup so all modules see the credentials.
# Key sources (in order of precedence):
#   1. AGE_KEY environment variable (CI/CD, SSH pipe)
#   2. age-key.txt file alongside this script (local deploy)
#
# FIX: Uses temp file instead of process substitution (source <(cmd)).
# Process substitution is unreliable in non-interactive SSH sessions —
# variables may appear sourced (exit 0) without actually loading.
# See v5/docs/FIXES.md entry S9 for details.
SECRETS_FILE="${SCRIPT_DIR}/secrets.env.age"

# Ensure age is installed before attempting decryption
if [ -f "$SECRETS_FILE" ]; then
    if ! command -v age &>/dev/null; then
        log "Installing age (required for secrets decryption)..."
        apt-get update -qq && apt-get install -y -qq age 2>/dev/null || \
            fail "Failed to install age. Install manually: apt-get install age"
    fi

    log "Decrypting ${SECRETS_FILE}..."
    SECRETS_TMP="/tmp/.secrets-$$.env"
    if [ -n "${AGE_KEY:-}" ]; then
        echo "$AGE_KEY" > "/tmp/age-key-$$.tmp"
        age -d -i "/tmp/age-key-$$.tmp" "$SECRETS_FILE" > "$SECRETS_TMP" 2>/dev/null || \
            { rm -f "/tmp/age-key-$$.tmp" "$SECRETS_TMP"; fail "Failed to decrypt secrets. Check AGE_KEY."; }
        rm -f "/tmp/age-key-$$.tmp"
    elif [ -f "${SCRIPT_DIR}/age-key.txt" ]; then
        age -d -i "${SCRIPT_DIR}/age-key.txt" "$SECRETS_FILE" > "$SECRETS_TMP" 2>/dev/null || \
            { rm -f "$SECRETS_TMP"; fail "Failed to decrypt secrets. Check age-key.txt."; }
    else
        cat >&2 << 'KEYERR'

╔══════════════════════════════════════════════════════════════╗
║  FATAL: secrets.env.age found but no age key available.     ║
║                                                            ║
║  Provide the key in one of these ways:                     ║
║    1. Set AGE_KEY environment variable (CI/CD / pipe)      ║
║    2. Place age-key.txt in the server/ directory            ║
║                                                            ║
║  See docs/SECRETS-MANAGEMENT.md for setup instructions.    ║
╚══════════════════════════════════════════════════════════════╝
KEYERR
        fail "No age key available for secrets.env.age"
    fi

    # Source from temp file (reliable in all shell environments)
    set -a
    source "$SECRETS_TMP" || { rm -f "$SECRETS_TMP"; fail "Failed to source decrypted secrets."; }
    set +a
    rm -f "$SECRETS_TMP"
    log "✓ Secrets decrypted from ${SECRETS_FILE}"
fi

# ── Required variables (may be set by decrypted secrets or environment) ──
: "${DOMAIN:?DOMAIN is required (e.g. networkingguides.duckdns.org)}"
: "${FORCE:=}"  # Set to 1 to continue on module failure

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

# ── Generate ADMIN_API_TOKEN (if not already set) ──
ADMIN_API_TOKEN_FILE="/root/.admin_api_token"
if [ -n "${ADMIN_API_TOKEN:-}" ]; then
    # Use pre-configured value (from decrypted secrets, env, or fallback)
    echo "$ADMIN_API_TOKEN" > "$ADMIN_API_TOKEN_FILE"
    chmod 600 "$ADMIN_API_TOKEN_FILE"
    if ! grep -q "ADMIN_API_TOKEN" /etc/environment 2>/dev/null; then
        echo "ADMIN_API_TOKEN=${ADMIN_API_TOKEN}" >> /etc/environment
        systemctl restart pocketbase 2>/dev/null || true
    fi
    log "✓ ADMIN_API_TOKEN configured from secrets"
elif [ ! -f "$ADMIN_API_TOKEN_FILE" ]; then
    # Fallback: generate a random token if no secrets file was used
    ADMIN_API_TOKEN=$(openssl rand -base64 24 | tr '/+' '_-')
    echo "$ADMIN_API_TOKEN" > "$ADMIN_API_TOKEN_FILE"
    chmod 600 "$ADMIN_API_TOKEN_FILE"
    echo "ADMIN_API_TOKEN=${ADMIN_API_TOKEN}" >> /etc/environment
    systemctl restart pocketbase 2>/dev/null || true
    log "✓ ADMIN_API_TOKEN generated (random) and saved to ${ADMIN_API_TOKEN_FILE}"
    log "  ⚠  This is a random fallback. For reproducible deploys, set ADMIN_API_TOKEN"
    log "  ⚠  in secrets.env.age. See docs/SECRETS-MANAGEMENT.md"
else
    ADMIN_API_TOKEN=$(cat "$ADMIN_API_TOKEN_FILE")
    log "ADMIN_API_TOKEN loaded from ${ADMIN_API_TOKEN_FILE}"
fi

# ── Run post-deploy smoke test ──
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/scripts" 2>/dev/null && pwd || echo "")"
SMOKE_TEST="${SCRIPT_DIR}/smoke-test.sh"
if [ -f "$SMOKE_TEST" ]; then
    log "Running post-deploy smoke test..."
    DOMAIN="$DOMAIN" bash "$SMOKE_TEST" 2>&1 | tee -a "$LOGFILE" || true
else
    warn "Smoke test script not found at ${SMOKE_TEST}"
fi

# ── Summary ──
log "══════════════════════════════════════════"
if [ "$ALL_OK" = true ]; then
    log "✅ Setup complete! All services running."
else
    log "⚠️  Setup finished with some issues. Review warnings above."
fi
log ""
log "═══════════════════════════════════════════"
log " NEXT STEPS"
log "═══════════════════════════════════════════"
log ""
log " 1. Create or verify PocketBase admin:"
log "    https://${DOMAIN}/_/"
log "    Auto-created admin credentials (if first run):"
log "    cat /root/.pb_admin_creds"
log ""
log " 2. Verify tier configs were seeded:"
log "    Check tier_configs collection in admin UI"
log "    If missing, run: DOMAIN=${DOMAIN} python3 ${SCRIPT_DIR}/seed-pb.py"
log ""
log " 3. Generate activation codes:"
log "    ./scripts/generate_codes.sh https://${DOMAIN} ${ADMIN_API_TOKEN} eco 50"
log ""
log " 4. Verify backups are running:"
log "    systemctl status pocketbase-backup.timer --no-pager"
log ""
log "═══════════════════════════════════════════"
log " Service Summary"
log "═══════════════════════════════════════════"
log "   Domain:        ${DOMAIN}"
log "   Eco port:      8443 (BBR, 5 Mbps tc)"
log "   Stealth port:  8444 (Brutal CC)"
log "   Strike port:   8445 (BBR+UDP, 200 Mbps tc)"
log "   PocketBase:    https://${DOMAIN}/_/"
log "   Admin API:     ${ADMIN_API_TOKEN_FILE}"
log "   Log file:      ${LOGFILE}"
log ""
log "   Tier passwords:   /root/.tier_passwords"
log "   B2 credentials:   /root/.b2-creds"
log "   Admin creds:      /root/.pb_admin_creds (if auto-created)"
log ""
log "   Smoke test:       /var/log/myvpn-smoke-test.log"
log "══════════════════════════════════════════"
