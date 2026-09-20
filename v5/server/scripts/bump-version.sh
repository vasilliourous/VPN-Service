#!/usr/bin/env bash
# bump-version.sh — advance the Locus client version everywhere it is recorded.
#
# WHY THIS EXISTS
# ---------------
# The client version is duplicated across six files, and the release pipeline
# reads them independently:
#
#   v5/VERSION                                    canonical, read by CI + Makefile
#   v5/client/main.go                             var version (fallback literal)
#   v5/client/main.go                             go:generate --product-version/--file-version
#   v5/client/internal/buildinfo/buildinfo.go      fallbackVersion
#   v5/client/wails.json                          Wails build metadata
#   v5/client/frontend/package.json               npm build metadata
#   v5/client/frontend/package-lock.json          npm lockfile root (see DRIFT below)
#
# Editing them by hand is how drift happens. In 2.1.0 the go:generate DIRECTIVE
# was bumped but the committed rsrc_windows_*.syso were not regenerated, so the
# shipped Windows executables reported version "2.0.0" and product name "MyVPN"
# in their Properties tab — a defect that was invisible to every text-based
# check because a correct directive says nothing about the artifact. That is why
# this script regenerates the resources and then re-reads them OUT of the
# binaries before declaring success.
#
# DRIFT (found 2026-09-19, fixed here): frontend/package-lock.json was rooted at
# "2.0.0" while package.json said "2.2.0", and nothing checked the lockfile at
# all. CI runs `npm install` (not `npm ci`) and ships only built output, so the
# drift was harmless — but it would have become a hard CI failure the day
# anyone switched to `npm ci`. This script now bumps it, and
# version_consistency_test.go asserts it.
#
# WHAT IT DOES NOT DO
# -------------------
# It does NOT touch git. No add, no commit, no tag. It rewrites files, verifies
# the result, and prints the exact git commands for you to run. Run it on a
# clean tree — a failed run leaves edits behind, and you want those edits to be
# the only thing in `git diff`.
#
# Usage:
#   v5/server/scripts/bump-version.sh patch          # 2.2.0 -> 2.2.1
#   v5/server/scripts/bump-version.sh minor          # 2.2.0 -> 2.3.0
#   v5/server/scripts/bump-version.sh major          # 2.2.0 -> 3.0.0
#   v5/server/scripts/bump-version.sh 2.5.3          # explicit
#   v5/server/scripts/bump-version.sh patch --dry-run
#   v5/server/scripts/bump-version.sh patch --no-verify   # skip the test gate
#
# Environment:
#   GO_BIN   path to the go binary if it is not on PATH (see local-toolchain note
#            in docs/OPS.md — this repo has no system Go)
#
# Exit codes: 0 success, 1 usage/validation error, 2 rewrite failure,
#             3 self-check (regeneration/consistency) failure.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

VERSION_FILE="v5/VERSION"
MAIN_GO="v5/client/main.go"
BUILDINFO_GO="v5/client/internal/buildinfo/buildinfo.go"
WAILS_JSON="v5/client/wails.json"
PKG_JSON="v5/client/frontend/package.json"
PKG_LOCK="v5/client/frontend/package-lock.json"

# ── The accounting list ────────────────────────────────────────────────────
# Every file whose copy of the version this script knows how to update. The
# drift guard (scripts/check-version-drift.sh) scans for the OLD version string
# and requires every hit to be inside one of these files — so an unaccounted
# copy is a test failure rather than a silent divergence. If you add a copy,
# add it here AND in version_consistency_test.go's consistency checks.
KNOWN_VERSION_FILES=(
    "$VERSION_FILE"
    "$MAIN_GO"
    "$BUILDINFO_GO"
    "$WAILS_JSON"
    "$PKG_JSON"
    "$PKG_LOCK"
)

DRY_RUN=0
VERIFY=1
BUMP=""

log()  { printf '\033[0;32m[bump]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[bump][WARN]\033[0m %s\n' "$*"; }
fail() { printf '\033[0;31m[bump][FAIL]\033[0m %s\n' "$*" >&2; exit "${2:-1}"; }

