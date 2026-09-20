#!/usr/bin/env bash
# deploy-console.sh — build the admin console and push it to the live hub.
#
# Usage:
#   v5/server/scripts/deploy-console.sh
#   VPS=root@host v5/server/scripts/deploy-console.sh
#
# Why a script: the console is a static SPA that Caddy serves from
# /var/www/admin. Getting a new build there means "build, tar, upload, extract,
# reload" — four steps that are easy to get half-right, and a half-deployed
# console is a blank page. This does all four atomically and verifies the result.
#
# The bundle is uploaded to /root/server/console-dist.tar.gz so that re-running
# `modules/05-caddy.sh` on the host (e.g. during a full setup re-run) picks up
# the same version rather than reverting to whatever was there before.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CONSOLE_DIR="${REPO_ROOT}/v5/console"
VPS="${VPS:-root@networkingguides.duckdns.org}"
DOMAIN="${DOMAIN:-networkingguides.duckdns.org}"
REMOTE_BUNDLE="/root/server/console-dist.tar.gz"
REMOTE_DIR="/var/www/admin"

log()  { printf '\033[0;32m[console]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[console][WARN]\033[0m %s\n' "$*"; }
fail() { printf '\033[0;31m[console][FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

[ -d "$CONSOLE_DIR" ] || fail "console directory not found: $CONSOLE_DIR"

# ── Build ──
log "Building console in ${CONSOLE_DIR}"
cd "$CONSOLE_DIR"
if [ ! -d node_modules ]; then
    log "Installing dependencies (first run)"
    npm ci --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund
fi
npm run build || fail "console build failed"

[ -f dist/index.html ] || fail "build produced no dist/index.html"

# Sanity: the bundle must reference /admin/ assets, or it will 404 behind the
# /admin path. This is the single easiest thing to break in vite.config.ts.
if ! grep -q '/admin/assets/' dist/index.html; then
    fail "dist/index.html does not reference /admin/assets/ — check 'base' in vite.config.ts"
fi
log "✓ Build OK ($(du -sh dist | cut -f1))"

# ── Package ──
TARBALL="$(mktemp -t locus-console-XXXXXX.tar.gz)"
trap 'rm -f "$TARBALL"' EXIT
tar czf "$TARBALL" -C dist .
log "✓ Packaged $(basename "$TARBALL")"

# ── Upload ──
log "Uploading to ${VPS}"
scp -q "$TARBALL" "${VPS}:${REMOTE_BUNDLE}" || fail "upload failed"

ssh "$VPS" "
    set -e
    rm -rf '${REMOTE_DIR}'/* 2>/dev/null || true
    mkdir -p '${REMOTE_DIR}'
    tar xzf '${REMOTE_BUNDLE}' -C '${REMOTE_DIR}'
    chown -R root:root '${REMOTE_DIR}'
    chmod -R a+rX '${REMOTE_DIR}'
    systemctl reload caddy 2>/dev/null || systemctl restart caddy
" || fail "remote deploy failed"
log "✓ Deployed to ${REMOTE_DIR}"

# ── Verify ──
sleep 2
CODE=$(curl -s -o /dev/null -w '%{http_code}' "https://${DOMAIN}/admin/" || echo 000)
[ "$CODE" = "200" ] || fail "console not serving (HTTP ${CODE}) at https://${DOMAIN}/admin/"

ASSET=$(curl -s "https://${DOMAIN}/admin/" | grep -o '/admin/assets/[^"]*\.js' | head -1 || true)
if [ -n "$ASSET" ]; then
    ACODE=$(curl -s -o /dev/null -w '%{http_code}' "https://${DOMAIN}${ASSET}" || echo 000)
    [ "$ACODE" = "200" ] || fail "console asset ${ASSET} returned HTTP ${ACODE}"
    log "✓ Serving at https://${DOMAIN}/admin/ (asset OK)"
else
    warn "Could not find an asset reference to verify — check the console manually"
fi

log "Done. Open https://${DOMAIN}/admin/ and sign in with the admin token."
