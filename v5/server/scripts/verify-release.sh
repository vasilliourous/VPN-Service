#!/usr/bin/env bash
# verify-release.sh — answer "is this release actually shippable?" in one command.
#
# WHY THIS EXISTS
# ---------------
# A release can look finished and not be. Every one of these has happened:
#
#   * the update_config row was written with empty download_* fields, so the hub
#     advertised a version with no platforms (FIXES.md 44);
#   * the hook that serves the release list was never deployed, so the public
#     route 404ed while the row looked fine (FIXES.md 43);
#   * artifacts were uploaded to one version directory and the row pointed at
#     another.
#
# Checking that by hand means SSHing in, querying sqlite, curling four URLs and
# comparing four hashes — five steps that each have to be right, which is how
# things get signed off unverified. This does all of them and exits non-zero if
# any disagree.
#
# It is READ-ONLY. It never uploads, never writes update_config, never restarts
# anything. Safe to run at any time, including on a release you are unsure about.
#
# Usage:
#   v5/server/scripts/verify-release.sh 2.2.1
#   v5/server/scripts/verify-release.sh 2.2.1 --local /path/to/artifacts
#
# Environment:
#   VPS      ssh target (default root@networkingguides.duckdns.org)
#   PB_API   hub base URL (default https://networkingguides.duckdns.org)
#   SSH      ssh command to use (default `ssh`)
#
# Exit codes: 0 everything checked out, 1 usage, 2 a check FAILED.
set -euo pipefail

VERSION="${1:-}"
case "$VERSION" in
    -h|--help)
        sed -n '2,35p' "$0" | sed 's/^# \{0,1\}//'
        exit 0 ;;
esac
if [ -z "$VERSION" ]; then
    echo "usage: $0 <version>   (e.g. 2.2.1)" >&2
    exit 1
fi
VERSION="${VERSION#v}"
case "$VERSION" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "error: '${VERSION}' does not look like a version" >&2; exit 1 ;;
esac

VPS="${VPS:-root@networkingguides.duckdns.org}"
PB_API="${PB_API:-https://networkingguides.duckdns.org}"
SSH="${SSH:-ssh}"
LOCAL_DIR=""
if [ "${2:-}" = "--local" ]; then LOCAL_DIR="${3:-}"; fi

REMOTE_UPDATES="/var/www/updates/${VERSION}"

pass=0; failed=0
ok()   { printf '\033[0;32m  ✓\033[0m %s\n' "$*"; pass=$((pass+1)); }
bad()  { printf '\033[0;31m  ✗\033[0m %s\n' "$*" >&2; failed=$((failed+1)); }
note() { printf '\033[1;33m    %s\033[0m\n' "$*"; }

remote() { $SSH -o StrictHostKeyChecking=accept-new "$VPS" "$@"; }

printf '\033[1mVerifying Locus %s\033[0m\n\n' "$VERSION"

# ── 1. The version directory exists on the host ────────────────────────────
echo "1. Artifacts on the host"
if remote "test -d '${REMOTE_UPDATES}'" 2>/dev/null; then
    ok "directory exists: ${REMOTE_UPDATES}"
    listing="$(remote "ls -1 '${REMOTE_UPDATES}'" 2>/dev/null || true)"
    while IFS= read -r line; do
        [ -n "$line" ] && note "$line"
    done <<<"$listing"
else
    bad "directory missing on the host: ${REMOTE_UPDATES}"
fi

# ── 2. The update_config row is complete ───────────────────────────────────
echo
echo "2. update_config row"
# Read the DB directly rather than through the admin API: this is a diagnostic,
# it must work even when the console or a hook is what is broken.
ROW="$(remote "python3 - <<'PY'
import sqlite3, json
try:
    c = sqlite3.connect('/opt/pocketbase/pb_data/data.db').cursor()
    c.execute('select version, rollout_percent, active, '
              'download_linux, download_windows, download_macos_intel, download_macos_arm, '
              'sha256_linux, sha256_windows, sha256_macos_intel, sha256_macos_arm '
              'from update_config where version = ?', ('${VERSION}',))
    r = c.fetchone()
    if not r:
        print(json.dumps({'found': False}))
    else:
        keys = ['version','rollout','active','dl_linux','dl_windows','dl_macos_intel','dl_macos_arm',
                'sha_linux','sha_windows','sha_macos_intel','sha_macos_arm']
        print(json.dumps(dict(zip(keys, r))))
except Exception as e:
    print(json.dumps({'error': str(e)}))
PY" 2>/dev/null || echo '{"error":"ssh failed"}')"

if printf '%s' "$ROW" | grep -q '"found": false'; then
    bad "no update_config row for version ${VERSION}"
    note "the hub is not advertising this release"
elif printf '%s' "$ROW" | grep -q '"error"'; then
    bad "could not read update_config: ${ROW}"
else
    ok "row found"
    # Every platform must have BOTH a URL and a hash. This is the check that
    # would have caught the empty-fields row.
    for pair in "dl_linux:sha_linux" "dl_windows:sha_windows" \
                "dl_macos_intel:sha_macos_intel" "dl_macos_arm:sha_macos_arm"; do
        url_key="${pair%%:*}"; sha_key="${pair#*:}"
        url="$(printf '%s' "$ROW" | python3 -c "import json,sys; print(json.load(sys.stdin).get('$url_key') or '')")"
        sha="$(printf '%s' "$ROW" | python3 -c "import json,sys; print(json.load(sys.stdin).get('$sha_key') or '')")"
        if [ -z "$url" ] && [ -z "$sha" ]; then
            bad "${url_key} is empty — this platform cannot update from this release"
        elif [ -z "$url" ]; then
            bad "${url_key} has a hash but no URL"
        elif [ -z "$sha" ]; then
            bad "${url_key} has a URL but no SHA-256 — clients refuse to apply an update with no checksum"
        else
            ok "${url_key} populated"
        fi
    done
    rollout="$(printf '%s' "$ROW" | python3 -c "import json,sys; print(json.load(sys.stdin).get('rollout'))" 2>/dev/null || echo '?')"
    active="$(printf '%s' "$ROW" | python3 -c "import json,sys; print(json.load(sys.stdin).get('active'))" 2>/dev/null || echo '?')"
    note "rollout_percent=${rollout}  active=${active}"
    if [ "$rollout" = "0" ]; then
        note "rollout is 0 — published but offered to nobody (expected before you widen it)"
    fi