while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run)   DRY_RUN=1 ;;
        --no-verify) VERIFY=0 ;;
        -h|--help)   sed -n '2,45p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        -*)          fail "unknown option: $1" ;;
        *)           [ -z "$BUMP" ] || fail "only one version argument is accepted (got '$BUMP' and '$1')"
                     BUMP="$1" ;;
    esac
    shift
done

[ -n "$BUMP" ] || fail "usage: $0 <major|minor|patch|x.y.z> [--dry-run] [--no-verify]"

# ── Read the current version ───────────────────────────────────────────────
[ -f "$VERSION_FILE" ] || fail "missing $VERSION_FILE — it is the single source of truth"
CURRENT="$(tr -d '[:space:]' < "$VERSION_FILE")"
case "$CURRENT" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) fail "$VERSION_FILE does not look like a version: '${CURRENT}'" ;;
esac

# ── Compute the next version ───────────────────────────────────────────────
case "$BUMP" in
    major|minor|patch)
        IFS=. read -r MA MI PA <<EOF
$CURRENT
EOF
        # Guard against a pre-release suffix reaching the arithmetic (e.g. if
        # VERSION were "2.2.0-rc1"). Bumping a pre-release by segment would
        # silently produce a nonsense number, so refuse rather than guess.
        case "$CURRENT" in
            *-*) fail "$VERSION_FILE contains a pre-release suffix ('$CURRENT'); pass an explicit version instead" ;;
        esac
        case "$BUMP" in
            major) NEXT="$((MA + 1)).0.0" ;;
            minor) NEXT="${MA}.$((MI + 1)).0" ;;
            patch) NEXT="${MA}.${MI}.$((PA + 1))" ;;
        esac
        ;;
    [0-9]*.[0-9]*.[0-9]*)
        NEXT="$BUMP"
        case "$NEXT" in
            *[!0-9.]*) fail "explicit version '$NEXT' may contain only digits and dots" ;;
        esac
        ;;
    *)
        fail "unknown bump '$BUMP' — expected major, minor, patch, or an explicit x.y.z"
        ;;
esac

[ "$NEXT" != "$CURRENT" ] || fail "next version equals current (${CURRENT}) — nothing to do"

log "current: ${CURRENT}"
log "next:    ${NEXT}"

# ── Refuse a dirty tree ────────────────────────────────────────────────────
# A failed run leaves the rewritten files in place. If the tree were already
# dirty you could not tell our edits from yours, and `git checkout --` would
# discard your work along with ours.
if [ "$DRY_RUN" != "1" ]; then
    DIRTY="$(git status --porcelain)"
    if [ -n "$DIRTY" ]; then
        printf '%s\n' "$DIRTY" >&2
        fail "working tree is dirty — commit or stash first, so a failed bump is recoverable with 'git checkout -- <files>'"
    fi
fi

for f in "${KNOWN_VERSION_FILES[@]}"; do
    [ -f "$f" ] || fail "expected file is missing: $f"
done

# ── Show what will change ──────────────────────────────────────────────────
# Count the occurrences we intend to replace, per file, so "0 replacements"
# cannot pass as success. This is the check that would have caught a copy whose
# formatting drifted (e.g. a reordered JSON key) and therefore no longer
# matched the expected literal.
count_in() { grep -c -- "$1" "$2" 2>/dev/null || true; }

log "occurrences of '${CURRENT}' to replace:"
for f in "${KNOWN_VERSION_FILES[@]}"; do
    printf '  %-48s %s\n' "$f" "$(count_in "$CURRENT" "$f")"
done

