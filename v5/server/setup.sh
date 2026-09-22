#!/usr/bin/env bash
# Locus Modular VPS Setup — Orchestrator
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

# ── Deployment toggles (resolved once, exported to every module) ──
# These MUST be exported: run_module runs each module as a child process and
# only forwards DOMAIN/FORCE inline. Without exporting these, setup.sh would
# honour ENABLE_UOT=0 in its own summary while the modules that actually act on
# it (02-shadowsocks, 08-firewall, 06-pocketbase/seed-pb) still saw the default
# and enabled the feature — a silently half-applied opt-out.
: "${ENABLE_UOT:=1}"          # 1 = install Strike UDP-over-TCP (:8446)
: "${UOT_PORT:=8446}"
: "${SKIP_CONSOLE:=0}"        # 1 = skip deploying the admin console
export ENABLE_UOT UOT_PORT SKIP_CONSOLE

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

    # Run module, tee output, capture exit code.
    # Deployment toggles are passed explicitly as well as being exported, so a
    # module can never silently act on the default when setup.sh was told
    # otherwise (see the toggle block near the top).
    DOMAIN="$DOMAIN" FORCE="$FORCE" \
    ENABLE_UOT="$ENABLE_UOT" UOT_PORT="$UOT_PORT" SKIP_CONSOLE="$SKIP_CONSOLE" \
    bash "$module" 2>&1 | tee -a "$LOGFILE"
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
║     Locus VPS — Modular Setup             ║
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

# ── Start Shadowsocks services (all modules done) ──
log "Starting Shadowsocks services..."
systemctl daemon-reload
for svc in shadowsocks-eco shadowsocks-stealth shadowsocks-strike; do
    if systemctl enable "$svc" 2>/dev/null; then
        log "  Enabled ${svc}"
    fi
done

# Start services and verify.
# sing-box-uot (Strike UDP-over-TCP, port 8446) is part of the default
# deployment; ENABLE_UOT=0 opts out of it.
SERVICES="shadowsocks-eco shadowsocks-stealth shadowsocks-strike tc-eco-cap tc-stealth-cap tc-strike-cap"
if [ "${ENABLE_UOT:-1}" = "1" ]; then
    SERVICES="${SERVICES} sing-box-uot"
fi
for svc in $SERVICES; do
    if systemctl start "$svc" 2>/dev/null; then
        log "  Started ${svc}"
    else
        warn "${svc} failed to start — check: journalctl -u ${svc} -n 20 --no-pager"
    fi
done