fi

# ── 3. The public routes work ──────────────────────────────────────────────
echo
echo "3. Public routes"
body="$(curl -s -m 15 -o - -w '\n%{http_code}' "${PB_API}/api/release" 2>/dev/null || printf '\n000')"
code="$(printf '%s' "$body" | tail -1)"
json="$(printf '%s' "$body" | sed '$d')"
if [ "$code" = "200" ]; then
    ok "GET /api/release -> 200"
    adv="$(printf '%s' "$json" | python3 -c "import json,sys; print(json.load(sys.stdin).get('version',''))" 2>/dev/null || echo '')"
    npl="$(printf '%s' "$json" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('platforms',{})))" 2>/dev/null || echo 0)"
    if [ "$adv" = "$VERSION" ]; then
        ok "/api/release advertises ${VERSION}"
    else
        note "/api/release advertises '${adv}' (checking ${VERSION} — expected if you have published a newer one, or none)"
    fi
    if [ "$npl" -gt 0 ]; then
        ok "/api/release lists ${npl} platform(s)"
    else
        bad "/api/release lists ZERO platforms — it is advertising a release no client can fetch"
        note "re-run publish-release.sh with the real artifacts"
    fi
else
    bad "GET /api/release -> HTTP ${code}"
    note "the release.pb.js hook may not be deployed: v5/server/scripts/hooks-sync.sh --check"
fi

# ── 4. Every advertised URL serves bytes matching its recorded hash ────────
echo
echo "4. Served bytes match the recorded hashes"
if printf '%s' "$ROW" | grep -q '"found": false\|"error"'; then
    note "skipped — no usable row"
else
    for pair in "dl_linux:sha_linux:locus-linux-amd64" \
                "dl_windows:sha_windows:locus-windows-amd64.exe" \
                "dl_macos_intel:sha_macos_intel:locus-darwin-amd64" \
                "dl_macos_arm:sha_macos_arm:locus-darwin-arm64"; do
        url_key="$(printf '%s' "$pair" | cut -d: -f1)"
        sha_key="$(printf '%s' "$pair" | cut -d: -f2)"
        label="$(printf '%s' "$pair" | cut -d: -f3)"
        url="$(printf '%s' "$ROW" | python3 -c "import json,sys; print(json.load(sys.stdin).get('$url_key') or '')")"
        want="$(printf '%s' "$ROW" | python3 -c "import json,sys; print(json.load(sys.stdin).get('$sha_key') or '')")"
        [ -n "$url" ] || continue

        # Hash the served bytes WITHOUT writing them to disk: this box is small
        # and the artifacts are ~10MB each.
        got="$(curl -s -m 300 "$url" 2>/dev/null | sha256sum | awk '{print $1}')"
        http="$(curl -s -m 30 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || echo 000)"
        if [ "$http" != "200" ]; then
            bad "${label}: HTTP ${http} from ${url}"
        elif [ "$got" = "$want" ]; then
            ok "${label}: served bytes match (${got:0:16}…)"
        else
            bad "${label}: HASH MISMATCH served=${got:0:16}… recorded=${want:0:16}…"
            note "clients will download this and fail the checksum"
        fi
    done
fi

# ── 5. Optional: the release exists on GitHub ──────────────────────────────
echo
echo "5. GitHub release"
if curl -fsS -m 15 -o /dev/null "https://api.github.com/repos/vasilliourous/VPN-Service/releases/tags/v${VERSION}" 2>/dev/null; then
    ok "GitHub Release v${VERSION} exists"
else
    note "no GitHub Release for tag v${VERSION} (or no network) — CI may not have finished"
fi

# ── 6. Optional: compare against a local copy of the artifacts ─────────────
if [ -n "$LOCAL_DIR" ]; then
    echo
    echo "6. Local artifacts match what is served"
    [ -d "$LOCAL_DIR" ] || bad "local dir not found: ${LOCAL_DIR}"
    for f in locus-linux-amd64 locus-windows-amd64.exe locus-darwin-amd64 locus-darwin-arm64; do
        [ -f "${LOCAL_DIR}/${f}" ] || { note "${f} not present locally — skipped"; continue; }
        local_sha="$(sha256sum "${LOCAL_DIR}/${f}" | awk '{print $1}')"
        remote_sha="$(remote "sha256sum '${REMOTE_UPDATES}/${f}' 2>/dev/null | awk '{print \$1}'" 2>/dev/null || true)"
        if [ "$local_sha" = "$remote_sha" ]; then
            ok "${f} matches the host"
        else
            bad "${f} differs: local=${local_sha:0:16}… host=${remote_sha:0:16}…"
        fi
    done
fi

# ── Verdict ────────────────────────────────────────────────────────────────
echo
if [ "$failed" -eq 0 ]; then
    printf '\033[0;32m%s checked out. %d check(s) passed.\033[0m\n' "$VERSION" "$pass"
    exit 0
fi
printf '\033[0;31m%s is NOT shippable: %d check(s) failed, %d passed.\033[0m\n' "$VERSION" "$failed" "$pass" >&2
exit 2
