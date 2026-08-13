#!/usr/bin/env bash
# Module 04: Traffic Shaping (tc)
# Applies bandwidth caps using tc HTB qdisc:
#   - Eco (port 8443): 5 Mbps
#   - Stealth (port 8444): 100 Mbps
#   - Strike (port 8445): 200 Mbps
# All tiers use BBR — the caps are enforced purely with tc.
#
# Uses a dedicated helper script for systemd oneshot services
# to avoid shell escaping bugs in ExecStart.
set -euo pipefail

log()  { echo "[04-tc] $*"; }
warn() { echo "[04-tc][WARN] $*"; }
fail() { echo "[04-tc][FAIL] $*"; exit 1; }

TC_HELPER="/usr/local/bin/myvpn-tc-apply.sh"
TC_CONF_DIR="/etc/myvpn/tc"

# ── Detect primary interface ──
IFACE=$(ip -4 route show default | awk '{print $5}' | head -1)
if [ -z "$IFACE" ]; then
    fail "Could not detect primary network interface."
fi
log "Primary interface: ${IFACE}"

# ── Write the tc helper script (used by systemd oneshot services) ──
# This avoids inline shell escaping bugs in ExecStart= lines.
write_tc_helper() {
    if [ -f "$TC_HELPER" ]; then
        log "tc helper script already exists at ${TC_HELPER}"
        return 0
    fi

    mkdir -p "$TC_CONF_DIR"

    cat > "$TC_HELPER" << 'HELPER'
#!/usr/bin/env bash
# MyVPN tc apply helper — called by systemd oneshot services
# Usage: myvpn-tc-apply.sh <port> <rate> <classid>
# Example: myvpn-tc-apply.sh 8443 5mbit 1:10
set -euo pipefail

PORT="${1:?Usage: $0 <port> <rate> <classid>}"
RATE="${2:?Usage: $0 <port> <rate> <classid>}"
CLASSID="${3:?Usage: $0 <port> <rate> <classid>}"
IFACE=$(ip -4 route show default | awk '{print $5}' | head -1)

if [ -z "$IFACE" ]; then
    echo "[tc-apply] ERROR: Could not detect default interface"
    exit 1
fi

# Ensure root qdisc exists (htb)
tc qdisc add dev "$IFACE" root handle 1: htb default 30 2>/dev/null || true

# Add or update the class
if ! tc class add dev "$IFACE" parent 1: classid "$CLASSID" htb rate "$RATE" ceil "$RATE" 2>/dev/null; then
    tc class change dev "$IFACE" parent 1: classid "$CLASSID" htb rate "$RATE" ceil "$RATE" 2>/dev/null || true
fi

# Add or replace the filter
# Idempotency: this parent holds ONLY the tier-port filters, so flush it
# first — `tc filter add` with an identical u32 spec silently creates
# DUPLICATES (observed on the 2026-08-14 deploy: 4 copies of each filter),
# and a spec-matching `tc filter del` is unreliable across versions.
tc filter del dev "$IFACE" parent 1: 2>/dev/null || true
tc filter add dev "$IFACE" protocol ip parent 1: prio 10 u32 \
    match ip sport "$PORT" 0xffff flowid "$CLASSID" 2>/dev/null || true

echo "[tc-apply] Port $PORT → $CLASSID @ $RATE on $IFACE"
HELPER
    chmod +x "$TC_HELPER"
    log "✓ Created tc helper script: ${TC_HELPER}"
}

# ── Create systemd oneshot service ──
create_tc_service() {
    local name="$1"        # e.g. "eco"
    local classid="$2"     # e.g. "1:10"
    local rate="$3"        # e.g. "5mbit"
    local port="$4"        # e.g. "8443"
    local extra_deps="${5:-}"  # optional: "shadowsocks-eco.service"

    local service_file="/etc/systemd/system/tc-${name}-cap.service"

    if [ -f "$service_file" ]; then
        log "tc-${name}-cap.service already exists"
        return 0
    fi

    cat > "$service_file" << SERVICE
[Unit]
Description=Apply tc bandwidth cap for ${name^} tier (port ${port})
After=network.target
Before=${extra_deps}

[Service]
Type=oneshot
ExecStart=${TC_HELPER} ${port} ${rate} ${classid}
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
SERVICE
    log "✓ Created ${service_file} (port ${port}, rate ${rate})"
}

# ── Apply tc rules immediately ──
apply_tc_now() {
    local port="$1"
    local classid="$2"
    local rate="$3"

    # Check if filter already exists for this port
    if tc filter show dev "$IFACE" parent 1: 2>/dev/null | grep -q "sport ${port}"; then
        log "tc filter for port ${port} already exists — updating rate"
    fi

    # Delegate to the helper script
    bash "$TC_HELPER" "$port" "$rate" "$classid" 2>&1 | while read -r line; do
        log "  ${line}"
    done

    log "✓ Applied tc: port ${port} → ${classid} @ ${rate}"
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════

write_tc_helper

# ── Apply to live interface ──
log "Applying tc rules to ${IFACE}..."
# Apply all three tiers (order doesn't matter — different classids)
apply_tc_now 8443 "1:10" "5mbit"
apply_tc_now 8444 "1:20" "100mbit"
apply_tc_now 8445 "1:30" "200mbit"

# ── Create systemd services for reboot persistence ──
create_tc_service "eco"     "1:10" "5mbit"   8443 "shadowsocks-eco.service"
create_tc_service "stealth" "1:20" "100mbit" 8444 "shadowsocks-stealth.service"
create_tc_service "strike"  "1:30" "200mbit" 8445 "shadowsocks-strike.service"

# ── Enable services ──
systemctl daemon-reload
systemctl enable tc-eco-cap.service 2>/dev/null || warn "Could not enable tc-eco-cap.service"
systemctl enable tc-stealth-cap.service 2>/dev/null || warn "Could not enable tc-stealth-cap.service"
systemctl enable tc-strike-cap.service 2>/dev/null || warn "Could not enable tc-strike-cap.service"

# ── Verify ──
log "Verifying tc configuration..."
tc -s class show dev "$IFACE" 2>/dev/null | head -20 || warn "No tc classes (normal for first boot without traffic)"

# Verify specific filters
for port in 8443 8444 8445; do
    if tc filter show dev "$IFACE" parent 1: 2>/dev/null | grep -q "sport ${port}"; then
        log "✓ Filter for port ${port} is active"
    else
        warn "Filter for port ${port} not found. Run: systemctl restart tc-eco-cap.service"
    fi
done

log "✓ Traffic shaping setup complete"
exit 0
