#!/usr/bin/env bash
# publish-release.sh — publish a Locus release to the live update hub.
#
# This is the missing link between CI and the client auto-updater. CI produces
# raw per-platform executables + a manifest.json on a GitHub Release; this
# script copies them to the VPS (served by Caddy at /updates/<version>/) and
# points `update_config` at them, starting at a small rollout.
#
# Usage:
#   v5/server/scripts/publish-release.sh 2.2.1
#
#   # Fetch the artifacts from the GitHub Release instead of from disk
#   # (this is the normal path — CI already built and attached them):
#   v5/server/scripts/publish-release.sh 2.2.1 --from-github
#
#   # Dry run — does everything except upload and touch the hub:
#   DRY_RUN=1 v5/server/scripts/publish-release.sh 2.2.1 --from-github
#
#   # Publish but keep the rollout at 0 (upload only, offer to nobody):
#   ROLLOUT_PERCENT=0 v5/server/scripts/publish-release.sh 2.2.1
#
# WHY --from-github EXISTS
#   The first live publish of a release was done with an empty RELEASE_DIR. The
#   script warned for every missing platform, found "no usable artifacts", and
#   then WROTE update_config ANYWAY — producing a row whose version/active/rollout
#   were real but whose download_* and sha256_* fields were all empty. That row
#   advertises a release that no client can fetch. Fetching the artifacts by
#   version removes the whole class of mistake: the version argument selects the
#   release, so there is no directory to get wrong.
#
#   See FIXES.md entry 41.
#
# Environment:
#   VPS              ssh target (default root@networkingguides.duckdns.org)
#   PB_API           hub base URL (default https://networkingguides.duckdns.org)
#   PB_ADMIN_EMAIL   PocketBase admin email   ─┐ required for update_config
#   PB_ADMIN_PASS    PocketBase admin password ─┘ (or PB_TOKEN to skip login)
#   ROLLOUT_PERCENT  initial rollout gate (default 5)
#   RELEASE_DIR      where to find the artifacts (default ./release-artifacts)
#   GITHUB_REPO      owner/name for --from-github (default vasilliourous/VPN-Service)
#   GH_TOKEN         optional; only needed for a private repo or to dodge
#                    anonymous rate limits on the download URL
#   DRY_RUN=1        print actions without uploading or updating the DB
#   ALLOW_PARTIAL=1  publish even though some platforms are missing. Refused by
#                    default: a release that omits a platform leaves those
#                    clients unable to update, and the omission is invisible
#                    from the operator's seat.
#
# Exit codes: 0 success, 1 usage/validation error, 2 upload failure,
#             3 update_config failure, 4 artifact acquisition failure.
set -euo pipefail

# --help must be handled BEFORE the version is read from $1, or
# `publish-release.sh --help` treats "--help" as the version and fails with a
# confusing "release dir not found" further down.
case "${1:-}" in
    -h|--help)
        sed -n '2,75p' "$0" | sed 's/^# \{0,1\}//'
        exit 0 ;;
esac

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    sed -n '10,30p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 1
fi
# Normalise a leading v so callers can pass either form.
VERSION="${VERSION#v}"
# Reject a version that is not a version. Without this, `publish-release.sh
# --from-github 2.2.1` (options before the version) would silently set
# VERSION="--from-github" and then try to fetch a release for that tag.
case "$VERSION" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "error: '${VERSION}' does not look like a version (expected x.y.z)" >&2; exit 1 ;;
esac

VPS="${VPS:-root@networkingguides.duckdns.org}"
PB_API="${PB_API:-https://networkingguides.duckdns.org}"
ROLLOUT_PERCENT="${ROLLOUT_PERCENT:-5}"
RELEASE_DIR="${RELEASE_DIR:-./release-artifacts}"
DRY_RUN="${DRY_RUN:-0}"
GITHUB_REPO="${GITHUB_REPO:-vasilliourous/VPN-Service}"
ALLOW_PARTIAL="${ALLOW_PARTIAL:-0}"
FROM_GITHUB=0

