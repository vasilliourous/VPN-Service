#!/bin/bash
# ============================================================
# VPN Service: Backup Script (VPS-Only)
# Run nightly: 0 3 * * * root /usr/local/bin/vpn-backup.sh
# ============================================================
set -e

BACKUP_DIR="/root/vpn-backups"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/vpn-backup-${DATE}.tar.gz"
LOG="/var/log/vpn-backup.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG"
}

mkdir -p "$BACKUP_DIR"

log "Starting backup..."

# Create archive of all critical files
tar czf "$BACKUP_FILE" \
    /opt/activation-api/activation.db \
    /opt/activation-api/.env \
    /opt/marzban/.env \
    /opt/marzban/docker-compose.yml \
    /etc/hysteria/ \
    /opt/tuic/*.json \
    /etc/amnezia/ \
    /etc/letsencrypt/live/ \
    /etc/nginx/sites-available/ \
    /etc/iptables/ \
    /usr/local/etc/xray/config.json \
    /usr/local/bin/vps-*.sh \
    /usr/local/bin/vpn-*.sh \
    /etc/systemd/system/vps-*.service \
    /etc/systemd/system/vpn-*.service \
    /etc/systemd/system/hysteria-*.service \
    /etc/systemd/system/tuic-*.service \
    /etc/systemd/system/awg-*.service \
    /etc/sysctl.d/99-*.conf \
    2>/dev/null

BACKUP_SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
log "Backup created: $BACKUP_FILE ($BACKUP_SIZE)"

# Rotate: keep last 30 backups
BACKUP_COUNT=$(ls -1 "$BACKUP_DIR"/vpn-backup-*.tar.gz 2>/dev/null | wc -l)
if [ "$BACKUP_COUNT" -gt 30 ]; then
    ls -1t "$BACKUP_DIR"/vpn-backup-*.tar.gz | tail -n +31 | xargs rm -f
    log "Rotated old backups (kept 30, removed $((BACKUP_COUNT - 30)))"
fi

log "Backup complete. To download: scp root@<vps-ip>:/root/vpn-backups/vpn-backup-${DATE}.tar.gz ."
