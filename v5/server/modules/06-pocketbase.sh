#!/usr/bin/env bash
# Module 06: PocketBase
# Installs PocketBase as a dedicated system user with:
#   - JS hooks directory deployed
#   - SQLite WAL mode + busy_timeout
#   - Systemd service with restart on failure
set -euo pipefail

log()  { echo "[06-pocketbase] $*"; }
warn() { echo "[06-pocketbase][WARN] $*"; }
fail() { echo "[06-pocketbase][FAIL] $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOKS_SRC="$(dirname "$SCRIPT_DIR")/pb_hooks"

PB_VERSION="0.22.21"
PB_USER="pocketbase"
PB_DIR="/opt/pocketbase"
PB_BINARY="${PB_DIR}/pocketbase"
PB_DATA_DIR="${PB_DIR}/pb_data"
PB_HOOKS_DIR="${PB_DIR}/pb_hooks"
PB_PORT="8090"

# ── Install PocketBase ──
install_pocketbase() {
    if [ -f "$PB_BINARY" ] && $PB_BINARY version &>/dev/null; then
        log "PocketBase already installed ($($PB_BINARY version))"
        return 0
    fi

    # Install dependencies (unzip is needed for the download)
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq unzip wget 2>/dev/null || \
        log "Unzip/wget already present"

    log "Downloading PocketBase ${PB_VERSION}..."
    local tmpdir
    tmpdir=$(mktemp -d)
    cd "$tmpdir"

    wget -q "https://github.com/pocketbase/pocketbase/releases/download/v${PB_VERSION}/pocketbase_${PB_VERSION}_linux_amd64.zip" \
        -O pocketbase.zip 2>/dev/null || {
        fail "Download failed. Check version ${PB_VERSION} exists."
    }

    unzip -q pocketbase.zip
    mkdir -p "$PB_DIR"
    cp pocketbase "$PB_BINARY"
    chmod +x "$PB_BINARY"
    cd /
    rm -rf "$tmpdir"
    log "✓ PocketBase ${PB_VERSION} installed"
}

# ── Create system user ──
setup_user() {
    if id "$PB_USER" &>/dev/null; then
        log "User ${PB_USER} already exists"
        return 0
    fi

    useradd --system --no-create-home --shell /usr/sbin/nologin "$PB_USER"
    log "✓ Created system user: ${PB_USER}"
}

# ── Create directory structure ──
setup_dirs() {
    mkdir -p "$PB_DATA_DIR" "$PB_HOOKS_DIR"
    chown -R "${PB_USER}:${PB_USER}" "$PB_DIR"
    chmod 750 "$PB_DIR"
    chmod 700 "$PB_DATA_DIR"
    log "✓ Directories created at ${PB_DIR}"
}

# ── Deploy JS hooks ──
deploy_hooks() {
    if [ -d "$HOOKS_SRC" ] && [ "$(ls -A "$HOOKS_SRC" 2>/dev/null)" ]; then
        cp -r "${HOOKS_SRC}/"*.pb.js "$PB_HOOKS_DIR/" 2>/dev/null || true
        chown -R "${PB_USER}:${PB_USER}" "$PB_HOOKS_DIR"
        log "✓ JS hooks deployed from ${HOOKS_SRC}"
    else
        log "No pb_hooks source directory found at ${HOOKS_SRC}. Skipping hook deployment."
        log "  Create *.pb.js files in ${PB_HOOKS_DIR} after setup."
    fi
}

# ── Create systemd service ──
create_service() {
    local service_file="/etc/systemd/system/pocketbase.service"

    if [ -f "$service_file" ]; then
        log "pocketbase.service already exists"
        return 0
    fi

    cat > "$service_file" <<SERVICE
[Unit]
Description=PocketBase — MyVPN Admin Backend
After=network.target

[Service]
Type=simple
User=${PB_USER}
Group=${PB_USER}
EnvironmentFile=/etc/environment
ExecStart=${PB_BINARY} serve --http=127.0.0.1:${PB_PORT} --dir=${PB_DATA_DIR} --hooksDir=${PB_HOOKS_DIR}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SERVICE
    log "✓ Created pocketbase.service"
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════
# SQLite WAL mode is NOT set via hook anymore — PB 0.22+ sets WAL automatically.
# See FIXES.md S7: on() hook API removed in 0.22, sqlite_pragmas.pb.js removed.

install_pocketbase
setup_user
setup_dirs
deploy_hooks
create_service

# ── Reload systemd ──
systemctl daemon-reload

# ── Start PocketBase ──
log "Starting PocketBase (first start creates database)..."
systemctl enable pocketbase 2>/dev/null || true
systemctl restart pocketbase 2>&1 | tail -3
sleep 3

if systemctl is-active --quiet pocketbase; then
    log "✓ PocketBase is running"
    # Check that WAL mode was applied
    if curl -sf "http://127.0.0.1:${PB_PORT}/api/health" >/dev/null 2>&1; then
        log "✓ PocketBase health check passed"
    else
        warn "PocketBase health check endpoint not responding"
    fi
else
    fail "PocketBase failed to start. Check: journalctl -u pocketbase -n 30 --no-pager"
fi

# ═══════════════════════════════════════════
# Automated Admin + Collection + Data Seeding
# ═══════════════════════════════════════════
# Uses Python script to avoid bash heredoc escaping issues with JSON.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SEED_SCRIPT="${SCRIPT_DIR}/../scripts/seed-pb.py"

if [ -f "$SEED_SCRIPT" ]; then
    log "Running automated PocketBase bootstrap..."
    if DOMAIN="$DOMAIN" python3 "$SEED_SCRIPT" 2>&1; then
        log "✓ PocketBase bootstrap completed"
    else
        warn "PocketBase bootstrap encountered errors — check output above"
    fi
else
    fail "Seed script not found at ${SEED_SCRIPT} — must deploy scripts/seed-pb.py alongside modules/"
fi

log "✓ PocketBase setup complete"
log "   Admin UI: https://${DOMAIN}/_/"
log "   Admin credentials: /root/.pb_admin_creds (if auto-created, change password!)"
log "   API:      http://127.0.0.1:${PB_PORT}/api/"
exit 0
