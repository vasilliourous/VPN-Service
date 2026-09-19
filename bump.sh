#!/usr/bin/env bash
# bump.sh — the repo-root entry point for cutting a new Locus version.
#
# Usage:
#   ./bump.sh            # patch (2.2.0 -> 2.2.1) — the common case
#   ./bump.sh minor      # 2.2.0 -> 2.3.0
#   ./bump.sh major      # 2.2.0 -> 3.0.0
#   ./bump.sh 2.5.3      # explicit
#   ./bump.sh --dry-run  # show what would change, write nothing
#
# WHY THIS IS AT THE ROOT
#   The version-bearing files are spread across v5/server/scripts (the tooling),
#   v5/client (the Go + Wails metadata) and v5/VERSION (the canonical value).
#   Running the real work from the repo root means the paths are obvious and the
#   script cannot be run from a directory where it would half-apply.
#
#   The actual rewriting, resource regeneration and verification live in
#   v5/server/scripts/bump-version.sh, which is the single implementation. This
#   wrapper only computes the NEXT version from the current one, so you do not
#   have to remember it, and forwards everything else.
#
# NOTHING IS COMMITTED. The script stops before git entirely; it prints the
# commands. See the header of bump-version.sh for why.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMPL="${REPO_ROOT}/v5/server/scripts/bump-version.sh"
[ -x "$IMPL" ] || { echo "bump: missing or not executable: ${IMPL}" >&2; exit 1; }

# Pass everything through; bump-version.sh already understands
# major/minor/patch/x.y.z and --dry-run/--no-verify.
#
# Default to `patch` when no version word is given, so `./bump.sh --dry-run`
# works as well as `./bump.sh`. A bare flag is not a version, and treating it as
# one produced "usage: <major|minor|patch|x.y.z>" for a command that looked
# correct — the kind of papercut that teaches people the tool is broken.
BUMP_WORD=""
for arg in "$@"; do
    case "$arg" in
        -*) ;;
        *)  BUMP_WORD="$arg" ;;
    esac
done

if [ -z "$BUMP_WORD" ]; then
    set -- patch "$@"
fi

exec "$IMPL" "$@"
