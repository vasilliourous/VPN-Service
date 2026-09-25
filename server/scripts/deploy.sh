#!/usr/bin/env bash
# deploy.sh — put the repo's server state onto the live hub, in one command.
#
# WHAT THIS REPLACES
#
# Deploying used to mean knowing which of six scripts to run, in what order, with
# which environment variables — and several of them defaulted to the production
# host, so a mistake aimed them at production silently. This does the whole job:
#
#   1. hooks    -> /opt/pocketbase/pb_hooks/   (diffed, uploaded atomically, restarted)
#   2. console  -> /var/www/admin/             (built, uploaded, verified)
#   3. staging  -> /root/server/               (so a later setup.sh re-run does not revert)
#   4. verify   -> health, the update endpoint, and hook drift
#
# It is IDEMPOTENT and DIFF-BASED: files whose content already matches are not
# uploaded, and nothing is restarted unless something actually changed.
#
# SAFETY RULES, learned the hard way on this project:
#
#   * A hook is uploaded to a `.new` name and then `install`-ed into place, so a
#     half-written hook is never loaded by the running server.
#   * Ownership is restored to `pocketbase:pocketbase` — a hook left as root is
#     readable but the service cannot rewrite its own data directory cleanly.
#   * A NEW hook file needs a RESTART to register; an EDITED one hot-reloads but
#     is restarted anyway, because "did it reload?" is not a thing an operator
#     should have to reason about.
#   * Runtime state is never touched: no records are written, no update_config
#     row is edited, no credentials are regenerated. Deploying code must not be
#     able to withdraw a release or reset a rollout.
#
# USAGE
#
#   server/scripts/deploy.sh                 # everything
#   server/scripts/deploy.sh --hooks         # hooks only
#   server/scripts/deploy.sh --console       # console only
#   server/scripts/deploy.sh --check         # dry run: report drift, change nothing
#   server/scripts/deploy.sh --no-console    # skip the console build (slow)
#
# Environment:
#   VPS      ssh target (default: the `locus-hub` alias, then the duckdns host)
#   PB_API   hub base URL for verification
#
# Exit codes: 0 success, 1 usage, 2 transfer failure, 3 restart failure,
#             4 verification failure.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOOKS_SRC="${REPO_ROOT}/server/pb_hooks"
CONSOLE_DIR="${REPO_ROOT}/server/console"
STAGING_SRC="${REPO_ROOT}/server"

# Prefer the `locus-hub` ssh alias, which carries the deploy key and needs no
# password. Fall back to the bare host so this still works on a machine that has
# key auth set up differently.
if [ -z "${VPS:-}" ]; then
    if ssh -o BatchMode=yes -o ConnectTimeout=5 locus-hub true 2>/dev/null; then
        VPS="locus-hub"
    else
        VPS="root@networkingguides.duckdns.org"
    fi
fi
PB_API="${PB_API:-https://networkingguides.duckdns.org}"

REMOTE_HOOKS="/opt/pocketbase/pb_hooks"
REMOTE_CONSOLE="/var/www/admin"
REMOTE_STAGING="/root/server"

DO_HOOKS=1
DO_CONSOLE=1
DO_STAGING=1
CHECK_ONLY=0

for arg in "$@"; do
    case "$arg" in
        --hooks)      DO_CONSOLE=0; DO_STAGING=0 ;;
        --console)    DO_HOOKS=0; DO_STAGING=0 ;;
        --no-console) DO_CONSOLE=0 ;;
        --staging)    DO_HOOKS=0; DO_CONSOLE=0 ;;
        --check)      CHECK_ONLY=1 ;;
        -h|--help)    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $arg" >&2; exit 1 ;;
    esac
done

# ── output ──
if [ -t 1 ]; then
    B=$'\033[1m'; G=$'\033[0;32m'; Y=$'\033[1;33m'; R=$'\033[0;31m'; N=$'\033[0m'
else
    B=''; G=''; Y=''; R=''; N=''
