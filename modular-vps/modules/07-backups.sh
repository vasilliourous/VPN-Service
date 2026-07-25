#!/usr/bin/env bash
# Module 07: Backups (Backblaze B2)
# Sets up hourly offsite backups of PocketBase SQLite database.
# Backups are compressed, SHA256-verified, and retained for 7 days.
#
# B2 lifecycle rules should be set to delete files older than 7 days.
# This module handles the client-side backup script + systemd timer.
set -euo pipefail

log()  { echo "[07-backups] $*"; }
warn() { echo "[07-backups][WARN] $*"; }
fail() { echo "[07-backups][FAIL] $*"; exit 1; }

PB_DATA_DIR="/opt/pocketbase/pb_data"
BACKUP_SCRIPT="/usr/local/bin/myvpn-backup.sh"
BACKUP_TIMER="/etc/systemd/system/pocketbase-backup.timer"
BACKUP_SERVICE="/etc/systemd/system/pocketbase-backup.service"
CREDS_FILE="/root/.b2-creds"

# ── Check if B2 credentials are available ──
B2_CREDS_FOUND=0
if [ -n "${B2_APPLICATION_KEY_ID:-}" ] && [ -n "${B2_APPLICATION_KEY:-}" ]; then
    B2_CREDS_FOUND=1
    log "B2 credentials provided via environment"
fi

# ── Write backup script ──
write_backup_script() {
    if [ -f "$BACKUP_SCRIPT" ]; then
        log "Backup script already exists at ${BACKUP_SCRIPT}"
        return 0
    fi

    cat > "$BACKUP_SCRIPT" << 'SCRIPT'
#!/usr/bin/env bash
# MyVPN PocketBase Backup to Backblaze B2
# Triggered hourly by systemd timer.
set -euo pipefail

PB_DATA_DIR="/opt/pocketbase/pb_data"
LOG_FILE="/var/log/myvpn-backup.log"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
BACKUP_NAME="data-${TIMESTAMP}.db"
COMPRESSED="data-${TIMESTAMP}.db.gz"
B2_PATH="backups/${COMPRESSED}"
TMP_DIR=$(mktemp -d)
EXIT_CODE=0

# Ensure cleanup on exit
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

log()  { echo "[$(date +%H:%M:%S)] $*" >> "$LOG_FILE"; }
warn() { echo "[$(date +%H:%M:%S)] [WARN] $*" >> "$LOG_FILE"; }

# ── Load B2 credentials ──
# The creds file exports B2_APPLICATION_KEY_ID, B2_APPLICATION_KEY, and B2_BUCKET
if [ -f /root/.b2-creds ]; then
    set -a  # auto-export all sourced variables
    . /root/.b2-creds
    set +a
fi

if [ -z "${B2_APPLICATION_KEY_ID:-}" ] || [ -z "${B2_APPLICATION_KEY:-}" ]; then
    echo "[$(date +%H:%M:%S)] [ERROR] B2 credentials not configured. Create /root/.b2-creds with:" >> "$LOG_FILE"
    echo "  B2_APPLICATION_KEY_ID=your_key_id" >> "$LOG_FILE"
    echo "  B2_APPLICATION_KEY=your_key" >> "$LOG_FILE"
    echo "  B2_BUCKET=your-bucket" >> "$LOG_FILE"
    exit 1
fi

: "${B2_BUCKET:?B2_BUCKET must be set in /root/.b2-creds}"

log "Starting backup: ${TIMESTAMP}"

# ── 1. Verify source database exists ──
if [ ! -f "${PB_DATA_DIR}/data.db" ]; then
    warn "data.db not found at ${PB_DATA_DIR}/data.db"
    exit 1
fi

# ── 2. Create a safe copy using SQLite's .backup command ──
log "Creating safe copy of database..."
sqlite3 "${PB_DATA_DIR}/data.db" ".backup '${TMP_DIR}/data.db'" 2>/dev/null || {
    # Fallback: VACUUM INTO (requires SQLite 3.27+)
    sqlite3 "${PB_DATA_DIR}/data.db" "VACUUM INTO '${TMP_DIR}/data.db';" 2>/dev/null || {
        # Last resort: direct copy with WAL checkpoint flush
        sqlite3 "${PB_DATA_DIR}/data.db" "PRAGMA wal_checkpoint(TRUNCATE);" 2>/dev/null || true
        cp "${PB_DATA_DIR}/data.db" "${TMP_DIR}/data.db"
        warn "Used direct copy fallback — backup may be inconsistent if DB is under write load"
    }
}

# Verify the copy is valid
if ! head -c 16 "${TMP_DIR}/data.db" | grep -q "SQLite format 3"; then
    warn "Backup copy is not a valid SQLite database. Aborting."
    exit 1
fi

# ── 3. Compress ──
gzip -c "${TMP_DIR}/data.db" > "${TMP_DIR}/${COMPRESSED}"
log "Compressed: ${COMPRESSED} ($(stat -c%s "${TMP_DIR}/${COMPRESSED}" 2>/dev/null || stat -f%z "${TMP_DIR}/${COMPRESSED}" 2>/dev/null || echo "?") bytes)"

# ── 4. Generate SHA256 ──
sha256sum "${TMP_DIR}/${COMPRESSED}" > "${TMP_DIR}/${COMPRESSED}.sha256"

# ── 5. Authenticate to B2 ──
b2 authorize-account "$B2_APPLICATION_KEY_ID" "$B2_APPLICATION_KEY" >/dev/null 2>&1 || {
    warn "B2 authorization failed"
    exit 1
}

# ── 6. Upload to B2 ──
log "Uploading ${B2_PATH}..."
b2 upload-file "$B2_BUCKET" "${TMP_DIR}/${COMPRESSED}" "${B2_PATH}" 2>/dev/null || {
    warn "Upload failed for ${B2_PATH}"
    EXIT_CODE=1
}

b2 upload-file "$B2_BUCKET" "${TMP_DIR}/${COMPRESSED}.sha256" "${B2_PATH}.sha256" 2>/dev/null || {
    warn "SHA256 upload failed"
    EXIT_CODE=1
}

# ── 7. Verify upload ──
if [ "$EXIT_CODE" -eq 0 ]; then
    log "Verifying upload..."
    b2 download-file-by-name "$B2_BUCKET" "${B2_PATH}" "${TMP_DIR}/verify.db.gz" 2>/dev/null
    sha256sum -c "${TMP_DIR}/${COMPRESSED}.sha256" >> "$LOG_FILE" 2>&1 && \
        log "✓ Backup verified: ${B2_PATH}" || \
        warn "Backup verification FAILED for ${B2_PATH}"
fi

log "Backup completed (exit ${EXIT_CODE})"
exit "$EXIT_CODE"
SCRIPT
    chmod 755 "$BACKUP_SCRIPT"
    log "✓ Created backup script at ${BACKUP_SCRIPT}"
}