# Parse the optional second argument. Kept position-independent of VERSION so
# `publish-release.sh 2.2.1 --from-github` and `--from-github 2.2.1` both work.
for arg in "${@:2}"; do
    case "$arg" in
        --from-github) FROM_GITHUB=1 ;;
        --allow-partial) ALLOW_PARTIAL=1 ;;
        -h|--help) sed -n '2,70p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $arg" >&2; exit 1 ;;
    esac
done

# The filenames are a contract with the client (PlatformDownloadURL) and with
# CI's raw-artifact staging step. Keep them in one place.
PLATFORMS=(
    "linux:locus-linux-amd64:update_linux"
    "windows:locus-windows-amd64.exe:update_windows"
    "macos_intel:locus-darwin-amd64:update_macos_intel"
    "macos_arm:locus-darwin-arm64:update_macos_arm"
)

log()  { printf '\033[0;32m[publish]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[publish][WARN]\033[0m %s\n' "$*"; }

# fail prints a multi-line message and exits with the given code (default 1).
#
# `%b` rather than `%s` so embedded \n in the message becomes a real newline.
# With %s every multi-line diagnostic printed the literal characters "\n", which
# made the most important failures (the ones that refuse a publish) unreadable.
#
# The exit code is taken from the LAST argument only when it is a bare number,
# so existing `fail "message" 2` calls keep working while a message that happens
# to end in a digit is not mistaken for a code.
fail() {
    local code=1
    if [ "$#" -gt 1 ]; then
        case "${!#}" in
            [0-9]|[0-9][0-9]) code="${!#}"; set -- "${@:1:$#-1}" ;;
        esac
    fi
    printf '\033[0;31m[publish][FAIL]\033[0m %b\n' "$*" >&2
    exit "$code"
}

# ── Acquire artifacts ──
# Two sources: a local directory the operator populated, or the GitHub Release
# for this exact version. --from-github is the normal path and exists because
# publishing from an empty directory once wrote a live update_config row with no
# download URLs in it (FIXES.md 41). Selecting artifacts BY VERSION removes the
# operator's chance to point at the wrong directory.

# fetch_from_github downloads this version's raw executables and manifest.json
# from its GitHub Release into RELEASE_DIR.
#
# Deliberately strict about what it accepts:
#   * the release MUST exist for tag v<version>, or we would publish artifacts
#     from a different release than the version we are writing into the row;
#   * every platform in PLATFORMS must be present, because a release missing a
#     platform is a CI failure, not something to paper over at publish time;
#   * manifest.json is fetched too. It is what proves (below) that the bytes we
#     are about to serve came from the same build CI hashed.
fetch_from_github() {
    local api="https://api.github.com/repos/${GITHUB_REPO}/releases/tags/v${VERSION}"
    local auth=()
    [ -n "${GH_TOKEN:-}" ] && auth=(-H "Authorization: Bearer ${GH_TOKEN}")

    log "Fetching artifacts for v${VERSION} from ${GITHUB_REPO}…"
    mkdir -p "$RELEASE_DIR"

    # `curl -f` so an HTTP error is a failure rather than a JSON error document
    # written into RELEASE_DIR and later mistaken for an artifact.
    if ! curl -fsSL "${auth[@]}" -H "Accept: application/vnd.github+json" \
            "$api" -o "${RELEASE_DIR}/.release.json"; then
        fail "no GitHub Release found for tag v${VERSION} in ${GITHUB_REPO}.\n"\
"Either CI has not finished, the tag was never pushed, or the release failed.\n"\
"Check: gh release view v${VERSION} --repo ${GITHUB_REPO}" 4
    fi

    # Pull the browser_download_url for each asset we need, so the download does
    # not depend on GitHub's asset naming staying in lockstep with our own.
    local files=(manifest.json)
    for entry in "${PLATFORMS[@]}"; do
        rest="${entry#*:}"; files+=("${rest%%:*}")
    done

    local missing=()
    for want in "${files[@]}"; do
        local url
        url=$(python3 - "${RELEASE_DIR}/.release.json" "$want" <<'PY'
import json, sys
try:
    rel = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for a in rel.get("assets", []):
    if a.get("name") == sys.argv[2]:
        print(a.get("browser_download_url", ""))
        break
PY
)
        if [ -z "$url" ]; then
            missing+=("$want")
            continue
        fi
        if ! curl -fsSL "${auth[@]}" "$url" -o "${RELEASE_DIR}/${want}"; then
            fail "download failed for ${want} from ${url}" 4
        fi
        local size
        size=$(stat -c%s "${RELEASE_DIR}/${want}" 2>/dev/null || stat -f%z "${RELEASE_DIR}/${want}")
        log "  ↓ ${want} (${size} bytes)"
    done
    rm -f "${RELEASE_DIR}/.release.json"

    if [ "${#missing[@]}" -gt 0 ]; then
        fail "the v${VERSION} release is missing: ${missing[*]}\n"\
"CI is supposed to attach all four platform executables plus manifest.json.\n"\
"A release without them cannot be published — fix CI and re-release rather than\n"\
"publishing a partial update." 4
    fi
    log "✓ Fetched $((${#files[@]})) assets for v${VERSION}"
}