fi
step() { printf '%s==>%s %s\n' "$B" "$N" "$*"; }
ok()   { printf '  %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '  %s!%s %s\n' "$Y" "$N" "$*"; }
die()  { printf '%s  ✗ %s%s\n' "$R" "$*" "$N" >&2; exit "${2:-1}"; }

remote() { ssh -o BatchMode=yes -o ConnectTimeout=15 "$VPS" "$@"; }

# ── preflight ──
step "Preflight"
remote true 2>/dev/null || die "cannot reach $VPS (is the deploy key installed?)" 2
ok "connected to $VPS"

ACTIVE=$(remote 'systemctl is-active pocketbase' 2>/dev/null || echo unknown)
[ "$ACTIVE" = "active" ] || warn "pocketbase is '$ACTIVE' — deploying anyway, but check after"
[ "$ACTIVE" = "active" ] && ok "pocketbase active"

# ── 1. hooks ──
if [ "$DO_HOOKS" = "1" ]; then
    step "PocketBase hooks"
    [ -d "$HOOKS_SRC" ] || die "no hooks directory at $HOOKS_SRC" 1

    # Compare by hash and upload only what differs, so a no-op deploy says so.
    remote "mkdir -p $REMOTE_HOOKS" || die "cannot create $REMOTE_HOOKS" 2
    live_listing=$(remote "cd $REMOTE_HOOKS && md5sum *.pb.js 2>/dev/null" || true)

    changed=0
    total=0
    for f in "$HOOKS_SRC"/*.pb.js; do
        name="$(basename "$f")"
        total=$((total + 1))
        local_hash=$(md5sum "$f" | awk '{print $1}')
        live_hash=$(printf '%s\n' "$live_listing" | awk -v n="$name" '$2 == n {print $1}')

        if [ "$local_hash" = "$live_hash" ]; then
            ok "$name (unchanged)"
            continue
        fi

        if [ "$CHECK_ONLY" = "1" ]; then
            warn "$name would be updated"
            changed=$((changed + 1))
            continue
        fi

        # Upload to a temp name, verify the bytes arrived, then move into place.
        remote "cat > /tmp/$name.new" < "$f" || die "upload failed for $name" 2
        got=$(remote "md5sum /tmp/$name.new | awk '{print \$1}'")
        [ "$got" = "$local_hash" ] || die "$name arrived corrupted ($got != $local_hash)" 2

        remote "install -o pocketbase -g pocketbase -m 644 /tmp/$name.new $REMOTE_HOOKS/$name && rm -f /tmp/$name.new" \
            || die "could not install $name into place" 2
        ok "$name (updated)"
        changed=$((changed + 1))
    done

    # A hook deleted from the repo should be removed from the host. This is how
    # /api/hiddify disappears if someone re-adds then removes it.
    live_names=$(printf '%s\n' "$live_listing" | awk '{print $2}' | grep -v '^$' || true)
    for name in $live_names; do
        if [ ! -f "$HOOKS_SRC/$name" ]; then
            if [ "$CHECK_ONLY" = "1" ]; then
                warn "$name is on the host but not in the repo (would be removed)"
                changed=$((changed + 1))
            else
                remote "rm -f $REMOTE_HOOKS/$name" || die "could not remove $name" 2
                ok "$name (removed — not in the repo)"
                changed=$((changed + 1))
            fi
        fi
    done

    if [ "$CHECK_ONLY" = "1" ]; then
        [ "$changed" -eq 0 ] && ok "all $total hooks in sync" || warn "$changed hook change(s) pending"
    elif [ "$changed" -eq 0 ]; then
        ok "all $total hooks already in sync — no restart needed"
    else
        step "Restarting PocketBase"
        remote 'systemctl restart pocketbase' || die "restart failed" 3
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            sleep 1
            [ "$(remote 'systemctl is-active pocketbase' 2>/dev/null || echo no)" = "active" ] && break
        done
        [ "$(remote 'systemctl is-active pocketbase')" = "active" ] || die "pocketbase did not come back up" 3
        ok "restarted and active"

        # A hook that throws on load leaves the hub serving 500s, so check.
        errs=$(remote "journalctl -u pocketbase --since '2 minutes ago' --no-pager | grep -ciE 'error|panic|failed to load' || true")
        if [ "${errs:-0}" -gt 0 ]; then
            warn "$errs error line(s) since restart:"
            remote "journalctl -u pocketbase -n 20 --no-pager | grep -iE 'error|panic' | tail -5" || true
        else
            ok "no hook errors in the journal"
        fi
    fi
fi

# ── 2. console ──
if [ "$DO_CONSOLE" = "1" ]; then
    step "Admin console"
    if [ "$CHECK_ONLY" = "1" ]; then
        ok "(skipped in --check: builds are slow)"
    else
        [ -d "$CONSOLE_DIR" ] || die "no console directory at $CONSOLE_DIR" 1

        # Build. `pnpm run build` re-validates dependencies first and can fail on
        # a machine whose install was pruned; vite is invoked directly for the
        # actual build so a dependency-status quirk cannot block a deploy.
        ( cd "$CONSOLE_DIR" && pnpm install --silent 2>/dev/null || true )
        if ! ( cd "$CONSOLE_DIR" && node node_modules/vite/bin/vite.js build >/tmp/console-build.log 2>&1 ); then
            tail -20 /tmp/console-build.log >&2
            die "console build failed (see /tmp/console-build.log)" 4
        fi
        [ -f "$CONSOLE_DIR/dist/index.html" ] || die "console build produced no index.html" 4
        ok "built"

        # Tar, upload, extract. Uploading a tarball rather than a file tree keeps
        # this to one transfer and makes the swap atomic-ish.
        tar -czf /tmp/console-dist.tar.gz -C "$CONSOLE_DIR/dist" .
        remote "mkdir -p $REMOTE_CONSOLE" || die "cannot reach $REMOTE_CONSOLE" 2
        remote "cat > /tmp/console-dist.tar.gz" < /tmp/console-dist.tar.gz || die "console upload failed" 2

        # Upload to the staging copy too, so a later setup.sh re-run does not
        # silently revert the console to an older build.
        remote "mkdir -p $REMOTE_STAGING/console" >/dev/null 2>&1 || true
        remote "cp /tmp/console-dist.tar.gz $REMOTE_STAGING/console-dist.tar.gz 2>/dev/null || true" >/dev/null 2>&1 || true

        remote "tar -xzf /tmp/console-dist.tar.gz -C $REMOTE_CONSOLE && rm -f /tmp/console-dist.tar.gz" \
            || die "console extract failed" 2
        remote "chown -R caddy:caddy $REMOTE_CONSOLE 2>/dev/null || true" >/dev/null 2>&1 || true
        ok "deployed to $REMOTE_CONSOLE"

        # Verify the served bundle, not just the file on disk: Caddy is what
        # users actually hit.
        served=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$PB_API/admin/" || echo 000)
        [ "$served" = "200" ] && ok "served at $PB_API/admin/ (200)" || warn "admin returned $served"
    fi
fi

# ── 3. staging copy ──
# `setup.sh` deploys from /root/server/, NOT from the repo. Leaving it stale is
# how a re-run silently reverts hooks to an older version.
if [ "$DO_STAGING" = "1" ]; then
    step "Staging copy (/root/server)"
    if [ "$CHECK_ONLY" = "1" ]; then
        ok "(skipped in --check)"
    else
        tar -czf /tmp/staging.tar.gz -C "$STAGING_SRC" \
            --exclude=console/node_modules --exclude=console/dist --exclude=__pycache__ .
        remote "mkdir -p $REMOTE_STAGING" || die "cannot create $REMOTE_STAGING" 2
        remote "cat > /tmp/staging.tar.gz" < /tmp/staging.tar.gz || die "staging upload failed" 2
        remote "tar -xzf /tmp/staging.tar.gz -C $REMOTE_STAGING && rm -f /tmp/staging.tar.gz" \
            || die "staging extract failed" 2
        ok "repo synced to $REMOTE_STAGING"
    fi
fi

# ── 4. verify ──
step "Verifying"
if [ "$CHECK_ONLY" = "1" ]; then
    warn "dry run — no verification (nothing was changed)"
    exit 0
fi

fail=0

health=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$PB_API/api/health" || echo 000)
[ "$health" = "200" ] && ok "hub health 200" || { warn "hub health $health"; fail=1; }

# The update endpoint: any of 200/204/400 means it is registered and answering.
# A 404 means the hook is not deployed, which is worth failing on.
upd=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$PB_API/api/update?version=0.0.1&platform=linux" || echo 000)
case "$upd" in
    200|204) ok "/api/update reachable ($upd)" ;;
    404)     warn "/api/update returned 404 — update.pb.js is not deployed"; fail=1 ;;
    *)       warn "/api/update returned $upd"; fail=1 ;;
esac

# The public manifest, which the client uses as a fallback.
rel=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$PB_API/api/release" || echo 000)
[ "$rel" = "200" ] && ok "/api/release reachable" || { warn "/api/release returned $rel"; fail=1; }

# Anything a client's heartbeat needs, so a hook error does not go unnoticed.
hb=$(curl -s -o /dev/null -w '%{http_code}' -m 20 -X POST "$PB_API/api/heartbeat" \
    -H 'Content-Type: application/json' -d '{"code":"RQ-ZVY6-7NSD-X9X2-T","fingerprint":"verifyfp0000000000000000"}' || echo 000)
case "$hb" in
    200|404) ok "/api/heartbeat reachable ($hb)" ;;
    *)       warn "/api/heartbeat returned $hb"; fail=1 ;;
esac

# Hook drift, computed the same way as the deploy so it cannot disagree.
drift=$(remote "cd $REMOTE_HOOKS 2>/dev/null && md5sum *.pb.js 2>/dev/null" || true)
drifted=0
for f in "$HOOKS_SRC"/*.pb.js; do
    name="$(basename "$f")"
    l=$(md5sum "$f" | awk '{print $1}')
    r=$(printf '%s\n' "$drift" | awk -v n="$name" '$2 == n {print $1}')
    [ "$l" = "$r" ] || { warn "$name differs from the repo"; drifted=$((drifted + 1)); }
done
[ "$drifted" -eq 0 ] && ok "hooks in sync with the repo" || fail=1

echo
if [ "$fail" -eq 0 ]; then
    printf '%sDeploy complete.%s\n' "$G" "$N"
    echo "  Admin console: $PB_API/admin/"
else
    printf '%sDeploy finished with warnings — review the lines above.%s\n' "$Y" "$N"
    exit 4
fi