# ── Verify services ──
sleep 2
ALL_OK=true
for svc in $SERVICES; do
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
    # Fallback: generate a random token if no secrets file was used.
    #
    # Like the tier passwords and PB_ADMIN_PASS, this is a FLEET-WIDE secret:
    # it authenticates the /api/admin/* hub endpoints (unbind, code generation)
    # and it is written into /etc/environment, where the hooks read it. A host
    # that invents its own token answers 401 to admin calls made with the
    # operator's recorded token -- an "admin console is broken" symptom whose
    # real cause is a diverged secret. So: opt-in only, never silent.
    if [ "${ALLOW_GENERATED_ADMIN_TOKEN:-0}" != "1" ]; then
        fail "ADMIN_API_TOKEN is not set and ${ADMIN_API_TOKEN_FILE} does not exist.
     Refusing to invent a fleet-wide secret.
     Fix: deploy via setup.sh so secrets.env.age is decrypted, or pass
     ADMIN_API_TOKEN=... explicitly, or set ALLOW_GENERATED_ADMIN_TOKEN=1 to
     generate one deliberately (you must then record it -- see
     docs/SECRETS-MANAGEMENT.md -> \"Generated credentials write-back\")."
    fi
    ADMIN_API_TOKEN=$(openssl rand -base64 24 | tr '/+' '_-')
    echo "$ADMIN_API_TOKEN" > "$ADMIN_API_TOKEN_FILE"
    chmod 600 "$ADMIN_API_TOKEN_FILE"
    echo "ADMIN_API_TOKEN=${ADMIN_API_TOKEN}" >> /etc/environment
    systemctl restart pocketbase 2>/dev/null || true
    log "✓ ADMIN_API_TOKEN GENERATED (random) and saved to ${ADMIN_API_TOKEN_FILE}"
    log "  ⚠  ALLOW_GENERATED_ADMIN_TOKEN=1 was set. This host DIVERGES from the"
    log "  ⚠  fleet until you complete the write-back in docs/SECRETS-MANAGEMENT.md"
    ADMIN_TOKEN_GENERATED=1
else
    ADMIN_API_TOKEN=$(cat "$ADMIN_API_TOKEN_FILE")
    log "ADMIN_API_TOKEN loaded from ${ADMIN_API_TOKEN_FILE}"
fi

# ── Run post-deploy smoke test ──
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/scripts" 2>/dev/null && pwd || echo "")"
SMOKE_TEST="${SCRIPT_DIR}/smoke-test.sh"
if [ -f "$SMOKE_TEST" ]; then
    log "Running post-deploy smoke test..."
    DOMAIN="$DOMAIN" ENABLE_UOT="$ENABLE_UOT" UOT_PORT="$UOT_PORT" \
        bash "$SMOKE_TEST" 2>&1 | tee -a "$LOGFILE" || true
else
    warn "Smoke test script not found at ${SMOKE_TEST}"
fi

# ── Generate a first batch of activation codes (optional) ──
# A fresh hub with zero codes is not usable out of the box — the operator has
# to go and generate some before anything can be sold. Set FIRST_BATCH to mint
# them during deploy.
#
#   FIRST_BATCH=20                  each tier gets 20 codes (eco/stealth/strike)
#   FIRST_BATCH_ECO=50              per-tier overrides
#   FIRST_BATCH_STEALTH=30
#   FIRST_BATCH_STRIKE=10
#   FIRST_BATCH_MIDDLEMAN=Sarah     recorded against every code in the batch
#   FIRST_BATCH_EXPIRES=2027-09-19  optional expiry (YYYY-MM-DD)
#
# Idempotent by design: codes are minted ONCE and recorded in
# /root/.first_batch_done. Re-running setup.sh does not mint a second batch
# (which would silently create unsold inventory). Delete that file to re-arm.
generate_first_batch() {
    local marker="/root/.first_batch_done"
    local eco_n="${FIRST_BATCH_ECO:-${FIRST_BATCH:-0}}"
    local stealth_n="${FIRST_BATCH_STEALTH:-${FIRST_BATCH:-0}}"
    local strike_n="${FIRST_BATCH_STRIKE:-${FIRST_BATCH:-0}}"

    if [ "$eco_n" = "0" ] && [ "$stealth_n" = "0" ] && [ "$strike_n" = "0" ]; then
        return 0   # nothing requested
    fi

    if [ -f "$marker" ]; then
        log "First-batch codes already generated ($(cat "$marker")) — skipping."
        log "  To generate another batch, use the admin console, or remove ${marker}"
        return 0
    fi

    # The generator needs a PocketBase ADMIN JWT (not ADMIN_API_TOKEN — PB 0.22
    # rejects the latter for record access).
    local pb_token="" pb_email="" pb_pass=""
    if [ -f /root/.pb_admin_creds ]; then
        pb_token=$(grep '^PB_TOKEN=' /root/.pb_admin_creds | cut -d= -f2)
        pb_email=$(grep '^PB_ADMIN_EMAIL=' /root/.pb_admin_creds | cut -d= -f2)
        pb_pass=$(grep '^PB_ADMIN_PASS=' /root/.pb_admin_creds | cut -d= -f2)
    fi
    if [ -z "$pb_token" ] && [ -n "$pb_email" ] && [ -n "$pb_pass" ]; then
        pb_token=$(curl -sf -X POST "http://127.0.0.1:8090/api/admins/auth-with-password" \
            -H "Content-Type: application/json" \
            -d "{\"identity\":\"${pb_email}\",\"password\":\"${pb_pass}\"}" 2>/dev/null \
            | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || echo "")
    fi
    if [ -z "$pb_token" ]; then
        warn "First-batch codes requested but no PocketBase admin token is available."
        warn "  Check /root/.pb_admin_creds, then generate codes from the admin console."
        return 0
    fi

    local gen="${SCRIPT_DIR}/../scripts/generate_codes.sh"
    if [ ! -f "$gen" ]; then
        warn "generate_codes.sh not found at ${gen} — generate codes from the admin console."
        return 0
    fi

    log "Generating first batch of activation codes..."
    local made=0
    for pair in "eco:${eco_n}" "stealth:${stealth_n}" "strike:${strike_n}"; do
        local tier="${pair%%:*}" n="${pair##*:}"
        [ "$n" = "0" ] && continue
        log "  ${tier}: ${n} codes"
        if ( cd "${SCRIPT_DIR}/.." && ADMIN_TOKEN="$pb_token" \
             bash "$gen" "https://${DOMAIN}" "$pb_token" "$tier" "$n" ) >>"$LOGFILE" 2>&1; then
            made=$((made + n))
        else
            warn "  ${tier}: generation failed — see ${LOGFILE}. Use the admin console."
        fi
    done

    if [ "$made" -gt 0 ]; then
        # Stamp middleman/expiry on the batch, if asked for. Done through the
        # admin console API so it takes the same path the operator would (and
        # records code_events for each change).
        if [ -n "${FIRST_BATCH_MIDDLEMAN:-}" ] || [ -n "${FIRST_BATCH_EXPIRES:-}" ]; then
            python3 "${SCRIPT_DIR}/stamp-batch.py" "$ADMIN_API_TOKEN" \
                "${FIRST_BATCH_MIDDLEMAN:-}" "${FIRST_BATCH_EXPIRES:-}" >>"$LOGFILE" 2>&1 || \
                warn "  Could not stamp middleman/expiry — set them in the console."
        fi

        echo "generated ${made} codes ($(date -Is))" > "$marker"
        chmod 600 "$marker"
        log "✓ First batch generated: ${made} codes (recorded in ${marker})"
        log "  View, export and label them in the admin console → Codes & Clients"
    fi
}

generate_first_batch

# ── Write the single admin credentials file ──
# Runs LAST, after every secret exists (tier passwords from module 02,
# PB admin creds from module 06, /etc/environment from the block above,
# the fetch link secret from locus-fetch's first run). Doing it here rather
# than inside a module guarantees nothing is missing yet.
#
# Written unconditionally -- including on a re-run -- so the file can never
# go stale, which is the failure mode /root/.pb_admin_creds hit.
CREDS_SCRIPT="${SCRIPT_DIR}/scripts/write-admin-credentials.sh"
if [ -f "$CREDS_SCRIPT" ]; then
    # Both flags are exported by the code that actually generated a secret,
    # and are the ONLY evidence of fleet divergence. Pass them through
    # explicitly so the file can say so rather than guess.
    TIER_PASSWORDS_GENERATED="${TIER_PASSWORDS_GENERATED:-0}" \
    ADMIN_TOKEN_GENERATED="${ADMIN_TOKEN_GENERATED:-0}" \
    DOMAIN="$DOMAIN" \
        bash "$CREDS_SCRIPT" 2>&1 | tee -a "$LOGFILE" || \
        warn "Could not write the admin credentials file — run it by hand: ${CREDS_SCRIPT}"
else
    warn "write-admin-credentials.sh not found at ${CREDS_SCRIPT} — skipping the"
    warn "credential summary. Credentials are still in their individual files."
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
log " WHAT WAS DEPLOYED (no manual steps needed)"
log "═══════════════════════════════════════════"
log ""
log "   Admin console:  https://${DOMAIN}/admin/"
log "     Sign in with the admin token: $(cat ${ADMIN_API_TOKEN_FILE} 2>/dev/null || echo '(see the file)')"
log "     Or bookmark:  https://${DOMAIN}/admin/?token=<token>"
log ""
log "   ALL credentials: /root/locus-credentials.txt  (chmod 600)"
log "     Tier passwords, PB admin, admin token, B2, fetch secret — all in one"
log "     file, with where each value came from. Paste the tier passwords"
log "     straight into a Clash Verge Rev config."
log ""
log "   PocketBase UI:  https://${DOMAIN}/_/"
log "                   admin: $(grep '^PB_ADMIN_EMAIL=' /root/.pb_admin_creds 2>/dev/null | cut -d= -f2 || echo 'see /root/.pb_admin_creds')"
log ""
log "   Tier configs:   seeded automatically (eco/stealth/strike)"
log "   Backups:        pocketbase-backup.timer (hourly -> B2)"
log "   SSH access:     fail2ban protected (no ufw rate-limit on port 22)"
log ""
log "═══════════════════════════════════════════"
log " Service Summary"
log "═══════════════════════════════════════════"
log "   Domain:        ${DOMAIN}"
log "   Eco port:      8443 (BBR, 5 Mbps tc)"
log "   Stealth port:  8444 (BBR, 100 Mbps tc)"
log "   Strike port:   8445 (BBR+UDP, 200 Mbps tc)"
if [ "${ENABLE_UOT:-1}" = "1" ]; then
log "   Strike UoT:    ${UOT_PORT:-8446} (TCP+UDP, UDP-over-TCP for game traffic)"
else
log "   Strike UoT:    disabled (ENABLE_UOT=0)"
fi
log "   PocketBase:    https://${DOMAIN}/_/"
log "   Admin console: https://${DOMAIN}/admin/"
log "   Admin API:     ${ADMIN_API_TOKEN_FILE}"
log "   Log file:      ${LOGFILE}"
log ""
log "   Tier passwords:   /root/.tier_passwords"
log "   B2 credentials:   /root/.b2-creds"
log "   Admin creds:      /root/.pb_admin_creds"
log ""
log "   Smoke test:       /var/log/myvpn-smoke-test.log"
log ""
log "═══════════════════════════════════════════"
log " OPTIONAL (only if you want them)"
log "═══════════════════════════════════════════"
log ""
if [ -f /root/.first_batch_done ]; then
log "   First-batch codes were generated ($(cat /root/.first_batch_done))."
log "   Print them:    ./scripts/print_codes.sh <tier>-codes.txt out.pdf"
log "   Manage them:   admin console -> Codes & Clients"
else
log "   Issue codes when you are ready:"
log "     * Console:    Codes & Clients -> Generate (recommended)"
log "     * Or deploy with a batch next time:"
log "         FIRST_BATCH=50 FIRST_BATCH_MIDDLEMAN=Sarah setup.sh"
log "     * Or from your workstation:"
log "         ./scripts/generate_codes.sh https://${DOMAIN} <PB_ADMIN_JWT> eco 50"
log "       (PB_ADMIN_JWT is the PB_TOKEN line in /root/.pb_admin_creds —"
log "        the ADMIN_API_TOKEN is rejected for record access by PB 0.22)"
fi
log ""
log "   Publish a client release: tag v* -> CI -> v5/server/scripts/publish-release.sh"
log ""
log "═══════════════════════════════════════════"
log "══════════════════════════════════════════"
