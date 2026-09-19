#!/usr/bin/env bash
# smoke-bump.sh — prove bump-version.sh works without mutating a real tree.
#
# The bump script rewrites six files and regenerates two Windows resources. Its
# failure modes are boring to describe and expensive to discover during a
# release ("it ran, printed success, and one copy silently did not change"), so
# this exercises it against a THROWAWAY COPY of the repository's version-bearing
# files and asserts the outcome.
#
# What it proves, in order:
#   1. --dry-run writes nothing at all (byte-compare the whole scratch tree).
#   2. A real bump changes every accounted copy and nothing else.
#   3. The stray-copy detector FAILS when a seventh copy is introduced, rather
#      than passing and letting a release ship a lie.
#   4. A dirty tree is refused before anything is touched.
#   5. An unrecognised bump word and an equal-to-current version are refused.
#
# Usage: v5/server/scripts/smoke-bump.sh [path-to-bump-version.sh]
#
# Exit codes: 0 all assertions held, 1 an assertion failed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
BUMP="${1:-${REPO_ROOT}/v5/server/scripts/bump-version.sh}"
[ -f "$BUMP" ] || { echo "smoke: no such script: $BUMP" >&2; exit 1; }
BUMP="$(cd "$(dirname "$BUMP")" && pwd)/$(basename "$BUMP")"

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

pass=0
failed=0
check() {
    local what="$1"; shift
    if "$@"; then
        printf '\033[0;32m  PASS\033[0m %s\n' "$what"; pass=$((pass + 1))
    else
        printf '\033[0;31m  FAIL\033[0m %s\n' "$what"; failed=$((failed + 1))
    fi
}

# ── Build a minimal scratch repo ───────────────────────────────────────────
# The bump script needs: a git repo (it refuses a dirty tree), the six
# accounted files, and enough of v5/client for the drift scan to have something
# to walk. It does NOT need the Go toolchain, because every case below runs
# with --no-verify (the .syso regeneration is the slow, network-dependent part
# and is covered by CI, not by this smoke test).
setup_scratch() {
    rm -rf "${SCRATCH:?}/repo"
    mkdir -p "$SCRATCH/repo/v5/client/internal/buildinfo" \
             "$SCRATCH/repo/v5/client/frontend" \
             "$SCRATCH/repo/v5/server/scripts"
    cd "$SCRATCH/repo"
    git init -q .
    git config user.email smoke@example.invalid
    git config user.name smoke

    echo -n "2.2.0" > v5/VERSION
    cat > v5/client/main.go <<'EOF'
package main

var version = "2.2.0"

//go:generate go run example.com/tool@latest --product-version 2.2.0 --file-version 2.2.0
EOF
    cat > v5/client/internal/buildinfo/buildinfo.go <<'EOF'
package buildinfo

const fallbackVersion = "2.2.0"
EOF
    cat > v5/client/wails.json <<'EOF'
{
  "name": "locus",
  "version": "2.2.0"
}
EOF
    cat > v5/client/frontend/package.json <<'EOF'
{
  "name": "locus-frontend",
  "version": "2.2.0",
  "dependencies": { "vue": "^3.4.0" }
}
EOF
    cat > v5/client/frontend/package-lock.json <<'EOF'
{
  "name": "locus-frontend",
  "version": "2.2.0",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "locus-frontend",
      "version": "2.2.0",
      "dependencies": { "vue": "^3.4.0" }
    }
  }
}
EOF
    # A docs file carrying the old version on purpose — history, not a copy.
    mkdir -p v5/docs
    echo "shipped in 2.2.0" > v5/docs/FIXES.md

    # Real scripts, so we exercise the code we are shipping rather than a copy.
    cp "$BUMP" v5/server/scripts/bump-version.sh
    chmod +x v5/server/scripts/bump-version.sh
    git add -A
    git commit -qm "scratch"
}

# Count files containing a literal string, excluding .git.
count_files_with() {
    grep -rl --exclude-dir=.git -e "$1" . 2>/dev/null | sort
}

echo "smoke-bump: exercising $BUMP"
echo

# ── Case 1: --dry-run must not write anything ──────────────────────────────
echo "case 1 — dry run is inert"
setup_scratch
cd "$SCRATCH/repo"
BEFORE="$(find . -path ./.git -prune -o -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum)"
v5/server/scripts/bump-version.sh patch --dry-run >"$SCRATCH/dry.log" 2>&1 || true
AFTER="$(find . -path ./.git -prune -o -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum)"
check "no file changed during --dry-run" [ "$BEFORE" = "$AFTER" ]
check "dry run still reports the target version" grep -q "2.2.1" "$SCRATCH/dry.log"

