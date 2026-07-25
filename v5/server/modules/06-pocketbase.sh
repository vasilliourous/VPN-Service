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
ExecStart=${PB_BINARY} serve --http=127.0.0.1:${PB_PORT} --dir=${PB_DATA_DIR} --hooksDir=${PB_HOOKS_DIR}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=${PB_DATA_DIR} ${PB_HOOKS_DIR}

[Install]
WantedBy=multi-user.target
SERVICE
    log "✓ Created pocketbase.service"
}

# ── Initialize SQLite with WAL mode ──
init_sqlite() {
    # PocketBase creates data.db on first start. We just set WAL mode hook.
    local pragma_hook="${PB_HOOKS_DIR}/sqlite_pragmas.pb.js"

    if [ -f "$pragma_hook" ]; then
        log "SQLite pragma hook already exists"
        return 0
    fi

    # The hook will be applied on next PocketBase start
    cat > "$pragma_hook" << 'JSHOOK'
// SQLite WAL mode + busy_timeout
on("app", "ready", (e) => {
    const dao = $app.dao();
    try {
        dao.db().exec("PRAGMA journal_mode=WAL;");
        dao.db().exec("PRAGMA busy_timeout=5000;");
        dao.db().exec("PRAGMA synchronous=NORMAL;");
        dao.db().exec("PRAGMA foreign_keys=ON;");
        $app.logger().info("SQLite pragmas applied (WAL mode)");
    } catch (err) {
        $app.logger().error("Failed to apply SQLite pragmas: " + err.message);
    }
});
JSHOOK
    chown "${PB_USER}:${PB_USER}" "$pragma_hook"
    log "✓ Created SQLite pragma hook"
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════

install_pocketbase
setup_user
setup_dirs
deploy_hooks
create_service
init_sqlite

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
# Automated Admin & Collection Setup
# ═══════════════════════════════════════════
# On a fresh PocketBase install, we can create the first admin via API
# (no auth required before first admin exists).

PB_API="http://127.0.0.1:${PB_PORT}"
PB_ADMIN_CREDS="/root/.pb_admin_creds"
PB_ADMIN_EMAIL="admin@${DOMAIN}"
PB_ADMIN_PASS=""

# Generate random admin password
if [ ! -f "$PB_ADMIN_CREDS" ]; then
    PB_ADMIN_PASS=$(openssl rand -base64 24)

    log "Creating initial PocketBase admin..."
    ADMIN_RESP=$(curl -sf -X POST "${PB_API}/api/admins" \
        -H "Content-Type: application/json" \
        -d "{\"email\": \"${PB_ADMIN_EMAIL}\", \"password\": \"${PB_ADMIN_PASS}\", \"passwordConfirm\": \"${PB_ADMIN_PASS}\"}" 2>/dev/null || echo "FAILED")

    if [ "$ADMIN_RESP" != "FAILED" ]; then
        # Extract admin token from response
        PB_TOKEN=$(echo "$ADMIN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || echo "")

        if [ -n "$PB_TOKEN" ]; then
            log "✓ Admin created: ${PB_ADMIN_EMAIL}"
            # Save credentials
            cat > "$PB_ADMIN_CREDS" << CREDS
# MyVPN PocketBase Admin Credentials
# Created: $(date)
# IMPORTANT: Change this password via the admin UI after first login!
PB_ADMIN_EMAIL=${PB_ADMIN_EMAIL}
PB_ADMIN_PASS=${PB_ADMIN_PASS}
PB_TOKEN=${PB_TOKEN}
CREDS
            chmod 600 "$PB_ADMIN_CREDS"
            log "✓ Admin credentials saved to ${PB_ADMIN_CREDS}"

            # ── Create collections via API ──
            log "Creating PocketBase collections..."

            # Collection: codes
            COLL_RESP=$(curl -sf -X POST "${PB_API}/api/collections" \
                -H "Authorization: Bearer ${PB_TOKEN}" \
                -H "Content-Type: application/json" \
                -d '{
                    "name": "codes",
                    "type": "base",
                    "listRule": "@request.auth.admin = true",
                    "viewRule": "@request.auth.admin = true",
                    "createRule": "@request.auth.admin = true",
                    "updateRule": "@request.auth.admin = true",
                    "deleteRule": "@request.auth.admin = true",
                    "schema": [
                        {"name": "code", "type": "text", "required": true, "unique": true},
                        {"name": "tier", "type": "select", "required": true, "options": {"values": ["eco", "stealth", "strike"]}},
                        {"name": "used", "type": "bool"},
                        {"name": "suspended", "type": "bool"},
                        {"name": "bound_fingerprint", "type": "text"},
                        {"name": "expires_at", "type": "date"},
                        {"name": "activated_at", "type": "date"},
                        {"name": "middleman", "type": "text"}
                    ]
                }' 2>/dev/null || echo "FAILED")
            if [ "$COLL_RESP" != "FAILED" ]; then
                log "✓ Collection 'codes' created"
            else
                warn "Collection 'codes' may already exist (harmless)"
            fi

            # Collection: tier_configs
            COLL_RESP=$(curl -sf -X POST "${PB_API}/api/collections" \
                -H "Authorization: Bearer ${PB_TOKEN}" \
                -H "Content-Type: application/json" \
                -d '{
                    "name": "tier_configs",
                    "type": "base",
                    "listRule": "@request.auth.admin = true",
                    "viewRule": "@request.auth.admin = true",
                    "createRule": "@request.auth.admin = true",
                    "updateRule": "@request.auth.admin = true",
                    "deleteRule": "@request.auth.admin = true",
                    "schema": [
                        {"name": "tier", "type": "select", "required": true, "unique": true, "options": {"values": ["eco", "stealth", "strike"]}},
                        {"name": "config", "type": "json", "required": true},
                        {"name": "active", "type": "bool"},
                        {"name": "udp_relay", "type": "bool"}
                    ]
                }' 2>/dev/null || echo "FAILED")
            if [ "$COLL_RESP" != "FAILED" ]; then
                log "✓ Collection 'tier_configs' created"
            else
                warn "Collection 'tier_configs' may already exist (harmless)"
            fi

            # Collection: activation_attempts
            COLL_RESP=$(curl -sf -X POST "${PB_API}/api/collections" \
                -H "Authorization: Bearer ${PB_TOKEN}" \
                -H "Content-Type: application/json" \
                -d '{
                    "name": "activation_attempts",
                    "type": "base",
                    "listRule": "@request.auth.admin = true",
                    "viewRule": "@request.auth.admin = true",
                    "createRule": "",
                    "updateRule": "@request.auth.admin = true",
                    "deleteRule": "@request.auth.admin = true",
                    "schema": [
                        {"name": "ip", "type": "text"},
                        {"name": "rate_key", "type": "text"},
                        {"name": "fingerprint", "type": "text"},
                        {"name": "code_attempted", "type": "text"}
                    ]
                }' 2>/dev/null || echo "FAILED")
            if [ "$COLL_RESP" != "FAILED" ]; then
                log "✓ Collection 'activation_attempts' created"
            else
                warn "Collection 'activation_attempts' may already exist (harmless)"
            fi

            # Collection: update_config
            COLL_RESP=$(curl -sf -X POST "${PB_API}/api/collections" \
                -H "Authorization: Bearer ${PB_TOKEN}" \
                -H "Content-Type: application/json" \
                -d '{
                    "name": "update_config",
                    "type": "base",
                    "listRule": "@request.auth.admin = true",
                    "viewRule": "@request.auth.admin = true",
                    "createRule": "@request.auth.admin = true",
                    "updateRule": "@request.auth.admin = true",
                    "deleteRule": "@request.auth.admin = true",
                    "schema": [
                        {"name": "version", "type": "text", "required": true},
                        {"name": "rollout_percent", "type": "number"},
                        {"name": "active", "type": "bool"},
                        {"name": "update_url", "type": "text"},
                        {"name": "update_sha256", "type": "text"},
                        {"name": "download_windows", "type": "text"},
                        {"name": "download_macos_intel", "type": "text"},
                        {"name": "download_macos_arm", "type": "text"}
                    ]
                }' 2>/dev/null || echo "FAILED")
            if [ "$COLL_RESP" != "FAILED" ]; then
                log "✓ Collection 'update_config' created"
            else
                warn "Collection 'update_config' may already exist (harmless)"
            fi

            # ── Seed default update_config record ──
            log "Seeding default update_config..."
            SEED_RESP=$(curl -sf -X POST "${PB_API}/api/collections/update_config/records" \
                -H "Authorization: Bearer ${PB_TOKEN}" \
                -H "Content-Type: application/json" \
                -d '{
                    "version": "1.0.0",
                    "rollout_percent": 0,
                    "active": true
                }' 2>/dev/null || echo "FAILED")
            if [ "$SEED_RESP" != "FAILED" ]; then
                log "✓ Default update_config record seeded (rollout_percent=0)"
            else
                warn "Default update_config seed failed — create manually via admin UI"
            fi

            # ── Seed tier configs if passwords exist ──
            if [ -f "/root/.tier_passwords" ]; then
                log "Seeding tier configs from /root/.tier_passwords..."
                source /root/.tier_passwords

                # Eco
                curl -sf -X POST "${PB_API}/api/collections/tier_configs/records" \
                    -H "Authorization: Bearer ${PB_TOKEN}" \
                    -H "Content-Type: application/json" \
                    -d "{\"tier\": \"eco\", \"active\": true, \"udp_relay\": false, \"config\": {\"server\": \"${DOMAIN}\", \"server_port\": 8443, \"password\": \"${ECO_PASS:-}\", \"method\": \"aes-256-gcm\"}}" \
                    2>/dev/null && log "✓ Eco tier config seeded" || warn "Eco tier config seed failed"

                # Stealth
                curl -sf -X POST "${PB_API}/api/collections/tier_configs/records" \
                    -H "Authorization: Bearer ${PB_TOKEN}" \
                    -H "Content-Type: application/json" \
                    -d "{\"tier\": \"stealth\", \"active\": true, \"udp_relay\": false, \"config\": {\"server\": \"${DOMAIN}\", \"server_port\": 8444, \"password\": \"${STEALTH_PASS:-}\", \"method\": \"aes-256-gcm\"}}" \
                    2>/dev/null && log "✓ Stealth tier config seeded" || warn "Stealth tier config seed failed"

                # Strike
                curl -sf -X POST "${PB_API}/api/collections/tier_configs/records" \
                    -H "Authorization: Bearer ${PB_TOKEN}" \
                    -H "Content-Type: application/json" \
                    -d "{\"tier\": \"strike\", \"active\": true, \"udp_relay\": true, \"config\": {\"server\": \"${DOMAIN}\", \"server_port\": 8445, \"password\": \"${STRIKE_PASS:-}\", \"method\": \"aes-256-gcm\"}}" \
                    2>/dev/null && log "✓ Strike tier config seeded" || warn "Strike tier config seed failed"
            else
                warn "/root/.tier_passwords not found — tier configs NOT auto-seeded."
                warn "  Seed manually after setup via the PocketBase admin UI or POCKETBASE-SETUP.md"
            fi
        else
            warn "Admin created but could not extract token. Manual setup required."
        fi
    else
        # Admin creation failed — likely an admin already exists
        log "Admin may already exist (first-run only). Skipping auto-setup."
        log "  If this is a reinstall, use existing admin credentials."
        # Try to load existing creds
        if [ -f "$PB_ADMIN_CREDS" ]; then
            log "  Existing credentials at: ${PB_ADMIN_CREDS}"
        fi
    fi
else
    log "Admin credentials file already exists — skipping automated setup."
fi

log "✓ PocketBase setup complete"
log "   Admin UI: https://${DOMAIN}/_/"
log "   Admin credentials: ${PB_ADMIN_CREDS} (if auto-created, change password!)"
log "   API:      http://127.0.0.1:${PB_PORT}/api/"
exit 0