# ── Apply ──────────────────────────────────────────────────────────────────
# `replace_exact` is deliberately strict: it fails if the expected literal is
# absent. sed would happily exit 0 having changed nothing, which is how a
# half-bumped tree looks identical to a fully-bumped one.
replace_exact() {
    local file="$1" pattern="$2" replacement="$3"
    if [ "$DRY_RUN" = "1" ]; then
        printf '  would edit %s: %s\n' "$file" "$pattern"
        return 0
    fi
    # Use a temp file rather than `sed -i` so a partial write cannot leave a
    # corrupt source file behind on failure.
    local tmp
    tmp="$(mktemp)"
    if ! sed "s|${pattern}|${replacement}|g" "$file" > "$tmp"; then
        rm -f "$tmp"
        fail "sed failed on $file" 2
    fi
    if cmp -s "$file" "$tmp"; then
        rm -f "$tmp"
        fail "no replacement was made in ${file} — expected literal '${pattern}' not found. Has the file's formatting changed?" 2
    fi
    cat "$tmp" > "$file"
    rm -f "$tmp"
}

if [ "$DRY_RUN" = "1" ]; then
    log "DRY RUN — no files will be written"
fi

# 1. Canonical version file. Written without a trailing newline, matching the
#    existing format (the Makefile and CI both `tr -d '[:space:]'` it, but a
#    stray newline is a diff noise generator).
if [ "$DRY_RUN" = "1" ]; then
    log "would write ${VERSION_FILE}: ${NEXT}"
else
    printf '%s' "$NEXT" > "$VERSION_FILE"
fi

# 2. main.go: the runtime fallback literal. Anchored on `var version = "` so it
#    cannot match the go:generate line or a comment.
replace_exact "$MAIN_GO" "var version = \"${CURRENT}\"" "var version = \"${NEXT}\""

# 3. main.go: the go:generate directive, which stamps the Windows .syso.
replace_exact "$MAIN_GO" "--product-version ${CURRENT} --file-version ${CURRENT}" \
                          "--product-version ${NEXT} --file-version ${NEXT}"

# 4. buildinfo.go: the fallback literal an uninstrumented build reports.
replace_exact "$BUILDINFO_GO" "const fallbackVersion = \"${CURRENT}\"" \
                              "const fallbackVersion = \"${NEXT}\""

# 5. wails.json. Anchored on the `"version": "x.y.z"` key so a dependency or
#    schema URL carrying the same digits cannot be hit.
replace_exact "$WAILS_JSON" "\"version\": \"${CURRENT}\"" "\"version\": \"${NEXT}\""

# 6. frontend/package.json — only the version key. The frontend build tag is not
#    a version, so this must not touch dependencies.
replace_exact "$PKG_JSON" "\"version\": \"${CURRENT}\"" "\"version\": \"${NEXT}\""

# 7. frontend/package-lock.json. Two lines carry the root version: the top-level
#    "version" and packages."" .version. Node 24's `npm install
#    --package-lock-only` also rewrites dependency RANGES (observed: it changed
#    vite from ^5.1.0 to ^5.4.21 on an unrelated global change), which is not
#    ours to make inside a version bump. So edit the two lines positionally: line
#    3 is the root, line 9 is packages[""]. Guarded by an explicit shape check.
LOCK_ROOT_LINE=3
LOCK_PKG_LINE=9
if [ "$DRY_RUN" != "1" ]; then
    lock_line() { sed -n "$1p" "$PKG_LOCK"; }
    for pair in "${LOCK_ROOT_LINE}:root" "${LOCK_PKG_LINE}:packages[\"\"]"; do
        lineno="${pair%%:*}"; label="${pair#*:}"
        actual="$(lock_line "$lineno")"
        if ! printf '%s' "$actual" | grep -q "\"version\": \"${CURRENT}\""; then
            fail "${PKG_LOCK} line ${lineno} (${label}) is '${actual}', expected the version key for '${CURRENT}'.\n"\
"The lockfile layout has changed. Update LOCK_ROOT_LINE/LOCK_PKG_LINE in this script, then re-run." 2
        fi
    done
    sed -i "${LOCK_ROOT_LINE}s|\"version\": \"${CURRENT}\"|\"version\": \"${NEXT}\"|; ${LOCK_PKG_LINE}s|\"version\": \"${CURRENT}\"|\"version\": \"${NEXT}\"|" "$PKG_LOCK"
