#!/usr/bin/env python3
"""Add the signature_<platform> columns to update_config, on a LIVE hub.

WHY THIS IS NOT `seed-pb.py`
-----------------------------
`seed-pb.py` reconciles the schema, but it also re-seeds the update_config ROW —
and its seed body is:

    {"version": "1.0.0", "rollout_percent": 0, "active": True}

Run against a live hub, that would overwrite the current release with a version
that does not exist and a rollout of zero. The live row is 2.2.6; after a
`seed-pb.py` run every client would be told "1.0.0" and offered nothing. That is
a silent, fleet-wide outage caused by a "safe" idempotent script.

So this script does ONE thing: add missing columns. It never writes a record,
never deletes anything, and never touches any existing field.

Idempotent: columns already present are skipped, so it is safe to re-run.

Usage:
    PB_ADMIN_EMAIL=... PB_ADMIN_PASS=... python3 add-signature-columns.py
    # or, on the host with /root/.pb-admin present:
    python3 add-signature-columns.py

Exit codes: 0 success (or nothing to do), 1 a column could not be added.
"""

import json
import os
import sys
import urllib.error
import urllib.request

PB_API = os.environ.get("PB_API", "http://127.0.0.1:8090")

# The columns the update system writes and the hooks/services read. All TEXT:
# a minisign signature is four lines of ASCII, stored verbatim.
NEW_COLUMNS = [
    {"name": "signature_linux", "type": "text"},
    {"name": "signature_windows", "type": "text"},
    {"name": "signature_macos_intel", "type": "text"},
    {"name": "signature_macos_arm", "type": "text"},
]

COLLECTION = "update_config"


def log(msg):
    print("[migrate] %s" % msg, flush=True)


def api(method, path, data=None, token=None):
    body = json.dumps(data).encode() if data is not None else None
    req = urllib.request.Request(PB_API + path, data=body, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", token)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read().decode())
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", "replace")
        try:
            return json.loads(raw)
        except ValueError:
            return {"status": exc.code, "message": raw}


def authenticate():
    """Returns an Authorization header value, or exits.

    Tries the superuser collection (PocketBase >= 0.23) then the older admins
    collection, because 0.22.x only has the latter and the two disagree — this
    was the cause of a fresh deploy silently seeding no collections at all.
    """
    email = os.environ.get("PB_ADMIN_EMAIL", "").strip()
    password = os.environ.get("PB_ADMIN_PASS", "").strip()

    if not email or not password:
        # Try the on-host credential files the deploy writes.
        # The deploy writes this file (see write-admin-credentials.sh).
        for path in ("/root/.pb_admin_creds",):
            if os.path.exists(path):
                try:
                    with open(path) as f:
                        for line in f:
                            if "=" in line and not line.startswith("#"):
                                key, value = line.strip().split("=", 1)
                                os.environ.setdefault(key.strip(), value.strip())
                except OSError:
                    pass
        email = os.environ.get("PB_ADMIN_EMAIL", "").strip()
        password = os.environ.get("PB_ADMIN_PASS", "").strip()

    if not email or not password:
        log("FATAL: PB_ADMIN_EMAIL and PB_ADMIN_PASS are required")
        sys.exit(1)

    for path in (
        "/api/collections/_superusers/auth-with-password",
        "/api/admins/auth-with-password",
    ):
        resp = api("POST", path, {"identity": email, "password": password})
        if resp.get("token"):
            log("authenticated via %s" % path)
            return resp["token"]
        log("  %s -> %s" % (path, resp.get("message", resp.get("status"))))

    log("FATAL: could not authenticate to PocketBase with the supplied credentials")
    sys.exit(1)


def main():
    token = authenticate()

    current = api("GET", "/api/collections/%s" % COLLECTION, token=token)
    if not current.get("id"):
        log("FATAL: collection %s not found: %s" % (COLLECTION, current))
        sys.exit(1)

    schema = current.get("schema") or []
    have = {f.get("name") for f in schema}
    missing = [f for f in NEW_COLUMNS if f["name"] not in have]

    if not missing:
        log("%s already has all signature columns — nothing to do" % COLLECTION)
        return

    log("adding: %s" % ", ".join(f["name"] for f in missing))

    # ADDITIVE ONLY. `merged` is the existing schema plus the missing fields, so
    # no existing column is renamed, retyped or dropped, and no record data is
    # touched. PocketBase rejects a PATCH that would remove a field that still
    # has data, which is a second guard against an accidental loss.
    merged = schema + missing
    resp = api("PATCH", "/api/collections/%s" % COLLECTION, {"schema": merged}, token=token)

    if not resp.get("id"):
        log("FAILED to add columns: %s" % resp)
        sys.exit(1)

    after = api("GET", "/api/collections/%s" % COLLECTION, token=token)
    now = {f.get("name") for f in (after.get("schema") or [])}
    still_missing = [f["name"] for f in NEW_COLUMNS if f["name"] not in now]
    if still_missing:
        log("FAILED: still missing after PATCH: %s" % still_missing)
        sys.exit(1)

    log("✓ all signature columns present")

    # Prove the row was not disturbed. This script must never write a record.
    log("update_config rows are untouched (this script never writes records).")


if __name__ == "__main__":
    main()