if [ "$FROM_GITHUB" = "1" ]; then
    fetch_from_github
fi

# ── Validate local artifacts ──
log "Publishing Locus ${VERSION} from ${RELEASE_DIR}"
[ -d "$RELEASE_DIR" ] || fail "release dir not found: ${RELEASE_DIR} (did you download the release artifacts?)" 4

declare -a HAVE_FILES=() HAVE_KEYS=() HAVE_SHA=()
for entry in "${PLATFORMS[@]}"; do
    key="${entry%%:*}"; rest="${entry#*:}"
    file="${rest%%:*}"; col="${rest#*:}"
    path="${RELEASE_DIR}/${file}"
    if [ ! -f "$path" ]; then
        warn "missing artifact for ${key}: ${file} (this platform will not be advertised)"
        continue
    fi
    # Refuse to publish something trivially small — a truncated download or a
    # Git LFS pointer would otherwise be served as a "binary".
    size=$(stat -c%s "$path" 2>/dev/null || stat -f%z "$path")
    if [ "$size" -lt 1048576 ]; then
        fail "${file} is only ${size} bytes — refusing to publish (expected >1MB)"
    fi
    sha=$(sha256sum "$path" | awk '{print $1}')
    HAVE_FILES+=("$path"); HAVE_KEYS+=("$key"); HAVE_SHA+=("$sha")
    log "  ${key}: ${file} ($(( size / 1024 / 1024 )) MB) sha256=${sha:0:16}…"
done

# ── Hard stop: never write a partial row ───────────────────────────────────
# The row this script writes is what the live hub serves to every client. A row
# with some download_* fields empty silently withholds updates from those
# platforms — the failure is invisible from the operator's seat and only shows
# up as students stuck on an old build. So refuse the publish entirely unless
# the operator explicitly accepts the gap.
#
# This is the check that did NOT exist when the first live publish wrote an
# update_config row whose version was real and whose URLs were all empty.
[ "${#HAVE_FILES[@]}" -gt 0 ] || fail "no usable artifacts found in ${RELEASE_DIR}.\n"\
"Refusing to write update_config — a row with no download URLs advertises a\n"\
"release that no client can fetch." 4

MISSING_PLATFORMS=()
for entry in "${PLATFORMS[@]}"; do
    key="${entry%%:*}"
    found=0
    for have in "${HAVE_KEYS[@]}"; do
        [ "$have" = "$key" ] && found=1 && break
    done
    [ "$found" = "1" ] || MISSING_PLATFORMS+=("$key")
done
if [ "${#MISSING_PLATFORMS[@]}" -gt 0 ]; then
    if [ "$ALLOW_PARTIAL" != "1" ]; then
        for k in "${MISSING_PLATFORMS[@]}"; do
            file=""
            for entry in "${PLATFORMS[@]}"; do
                case "$entry" in "$k:"*) rest="${entry#*:}"; file="${rest%%:*}" ;; esac
            done
            warn "  ${k}: ${file} absent"
        done
        fail "${#MISSING_PLATFORMS[@]} platform(s) have no artifact (${MISSING_PLATFORMS[*]}).\n"\
"Publishing now would withhold updates from those platforms with no visible\n"\
"symptom, so this is refused by default. If the gap is deliberate, re-run with\n"\
"ALLOW_PARTIAL=1 (or --allow-partial) and say why in the release notes." 4
    fi
    warn "ALLOW_PARTIAL=1 — publishing without: ${MISSING_PLATFORMS[*]}"