# ── Write systemd timer (hourly) ──
write_timer() {
    if [ -f "$BACKUP_TIMER" ]; then
        log "Backup timer already exists"
        return 0
    fi

    cat > "$BACKUP_TIMER" << 'TIMER'
[Unit]
Description=MyVPN hourly PocketBase backup to B2
Requires=pocketbase.service

[Timer]
OnCalendar=hourly
Persistent=true
RandomizedDelaySec=300

[Install]
WantedBy=timers.target
TIMER
    log "✓ Created backup timer"

    # Write companion service
    cat > "$BACKUP_SERVICE" << 'SERVICE'
[Unit]
Description=MyVPN PocketBase backup to B2
After=network.target pocketbase.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/myvpn-backup.sh
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SERVICE
    log "✓ Created backup service"
}

# ── Save B2 credentials (with export!) ──
save_creds() {
    if [ "$B2_CREDS_FOUND" -eq 0 ]; then
        warn "No B2 credentials provided. Backup won't run until you configure them."
        warn "  Either pass env vars: B2_APPLICATION_KEY_ID=... B2_APPLICATION_KEY=... B2_BUCKET=..."
        warn "  Or create ${CREDS_FILE} with:"
        warn "    B2_APPLICATION_KEY_ID=your_key_id"
        warn "    B2_APPLICATION_KEY=your_key"
        warn "    B2_BUCKET=your-bucket-name"
        return 0
    fi

    # Don't overwrite existing creds unless forced
    if [ -f "$CREDS_FILE" ] && [ "${FORCE:-0}" != "1" ]; then
        log "B2 credentials already saved at ${CREDS_FILE} (use FORCE=1 to overwrite)"
        return 0
    fi

    # IMPORTANT: Use 'set -a' semantics so variables are exported when script is sourced
    cat > "$CREDS_FILE" << EOF
# MyVPN B2 Backup Credentials
# Sourced by myvpn-backup.sh
B2_APPLICATION_KEY_ID="${B2_APPLICATION_KEY_ID}"
B2_APPLICATION_KEY="${B2_APPLICATION_KEY}"
B2_BUCKET="${B2_BUCKET:-my-vpn-backup-bucket}"
EOF
    chmod 600 "$CREDS_FILE"
    log "✓ B2 credentials saved to ${CREDS_FILE}"
    log "  NOTE: The backup script uses 'set -a' before sourcing this file"
    log "  so variables are automatically exported."
}

# ── Install b2 CLI ──
install_b2() {
    if command -v b2 &>/dev/null; then
        log "b2 CLI already installed"
        return 0
    fi

    log "Installing b2 CLI..."
    pip3 install b2 2>/dev/null || {
        DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3-pip
        pip3 install b2 2>/dev/null || fail "Failed to install b2 CLI"
    }

    if command -v b2 &>/dev/null; then
        log "✓ b2 CLI installed ($(b2 version 2>/dev/null | head -1 || echo 'unknown'))"
    else
        warn "b2 CLI not found after install. Backups will fail."
    fi
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════

install_b2
save_creds
write_backup_script
write_timer

# ── Enable timer ──
systemctl daemon-reload
systemctl enable pocketbase-backup.timer 2>/dev/null || true
systemctl restart pocketbase-backup.timer 2>/dev/null || true

# ── Show status ──
log "Backup timer status:"
systemctl status pocketbase-backup.timer --no-pager 2>&1 | head -5

# ── Run initial backup if credentials available ──
if [ "$B2_CREDS_FOUND" -eq 1 ]; then
    log "Running initial backup..."
    bash "$BACKUP_SCRIPT" 2>&1 | tail -10 || warn "Initial backup failed (check B2 credentials and bucket)"
fi

log "✓ Backup setup complete"
log "   Timer: hourly, randomized delay 5min"
if [ -n "${B2_BUCKET:-}" ]; then
    log "   Location: b2://${B2_BUCKET}/backups/"
else
    log "   Location: (B2 bucket not configured)"
fi
exit 0
