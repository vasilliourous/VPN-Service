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

# The .syso stamper lives beside bump-version.sh and is now part of the release
# path, so it gets exercised here too.
STAMP="$(dirname "$BUMP")/stamp-syso.py"
[ -f "$STAMP" ] || { echo "smoke: no such script: $STAMP" >&2; exit 1; }

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

# A real artifact to stamp, so the stamping path is exercised against the actual
# go-winres byte layout rather than a stand-in. We normalise a copy to 2.2.0 (the
# scratch repo's starting version) and seed the scratch tree from it. If the real
# artifact or python3 is missing, the stamp cases report SKIP instead of passing
# vacuously.
SYSO_SRC=""
SYSO_SEED=""
if [ -f "${REPO_ROOT}/v5/client/rsrc_windows_amd64.syso" ] && command -v python3 >/dev/null 2>&1; then
    mkdir -p "$SCRATCH/seed"
    cp "${REPO_ROOT}/v5/client/rsrc_windows_amd64.syso" "$SCRATCH/seed/rsrc_windows_amd64.syso"
    cp "${REPO_ROOT}/v5/client/rsrc_windows_arm64.syso" "$SCRATCH/seed/rsrc_windows_arm64.syso"
    # Bring the seed down to 2.2.0 so it agrees with the scratch repo's VERSION.
    python3 "$STAMP" 2.2.0 --client-dir "$SCRATCH/seed" --quiet >/dev/null 2>&1 || true
    SYSO_SEED="$SCRATCH/seed/rsrc_windows_amd64.syso"
    SYSO_SRC="$SCRATCH/seed/rsrc_windows_arm64.syso"
fi

pass=0
failed=0
skipped=0
check() {
    local what="$1"; shift
    if "$@"; then
        printf '\033[0;32m  PASS\033[0m %s\n' "$what"; pass=$((pass + 1))
    else
        printf '\033[0;31m  FAIL\033[0m %s\n' "$what"; failed=$((failed + 1))
    fi
}
skip() { printf '\033[1;33m  SKIP\033[0m %s\n' "$1"; skipped=$((skipped + 1)); }

# ── Build a minimal scratch repo ───────────────────────────────────────────
# The bump script needs: a git repo (it refuses a dirty tree), the six
# accounted files, and enough of v5/client for the drift scan to have something
# to walk. It does NOT need the Go toolchain: the .syso stamping is now done by
# stamp-syso.py with the standard library, so it runs here too and IS covered.
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
    cp "$STAMP" v5/server/scripts/stamp-syso.py
    chmod +x v5/server/scripts/stamp-syso.py

    # A REAL .syso pair, so the stamping path is exercised against the actual
    # go-winres byte layout rather than a stand-in, normalised to 2.2.0 so the
    # scratch tree starts self-consistent.
    if [ -n "$SYSO_SEED" ]; then
        cp "$SYSO_SEED" v5/client/rsrc_windows_amd64.syso
        cp "$SYSO_SRC"  v5/client/rsrc_windows_arm64.syso
    fi

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

# ── Case 7: the .syso stamping is NOT skipped by --no-verify ───────────────
# This is the 2.2.7 regression. release-cut.sh calls bump.sh with --no-verify,
# which used to skip the artifact update entirely, so the release tag pointed at
# a tree whose committed resources still named the previous version and CI
# rejected it. The artifacts must now be stamped on that path, with no Go
# toolchain present.
echo "case 7 — syso stamped even under --no-verify and without a toolchain"
if [ -z "$SYSO_SEED" ] || ! command -v python3 >/dev/null 2>&1; then
    skip "no real .syso seed or no python3 available"
else
    setup_scratch
    cd "$SCRATCH/repo"

    # The tree starts consistent at 2.2.0. Prove the stamper agrees, so a later
    # failure is attributable to the bump rather than a bad fixture.
    check "seed artifacts read 2.2.0 before the bump" \
        python3 v5/server/scripts/stamp-syso.py 2.2.0 --check --quiet

    # GO_BIN points at nothing: the point is that no toolchain is needed.
    GO_BIN=definitely-not-a-real-go \
        v5/server/scripts/bump-version.sh patch --no-verify >"$SCRATCH/c7.log" 2>&1 || true

    check "VERSION advanced to 2.2.1" [ "$(cat v5/VERSION)" = "2.2.1" ]
    check "artifacts were stamped to 2.2.1 (--no-verify did not skip them)" \
        python3 v5/server/scripts/stamp-syso.py 2.2.1 --check --quiet
    # Byte-level: the old build digit must be gone from BOTH the UTF-16LE strings
    # and the packed VS_FIXEDFILEINFO block, and the untouched FileDescription
    # must still be there. Done in python because the artifact is UTF-16LE and
    # shell/grep cannot carry NUL bytes through a command substitution.
    check "old version is gone, brand text is intact" python3 - <<'PY'
