#!/usr/bin/env bash
# smoke-publish.sh — prove publish-release.sh's REFUSAL paths, with no network
# and no changes to the live hub.
#
# WHY: the script's dangerous behaviour is not "uploads the wrong bytes" (the
# manifest cross-check and per-byte re-download handle that). It is "decides to
# write update_config anyway". The first live publish of a release did exactly
# that with an empty artifact directory, producing a row whose version was real
# and whose download_* / sha256_* fields were all empty — a release advertised to
# every client that none of them could fetch. This asserts that can no longer
# happen, and that the refusal cannot be silently reverted by a later edit.
#
# Everything runs with DRY_RUN=1, so the script never SSHes, never uploads and
# never touches PocketBase. Only the decision logic is exercised.
#
# Usage: server/scripts/smoke-publish.sh [path-to-publish-release.sh]
#
# Exit codes: 0 all assertions held, 1 an assertion failed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PUBLISH="${1:-${REPO_ROOT}/server/scripts/publish-release.sh}"
[ -f "$PUBLISH" ] || { echo "smoke: no such script: $PUBLISH" >&2; exit 1; }
PUBLISH="$(cd "$(dirname "$PUBLISH")" && pwd)/$(basename "$PUBLISH")"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0; failed=0
check() {
    local what="$1"; shift
    if "$@"; then printf '\033[0;32m  PASS\033[0m %s\n' "$what"; pass=$((pass+1))
    else       printf '\033[0;31m  FAIL\033[0m %s\n' "$what"; failed=$((failed+1)); fi
}
# run <dir> <extra env/args...> — capture output + exit code without letting
# `set -e` abort the harness.
OUT=""; RC=0
run() {
    local dir="$1"; shift
    set +e
    OUT="$(RELEASE_DIR="$dir" DRY_RUN=1 "${@}" 2>&1)"
    RC=$?
    set -e
}

# Plausible-sized fake artifacts. The script rejects anything under 1MB as a
# truncated download, so zero-byte stubs would test the wrong branch.
mkart() {
    mkdir -p "$1"
    shift
    for f in "$@"; do
        [ -f "$1/$f" ] || head -c 2097152 /dev/urandom > "$1/$f"
    done
}

ALL_FILES=(locus-linux-amd64 locus-windows-amd64.exe locus-darwin-amd64 locus-darwin-arm64)

echo "smoke-publish: exercising $PUBLISH"
echo

# ── 1. Empty directory: the exact live failure ─────────────────────────────
echo "case 1 — empty artifact directory is refused"
mkdir -p "$WORK/empty"
run "$WORK/empty" bash "$PUBLISH" 2.2.1
check "exits non-zero" [ "$RC" -ne 0 ]
check "names the real cause" grep -q "no usable artifacts found" <<<"$OUT"
check "says update_config was NOT written" grep -q "Refusing to write update_config" <<<"$OUT"
check "does not reach the update_config step" bash -c '! grep -q "Updating update_config" <<<"$1"' _ "$OUT"

# ── 2. Partial platform set is refused by default ──────────────────────────
echo "case 2 — a partial platform set is refused"
mkdir -p "$WORK/partial"
for f in locus-linux-amd64 locus-windows-amd64.exe; do
    head -c 2097152 /dev/urandom > "$WORK/partial/$f"
done
run "$WORK/partial" bash "$PUBLISH" 2.2.1
check "exits non-zero" [ "$RC" -ne 0 ]
check "names the missing platforms" grep -q "macos_intel macos_arm" <<<"$OUT"
check "does not reach the update_config step" bash -c '! grep -q "Updating update_config" <<<"$1"' _ "$OUT"

# ── 3. …but --allow-partial is an explicit override ────────────────────────
echo "case 3 — ALLOW_PARTIAL=1 overrides, loudly"
set +e
OUT="$(RELEASE_DIR="$WORK/partial" ALLOW_PARTIAL=1 DRY_RUN=1 bash "$PUBLISH" 2.2.1 2>&1)"; RC=$?
set -e
check "proceeds when told to" [ "$RC" -eq 0 ]
check "says what it is dropping" grep -q "publishing without: macos_intel macos_arm" <<<"$OUT"

