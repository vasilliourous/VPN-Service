#!/usr/bin/env bash
# MyVPN VPS Restore — Provision + restore from B2 backup
# Usage:
#   From-scratch restore (interactive):
#     B2_APPLICATION_KEY_ID=xxx \
#     B2_APPLICATION_KEY=xxx \
#     B2_BUCKET=my-vpn-backup-bucket \
#     DOMAIN=api.yourdomain.com \
#     ssh root@new-vps 'bash -s' < restore.sh
#
#   Use specific backup:
#     ... BACKUP_PATH=backups/data-20260724-143000.db ... restore.sh
#
#   Automatic latest backup:
#     ... restore.sh   # (finds latest in B2 bucket automatically)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOGFILE="/var/log/myvpn-restore.log"

: "${DOMAIN:?DOMAIN is required}"
export DOMAIN
: "${B2_APPLICATION_KEY_ID:?B2 application key ID required}"
: "${B2_APPLICATION_KEY:?B2 application key required}"
: "${B2_BUCKET:?B2 bucket name required}"
: "${BACKUP_PATH:=}"  # Optional: specific backup path. Auto-detects latest if empty.

# ── Colors ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

log()  { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*" | tee -a "$LOGFILE"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*" | tee -a "$LOGFILE"; }
fail() { echo -e "${RED}[FAIL]${NC} $*" | tee -a "$LOGFILE"; exit 1; }

log "══════════════════════════════════════════"
log " MyVPN VPS — Full Restore from B2 Backup"
log " Domain: ${DOMAIN}"
log " Bucket: ${B2_BUCKET}"
log "══════════════════════════════════════════"

# ── Step 1: Run full setup ──
log "Step 1/4: Provisioning VPS with full setup..."
FORCE=1 bash "${SCRIPT_DIR}/setup.sh" | tee -a "$LOGFILE"

# ── Step 2: Install b2 CLI and authenticate ──
log "Step 2/4: Authenticating to Backblaze B2..."
if ! command -v b2 &>/dev/null; then
    pip3 install b2 2>/dev/null || {
        apt-get update -qq && apt-get install -y -qq python3-pip
        pip3 install b2
    }
fi

b2 authorize-account "$B2_APPLICATION_KEY_ID" "$B2_APPLICATION_KEY" 2>&1 | tee -a "$LOGFILE"

# ── Step 3: Download and restore latest backup ──
log "Step 3/4: Downloading backup..."

if [ -z "$BACKUP_PATH" ]; then
    log "No specific backup given. Finding latest in b2://${B2_BUCKET}/..."
    BACKUP_PATH=$(b2 ls --long "$B2_BUCKET" backups/ 2>/dev/null | sort -k1,1 | tail -1 | awk '{print $4}')
    if [ -z "$BACKUP_PATH" ]; then
        fail "No backups found in b2://${B2_BUCKET}/backups/. Check B2 credentials and bucket name."
    fi
    log "Latest backup: ${BACKUP_PATH}"
fi

# Download to temp location
TMP_DIR=$(mktemp -d)
RESTORE_FILE="${TMP_DIR}/restore.db"
SHA256_FILE="${TMP_DIR}/restore.sha256"

log "Downloading ${BACKUP_PATH}..."
b2 download-file-by-name "$B2_BUCKET" "$BACKUP_PATH" "$RESTORE_FILE" 2>&1 | tee -a "$LOGFILE"

# Try to download SHA256 if it exists
SHA256_PATH="${BACKUP_PATH}.sha256"
if b2 file-info "$B2_BUCKET" "$SHA256_PATH" &>/dev/null; then
    b2 download-file-by-name "$B2_BUCKET" "$SHA256_PATH" "$SHA256_FILE" 2>&1 | tee -a "$LOGFILE"
    log "Verifying SHA256 checksum..."
    EXPECTED=$(cat "$SHA256_FILE" | awk '{print $1}')
    ACTUAL=$(sha256sum "$RESTORE_FILE" | awk '{print $1}')
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        fail "Checksum mismatch! Expected ${EXPECTED}, got ${ACTUAL}. Backup may be corrupted."
    fi
    log "✓ Checksum verified."
else
    warn "No SHA256 file found for backup. Skipping verification."
fi

# ── Step 4: Restore PocketBase database ──
log "Step 4/4: Restoring PocketBase database..."

PB_DATA_DIR="/opt/pocketbase/pb_data"

# Verify the backup is a valid SQLite database
if ! head -c 16 "$RESTORE_FILE" | grep -q "SQLite format 3"; then
    fail "Downloaded file is not a valid SQLite database. Backup may be corrupted."
fi

systemctl stop pocketbase
sleep 1

# Backup current (fresh) DB just in case
if [ -f "${PB_DATA_DIR}/data.db" ]; then
    cp "${PB_DATA_DIR}/data.db" "${PB_DATA_DIR}/data.db.pre-restore"
    log "Backed up fresh DB to data.db.pre-restore"
fi

cp "$RESTORE_FILE" "${PB_DATA_DIR}/data.db"
chown pocketbase:pocketbase "${PB_DATA_DIR}/data.db"
chmod 600 "${PB_DATA_DIR}/data.db"

systemctl start pocketbase
sleep 2

# Verify restore
if systemctl is-active --quiet pocketbase; then
    log "✓ PocketBase started successfully after restore."
else
    fail "PocketBase failed to start after restore. Check journalctl -u pocketbase"
fi

# Quick sanity check: count records in codes collection
CODES_COUNT=$(curl -s "http://127.0.0.1:8090/api/collections/codes/records?skipTotal=1" \
    -H "Authorization: Bearer $(cat ${PB_DATA_DIR}/admin_token 2>/dev/null || echo '')" \
    2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('items',[])))" 2>/dev/null || echo "unknown")
log "Restored codes collection has ${CODES_COUNT} records."

# Cleanup
rm -rf "$TMP_DIR"

log "══════════════════════════════════════════"
log "✅ Restore complete!"
log "   Domain:        ${DOMAIN}"
log "   PocketBase:    https://${DOMAIN}/_/"
log "   Records:       ~${CODES_COUNT} codes in database"
log ""
log "   NEXT STEP: Point your DNS A record"
log "   for ${DOMAIN} to THIS server's IP."
log "══════════════════════════════════════════"

# ── Test endpoints ──
log "Running smoke tests..."
sleep 1
curl -f -s -o /dev/null "http://127.0.0.1:8090/api/health" && log "✓ PocketBase health check OK" || warn "PocketBase health check failed"
curl -f -s -o /dev/null "https://${DOMAIN}/api/health" && log "✓ Public health endpoint OK" || warn "Public health check failed (DNS may not point here yet)"