# ── Case 2: a real bump updates every accounted copy ───────────────────────
echo "case 2 — real bump updates all six copies"
v5/server/scripts/bump-version.sh patch --no-verify >"$SCRATCH/bump.log" 2>&1 || {
    echo "  (bump failed; log follows)"; sed 's/^/    /' "$SCRATCH/bump.log"; }
check "v5/VERSION now reads 2.2.1" [ "$(cat v5/VERSION)" = "2.2.1" ]
check "main.go runtime literal bumped" grep -q 'var version = "2.2.1"' v5/client/main.go
check "go:generate directive bumped" grep -q -- '--product-version 2.2.1 --file-version 2.2.1' v5/client/main.go
check "buildinfo fallback bumped" grep -q 'fallbackVersion = "2.2.1"' v5/client/internal/buildinfo/buildinfo.go
check "wails.json bumped" grep -q '"version": "2.2.1"' v5/client/wails.json
check "package.json bumped" grep -q '"version": "2.2.1"' v5/client/frontend/package.json
check "package-lock root bumped" grep -q '"version": "2.2.1"' v5/client/frontend/package-lock.json

# ── Case 3: no stale copy may survive in the client tree ───────────────────
echo "case 3 — no stale copy survives"
STALE="$(grep -rl --exclude-dir=.git --exclude-dir=docs -e '2\.2\.0' v5/client 2>/dev/null || true)"
check "old version is gone from v5/client" [ -z "$STALE" ]
check "docs history is left alone"   grep -q "2.2.0" v5/docs/FIXES.md

# ── Case 4: the stray-copy detector must actually fire ─────────────────────
echo "case 4 — a seventh copy is detected, not tolerated"
setup_scratch
cd "$SCRATCH/repo"
mkdir -p v5/client/internal/newpkg
cat > v5/client/internal/newpkg/const.go <<'EOF'
package newpkg

// A copy the tooling does not know about — the exact drift class that shipped
// a Windows binary reporting a version that no longer existed.
const ReleaseVersion = "2.2.0"
EOF
git add -A && git commit -qm "add unaccounted copy"
if v5/server/scripts/bump-version.sh patch --no-verify >"$SCRATCH/stray.log" 2>&1; then
    check "bump refuses when an unaccounted copy exists" false
    echo "    (it exited 0; log follows)"; sed 's/^/    /' "$SCRATCH/stray.log"
else
    check "bump refuses when an unaccounted copy exists" true
    check "the offending file is named" grep -q "newpkg/const.go" "$SCRATCH/stray.log"
fi

# ── Case 5: refusals ───────────────────────────────────────────────────────
echo "case 5 — refusals"
setup_scratch
cd "$SCRATCH/repo"
if v5/server/scripts/bump-version.sh 2.2.0 --no-verify >"$SCRATCH/same.log" 2>&1; then
    check "refuses a version equal to current" false
else
    check "refuses a version equal to current" true
fi
if v5/server/scripts/bump-version.sh banana --no-verify >"$SCRATCH/banana.log" 2>&1; then
    check "refuses an unknown bump word" false
else
    check "refuses an unknown bump word" true
fi

echo "a dirty tree is refused"
setup_scratch
cd "$SCRATCH/repo"
echo "// local edit" >> v5/client/main.go
if v5/server/scripts/bump-version.sh patch --no-verify >"$SCRATCH/dirty.log" 2>&1; then
    check "refuses a dirty tree" false
else
    check "refuses a dirty tree" true
    check "the local edit is untouched" grep -q "// local edit" v5/client/main.go
    check "v5/VERSION untouched" [ "$(cat v5/VERSION)" = "2.2.0" ]
fi

# ── Case 6: version arithmetic ─────────────────────────────────────────────
echo "case 6 — version arithmetic"
for spec in "major:3.0.0" "minor:2.3.0" "patch:2.2.1"; do
    word="${spec%%:*}"; want="${spec#*:}"
    setup_scratch
    cd "$SCRATCH/repo"
    v5/server/scripts/bump-version.sh "$word" --no-verify >/dev/null 2>&1 || true
    got="$(cat v5/VERSION)"
    check "'$word' produces $want" [ "$got" = "$want" ]
done

echo
printf 'smoke-bump: %d passed, %d failed\n' "$pass" "$failed"
[ "$failed" -eq 0 ]