# ── 4. A complete set with a manifest passes and is self-consistent ────────
echo "case 4 — complete artifact set passes the guards"
mkdir -p "$WORK/full"
for f in "${ALL_FILES[@]}"; do
    head -c 2097152 /dev/urandom > "$WORK/full/$f"
done
# A manifest whose hashes MATCH the files — the cross-check must pass.
python3 - "$WORK/full" "${ALL_FILES[@]}" <<'PY'
import hashlib, json, os, sys
d = sys.argv[1]
mapping = {"locus-linux-amd64": "linux", "locus-windows-amd64.exe": "windows",
           "locus-darwin-amd64": "macos_intel", "locus-darwin-arm64": "macos_arm"}
out = {"version": "2.2.1", "platforms": {}}
for name in sys.argv[2:]:
    p = os.path.join(d, name)
    out["platforms"][mapping[name]] = {
        "file": name,
        "sha256": hashlib.sha256(open(p, "rb").read()).hexdigest(),
        "size": os.path.getsize(p),
    }
json.dump(out, open(os.path.join(d, "manifest.json"), "w"), indent=2)
PY
run "$WORK/full" bash "$PUBLISH" 2.2.1
check "exits zero" [ "$RC" -eq 0 ]
check "cross-checks the manifest" grep -q "Cross-checking against manifest.json" <<<"$OUT"
check "reaches the update_config step" grep -q "Would set update_config: version=2.2.1" <<<"$OUT"

# ── 5. A manifest whose hash disagrees is refused ──────────────────────────
echo "case 5 — a manifest hash mismatch is refused"
cp -r "$WORK/full" "$WORK/mismatch"
python3 - "$WORK/mismatch/manifest.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
for k in m["platforms"]:
    m["platforms"][k]["sha256"] = "0" * 64
json.dump(m, open(sys.argv[1], "w"))
PY
run "$WORK/mismatch" bash "$PUBLISH" 2.2.1
check "exits non-zero" [ "$RC" -ne 0 ]
check "reports the mismatch" grep -q "manifest cross-check FAILED" <<<"$OUT"

# ── 6. Option and version parsing ──────────────────────────────────────────
echo "case 6 — argument handling"
set +e
OUT="$(bash "$PUBLISH" --help 2>&1)"; RC=$?
set -e
check "--help exits zero" [ "$RC" -eq 0 ]
check "--help prints usage" grep -q "Usage:" <<<"$OUT"

set +e
OUT="$(bash "$PUBLISH" 2>&1)"; RC=$?
set -e
check "no arguments exits non-zero" [ "$RC" -ne 0 ]

set +e
OUT="$(bash "$PUBLISH" --from-github 2>&1)"; RC=$?
set -e
check "a flag where the version belongs is rejected" [ "$RC" -ne 0 ]
check "and says so plainly" grep -q "does not look like a version" <<<"$OUT"

# --from-github with no such release must fail at acquisition, exit 4, and
# never reach update_config. Requires network; skip cleanly if there is none.
echo "case 7 — --from-github for a version that does not exist"
if curl -fsS -m 8 -o /dev/null https://api.github.com 2>/dev/null; then
    set +e
    OUT="$(RELEASE_DIR="$WORK/gh" GITHUB_REPO=vasilliourous/VPN-Service DRY_RUN=1 \
           bash "$PUBLISH" 99.99.99 --from-github 2>&1)"; RC=$?
    set -e
    check "exits 4 (artifact acquisition)" [ "$RC" -eq 4 ]
    check "does not reach the update_config step" \
        bash -c '! grep -q "Updating update_config\|Would set update_config" <<<"$1"' _ "$OUT"
else
    echo "  SKIP (no network to api.github.com)"
fi

echo
printf 'smoke-publish: %d passed, %d failed\n' "$pass" "$failed"
[ "$failed" -eq 0 ]