fi

# ── Cross-check against manifest.json if present ──
# CI generates manifest.json from the same build. If it disagrees with the
# files on disk, something did not come from the release we think it did —
# abort rather than serve a mismatched hash to every client.
MANIFEST="${RELEASE_DIR}/manifest.json"
if [ -f "$MANIFEST" ]; then
    log "Cross-checking against manifest.json…"
    python3 - "$MANIFEST" "$RELEASE_DIR" "${HAVE_KEYS[@]}" <<'PY' || fail "manifest cross-check FAILED — refusing to publish"
import hashlib, json, os, sys
manifest_path, rel_dir = sys.argv[1], sys.argv[2]
keys = sys.argv[3:]
m = json.load(open(manifest_path))
plats = m.get("platforms", {})
bad = 0
for k in keys:
    entry = plats.get(k)
    if not entry:
        print(f"  {k}: absent from manifest — skipping check")
        continue
    p = os.path.join(rel_dir, entry["file"])
    actual = hashlib.sha256(open(p, "rb").read()).hexdigest()
    if actual != entry["sha256"]:
        print(f"  {k}: MISMATCH manifest={entry['sha256'][:16]}… actual={actual[:16]}…")
        bad += 1
    else:
        print(f"  {k}: ok ({actual[:16]}…)")
sys.exit(1 if bad else 0)
PY
else
    warn "no manifest.json — skipping CI cross-check (hashes derived from disk)"
fi

# ── Create the version directory and upload ──
DEST_DIR="/var/www/updates/${VERSION}"
log "Target: ${VPS}:${DEST_DIR}"

if [ "$DRY_RUN" = "1" ]; then
    warn "DRY_RUN=1 — not uploading, not touching update_config"
else
    # `install -d` is idempotent; failures here mean we cannot publish at all.
    ssh "$VPS" "install -d -m 755 -o root -g root '${DEST_DIR}'" \
        || fail "could not create ${DEST_DIR} on ${VPS}" 2

    for i in "${!HAVE_FILES[@]}"; do
        f="${HAVE_FILES[$i]}"
        base=$(basename "$f")
        # Upload to a temp name, then atomically move into place so a client can
        # never fetch a half-written binary mid-publish.
        scp -q "$f" "${VPS}:${DEST_DIR}/${base}.uploading" \
            || fail "scp failed for ${base}" 2
        ssh "$VPS" "mv '${DEST_DIR}/${base}.uploading' '${DEST_DIR}/${base}' && chmod 644 '${DEST_DIR}/${base}'" \
            || fail "could not finalise ${base}" 2
        log "  uploaded ${base}"
    done
fi

# ── Verify the hub actually serves them ──
if [ "$DRY_RUN" != "1" ]; then
    log "Verifying downloads through Caddy…"
    ok=0
    for i in "${!HAVE_FILES[@]}"; do
        base=$(basename "${HAVE_FILES[$i]}")
        url="${PB_API}/updates/${VERSION}/${base}"
        code=$(curl -s -o /dev/null -w '%{http_code}' "$url" || echo "000")
        if [ "$code" = "200" ]; then
            # Confirm the bytes served match what we uploaded.
            remote=$(curl -s "$url" | sha256sum | awk '{print $1}')
            if [ "$remote" = "${HAVE_SHA[$i]}" ]; then
                log "  ✓ ${base} served and hash matches"
                ok=$((ok+1))
            else
                fail "  ${base} served but hash MISMATCH (remote=${remote:0:16}… local=${HAVE_SHA[$i]:0:16}…)" 2
            fi
        else
            fail "  ${url} returned HTTP ${code}" 2
        fi
    done
    log "✓ ${ok} artifact(s) verified over HTTPS"
fi

# ── Point update_config at the new version ──
if [ "$DRY_RUN" = "1" ]; then
    log "Would set update_config: version=${VERSION} rollout=${ROLLOUT_PERCENT} active=true"
    log "✓ Dry run complete"
    exit 0
fi

