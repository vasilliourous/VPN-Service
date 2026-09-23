#!/usr/bin/env bash
# hooks-sync.sh — deploy the repo's PocketBase hooks to the live hub, and verify.
#
# WHY THIS EXISTS
# ---------------
# Hooks have been deployed by hand (scp, then cp) for every change so far, and
# the drift that produces is not theoretical. Found 2026-09-19: release.pb.js
# had been written, committed, reviewed — and never copied to the host. The
# credential-free update path (GET /api/release, the one route that works when a
# client cannot activate) returned 404 in production, and nothing anywhere said
# so, because there was no step whose job was to compare the repo against the
# running server.
#
# Two sources of truth with no reconciliation is what made that possible. This
# script makes the REPO canonical: it uploads what is committed, tells you what
# differed, and then proves the result works rather than assuming the restart
# succeeded.
#
# WHAT IT DOES
#   1. Compares each server/pb_hooks/*.pb.js against the live
#      /opt/pocketbase/pb_hooks/ copy (sha256), and reports the diff.
#   2. Uploads only what changed, to a temp name, then moves it into place
#      atomically — so a half-written hook is never loaded.
#   3. Restarts pocketbase and waits for it to accept connections.
#   4. Verifies: the service is active, the journal has no new errors, and a
#      real request to GET /api/release returns 200 with valid JSON.
#
# Step 4 is the point. A restart that leaves the hub broken is currently
# indistinguishable from a successful deploy until a student complains.
#
# Usage:
#   server/scripts/hooks-sync.sh                # sync + restart + verify
#   server/scripts/hooks-sync.sh --dry-run      # show the diff, change nothing
#   server/scripts/hooks-sync.sh --no-restart   # upload only
#   server/scripts/hooks-sync.sh --check        # verify only, upload nothing
#
# Environment:
#   VPS      ssh target (default root@networkingguides.duckdns.org)
#   PB_API   hub base URL for the liveness probe (default the duckdns host)
#   SSH      ssh command to use. Defaults to plain `ssh`; set it to
#            "sshpass -e ssh" (or similar) on a host with no key-based access.
#            The script does not invent credentials.
#
# Exit codes: 0 success, 1 usage/validation, 2 transfer failure, 3 restart
#             failure, 4 verification failure.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HOOKS_SRC="${REPO_ROOT}/server/pb_hooks"

VPS="${VPS:-root@networkingguides.duckdns.org}"
PB_API="${PB_API:-https://networkingguides.duckdns.org}"
SSH="${SSH:-ssh}"
REMOTE_HOOKS="/opt/pocketbase/pb_hooks"
DRY_RUN=0
DO_RESTART=1
CHECK_ONLY=0

log()  { printf '\033[0;32m[hooks]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[hooks][WARN]\033[0m %s\n' "$*"; }
fail() {
    local code=1
    if [ "$#" -gt 1 ]; then
        case "${!#}" in
            [0-9]|[0-9][0-9]) code="${!#}"; set -- "${@:1:$#-1}" ;;
        esac
    fi
    printf '\033[0;31m[hooks][FAIL]\033[0m %b\n' "$*" >&2
    exit "$code"
}

for arg in "$@"; do
    case "$arg" in
        --dry-run)    DRY_RUN=1 ;;
        --no-restart) DO_RESTART=0 ;;
        --check)      CHECK_ONLY=1 ;;
        -h|--help)    sed -n '2,45p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *)            fail "unknown option: $arg" ;;
    esac
done

