#!/usr/bin/env python3
"""Stamp middleman/expiry onto the first batch of freshly generated codes.

Called by setup.sh after FIRST_BATCH code generation. Exists as a separate
file rather than an inline heredoc because setup.sh is a bash script with its
own heredocs, and nesting python inside it is a quoting trap.

Usage:
    stamp-batch.py <admin_api_token> <middleman> <expires_at>

Either middleman or expires_at may be empty; only the non-empty ones are
applied. Only codes with NO middleman yet are touched, so re-running never
rewrites details an operator has already curated.

Talks to the admin console hook over loopback. That hook is the same audited
path the web console uses, so every change lands in code_events.
"""

import json
import sys
import urllib.error
import urllib.request

ENDPOINT = "http://127.0.0.1:8090/api/admin/console"


def rpc(token: str, action: str, **kwargs):
    """Call one admin console action. Returns the parsed JSON response."""
    body = {"action": action, "admin_token": token}
    body.update(kwargs)
    req = urllib.request.Request(
        ENDPOINT,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def main() -> int:
    if len(sys.argv) < 4:
        print("usage: stamp-batch.py <token> <middleman> <expires_at>")
        return 2

    token, middleman, expires = sys.argv[1], sys.argv[2].strip(), sys.argv[3].strip()

    if not token:
        print("stamp: no admin API token — nothing to do")
        return 0
    if not middleman and not expires:
        return 0

    try:
        listing = rpc(token, "codes.list")
    except (urllib.error.URLError, OSError) as exc:
        print(f"stamp: codes.list failed: {exc}")
        return 0

    if not listing.get("ok"):
        print(f"stamp: codes.list rejected: {listing.get('message')}")
        return 0

    # Only unlabelled codes, so an operator's edits are never overwritten.
    targets = [c["code"] for c in listing.get("codes", []) if not c.get("middleman")]
    if not targets:
        print("stamp: no unstamped codes found")
        return 0

    changed = 0
    for code in targets:
        try:
            if middleman:
                rpc(token, "codes.update", code=code, middleman=middleman)
            if expires:
                rpc(token, "codes.expire", code=code, expires_at=expires)
            changed += 1
        except (urllib.error.URLError, OSError, ValueError) as exc:
            print(f"stamp: failed for {code}: {exc}")

    print(f"stamp: updated {changed} of {len(targets)} codes")
    return 0


if __name__ == "__main__":
    sys.exit(main())
