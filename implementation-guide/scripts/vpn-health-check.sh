#!/bin/bash
# ============================================================
# VPN Service: Health Check (VPS-Only)
# Run via cron every 5 minutes: */5 * * * * root /usr/local/bin/vpn-health-check.sh
# ============================================================

ALERT_EMAIL="${ALERT_EMAIL:-root}"
LOGFILE="/var/log/vpn-health.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOGFILE"
}

alert() {
    log "ALERT: $1"
    if command -v mail &>/dev/null; then
        echo "$1" | mail -s "VPN ALERT: $(hostname) - $1" "$ALERT_EMAIL"
    fi
}

# --- Check protocol services ---
for svc in hysteria-premium tuic-lite awg-standard xray; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        log "Service $svc: RUNNING"
    else
        alert "Service $svc is DOWN. Attempting restart..."
        systemctl restart "$svc" 2>/dev/null || true
        sleep 3
        if systemctl is-active --quiet "$svc" 2>/dev/null; then
            log "Service $svc: RESTARTED OK"
        else
            alert "Service $svc: FAILED TO RESTART"
        fi
    fi
done

# --- Check ports ---
check_port() {
    local port=$1 proto=$2 name=$3
    if ss -tulnp | grep -q "${port}"; then
        log "Port ${port}/${proto} (${name}): LISTENING"
    else
        alert "Port ${port}/${proto} (${name}): NOT LISTENING"
    fi
}

check_port 443 udp "Hysteria2"
check_port 443 tcp "VLESS-REALITY"
check_port 8443 udp "TUIC V5"
check_port 9443 udp "AmneziaWG"

# --- Check NAT ---
if ! iptables -t nat -L POSTROUTING 2>/dev/null | grep -q MASQUERADE; then
    alert "NAT MASQUERADE rule missing. Reapplying..."
    /usr/local/bin/vps-nat-setup.sh 2>/dev/null || true
fi

# --- Check internet connectivity ---
if ! ping -c 2 -W 5 1.1.1.1 > /dev/null 2>&1; then
    alert "Internet connectivity FAILED — VPS may be isolated"
else
    log "Internet connectivity: OK"
fi

# --- Check memory ---
MEM_AVAIL=$(free -m | awk '/Mem:/ {print $7}')
if [ "$MEM_AVAIL" -lt 256 ]; then
    alert "Low memory: ${MEM_AVAIL}MB available"
else
    log "Memory OK: ${MEM_AVAIL}MB available"
fi

# --- Check disk ---
DISK_USE=$(df / | awk 'NR==2 {print $5}' | tr -d '%')
if [ "$DISK_USE" -gt 90 ]; then
    alert "Disk usage at ${DISK_USE}%"
else
    log "Disk OK: ${DISK_USE}% used"
fi

# --- Check load ---
LOAD=$(uptime | awk -F'load average:' '{print $2}' | awk '{print $1}' | tr -d ',')
if (( $(echo "$LOAD > 10" | bc -l) )); then
    alert "High load: ${LOAD}"
else
    log "Load OK: ${LOAD}"
fi

# --- Check connection count ---
CONN_COUNT=$(ss -s | grep TCP | awk '{print $2}')
if [ "$CONN_COUNT" -gt 1000 ]; then
    log "INFO: High connection count: ${CONN_COUNT}"
fi