[ -d "$HOOKS_SRC" ] || fail "hook source dir not found: ${HOOKS_SRC}"
shopt -s nullglob
HOOKS=("${HOOKS_SRC}"/*.pb.js)
shopt -u nullglob
[ "${#HOOKS[@]}" -gt 0 ] || fail "no *.pb.js files in ${HOOKS_SRC}"

# ssh wrapper so a single remote command is spelled the same everywhere, and so
# a custom transport (SSH="sshpass -e ssh") needs no branching at each call site.
remote() { $SSH -o StrictHostKeyChecking=accept-new "$VPS" "$@"; }

# ── Verification (also used standalone with --check) ───────────────────────
verify() {
    local rc=0

    log "Verifying the hub…"

    # 1. The service must be running. `is-active` is the cheapest honest signal.
    local active
    active="$(remote 'systemctl is-active pocketbase' 2>/dev/null || true)"
    if [ "$active" = "active" ]; then
        log "  ✓ pocketbase is active"
    else
        warn "  ✗ pocketbase is '${active:-unknown}'"
        rc=4
    fi

    # 2. The hook files on disk must match the repo. This is the check whose
    #    absence let release.pb.js sit undeployed.
    local drift=0
    for f in "${HOOKS[@]}"; do
        local base local_sha remote_sha
        base="$(basename "$f")"
        local_sha="$(sha256sum "$f" | awk '{print $1}')"
        remote_sha="$(remote "sha256sum '${REMOTE_HOOKS}/${base}' 2>/dev/null | awk '{print \$1}'" 2>/dev/null || true)"
        if [ "$local_sha" != "$remote_sha" ]; then
            warn "  ✗ ${base} differs (repo ${local_sha:0:12}… host ${remote_sha:0:12}…)"
            drift=1
            rc=4
        else
            log "  ✓ ${base}"
        fi
    done

    # 3. A hook that fails to parse does not stop PocketBase from starting — it
    #    just makes the route 404 or 500. So probe a real route that a hook owns.
    #    /api/release is the right canary: it needs no credentials and it was the
    #    route that was silently missing.
    local body code
    body="$(curl -s -m 15 -o - -w '\n%{http_code}' "${PB_API}/api/release" 2>/dev/null || echo -e '\n000')"
    code="$(printf '%s' "$body" | tail -1)"
    body="$(printf '%s' "$body" | sed '$d')"
    case "$code" in
        200)
            if printf '%s' "$body" | grep -q '"ok"'; then
                log "  ✓ GET /api/release -> 200 (${body:0:80}…)"
            else
                warn "  ✗ GET /api/release returned 200 but not the expected JSON: ${body:0:120}"
                rc=4
            fi ;;
        *)
            warn "  ✗ GET /api/release -> HTTP ${code}"
            warn "    A missing or unparsable release.pb.js looks exactly like this."
            rc=4 ;;
    esac

    if [ "$drift" = "1" ]; then
        warn "  Hook drift detected — re-run without --check to deploy."
    fi
    return "$rc"
}

if [ "$CHECK_ONLY" = "1" ]; then
    verify || exit $?
    log "✓ Hooks are in sync and the hub is serving"
    exit 0
fi

# ── Compare, then upload what changed ──────────────────────────────────────
log "Comparing ${#HOOKS[@]} hooks against ${VPS}:${REMOTE_HOOKS}"

CHANGED=()
UNCHANGED=0
MISSING=()
for f in "${HOOKS[@]}"; do
    base="$(basename "$f")"
    local_sha="$(sha256sum "$f" | awk '{print $1}')"
    remote_sha="$(remote "sha256sum '${REMOTE_HOOKS}/${base}' 2>/dev/null | awk '{print \$1}'" 2>/dev/null || true)"
    if [ -z "$remote_sha" ]; then
        MISSING+=("$base")
        CHANGED+=("$f")
    elif [ "$local_sha" != "$remote_sha" ]; then
        CHANGED+=("$f")
    else
        UNCHANGED=$((UNCHANGED + 1))
    fi
done

if [ "${#MISSING[@]}" -gt 0 ]; then
    warn "not deployed at all: ${MISSING[*]}"
fi
log "${#CHANGED[@]} changed, ${UNCHANGED} already current"

if [ "${#CHANGED[@]}" -eq 0 ]; then
    log "Nothing to upload."
    [ "$DO_RESTART" = "1" ] && { log "Restarting to pick up any in-place edits…"; remote 'systemctl restart pocketbase'; sleep 2; }
else
    for f in "${CHANGED[@]}"; do
        log "  ↑ $(basename "$f")"
    done
fi

if [ "$DRY_RUN" = "1" ]; then
    warn "DRY_RUN — not uploading, not restarting"
    exit 0
fi

[ "${#CHANGED[@]}" -gt 0 ] || { verify || exit $?; exit 0; }

# Upload to a temp name, then move atomically and fix ownership. PocketBase
# watches this directory; writing in place would let it load a partial file.
for f in "${CHANGED[@]}"; do
    base="$(basename "$f")"
    if ! $SSH -o StrictHostKeyChecking=accept-new "$VPS" "cat > '${REMOTE_HOOKS}/${base}.uploading'" < "$f"; then
        fail "upload failed for ${base}" 2
    fi
    remote "mv '${REMOTE_HOOKS}/${base}.uploading' '${REMOTE_HOOKS}/${base}' && chown pocketbase:pocketbase '${REMOTE_HOOKS}/${base}' && chmod 644 '${REMOTE_HOOKS}/${base}'" \
        || fail "could not finalise ${base}" 2
done
log "✓ ${#CHANGED[@]} hook(s) uploaded"

# ── Restart ────────────────────────────────────────────────────────────────
# Hooks are loaded at startup. Some PocketBase builds also reload on file change,
# but relying on that is how "it worked when I tested it" happens.
if [ "$DO_RESTART" = "1" ]; then
    # Record the journal position so the error check below looks only at the
    # restart we just performed, not at hours of unrelated history.
    local_since="$(date -u '+%Y-%m-%d %H:%M:%S')"
    log "Restarting pocketbase…"
    remote 'systemctl restart pocketbase' || fail "restart command failed" 3

    # Wait for it to answer rather than sleeping a fixed amount: on a small
    # droplet the restart time varies and a fixed sleep is either flaky or slow.
    for _ in $(seq 1 20); do
        if remote 'curl -fsS -m 3 -o /dev/null http://127.0.0.1:8090/api/health' 2>/dev/null; then
            log "  ✓ pocketbase is answering on 127.0.0.1:8090"
            break
        fi
        sleep 1
    done

    # New errors in the journal since the restart. Warnings are ignored: this
    # build logs benign ones on every start and failing on them would train
    # people to ignore the check.
    errs="$(remote "journalctl -u pocketbase --since '${local_since}' --no-pager -p err -q 2>/dev/null | wc -l" || echo 0)"
    errs="$(printf '%s' "$errs" | tr -dc '0-9')"
    if [ "${errs:-0}" -gt 0 ]; then
        warn "pocketbase logged ${errs} error line(s) since the restart:"
        remote "journalctl -u pocketbase --since '${local_since}' --no-pager -p err -q" | tail -20 | sed 's/^/    /'
    else
        log "  ✓ no new errors in the journal"
    fi
else
    warn "--no-restart: hooks are on disk but the running process has not loaded them"
fi

# ── Verify ─────────────────────────────────────────────────────────────────
verify || fail "deployed, but the hub did not pass verification. Do not treat this as a successful deploy." 4

log "✓ Hooks in sync and the hub is serving"
