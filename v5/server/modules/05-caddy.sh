#!/usr/bin/env bash
# Module 05: Caddy Reverse Proxy
# Installs Caddy with the caddy-ratelimit plugin and deploys Caddyfile with:
#   - TLS via Let's Encrypt
#   - Rate limiting zones (fingerprint-keyed for activation)
#   - Reverse proxy to PocketBase (127.0.0.1:8090)
#   - Static file serving for update.json
#
# Installation strategy:
#  1. Install base Caddy from official APT repo (for systemd integration)
#  2. Check if rate_limit module exists
#  3. If missing, build custom Caddy with xcaddy (adds caddy-ratelimit plugin)
#  4. Replace the binary with the custom build
set -euo pipefail

log()  { echo "[05-caddy] $*"; }
warn() { echo "[05-caddy][WARN] $*"; }
fail() { echo "[05-caddy][FAIL] $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATES_DIR="$(dirname "$SCRIPT_DIR")/templates"
CADDYFILE="/etc/caddy/Caddyfile"
CADDY_DATA_DIR="/var/www/html"

: "${DOMAIN:?DOMAIN is required}"

# ── Install or rebuild Caddy with rate_limit support ──
install_caddy() {
    # Check if Caddy already has rate_limit
    if command -v caddy &>/dev/null; then
        local ver
        ver=$(caddy version 2>/dev/null | head -1 | cut -d' ' -f1 || echo "unknown")
        log "Caddy already installed (${ver})"
        if [ "$(caddy list-modules 2>/dev/null | grep -c 'http.handlers.rate_limit')" -gt 0 ]; then
            log "✓ rate_limit module present"
            return 0
        fi
        log "rate_limit module missing. Will download custom build..."
    else
        log "Caddy not found. Will download custom build with rate_limit..."
    fi

    # Download pre-built Caddy with ratelimit plugin from official API
    # This is MUCH faster than building from source with xcaddy
    log "Downloading Caddy with ratelimit plugin from caddyserver.com..."

    local tmpfile
    tmpfile=$(mktemp /tmp/caddy-XXXXXX)

    # NOTE: the caddyserver.com download API ignores the version param and
    # serves the latest release (observed 2026-08-14: requested 2.8.4, got
    # 2.11.4), and occasionally returns a build WITHOUT the plugin (same
    # date). The xcaddy fallback below makes this deterministic.
    CADDY_VERSION="2.8.4"
    if ! curl -sL "https://caddyserver.com/api/download?os=linux&arch=amd64&p=github.com/mholt/caddy-ratelimit&version=${CADDY_VERSION}" \
        -o "$tmpfile"; then
        rm -f "$tmpfile"
        fail "Failed to download Caddy with ratelimit plugin"
    fi

    # Verify it's a valid binary
    if ! file "$tmpfile" | grep -q "ELF"; then
        rm -f "$tmpfile"
        fail "Downloaded file is not a valid ELF binary"
    fi

    chmod +x "$tmpfile"
    local ver
    ver=$("$tmpfile" version 2>/dev/null | head -1 || echo "unknown")
    log "Downloaded Caddy ${ver}"

    # Verify ratelimit module is included
    # NOTE: use grep -c (not -q) — under `set -euo pipefail`, grep -q closes
    # the pipe after the first match, caddy gets SIGPIPE, and pipefail turns
    # the whole check into a FALSE NEGATIVE (hit 2026-08-14: a good binary
    # was rejected and setup fell back/aborted).
    if [ "$("$tmpfile" list-modules 2>/dev/null | grep -c 'http.handlers.rate_limit')" -eq 0 ]; then
        log "API build lacks rate_limit module — falling back to xcaddy build (deterministic)..."
        rm -f "$tmpfile"
        if command -v go &>/dev/null; then
            local xcaddy_bin xcaddy_dir
            xcaddy_bin=$(mktemp /tmp/caddy-xcaddy-XXXXXX)
            xcaddy_dir="$(dirname "$xcaddy_bin")"
            log "Building Caddy ${CADDY_VERSION} + caddy-ratelimit with xcaddy (one-time, ~2-4 min)..."
            if GOBIN="$xcaddy_dir" go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest 2>/dev/null \
                && "$xcaddy_dir/xcaddy" build "v${CADDY_VERSION}" --with github.com/mholt/caddy-ratelimit -o "$xcaddy_bin" 2>/dev/null; then
                if [ "$("$xcaddy_bin" list-modules 2>/dev/null | grep -c 'http.handlers.rate_limit')" -gt 0 ]; then
                    log "✓ xcaddy build has rate_limit (v${CADDY_VERSION})"
                    tmpfile="$xcaddy_bin"
                else
                    rm -f "$xcaddy_bin"
                    fail "xcaddy build succeeded but lacks rate_limit module"
                fi
            else
                rm -f "$xcaddy_bin"
                fail "API build lacked rate_limit AND xcaddy build failed. Install manually: xcaddy build v${CADDY_VERSION} --with github.com/mholt/caddy-ratelimit"
            fi
        else
            fail "API build lacked rate_limit and go/xcaddy is unavailable. Install manually: xcaddy build v${CADDY_VERSION} --with github.com/mholt/caddy-ratelimit"
        fi
    fi
    log "✓ rate_limit module confirmed in downloaded binary"

    # Replace the installed Caddy binary
    systemctl stop caddy 2>/dev/null || true
    cp "$tmpfile" /usr/bin/caddy
    chmod +x /usr/bin/caddy
    rm -f "$tmpfile"

    log "✓ Caddy replaced with ratelimit-enabled version: $(caddy version | head -1)"
}

# ── Deploy Caddyfile ──
deploy_caddyfile() {
    mkdir -p /etc/caddy "$CADDY_DATA_DIR"

    cat > "$CADDYFILE" << CADDY
# MyVPN Caddyfile — generated by modular setup
# Domain: ${DOMAIN}

{
    # rate_limit is configured per-handle below
}

# ── Main site block ──
${DOMAIN} {
    root * ${CADDY_DATA_DIR}
    file_server

    # ── Security headers ──
    header {
        -Server
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        Referrer-Policy "no-referrer"
        Permissions-Policy "geolocation=(), microphone=(), camera=()"
    }

    # ── Activation endpoint (rate limited: 5 per 10 min per IP) ──
    handle /api/activate {
        rate_limit {
            zone activation {
                key {remote_host}
                events 5
                window 10m
            }
        }
        reverse_proxy 127.0.0.1:8090
    }

    # ── Heartbeat endpoint (rate limited: 1 per 10s per IP) ──
    handle /api/heartbeat {
        rate_limit {
            zone heartbeat {
                key {remote_host}
                events 1
                window 10s
            }
        }
        reverse_proxy 127.0.0.1:8090
    }

    # ── Admin console ──
    # The console is a static SPA served at /admin/. `/admin` (no slash) is
    # redirected so a typed URL or bookmark does not 404.
    #
    # Ordering matters: the more specific /api/admin/upload handle must appear
    # BEFORE the general /api/* block, or the generic rate-limited proxy would
    # swallow uploads and reject them at the 100-requests/10s limit.
    # `/admin` (no trailing slash) redirects to `/admin/`. Using an exact-path
    # matcher inside `handle` proved unreliable here — the file_server handle
    # below is a handle_path and won the match. A `route` with an explicit
    # path matcher is unambiguous.
    @admin_root path /admin
    redir @admin_root /admin/ permanent

    handle_path /admin/* {
        root * /var/www/admin
        header {
            # The console is an internal tool: never index, never cache the
            # shell (so a redeploy is picked up immediately).
            X-Robots-Tag "noindex, nofollow"
            Cache-Control "no-cache"
            X-Content-Type-Options "nosniff"
            X-Frame-Options "DENY"
        }
        # SPA fallback: any unknown path under /admin/ serves index.html so a
        # refresh on a sub-view still loads the app.
        try_files {path} /index.html
        file_server
    }

    # Large release uploads go to the dedicated uploader, NOT PocketBase
    # (which rejects bodies above a few MB and exposes no multipart API).
    handle /api/admin/upload {
        reverse_proxy 127.0.0.1:8091
    }

    # ── General API (rate limited: 100 per 10s per IP) ──
    handle /api/* {
        rate_limit {
            zone api_default {
                key {remote_host}
                events 100
                window 10s
            }
        }
        reverse_proxy 127.0.0.1:8090
    }

    # ── PocketBase admin UI (no rate limit) ──
    handle /_/* {
        reverse_proxy 127.0.0.1:8090
    }

    # ── Static files ──
    handle /update.json {
        file_server
    }

    # ── Release binaries for the auto-updater ──
    # Layout on disk: /var/www/updates/<version>/<filename>, e.g.
    #   /var/www/updates/1.1.0/locus-linux-amd64
    #   /var/www/updates/1.1.0/locus-linux-amd64.sha256
    # handle_path strips the /updates/ prefix, so root points AT the releases
    # directory. Directory browsing stays off: clients never need to enumerate
    # builds, and listings would expose the naming convention.
    # NOTE: this heredoc is UNQUOTED (it interpolates ${DOMAIN}), so no shell
    # substitution syntax may appear anywhere inside it — bash would execute it
    # as a command while writing the file. Keep comments backtick-free.
    handle_path /updates/* {
        root * /var/www/updates
        header {
            Cache-Control "public, max-age=300"
            X-Content-Type-Options "nosniff"
        }
        # Directory listing is off by default in Caddy; do NOT add
        # `browse <arg>` — Caddy 2 parses that argument as a TEMPLATE FILE
        # path, so both `browse false` and `browse off` fail at request time
        # with HTTP 500 ("open off: no such file or directory").
        file_server
    }

    # ── Logging ──
    log {
        output file /var/log/caddy/access.log
        format json
    }

    # ── TLS via Let's Encrypt ──
    tls {
        protocols tls1.2 tls1.3
        curves x25519 secp384r1
    }
}
CADDY
    log "✓ Caddyfile deployed to ${CADDYFILE}"
}

# ── Create update.json placeholder ──
deploy_update_json() {
    local update_file="${CADDY_DATA_DIR}/update.json"
    # Only write if missing or version changed
    if [ ! -f "$update_file" ]; then
        cat > "$update_file" <<'EOF'
{
  "version": "1.0.0",
  "rollout_percent": 0,
  "linux_amd64": null,
  "windows": null,
  "macos_intel": null,
  "macos_arm": null
}
EOF
        log "✓ Created placeholder update.json"
    else
        log "update.json already exists"
    fi
    chmod 644 "$update_file"
}

# ── Create systemd service (if not installed via APT) ──
create_systemd_service() {
    local service_file="/etc/systemd/system/caddy.service"
    if [ -f "$service_file" ]; then
        log "Caddy systemd service already exists"
        return 0
    fi

    # Create caddy user if not exists
    if ! id -u caddy &>/dev/null; then
        useradd --system --no-create-home --shell /usr/sbin/nologin caddy 2>/dev/null || true
    fi

    mkdir -p /var/lib/caddy /var/log/caddy /var/www/html /etc/caddy
    chown -R caddy:caddy /var/lib/caddy /var/log/caddy /var/www/html
    chmod 755 /var/www/html

    cat > "$service_file" << 'SERVICE'
[Unit]
Description=Caddy Web Server
Documentation=https://caddyserver.com/docs/
After=network.target network-online.target
Requires=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
ExecStart=/usr/bin/caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
TimeoutStopSec=5s
LimitNOFILE=1048576
LimitNPROC=512
PrivateTmp=true
Environment=XDG_CONFIG_HOME=/var/lib/caddy
Environment=XDG_DATA_HOME=/var/lib/caddy

[Install]
WantedBy=multi-user.target
SERVICE
    log "✓ Created systemd service: ${service_file}"

    # Grant capability to bind to low ports (<1024) as non-root
    setcap cap_net_bind_service=+ep /usr/bin/caddy 2>/dev/null && \
        log "✓ Granted cap_net_bind_service to caddy binary" || \
        warn "Could not set cap_net_bind_service (Caddy will need root to bind :80/:443)"
}

# ── Create the release-binaries directory ──
# Published update artifacts live here, served by the /updates/* handler above.
# Created unconditionally so a fresh deploy does not 404 the auto-updater before
# the first release is published. Ownership stays root:root and the directory is
# world-readable (0755) — these are public release binaries, but the deploy user
# needs write access to push new versions.
deploy_updates_dir() {
    local updates_dir="/var/www/updates"
    local admin_dir="/var/www/admin"
    mkdir -p "$updates_dir" "$admin_dir"
    chown root:root "$updates_dir" "$admin_dir"
    chmod 755 "$updates_dir" "$admin_dir"
    log "✓ Updates directory ready at ${updates_dir}"
    log "✓ Admin console directory ready at ${admin_dir}"
}

# ── Install the release upload service ──
# The console uploads multi-MB binaries; PocketBase rejects bodies above a few
# MB and does not expose multipart files to hooks, and this Caddy build has no
# upload handler. So a small dedicated service owns uploads, bound to
# 127.0.0.1 and reached only through Caddy at /api/admin/upload.
install_upload_service() {
    local unit="/etc/systemd/system/locus-upload.service"
    local tmpl
    tmpl="$(dirname "$SCRIPT_DIR")/templates/locus-upload.service"

    if [ ! -f "$tmpl" ]; then
        fail "templates/locus-upload.service not found — the release uploader cannot be installed.
     Release binaries are uploaded through it, so without it the console's
     Releases page cannot publish anything. Expected at ${tmpl}"
    fi
    # The uploader script itself must exist where the unit expects it.
    if [ ! -f /root/server/scripts/release_upload.py ]; then
        fail "scripts/release_upload.py not found at /root/server/scripts — the release
     uploader cannot be installed. Re-upload the server tree (scp -r v5/server)."
    fi

    # Only rewrite when the content differs, so a re-run does not restart a
    # service that is happily serving.
    if [ ! -f "$unit" ] || ! cmp -s "$tmpl" "$unit"; then
        cp "$tmpl" "$unit"
        systemctl daemon-reload
    fi
    systemctl enable locus-upload 2>/dev/null || true
    systemctl restart locus-upload 2>/dev/null || true
    sleep 1
    if systemctl is-active --quiet locus-upload; then
        log "✓ Upload service running on 127.0.0.1:8091"
    else
        fail "Upload service is NOT running — releases cannot be published.
     Check: journalctl -u locus-upload -n 20 --no-pager"
    fi
}

# ── Deploy the admin console bundle ──
# The console is part of the standard deployment, so a missing bundle is a
# deployment failure, not a detail: without it /admin/ 404s and every operator
# has to fall back to SSH + sqlite. It used to warn and continue, which made a
# half-deployed hub look finished.
#
# The bundle is a tarball at /root/server/console-dist.tar.gz, produced by
# scripts/deploy-console.sh (which builds it locally with npm and uploads it).
# We cannot build it here: the VPS has no node, and the console source is not
# shipped to the server tree.
deploy_console() {
    local bundle="/root/server/console-dist.tar.gz"
    local target="/var/www/admin"
    if [ ! -f "$bundle" ]; then
        if [ -f "$target/index.html" ]; then
            log "No console bundle provided — keeping the existing console at ${target}"
            return 0
        fi
        fail "Admin console bundle missing at ${bundle} and no console deployed.
     Fix: run  v5/server/scripts/deploy-console.sh  from your workstation
     (it builds the SPA locally and uploads the tarball), then re-run setup.sh.
     To deploy without the console, set SKIP_CONSOLE=1 (not recommended)."
    fi
    mkdir -p "$target"
    # Extract to a temp dir and verify BEFORE touching the live console. A
    # corrupt bundle extracted straight into /var/www/admin would leave a
    # half-written SPA (missing assets, stale index.html) — the console would
    # be broken in a way that looks like a code bug rather than a bad upload.
    local staging
    staging="$(mktemp -d /tmp/locus-console-XXXXXX)"
    if ! tar xzf "$bundle" -C "$staging" 2>/dev/null; then
        find "$staging" -type f -delete 2>/dev/null
        find "$staging" -depth -type d -empty -delete 2>/dev/null
        fail "Could not extract ${bundle} — the console bundle is corrupt.
     Re-run v5/server/scripts/deploy-console.sh to rebuild and re-upload it."
    fi
    # A bundle that extracts but has no entrypoint is still a broken console —
    # e.g. a tarball made from the wrong directory level (dist/ vs dist/*).
    if [ ! -f "$staging/index.html" ]; then
        find "$staging" -type f -delete 2>/dev/null
        find "$staging" -depth -type d -empty -delete 2>/dev/null
        fail "Console bundle has no index.html.
     The tarball was probably built from the wrong level — it must contain the
     CONTENTS of v5/console/dist/, not the dist/ directory itself."
    fi
    # The Vite base must be /admin/ or every asset 404s behind the subpath.
    if ! grep -q '/admin/assets/' "$staging/index.html"; then
        find "$staging" -type f -delete 2>/dev/null
        find "$staging" -depth -type d -empty -delete 2>/dev/null
        fail "Console bundle index.html does not reference /admin/assets/.
     Check 'base' in v5/console/vite.config.ts — it must be '/admin/'."
    fi
    # Verified — replace the live console.
    find "$target" -mindepth 1 -delete 2>/dev/null
    cp -a "$staging"/. "$target"/
    find "$staging" -type f -delete 2>/dev/null
    find "$staging" -depth -type d -empty -delete 2>/dev/null
    chown -R root:root "$target"
    chmod -R a+rX "$target"
    log "✓ Admin console deployed to ${target}"
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════

install_caddy
create_systemd_service
deploy_caddyfile
deploy_update_json
deploy_updates_dir
install_upload_service
if [ "${SKIP_CONSOLE:-0}" = "1" ]; then
    log "SKIP_CONSOLE=1 — not deploying the admin console (and not requiring its bundle)"
else
    deploy_console
fi

# ── Enable and start Caddy ──
systemctl daemon-reload
systemctl enable caddy 2>/dev/null || true
systemctl restart caddy 2>&1 | tail -5 || {
    warn "Caddy restart failed. Checking config..."
    caddy validate --config "$CADDYFILE" 2>&1 | tail -5 || true
    journalctl -u caddy -n 10 --no-pager 2>&1 | tail -5 || true
}

# ── Verify ──
sleep 2
if systemctl is-active --quiet caddy; then
    log "✓ Caddy is running"
    log "  Caddy version: $(caddy version 2>/dev/null | head -1)"
    if [ "$(caddy list-modules 2>/dev/null | grep -c 'http.handlers.rate_limit')" -gt 0 ]; then
        log "✓ rate_limit module confirmed active"
    fi
else
    warn "Caddy may not be running. Check: journalctl -u caddy -n 20 --no-pager"
    warn "  Config: ${CADDYFILE}"
    warn "  Test: caddy validate --config ${CADDYFILE}"
fi

log "✓ Caddy setup complete"
exit 0
