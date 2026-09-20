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

# bump.sh rewrites the six version files. It does NOT touch git.
GO_BIN="${GO_BIN:-go}" ./bump.sh "$BUMP" --no-verify

NEXT="$(tr -d '[:space:]' < v5/VERSION)"

# bump.sh's go:generate drags winres deps into go.mod/go.sum. Drop that noise.
git checkout -- v5/client/go.mod v5/client/go.sum 2>/dev/null || true

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
echo "publish: v5/server/scripts/publish-release.sh ${NEXT} --from-github"