else
    log "would edit ${PKG_LOCK} lines ${LOCK_ROOT_LINE} and ${LOCK_PKG_LINE}: ${CURRENT} -> ${NEXT}"
fi

[ "$DRY_RUN" != "1" ] && log "rewrote ${#KNOWN_VERSION_FILES[@]} files"

# ── Drift guard: no copy may remain ────────────────────────────────────────
# Scan the whole client tree for the OLD version and require every hit to be a
# file we know about. A hit in an unaccounted file means a seventh copy exists
# that this script (and the consistency test) does not maintain — fail and name
# it rather than shipping a binary that lies.
if [ "$DRY_RUN" != "1" ]; then
    STRAY=0
    while IFS= read -r hit; do
        [ -n "$hit" ] || continue
        accounted=0
        for f in "${KNOWN_VERSION_FILES[@]}"; do
            [ "$hit" = "$f" ] && accounted=1 && break
        done
        # Also allow the version string in documentation and test fixtures:
        # docs record historical versions, and version_test.go uses literals on
        # purpose. These are not copies that a build reads.
        case "$hit" in
            v5/docs/*|*/version_test.go|*/version_consistency_test.go|*/winres_test.go) accounted=1 ;;
        esac
        if [ "$accounted" = "0" ]; then
            warn "unaccounted copy of ${CURRENT} in: ${hit}"
            STRAY=1
        fi
    done <<EOF
$(grep -rl --include='*.go' --include='*.json' --include='*.ts' --include='*.js' \
        --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=.git \
        -e "${CURRENT}" v5/client 2>/dev/null || true)
EOF
    if [ "$STRAY" = "1" ]; then
        fail "the old version ${CURRENT} survives in files this script does not manage.\n"\
"Add the file to KNOWN_VERSION_FILES and to version_consistency_test.go, or remove the copy." 2
    fi
    log "drift guard: no unaccounted copies of ${CURRENT}"
fi

# ── Self-check: stamp the Windows resources, then read them back ───────
# This is the 2.1.0 failure, and it came back in 2.2.7. The .syso is committed,
# the directive is text, and only the compiled artifact carries the truth. A
# correct directive with a stale .syso passes every grep-based check in CI and
# still ships a Windows exe whose Properties tab names a wrong version — the
# exact breakage that failed the 2.2.7 release.
#
# ── WHY THIS NO LONGER NEEDS A GO TOOLCHAIN ───────────────────────────────
# It used to be `go generate -tags windows`, which meant every release depended
# on a working Go install AND on skipping the step under --no-verify, which is
# how the artifacts went stale twice. A patch-level bump (2.2.6 -> 2.2.7) only
# changes four bytes: the two UTF-16LE display strings and the packed
# VS_FIXEDFILEINFO block. stamp-syso.py rewrites exactly those with the standard
# library, and its output is byte-for-byte identical to go-winres' own (verified
# in both directions against the real 2.1.0..2.2.7 artifacts).
#
# So the stamping now happens BEFORE the --no-verify branch, and unconditionally:
# --no-verify skips the *test* gate, never the artifact update. A version bump
# that changes the string WIDTH (2.9.9 -> 2.10.0) cannot be byte-patched; the
# tool refuses, and this script falls back to go generate when one is available.
if [ "$DRY_RUN" = "1" ]; then
    log "DRY RUN — skipping .syso stamping and consistency tests"
    log "done (dry run): ${CURRENT} -> ${NEXT}"
    exit 0
fi

GO="${GO_BIN:-go}"
STAMP="${REPO_ROOT}/v5/server/scripts/stamp-syso.py"
stamped=0

