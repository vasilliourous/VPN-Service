#!/usr/bin/env bash
# Module 02: Shadowsocks Server — 3-tier instances
# Installs ssserver and creates three systemd services:
#   - Eco (port 8443, BBR, TCP only)
#   - Stealth (port 8444, BBR, TCP only)
#   - Strike (port 8445, BBR, TCP+UDP)
set -euo pipefail

log()  { echo "[02-shadowsocks] $*"; }
warn() { echo "[02-shadowsocks][WARN] $*"; }
fail() { echo "[02-shadowsocks][FAIL] $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SS_VERSION="v1.23.0"
SS_BINARY="/usr/local/bin/ssserver"
PASS_FILE="/root/.tier_passwords"

# ── Install ssserver if not present ──
install_ssserver() {
    if [ -f "$SS_BINARY" ] && $SS_BINARY --version &>/dev/null; then
        log "ssserver already installed at ${SS_BINARY}"
        return 0
    fi

    log "Downloading shadowsocks-rust ${SS_VERSION}..."
    local tmpdir
    tmpdir=$(mktemp -d)
    cd "$tmpdir"

    local url="https://github.com/shadowsocks/shadowsocks-rust/releases/download/${SS_VERSION}/shadowsocks-${SS_VERSION}.x86_64-unknown-linux-gnu.tar.xz"
    wget -q "$url" -O shadowsocks.tar.xz 2>/dev/null || {
        log "Version ${SS_VERSION} download failed. Falling back to latest stable..."
        # Fetch latest release tag from GitHub API
        local latest_tag
        latest_tag=$(wget -q -O- "https://api.github.com/repos/shadowsocks/shadowsocks-rust/releases/latest" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])" 2>/dev/null || echo "")
        if [ -n "$latest_tag" ]; then
            SS_VERSION="$latest_tag"
            url="https://github.com/shadowsocks/shadowsocks-rust/releases/download/${SS_VERSION}/shadowsocks-${SS_VERSION}.x86_64-unknown-linux-gnu.tar.xz"
            wget -q "$url" -O shadowsocks.tar.xz 2>/dev/null || fail "Download failed for latest version ${SS_VERSION} too."
        else
            fail "Download failed for ${SS_VERSION} and couldn't fetch latest. Check GitHub releases manually."
        fi
    }

    tar -xf shadowsocks.tar.xz
    cp ssserver "$SS_BINARY"
    chmod +x "$SS_BINARY"
    cd /
    rm -rf "$tmpdir"
    log "✓ ssserver installed"
}

# ── Load or generate tier passwords ──
# Priority: 1) env vars from decrypted secrets  2) existing file on VPS  3) auto-generate
setup_passwords() {
    ECO_PASS="${ECO_PASS:-}"
    STEALTH_PASS="${STEALTH_PASS:-}"
    STRIKE_PASS="${STRIKE_PASS:-}"

    if [ -n "$ECO_PASS" ] && [ -n "$STEALTH_PASS" ] && [ -n "$STRIKE_PASS" ]; then
        log "Using tier passwords from environment (decrypted secrets)."
    elif [ -f "$PASS_FILE" ]; then
        log "Loading existing passwords from ${PASS_FILE}"
        . "$PASS_FILE"
    else
        log "WARNING: No secrets file and no existing password file found."
        log "Auto-generating tier passwords (non-reproducible — existing clients will break on re-deploy)."
        ECO_PASS=$(openssl rand -hex 16)
        STEALTH_PASS=$(openssl rand -hex 16)
        STRIKE_PASS=$(openssl rand -hex 16)
    fi

    # Persist to file so seed-pb.py and later runs can find them
    cat > "$PASS_FILE" <<EOF
ECO_PASS=$ECO_PASS
STEALTH_PASS=$STEALTH_PASS
STRIKE_PASS=$STRIKE_PASS
EOF
    chmod 600 "$PASS_FILE"

    # Export for use in config files
    export ECO_PASS STEALTH_PASS STRIKE_PASS
    log "✓ Tier passwords ready (eco/stealth/strike)"
}

# ── Create Shadowsocks config ──
write_config() {
    local tier="$1"    # eco, stealth, strike
    local port="$2"
    local pass_var="$3"
    local mode="$4"    # tcp_only or tcp_and_udp

    local config_file="/etc/shadowsocks/${tier}.json"
    local pass="${!pass_var}"

    if [ -f "$config_file" ]; then
        log "Config ${config_file} already exists"
        # Check if password matches
        CURRENT_PASS=$(python3 -c "import json; print(json.load(open('${config_file}'))['password'])" 2>/dev/null || echo "")
        if [ "$CURRENT_PASS" != "$pass" ]; then
            warn "Password mismatch in ${config_file}. Regenerating."
        else
            return 0
        fi
    fi

    mkdir -p /etc/shadowsocks

    cat > "$config_file" <<EOF
{
  "server": "0.0.0.0",
  "server_port": ${port},
  "password": "${pass}",
  "method": "aes-256-gcm",
  "mode": "${mode}",
  "fast_open": true,
  "no_delay": true
}
EOF
    log "✓ Created ${config_file} (port ${port}, mode ${mode})"
}

# ── Create systemd service ──
write_service() {
    local tier="$1"
    local desc="$2"
    local extra_execstart="${3:-}"
    local extra_deps="${4:-}"
    local post_start="${5:-}"

    local service_file="/etc/systemd/system/shadowsocks-${tier}.service"

    if [ -f "$service_file" ]; then
        log "Service ${service_file} already exists"
        return 0
    fi

    cat > "$service_file" <<SERVICE
[Unit]
Description=Shadowsocks Server (${desc})
After=network.target${extra_deps}
${extra_deps:+Requires=${extra_deps}}

[Service]
Type=simple
ExecStart=${SS_BINARY} -c /etc/shadowsocks/${tier}.json
${extra_execstart}
Restart=on-failure
RestartSec=5
${post_start:+ExecStartPost=${post_start}}

[Install]
WantedBy=multi-user.target
SERVICE
    log "✓ Created ${service_file}"
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════

install_ssserver
setup_passwords

mkdir -p /etc/shadowsocks

# ── Eco (BBR, TCP only, 5 Mbps tc) ──
write_config "eco" 8443 "ECO_PASS" "tcp_only"
write_service "eco" "Eco — BBR, 5 Mbps tc cap"

# ── Stealth (BBR, TCP only, 100 Mbps tc) ──
write_config "stealth" 8444 "STEALTH_PASS" "tcp_only"
write_service "stealth" "Stealth — BBR, 100 Mbps tc cap"

# ── Strike (BBR, TCP+UDP, 200 Mbps tc) ──
write_config "strike" 8445 "STRIKE_PASS" "tcp_and_udp"
write_service "strike" "Strike — BBR, 200 Mbps tc cap, UDP"

# ── Reload systemd ──
systemctl daemon-reload

# ── Enable services (started by setup.sh after all modules complete) ──
systemctl enable shadowsocks-eco.service 2>/dev/null || true
systemctl enable shadowsocks-stealth.service 2>/dev/null || true
systemctl enable shadowsocks-strike.service 2>/dev/null || true
log "   Services enabled (will start after all modules complete)"
log "   Eco:     :8443 (TCP, BBR)"
log "   Stealth: :8444 (TCP, BBR)"
log "   Strike:  :8445 (TCP+UDP, BBR)"
log ""
log "   Passwords saved to ${PASS_FILE} (chmod 600)"
exit 0
