#!/usr/bin/env bash
#
# enable-uot.sh — Idempotently install & start the sing-box UDP-over-TCP
# endpoint on an ALREADY-DEPLOYED Locus VPS, then advertise uot_port +
# udp_relay=true for the Strike tier so compatible clients (sing-box) route
# game/voice UDP over TCP.
#
# This is the "enact #7 / gaming UDP" step for an existing box. It shares the
# exact logic of server/modules/02-shadowsocks.sh (the optional ENABLE_UOT
# block) but is safe to run again on a live box without re-running the full
# setup (it will not touch the 8443/44/45 shadowsocks-* services, Caddy,
# PocketBase, tc, or backups).
#
# Usage:
#   # one-time, on the VPS from the repo copy:
#   UOT_PORT="${UOT_PORT:-8446}" \
#   SING_BOX_VERSION="${SING_BOX_VERSION:-v1.12.1}" \
#     bash server/scripts/enable-uot.sh
#
# Rollback:  systemctl disable --now sing-box-uot && rm -f /etc/sing-box/config.json
#
set -euo pipefail

log()  { echo "[enable-uot] $*"; }
err()  { echo "[enable-uot][ERROR] $*" >&2; exit 1; }
imply(){ echo "[enable-uot][run-me-after] $*"; }

UOT_PORT="${UOT_PORT:-8446}"
SING_BOX_VERSION="${SING_BOX_VERSION:-v1.12.1}"
SING_BOX_BINARY="${SING_BOX_BINARY:-/usr/local/bin/sing-box}"
STRIKE_CONF="${STRIKE_CONF:-/etc/shadowsocks/strike.json}"
CONFIG_FILE="${CONFIG_FILE:-/etc/sing-box/config.json}"
SERVICE_FILE="${SERVICE_FILE:-/etc/systemd/system/sing-box-uot.service}"

[ -f "$STRIKE_CONF" ] || err "strike config not found at $STRIKE_CONF — run the full setup (module 02) first"

# Read Strike password + method from the standard deployed JSON.
STRIKE_PASS="$(sed -n 's/.*"password"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$STRIKE_CONF" | head -1)"
STRIKE_METHOD="$(sed -n 's/.*"method"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$STRIKE_CONF" | head -1)"
[ -n "$STRIKE_PASS" ] || err "could not read password from $STRIKE_CONF"
[ -n "$STRIKE_METHOD" ] || STRIKE_METHOD="aes-256-gcm"
log "Using Strike creds (method ${STRIKE_METHOD}) on UoT port ${UOT_PORT}"

# ── 1. Install sing-box (server-side UoT peer) if missing ──
if ! [ -x "$SING_BOX_BINARY" ] || ! "$SING_BOX_BINARY" version >/dev/null 2>&1; then
  log "Downloading sing-box ${SING_BOX_VERSION}..."
  tmp="$(mktemp -d)"
  url="https://github.com/SagerNet/sing-box/releases/download/${SING_BOX_VERSION}/sing-box-${SING_BOX_VERSION#v}-linux-amd64.tar.gz"
  ( cd "$tmp" && wget -q "$url" -O sing-box.tar.gz && tar -xzf sing-box.tar.gz \
      && cp "sing-box-${SING_BOX_VERSION#v}-linux-amd64/sing-box" "$SING_BOX_BINARY" && chmod +x "$SING_BOX_BINARY" ) \
      || { rm -rf "$tmp"; err "sing-box download failed (TCP tiers untouched)"; }
  rm -rf "$tmp"
  log "✓ sing-box ${SING_BOX_VERSION} installed"
else
  log "sing-box already present: $($SING_BOX_BINARY version 2>&1 | head -1)"
fi

# ── 2. Write sing-box server config (shadowsocks inbound; UoT auto-handled) ──
mkdir -p "$(dirname "$CONFIG_FILE")"
cat > "$CONFIG_FILE" <<EOF
{
  "log": { "level": "info" },
  "inbounds": [
    {
      "type": "shadowsocks",
      "tag": "strike-uot",
      "listen": "::",
      "listen_port": ${UOT_PORT},
      "method": "${STRIKE_METHOD}",
      "password": "${STRIKE_PASS}"
    }
  ]
}
EOF
log "✓ wrote $CONFIG_FILE (port ${UOT_PORT}, method ${STRIKE_METHOD})"

# ── 3. systemd unit ──
cat > "$SERVICE_FILE" <<UNIT
[Unit]
Description=sing-box server (Strike UDP-over-TCP) — Locus #7
After=network.target

[Service]
Type=simple
ExecStart=$SING_BOX_BINARY run -c $CONFIG_FILE
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
log "✓ wrote $SERVICE_FILE"

# ── 4. Reload + enable ──
systemctl daemon-reload
systemctl enable sing-box-uot.service >/dev/null 2>&1 || true
systemctl restart sing-box-uot.service
sleep 1
systemctl is-active sing-box-uot.service >/dev/null || err "sing-box-uot.service not active"

# ── 5. Verify listening port ──
ss -ltnp 2>/dev/null | grep -q ":$UOT_PORT " && log "✓ listening on :${UOT_PORT}" \
  || log "note: couldn't confirm :$UOT_PORT with ss (check firewall/UFW allows it)."

log "Done. sing-box UoT endpoint active on :${UOT_PORT}."
imply "Make Strike advertise it to clients (udp_relay=true + uot_port=${UOT_PORT}) by re-running the seed:"
imply "  cd server && DOMAIN=\$DOMAIN python3 scripts/seed-pb.py   (or set via PocketBase tier_configs->Strike)"
imply "  Add VERIFY=1 to also run a throwaway end-to-end activation check."
imply "Then run a 5-minute UDP gaming/voice check and the client DNS-over-UoT probe (see docs/OPS.md #7 runbook)."
imply "Rollback:  systemctl disable --now sing-box-uot && rm -f $CONFIG_FILE"