log "Updating update_config…"
PB_TOKEN="${PB_TOKEN:-}"
if [ -z "$PB_TOKEN" ]; then
    : "${PB_ADMIN_EMAIL:?set PB_ADMIN_EMAIL (or PB_TOKEN) to update update_config}"
    : "${PB_ADMIN_PASS:?set PB_ADMIN_PASS (or PB_TOKEN) to update update_config}"
    PB_TOKEN=$(curl -s -X POST "${PB_API}/api/admins/auth-with-password" \
        -H "Content-Type: application/json" \
        -d "$(python3 -c 'import json,sys; print(json.dumps({"identity":sys.argv[1],"password":sys.argv[2]}))' "$PB_ADMIN_EMAIL" "$PB_ADMIN_PASS")" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
    [ -n "$PB_TOKEN" ] || fail "could not authenticate to PocketBase" 3
fi

# Build the record. Only advertise platforms we actually uploaded.
python3 - "$PB_API" "$PB_TOKEN" "$VERSION" "$ROLLOUT_PERCENT" "$RELEASE_DIR" "${HAVE_KEYS[@]}" <<'PY' || fail "update_config update failed" 3
import hashlib, json, os, subprocess, sys
api, token, version, rollout, rel_dir = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5]
keys = sys.argv[6:]

FILES = {"linux": "locus-linux-amd64",
         "windows": "locus-windows-amd64.exe",
         "macos_intel": "locus-darwin-amd64",
         "macos_arm": "locus-darwin-arm64"}

def req(method, path, data=None):
    cmd = ["curl", "-s", "-X", method, api + path,
           "-H", "Content-Type: application/json",
           "-H", "Authorization: " + token]
    if data is not None:
        cmd += ["-d", json.dumps(data)]
    out = subprocess.run(cmd, capture_output=True, text=True).stdout
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        return {"raw": out}

# One record only — duplicates make the "first match" read ambiguous.
existing = req("GET", "/api/collections/update_config/records?perPage=100")
items = existing.get("items", []) if isinstance(existing, dict) else []

body = {
    "version": version,
    "rollout_percent": rollout,
    "active": True,
    # Generic fallback fields. update_url/update_sha256 are kept for older
    # clients that predate the per-platform fields — point them at linux as the
    # most likely default rather than leaving them empty.
    "update_url": f"{api}/updates/{version}/{FILES['linux']}",
    "update_sha256": "",
}
for key in keys:
    if key not in FILES:
        continue
    fname = FILES[key]
    body["download_" + key] = f"{api}/updates/{version}/{fname}"
    # Per-platform checksum, read from the .sha256 we just uploaded. Without
    # this the updater refuses to apply the update ("empty SHA256 checksum").
    h = hashlib.sha256(open(os.path.join(rel_dir, fname), "rb").read()).hexdigest()
    body["sha256_" + key] = h
# Legacy single hash: kept populated so any older client that only reads
# update_sha256 still has a usable (linux) value.
if body.get("sha256_linux"):
    body["update_sha256"] = body["sha256_linux"]

if items:
    items.sort(key=lambda r: r.get("created", ""))
    for dup in items[:-1]:
        req("DELETE", "/api/collections/update_config/records/" + dup["id"])
        print(f"  removed duplicate update_config {dup['id'][:12]}")
    rid = items[-1]["id"]
    resp = req("PATCH", "/api/collections/update_config/records/" + rid, body)
    print(f"  updated update_config {rid[:12]}" if resp.get("id")
          else f"  UPDATE FAILED: {resp}")
else:
    resp = req("POST", "/api/collections/update_config/records", body)
    print("  created update_config" if resp.get("id") else f"  CREATE FAILED: {resp}")
print("  advertised platforms: " + ", ".join(sorted(keys)))
sys.exit(0 if resp.get("id") else 1)
PY

log "✓ Published ${VERSION} — rollout ${ROLLOUT_PERCENT}%"
cat <<EOF

Next steps:
  1. Watch for update failures:
       ssh ${VPS} "grep -i update /var/log/caddy/access.log | tail"
  2. Widen the rollout as confidence grows:
       version=${VERSION} rollout=25  → then 100
  3. To stop offering the update entirely:
       set rollout_percent=0 in update_config (clients already updated stay updated)

Note: this uploads only the CLIENT binary. sing-box is unchanged by client
updates — it is bundled in the installer, not the updater.
EOF