if [ -f "$STAMP" ] && command -v python3 >/dev/null 2>&1; then
    # Force the client dir to the repo copy so an out-of-tree CWD cannot stamp
    # the wrong artifacts.
    #
    # `set -e` is active, so the call must be guarded with `|| rc=$?` rather than
    # tested directly — and the code has to be captured in the same statement,
    # because `$?` after an `if`/`elif` belongs to the last command the shell
    # ran, not to the one you meant.
    rc=0
    python3 "$STAMP" "$NEXT" --client-dir "v5/client" || rc=$?
    case "$rc" in
        0) stamped=1 ;;
        # 3 = "this is not a file I know how to byte-patch" (a width change, or
        # an unfamiliar resource format). Not fatal on its own: fall back to
        # go-winres below if it is available.
        3) warn "stamp-syso.py could not byte-patch these artifacts (a version"
           warn "width change, or an unfamiliar resource format). Will fall back"
           warn "to go generate if a Go toolchain is available." ;;
        2) warn "stamp-syso.py reported the artifacts stale but did not patch them." ;;
        *) fail "stamp-syso.py failed (exit ${rc})" 3 ;;
    esac
else
    warn "stamp-syso.py or python3 missing — cannot stamp the Windows resources directly."
fi

if [ "$stamped" != "1" ]; then
    if command -v "$GO" >/dev/null 2>&1; then
        # `go generate -tags windows` fetches winres via the directive, so this
        # needs network on a first run.
        log "regenerating rsrc_windows_{amd64,arm64}.syso with go-winres…"
        ( cd v5/client && "$GO" generate -tags windows ) || fail "go generate -tags windows failed" 3
        stamped=1
    else
        warn "no Go toolchain on PATH and stamp-syso.py did not run."
        warn "The committed rsrc_windows_*.syso are STALE for ${NEXT}."
        warn "CI will fail 'Check version consistency' on this tree."
        warn "Fix: install Go (docs/OPS.md) or ensure python3 is available,"
        warn "     then re-run: ./bump.sh ${NEXT}"
    fi
fi

for arch in amd64 arm64; do
    syso="v5/client/rsrc_windows_${arch}.syso"
    [ -f "$syso" ] || fail "expected ${syso}" 3
done

# A byte-identical .syso after a real bump means the stamp silently did nothing.
if [ "$stamped" = "1" ] && [ "$VERIFY" = "1" ] && git diff --quiet -- v5/client/rsrc_windows_amd64.syso; then
    warn "rsrc_windows_amd64.syso is byte-identical after stamping — expected a version change."
fi

if [ "$VERIFY" != "1" ]; then
    if [ "$stamped" = "1" ]; then
        warn "--no-verify: the .syso artifacts ARE stamped for ${NEXT}; the test gate is skipped."
        warn "Run before tagging: cd v5/client && go test . ./internal/winres/ -run 'Version|Syso|Consistency' -count=1"
    fi
    printf '\nNext:\n  git add -A && git commit -m "chore(release): %s"\n  git tag -a v%s -m "Locus %s"\n  git push origin main\n  git push origin v%s\n' "$NEXT" "$NEXT" "$NEXT" "$NEXT"
    exit 0
fi

command -v "$GO" >/dev/null 2>&1 || fail "no Go toolchain on PATH (set GO_BIN=/path/to/go).\n"\
"The artifacts were stamped, but the consistency gate needs Go.\n"\
"Local toolchain bootstrap is documented in v5/docs/OPS.md." 3

log "running the version-consistency gate…"
# Run the two guards that matter here and nothing else: the full suite is the
# release gate, not the bump gate. TestCommittedSysoMatchesRepoVersion reads the
# version OUT of the compiled resource, which is the only check that can catch a
# stale artifact.
( cd v5/client && "$GO" test . ./internal/winres/ -run 'Version|Syso|Consistency' -count=1 ) \
    || fail "version consistency tests FAILED — the tree is inconsistent. Fix before committing." 3

log "✓ ${CURRENT} -> ${NEXT} — all copies updated, resources stamped, consistency gate passed"
cat <<EOF

Nothing has been committed. Next:

  git add -A
  git commit -m "chore(release): ${NEXT}"
  git tag -a v${NEXT} -m "Locus ${NEXT}"
  git push origin main
  git push origin v${NEXT}

Then publish (CI must have finished first):

  v5/server/scripts/publish-release.sh ${NEXT}      # or: --from-github
  v5/server/scripts/verify-release.sh ${NEXT}
EOF
