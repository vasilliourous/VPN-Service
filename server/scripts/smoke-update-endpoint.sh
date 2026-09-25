#!/usr/bin/env bash
# smoke-update-endpoint.sh — prove the hub's update manifest agrees with the
# client's version rules, with no network and no changes to the live hub.
#
# WHY THIS EXISTS
#
# The hub and the client both decide "is this version newer?" — independently, on
# purpose (the hub must not advertise a downgrade even if update_config is stale;
# the client must not accept one even if the hub is wrong). Two implementations
# of one rule is exactly the arrangement that drifts, and the failure is silent:
# a client stops updating and nothing anywhere says why.
#
# The retired client learned this the expensive way. Its update gate once used a
# bare string comparison, under which "1.9.0" > "1.10.0" — so a client on 1.10.0
# would have happily installed 1.9.0. There is no server-driven downgrade in this
# system, so that would have been unrecoverable without shipping a higher
# version, which the downgraded client may not be able to fetch.
#
# This script extracts the version functions FROM THE HOOK SOURCE and exercises
# them, so the test cannot pass against a reimplementation that has drifted from
# what actually ships.
#
# Usage: server/scripts/smoke-update-endpoint.sh
#
# Exit codes: 0 all assertions held, 1 an assertion failed, 2 setup problem.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOOK="${REPO_ROOT}/server/pb_hooks/update.pb.js"

[ -f "$HOOK" ] || { echo "smoke-update: no such hook: $HOOK" >&2; exit 2; }
command -v node >/dev/null 2>&1 || { echo "smoke-update: node is required" >&2; exit 2; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# ── Extract the two pure functions from the shipped hook ──
# Everything between the `function parseVersion` and the end of `isNewer`.
python3 - "$HOOK" > "$WORK/logic.js" <<'PY'
import re, sys

src = open(sys.argv[1]).read()

def extract(name):
    m = re.search(rf"function {name}\(", src)
    if not m:
        sys.exit(f"smoke-update: could not find {name} in the hook")
    i = m.start()
    depth = 0
    seen = False
    while i < len(src):
        if src[i] == "{":
            depth += 1
            seen = True
        elif src[i] == "}":
            depth -= 1
            if seen and depth == 0:
                return src[m.start():i + 1]
        i += 1
    sys.exit(f"smoke-update: unterminated {name}")

print(extract("parseVersion"))
print(extract("isNewer"))
PY

if [ ! -s "$WORK/logic.js" ]; then
    echo "smoke-update: failed to extract the version logic" >&2
    exit 2
fi

# ── Exercise it ──
cat > "$WORK/run.js" <<'JS'
const fs = require('fs')
eval(fs.readFileSync(process.argv[2], 'utf8'))

let pass = 0, fail = 0
const check = (desc, actual, expected) => {
  if (actual === expected) { pass++; return }
  fail++
  console.log(`  \x1b[0;31mFAIL\x1b[0m ${desc} -> got ${actual}, want ${expected}`)
}

// The bug that motivates hand-writing this at all.
check('1.10.0 is newer than 1.9.0', isNewer('1.10.0', '1.9.0'), true)
check('1.9.0 is NOT newer than 1.10.0', isNewer('1.9.0', '1.10.0'), false)

// The no-downgrade rule the whole gate exists for.
check('equal is not newer', isNewer('2.0.0', '2.0.0'), false)
check('older is not newer', isNewer('1.9.9', '2.0.0'), false)

// Tag shapes this project actually produces.
check('leading v is ignored', isNewer('v2.0.0', '1.0.0'), true)
check('release beats its prerelease', isNewer('2.0.0', '2.0.0-rc1'), true)
check('prerelease is not newer than release', isNewer('2.0.0-rc1', '2.0.0'), false)
check('build metadata is ignored', isNewer('2.0.0+build5', '2.0.0'), false)

// Padding.
check('missing components are zero', isNewer('1.2.1', '1.2'), true)
check('1.2 == 1.2.0', isNewer('1.2', '1.2.0'), false)

// Never act on an input we cannot parse.
check('empty candidate refused', isNewer('', '1.0.0'), false)
check('empty current refused', isNewer('1.0.0', ''), false)

// Real versions from this project's release history.
check('2.2.7 > 2.2.6', isNewer('2.2.7', '2.2.6'), true)
check('2.3.0 > 2.2.8', isNewer('2.3.0', '2.2.8'), true)
check('2.2.2 NOT newer than 2.2.6', isNewer('2.2.2', '2.2.6'), false)

console.log(`  ${pass} assertion(s) passed`)
process.exit(fail === 0 ? 0 : 1)
JS

echo "smoke-update: exercising the version gate extracted from $(basename "$HOOK")"
node "$WORK/run.js" "$WORK/logic.js"
rc=$?

echo
if [ "$rc" -eq 0 ]; then
    printf '\033[0;32mThe hub update gate agrees with the documented version rules.\033[0m\n'
else
    printf '\033[0;31mThe hub update gate FAILED: a client would update (or refuse) incorrectly.\033[0m\n' >&2
fi
exit "$rc"
