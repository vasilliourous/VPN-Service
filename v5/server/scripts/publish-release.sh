#!/usr/bin/env bash
# publish-release.sh — publish a Locus release to the live update hub.
#
# This is the missing link between CI and the client auto-updater. CI produces
# raw per-platform executables + a manifest.json on a GitHub Release; this
# script copies them to the VPS (served by Caddy at /updates/<version>/) and
# points `update_config` at them, starting at a small rollout.
#
# Usage:
#   v5/server/scripts/publish-release.sh 1.1.0
#
#   # Dry run — does everything except touch the hub:
#   DRY_RUN=1 v5/server/scripts/publish-release.sh 1.1.0
#
#   # Publish but keep the rollout at 0 (upload only, offer to nobody):
#   ROLLOUT_PERCENT=0 v5/server/scripts/publish-release.sh 1.1.0
#
# Environment:
#   VPS              ssh target (default root@networkingguides.duckdns.org)
#   PB_API           hub base URL (default https://networkingguides.duckdns.org)
#   PB_ADMIN_EMAIL   PocketBase admin email   ─┐ required for update_config
#   PB_ADMIN_PASS    PocketBase admin password ─┘ (or PB_TOKEN to skip login)
#   ROLLOUT_PERCENT  initial rollout gate (default 5)
#   RELEASE_DIR      where to find the artifacts (default ./release-artifacts)
#   DRY_RUN=1        print actions without uploading or updating the DB
#
# Exit codes: 0 success, 1 usage/validation error, 2 upload failure,
#             3 update_config failure.
set -euo pipefail

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    echo "usage: $0 <version>   (e.g. 1.1.0 — no leading v)" >&2
    exit 1
fi
# Normalise a leading v so callers can pass either form.
VERSION="${VERSION#v}"

VPS="${VPS:-root@networkingguides.duckdns.org}"
PB_API="${PB_API:-https://networkingguides.duckdns.org}"
ROLLOUT_PERCENT="${ROLLOUT_PERCENT:-5}"
RELEASE_DIR="${RELEASE_DIR:-./release-artifacts}"
DRY_RUN="${DRY_RUN:-0}"

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
fail() { printf '\033[0;31m[publish][FAIL]\033[0m %s\n' "$*" >&2; exit "${2:-1}"; }

# ── Validate local artifacts ──
log "Publishing Locus ${VERSION} from ${RELEASE_DIR}"
[ -d "$RELEASE_DIR" ] || fail "release dir not found: ${RELEASE_DIR} (did you download the release artifacts?)"

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

[ "${#HAVE_FILES[@]}" -gt 0 ] || fail "no usable artifacts found in ${RELEASE_DIR}"

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
