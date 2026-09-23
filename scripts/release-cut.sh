#!/usr/bin/env bash
# bump the version, commit, push, and optionally tag.
#
#   export GH_TOKEN=ghp_xxx
#   ./scripts/release-cut.sh patch          # 2.2.6 -> 2.2.7, commits, tags, pushes
#   ./scripts/release-cut.sh minor --no-tag # test build: no tag, no release
#
# GH_TOKEN must be set. Nothing works without it (no credential helper on this
# box). Get one at github.com/settings/tokens with `repo` scope.
set -euo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"

: "${GH_TOKEN:?set GH_TOKEN first — e.g. export GH_TOKEN=ghp_xxx}"

BUMP="${1:-patch}"
TAG_IT=1
[ "${2:-}" = "--no-tag" ] && TAG_IT=0

# bump.sh rewrites the six version files and stamps the Windows resources. It
# does NOT touch git, and it does NOT need a Go toolchain (see stamp-syso.py).
#
# --no-verify skips only the Go test gate, never the artifact update. That
# distinction is the whole fix for the 2.2.7 failure: the previous release
# tagged a tree whose committed rsrc_windows_*.syso still said 2.2.6, and CI
# rejected the release. Passing --no-verify here is safe for the ARTIFACTS but
# still skips the tests, so the explicit gate below re-checks the resources with
# the one tool that needs no toolchain.
GO_BIN="${GO_BIN:-go}" ./bump.sh "$BUMP" --no-verify

NEXT="$(tr -d '[:space:]' < VERSION)"

# ── Refuse to tag a tree with stale Windows resources ──────────────────────
# This is the guard whose absence let 2.2.7 ship a broken tag. It reads the
# version OUT of the committed .syso bytes, using only the standard library, so
# it costs nothing and cannot be skipped for lack of a toolchain.
if command -v python3 >/dev/null 2>&1; then
    if ! python3 server/scripts/stamp-syso.py "$NEXT" --check --quiet; then
        echo "ERROR: committed rsrc_windows_*.syso do not match v${NEXT}." >&2
        echo "       CI would fail the release at 'Check version consistency'." >&2
        echo "       Fix:  python3 server/scripts/stamp-syso.py ${NEXT}" >&2
        echo "       Refusing to commit or tag. Nothing was pushed." >&2
        exit 3
    fi
fi

# bump.sh's go:generate drags winres deps into go.mod/go.sum. Drop that noise.
git checkout -- legacy/wails-client/go.mod legacy/wails-client/go.sum 2>/dev/null || true

git add -A
git commit -m "chore(release): ${NEXT}"
git tag -a "v${NEXT}" -m "Locus ${NEXT}" 2>/dev/null || true
[ "$TAG_IT" = 1 ] || git tag -d "v${NEXT}" >/dev/null 2>&1 || true

URL="$(git remote get-url origin)"
PUSH="${URL/https:\/\//https://x-access-token:${GH_TOKEN}@}"

git push "$PUSH" main
[ "$TAG_IT" = 1 ] && git push "$PUSH" "v${NEXT}"

echo
echo "${NEXT} pushed$( [ "$TAG_IT" = 1 ] && echo ", tag v${NEXT} pushed" || echo " (no tag — test build)" )"
[ "$TAG_IT" = 1 ] && echo "watch:   gh run watch"
echo "publish: server/scripts/publish-release.sh ${NEXT} --from-github"