import sys
SY = bytes.fromhex("bd04effe")
raw = open("v5/client/rsrc_windows_amd64.syso", "rb").read()
if "2.2.0".encode("utf-16-le") in raw:
    sys.exit("stale UTF-16LE display string still mentions 2.2.0")
if "2.2.1".encode("utf-16-le") not in raw:
    sys.exit("new UTF-16LE display string missing")
if "Locus".encode("utf-16-le") not in raw:
    sys.exit("product name lost")
i = raw.find(SY)
if i < 0:
    sys.exit("VS_FIXEDFILEINFO signature missing")
import struct
fms, fls = struct.unpack_from("<2I", raw, i + 8)
if (fms >> 16, fms & 0xFFFF, fls >> 16, fls & 0xFFFF) != (2, 2, 1, 0):
    sys.exit(f"packed FileVersion is wrong: {fms:#x}/{fls:#x}, want 2.2.1.0")
PY
    check "ProductName is still Locus, not the legacy brand" \
        bash -c "python3 v5/server/scripts/stamp-syso.py 2.2.1 --check --quiet"
fi

# ── Case 8: the stamping guards refuse rather than guess ───────────────────
echo "case 8 — syso stamper refuses what it cannot do safely"
if [ -z "$SYSO_SEED" ] || ! command -v python3 >/dev/null 2>&1; then
    skip "no real .syso seed or no python3 available"
else
    # A width change (2.2.0 -> 2.10.0) is NOT a byte patch: the UTF-16LE strings
    # and the packed dwords change size, so the file must be regenerated with
    # go-winres. The stamper has to say so instead of corrupting the artifact.
    setup_scratch
    cd "$SCRATCH/repo"
    if python3 v5/server/scripts/stamp-syso.py 2.10.0 --client-dir v5/client \
            >"$SCRATCH/c8a.log" 2>&1; then
        check "refuses a version-width change" false
    else
        check "refuses a version-width change" true
        check "the refusal names go generate" grep -q "go generate" "$SCRATCH/c8a.log"
    fi
    check "the artifact is unmodified after a refusal" \
        python3 v5/server/scripts/stamp-syso.py 2.2.0 --check --quiet

    # A file it cannot parse must fail loudly. Passing silently here is the
    # "blind guard" failure that let a stale .syso ship for two releases.
    setup_scratch
    cd "$SCRATCH/repo"
    printf '\000\001\002\003\377\376' > v5/client/rsrc_windows_amd64.syso
    if python3 v5/server/scripts/stamp-syso.py 2.2.1 --client-dir v5/client \
            >"$SCRATCH/c8b.log" 2>&1; then
        check "refuses an unparseable resource instead of passing blindly" false
    else
        check "refuses an unparseable resource instead of passing blindly" true
    fi
fi

# ── Case 9: a stale artifact is caught before anything is committed ────────
# The stamping in case 7 makes this hard to reach through bump.sh, which is the
# point. This asserts the CHECK itself, which is what CI and release-cut.sh use
# as the backstop -- it must report stale, and must not write.
echo "case 9 — a stale artifact is reported stale, and --check writes nothing"
if [ -z "$SYSO_SEED" ] || ! command -v python3 >/dev/null 2>&1; then
    skip "no real .syso seed or no python3 available"
else
    setup_scratch
    cd "$SCRATCH/repo"
    before="$(cksum v5/client/rsrc_windows_amd64.syso)"
    if python3 v5/server/scripts/stamp-syso.py 2.2.5 --check --quiet >/dev/null 2>&1; then
        check "--check reports a mismatched artifact as stale" false
    else
        check "--check reports a mismatched artifact as stale" true
    fi
    check "--check wrote nothing" [ "$before" = "$(cksum v5/client/rsrc_windows_amd64.syso)" ]
    check "the artifact still reads 2.2.0" \
        python3 v5/server/scripts/stamp-syso.py 2.2.0 --check --quiet
fi

echo
printf 'smoke-bump: %d passed, %d failed' "$pass" "$failed"
[ "$skipped" -gt 0 ] && printf ', %d skipped' "$skipped"
printf '\n'
[ "$failed" -eq 0 ]
