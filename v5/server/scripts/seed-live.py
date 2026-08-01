#!/usr/bin/env python3
"""MyVPN live-seed for a PocketBase on a (re-)provisioned VPS.

Creates the collections, update_config, tier_configs (reading passwords from
/etc/shadowsocks/*.json — the single source of truth on the box) and seeds
activation codes from /tmp/codes.json. Idempotent. Includes a throwaway
end-to-end activation test (a generated test code is activated against the
public /api/activate endpoint, verified, then deleted).

Usage (on the VPS):
    python3 /tmp/seed-live.py
"""
import json
import os
import secrets
import subprocess
import sys

API = "http://127.0.0.1:8090"
DOMAIN = os.environ.get("DOMAIN", "networkingguides.duckdns.org")
ADMIN_EMAIL = f"admin@{DOMAIN}"
CREDS = "/root/.pb_admin_creds"
CODES_FILE = "/tmp/codes.json"

CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
N = len(CHARSET)

COLLECTIONS = [
    ("codes", [
        {"name": "code", "type": "text", "required": True, "unique": True},
        {"name": "tier", "type": "text", "required": True},
        {"name": "used", "type": "bool"},
        {"name": "suspended", "type": "bool"},
        {"name": "bound_fingerprint", "type": "text"},
        {"name": "expires_at", "type": "date"},
        {"name": "activated_at", "type": "date"},
        {"name": "middleman", "type": "text"},
    ]),
    ("tier_configs", [
        {"name": "tier", "type": "text", "required": True, "unique": True},
        {"name": "config", "type": "text", "required": True},
        {"name": "active", "type": "bool"},
        {"name": "udp_relay", "type": "bool"},
    ]),
    ("activation_attempts", [
        {"name": "ip", "type": "text"},
        {"name": "rate_key", "type": "text"},
        {"name": "fingerprint", "type": "text"},
        {"name": "code_attempted", "type": "text"},
    ]),
    ("update_config", [
        {"name": "version", "type": "text", "required": True},
        {"name": "rollout_percent", "type": "number"},
        {"name": "active", "type": "bool"},
        {"name": "update_url", "type": "text"},
        {"name": "update_sha256", "type": "text"},
        {"name": "download_linux", "type": "text"},
        {"name": "download_windows", "type": "text"},
        {"name": "download_macos_intel", "type": "text"},
        {"name": "download_macos_arm", "type": "text"},
    ]),
]

RULES = {
    "listRule": "@request.auth.admin = true",
    "viewRule": "@request.auth.admin = true",
    "createRule": "@request.auth.admin = true",
    "updateRule": "@request.auth.admin = true",
    "deleteRule": "@request.auth.admin = true",
}


def curl(method, path, data=None, token=None):
    cmd = ["curl", "-s", "-X", method, API + path,
           "-H", "Content-Type: application/json"]
    if token:
        cmd += ["-H", f"Authorization: {token}"]
    if data is not None:
        cmd += ["-d", json.dumps(data)]
    r = subprocess.run(cmd, capture_output=True, text=True)
    try:
        return json.loads(r.stdout)
    except json.JSONDecodeError:
        return {"_raw": (r.stdout or "")[:300]}


def get_token():
    token, pw = "", ""
    if os.path.exists(CREDS):
        for line in open(CREDS):
            line = line.strip()
            if line.startswith("PB_TOKEN="):
                token = line.split("=", 1)[1]
            if line.startswith("PB_ADMIN_PASS="):
                pw = line.split("=", 1)[1]
    if token:
        t = curl("GET", "/api/collections", token=token)
        if isinstance(t, dict) and "items" in t:
            print("✓ existing admin token valid")
            return token
        print("existing token invalid — re-authenticating")
    if pw:
        r = curl("POST", "/api/admins/auth-with-password",
                 {"identity": ADMIN_EMAIL, "password": pw})
        if r.get("token"):
            print("✓ re-authenticated with saved password")
            return r["token"]
    # Fresh DB: create the first admin (allowed without auth), then auth.
    pw = secrets.token_urlsafe(24)
    r = curl("POST", "/api/admins",
             {"email": ADMIN_EMAIL, "password": pw, "passwordConfirm": pw})
    if r.get("id"):
        r2 = curl("POST", "/api/admins/auth-with-password",
                  {"identity": ADMIN_EMAIL, "password": pw})
        if r2.get("token"):
            with open(CREDS, "w") as f:
                f.write(f"PB_ADMIN_EMAIL={ADMIN_EMAIL}\n"
                        f"PB_ADMIN_PASS={pw}\nPB_TOKEN={r2['token']}\n")
            os.chmod(CREDS, 0o600)
            print("✓ fresh admin created and creds saved")
            return r2["token"]
    print("AUTH FAILED:", json.dumps(r)[:300])
    sys.exit(1)


def make_test_code():
    body = "MYVPN" + "".join(secrets.choice(CHARSET) for _ in range(12))
    total, double = 0, False
    for i in range(len(body) - 1, -1, -1):
        val = CHARSET.index(body[i])
        if double:
            val *= 2
            if val >= N:
                val = val - N + 1
        total += val
        double = not double
    chk = CHARSET[(N - (total % N)) % N]
    code = body + chk
    return f"{code[0:5]}-{code[5:9]}-{code[9:13]}-{code[13:17]}-{code[17]}"


