#!/usr/bin/env bash
# MyVPN Post-Deploy Smoke Test
# Run AFTER setup.sh completes. Tests the full deployment chain.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOGFILE="/var/log/myvpn-smoke-test.log"
PASS=0
FAIL=0
WARN=0

: "${DOMAIN:?DOMAIN is required}"

# ── Colors ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

log()    { echo -e "${GREEN}[$(date +%H:%M:%S)]${NC} $*" | tee -a "$LOGFILE"; }
pass()   { echo -e "${GREEN}  ✅ PASS:${NC} $*"; PASS=$((PASS+1)); }
fail()   { echo -e "${RED}  ❌ FAIL:${NC} $*"; FAIL=$((FAIL+1)); }
warn()   { echo -e "${YELLOW}  ⚠️  WARN:${NC} $*"; WARN=$((WARN+1)); }

# ── Header ──
cat << EOF | tee -a "$LOGFILE"
═══════════════════════════════════════════
 MyVPN Post-Deploy Smoke Test
 Domain: ${DOMAIN}
 Date:   $(date)
═══════════════════════════════════════════
EOF

# ── 1. Verify all systemd services are active ──
log "Step 1/9: Checking systemd services..."
for svc in caddy pocketbase shadowsocks-eco shadowsocks-stealth shadowsocks-strike; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        pass "${svc} is running"
    else
        fail "${svc} is NOT running"
        journalctl -u "$svc" -n 5 --no-pager 2>/dev/null | tail -3
    fi
done

# ── 2. Verify Brutal CC kernel module ──
log "Step 2/9: Checking Brutal CC..."
if lsmod 2>/dev/null | grep -q "^brutal"; then
    pass "tcp_brutal kernel module loaded"
    TARGET=$(cat /sys/module/tcp_brutal/parameters/target_rate 2>/dev/null || echo "unknown")
    log "  Brutal target rate: ${TARGET} Mbps"
else
    if lsmod 2>/dev/null | grep -q "^tcp_brutal"; then
        pass "tcp_brutal kernel module loaded (legacy name)"
    else
        warn "Brutal CC module not loaded (Stealth will use BBR fallback)"
    fi
fi

# ── 3. Verify BBR is default CC ──
log "Step 3/9: Checking BBR..."
CC=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo "unknown")
if [ "$CC" = "bbr" ]; then
    pass "BBR is default congestion control ($CC)"
else
    fail "BBR is NOT default (current: $CC)"
fi

# ── 4. Verify tc traffic shaping rules ──
log "Step 4/9: Checking tc traffic shaping..."
IFACE=$(ip -4 route show default | awk '{print $5}' | head -1)
if [ -n "$IFACE" ]; then
    log "  Primary interface: ${IFACE}"
    TC_CLASSES=$(tc -s class show dev "$IFACE" 2>/dev/null | head -20)
    echo "$TC_CLASSES" | grep -q "1:10" && pass "tc Eco class (1:10) exists" || warn "tc Eco class not found"
    echo "$TC_CLASSES" | grep -q "1:30" && pass "tc Strike class (1:30) exists" || warn "tc Strike class not found"
else
    warn "Could not detect primary interface"
fi

# ── 5. Verify UFW is active ──
log "Step 5/9: Checking firewall..."
if ufw status 2>/dev/null | grep -q "Status: active"; then
    pass "UFW is active"
    for port in 22 80 443 8443 8444 8445; do
        ufw status 2>/dev/null | grep -q "${port}/tcp" && \
            pass "  Port ${port}/tcp allowed" || \
            warn "  Port ${port}/tcp rule not found"
    done
else
    fail "UFW is NOT active"
fi

# ── 6. Verify HTTP health endpoint ──
log "Step 6/9: Checking HTTP health endpoint..."
if curl -sf "http://127.0.0.1:8090/api/health" >/dev/null 2>&1; then
    pass "PocketBase health check (local HTTP)"
else
    fail "PocketBase health check FAILED (local)"
fi

# Check via Caddy (HTTPS) — skip if DNS doesn't point here
log "Step 6b/9: Checking HTTPS health endpoint..."
VPS_IP=$(ip -4 route show default | awk '{print $3}' | head -1)
DOMAIN_IP=$(host "$DOMAIN" 2>/dev/null | awk '/has address/ {print $4; exit}' || echo "")
if [ -n "$DOMAIN_IP" ] && [ "$DOMAIN_IP" = "$VPS_IP" ]; then
    if curl -sf "https://${DOMAIN}/api/health" >/dev/null 2>&1; then
        pass "Caddy TLS + health check (HTTPS)"
    else
        fail "Caddy TLS health check FAILED — check: journalctl -u caddy -n 20"
    fi
else
    warn "DNS does not point to this VPS (${DOMAIN} → ${DOMAIN_IP:-none}, VPS: ${VPS_IP}). Skipping HTTPS test."
    warn "  After updating DNS, run: curl -sf https://${DOMAIN}/api/health"
fi

# ── 7. Verify TLS certificate ──
log "Step 7/9: Checking TLS certificate..."
if [ -n "$DOMAIN_IP" ] && [ "$DOMAIN_IP" = "$VPS_IP" ]; then
    CERT_INFO=$(echo | openssl s_client -connect "${DOMAIN}:443" -servername "$DOMAIN" 2>/dev/null | openssl x509 -noout -dates 2>/dev/null || echo "")
    if [ -n "$CERT_INFO" ]; then
        pass "TLS certificate present"
        echo "$CERT_INFO" | while read -r line; do log "  ${line}"; done
    else
        warn "TLS certificate check failed (Caddy may still be provisioning via Let's Encrypt)"
        warn "  Check: journalctl -u caddy -n 20 | grep -i tls"
    fi
else
    warn "DNS mismatch — TLS certificate check skipped"
fi

# ── 8. Verify Pingora firewall rules for Shadowsocks ──
log "Step 8/9: Checking Shadowsocks port reachability..."
# Only check locally since external access depends on DNS
for port in 8443 8444 8445; do
    if ss -tlnp 2>/dev/null | grep -q ":${port} "; then
        pass "Shadowsocks port ${port} is listening"
    else
        fail "Shadowsocks port ${port} is NOT listening"
    fi
done

# ── 9. Generate smoke test summary ──
log "Step 9/9: Summary"
echo "" | tee -a "$LOGFILE"
echo "═══════════════════════════════════════════" | tee -a "$LOGFILE"
echo " Smoke Test Results" | tee -a "$LOGFILE"
echo "═══════════════════════════════════════════" | tee -a "$LOGFILE"
echo "  ✅ Passed: ${PASS}" | tee -a "$LOGFILE"
echo "  ❌ Failed: ${FAIL}" | tee -a "$LOGFILE"
echo "  ⚠️  Warnings: ${WARN}" | tee -a "$LOGFILE"
echo "═══════════════════════════════════════════" | tee -a "$LOGFILE"

if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}✅ All critical checks passed!${NC}" | tee -a "$LOGFILE"
else
    echo -e "${RED}❌ ${FAIL} critical check(s) failed. Review warnings above.${NC}" | tee -a "$LOGFILE"
    echo "  Re-run: journalctl -u caddy -n 30 | tail -20" | tee -a "$LOGFILE"
    echo "  Re-run: journalctl -u pocketbase -n 30 | tail -20" | tee -a "$LOGFILE"
fi

exit "$FAIL"
