#!/bin/bash
# ============================================================
# VPN Service: VPS NAT Setup
# Must be run as root on the VPS
# Simple MASQUERADE — all outbound traffic gets VPS public IP
# ============================================================
set -e

# --- Configuration ---
VPS_MAIN_IF="${VPS_MAIN_IF:-$(ip route get 1.1.1.1 | grep -oP 'dev \K\S+')}"

echo "=== VPS NAT Setup ==="
echo "VPS Main IF:   $VPS_MAIN_IF"
echo ""

# --- 1. Enable IP forwarding ---
sysctl -w net.ipv4.ip_forward=1
echo "[+] IP forwarding enabled"

# --- 2. NAT masquerade (all outbound → VPS IP) ---
iptables -t nat -C POSTROUTING -o ${VPS_MAIN_IF} -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -o ${VPS_MAIN_IF} -j MASQUERADE
echo "[+] NAT: masquerade on ${VPS_MAIN_IF}"

# --- 3. Save iptables ---
mkdir -p /etc/iptables
iptables-save > /etc/iptables/rules.v4

echo ""
echo "=== Verification ==="
iptables -t nat -L POSTROUTING -v | grep MASQUERADE
echo ""
echo "=== Done ==="
echo "Test: curl https://ifconfig.me"
echo "(Should return your VPS IP)"