def main():
    token = get_token()

    # ── Collections ──
    existing = {c.get("name") for c in
                curl("GET", "/api/collections", token=token).get("items", [])}
    for name, schema in COLLECTIONS:
        if name in existing:
            print(f"  collection {name}: exists")
            continue
        body = {"name": name, "type": "base", "schema": schema}
        body.update(RULES)
        r = curl("POST", "/api/collections", body, token)
        print(f"  collection {name}: "
              f"{'created' if r.get('id') else 'FAIL ' + json.dumps(r)[:200]}")

    # ── update_config ──
    recs = curl("GET", "/api/collections/update_config/records?perPage=10",
                token=token).get("items", [])
    if not recs:
        r = curl("POST", "/api/collections/update_config/records",
                 {"version": "1.0.0", "rollout_percent": 0, "active": True},
                 token)
        print(f"  update_config: "
              f"{'seeded' if r.get('id') else 'FAIL ' + json.dumps(r)[:200]}")
    else:
        print(f"  update_config: exists ({len(recs)} record(s))")

    # ── tier_configs (passwords from /etc/shadowsocks/*.json) ──
    for t in ("eco", "stealth", "strike"):
        path = f"/etc/shadowsocks/{t}.json"
        if not os.path.exists(path):
            print(f"  tier {t}: MISSING {path} — skipping")
            continue
        cfg = json.load(open(path))
        udp = cfg.get("mode") == "tcp_and_udp"
        config_str = json.dumps({
            "server": DOMAIN,
            "server_port": cfg["server_port"],
            "password": cfg["password"],
            "method": cfg["method"],
        })
        recs = curl("GET",
                    f"/api/collections/tier_configs/records?filter=(tier='{t}')&perPage=100",
                    token=token).get("items", [])
        body = {"tier": t, "config": config_str, "active": True,
                "udp_relay": udp}
        if recs:
            rid = recs[0]["id"]
            r = curl("PATCH", f"/api/collections/tier_configs/records/{rid}",
                     body, token)
            ok = r.get("id")
            print(f"  tier {t}: {'updated ' + rid[:12] if ok else 'FAIL ' + json.dumps(r)[:200]}")
        else:
            r = curl("POST", "/api/collections/tier_configs/records", body,
                     token)
            print(f"  tier {t}: "
                  f"{'seeded' if r.get('id') else 'FAIL ' + json.dumps(r)[:200]}")

    # ── codes ──
    if not os.path.exists(CODES_FILE):
        print("WARN: no codes file at " + CODES_FILE + " — skipping codes")
    else:
        codes = json.load(open(CODES_FILE))
        existing_codes = {c.get("code") for c in
                          curl("GET", "/api/collections/codes/records?perPage=200",
                               token=token).get("items", [])}
        added = 0
        for item in codes:
            code = item["code"]
            tier = item.get("tier", "eco")
            if code in existing_codes:
                continue
            r = curl("POST", "/api/collections/codes/records",
                     {"code": code, "tier": tier, "used": False,
                      "suspended": False,
                      "expires_at": "2027-07-29T02:17:35Z"}, token)
            if r.get("id"):
                added += 1
            else:
                print(f"  code {code[:12]}... FAIL {json.dumps(r)[:150]}")
        print(f"  codes: {added} added, {len(codes)} in file, "
              f"{len(existing_codes)} already present")

    # ── Verification ──
    print("── VERIFY ──")
    names = [c.get("name") for c in
             curl("GET", "/api/collections", token=token).get("items", [])]
    print("  collections:", names)
    for t in ("eco", "stealth", "strike"):
        recs = curl("GET",
                    f"/api/collections/tier_configs/records?filter=(tier='{t}')",
                    token=token).get("items", [])
        if recs:
            cfg = json.loads(recs[0]["config"])
            print(f"  tier {t}: {cfg['server']}:{cfg['server_port']} "
                  f"{cfg['method']} pw={cfg['password'][:6]}... "
                  f"udp={recs[0].get('udp_relay')}")
        else:
            print(f"  tier {t}: MISSING")
    cnt = curl("GET", "/api/collections/codes/records?perPage=200",
               token=token)
    print(f"  codes in db: {len(cnt.get('items', []))} "
          f"(total {cnt.get('totalItems')})")

    # ── Throwaway end-to-end activation test ──
    test_code = make_test_code()
    r = curl("POST", "/api/collections/codes/records",
             {"code": test_code, "tier": "eco", "used": False,
              "suspended": False}, token)
    if not r.get("id"):
        print("  TEST: could not create test code:", json.dumps(r)[:200])
        return
    test_id = r["id"]
    act = curl("POST", "/api/activate",
               {"code": test_code, "fingerprint": "testfp-" + secrets.token_hex(8)})
    if act.get("code") == 200 and act.get("server_config"):
        sc = act["server_config"]
        print(f"  TEST: activation OK -> server_config "
              f"{sc['server']}:{sc['server_port']} {sc['method']} "
              f"pw={sc['password'][:6]}... udp={act.get('udp_relay')}")
    else:
        print("  TEST: activation FAILED ->", json.dumps(act)[:300])
    curl("DELETE", f"/api/collections/codes/records/{test_id}", token=token)
    print("  TEST: test code deleted")


if __name__ == "__main__":
    main()
