#!/usr/bin/env python3
"""
fix-tier-configs.py — repair tier_configs on a LIVE VPS.

WHY: activation/heartbeat serve connection parameters from PocketBase's
`tier_configs` collection. If that collection is empty, has stale passwords,
or contains duplicates (older seed scripts always POSTed new records), clients
receive wrong/stale passwords and the Shadowsocks server rejects them with
"An existing connection was forcibly closed by the remote host".

WHAT THIS DOES:
  1. Reads the GROUND-TRUTH passwords from /etc/shadowsocks/{eco,stealth,strike}.json
  2. Authenticates against PocketBase using /root/.pb_admin_creds (PB_TOKEN)
  3. Replaces every tier_configs record with a fresh one matching the live
     ssserver configs (server = the configured DOMAIN, ports 8443/8444/8445)
  4. Prints the final state + the collection schema (diagnostics)

RUN (on the VPS, as root):
    python3 fix-tier-configs.py

Requires: curl, root, PocketBase running on 127.0.0.1:8090.
"""
import json
import os
import subprocess
import sys

API = "http://127.0.0.1:8090"
DOMAIN = os.environ.get("DOMAIN", "networkingguides.duckdns.org")
ADMIN_CREDS = "/root/.pb_admin_creds"
SS_CONFIG_DIR = "/etc/shadowsocks"

# udp_relay MUST stay false: it historically enabled the client's sing-box
# "udp_over_tcp v2" option, but that protocol is proprietary to sing-box —
# shadowsocks-rust closes those connections (RST, observed 2026-08-01).
# UDP flows via standard ss UDP (server mode tcp_and_udp).
TIERS = [
    ("eco", 8443, False),
    ("stealth", 8444, False),
    ("strike", 8445, False),
]


def log(msg):
    print(msg)


def api(method, path, data=None, token=None):
    cmd = ["curl", "-s", "-X", method, f"{API}{path}", "-H", "Content-Type: application/json"]
    if token:
        cmd += ["-H", f"Authorization: {token}"]
    if data is not None:
        tf = f"/tmp/fixcfg_{os.getpid()}.json"
        with open(tf, "w") as f:
            json.dump(data, f)
        cmd += ["-d", f"@{tf}"]
    r = subprocess.run(cmd, capture_output=True, text=True)
    try:
        return json.loads(r.stdout)
    except json.JSONDecodeError:
        return {"error": r.stdout[:300]}


def main():
    log("== MyVPN tier_configs repair ==")

    # ── 0. Sanity checks ──
    if not os.path.isdir(SS_CONFIG_DIR):
        log(f"ERROR: {SS_CONFIG_DIR} not found — is this the VPN VPS?")
        sys.exit(1)

    # ── 1. Ground-truth passwords from live ssserver configs ──
    live = {}
    for tier, port, _udp in TIERS:
        path = os.path.join(SS_CONFIG_DIR, f"{tier}.json")
        try:
            with open(path) as f:
                cfg = json.load(f)
            live[tier] = {
                "password": cfg.get("password", ""),
                "port": cfg.get("server_port", port),
                "method": cfg.get("method", "aes-256-gcm"),
            }
            log(f"  {tier}: ssserver on :{live[tier]['port']} (method {live[tier]['method']})")
        except Exception as ex:
            log(f"ERROR: cannot read {path}: {ex}")
            sys.exit(1)

    # ── 2. Admin auth ──
    token = ""
    if os.path.exists(ADMIN_CREDS):
        with open(ADMIN_CREDS) as f:
            for line in f:
                if "=" in line and not line.startswith("#"):
                    k, v = line.strip().split("=", 1)
                    if k == "PB_TOKEN":
                        token = v
    if not token:
        log(f"ERROR: no PB_TOKEN in {ADMIN_CREDS} — cannot authenticate.")
        sys.exit(1)
    test = api("GET", "/api/collections", token=token)
    if test.get("items") is None:
        log("ERROR: admin token rejected. Regenerate it:")
        log("  # on the VPS, as the pocketbase user or via the admin UI:")
        log("  curl -X POST http://127.0.0.1:8090/api/admins/auth-with-password \\")
        log("    -H 'Content-Type: application/json' \\")
        log("    -d '{\"identity\":\"admin@YOURDOMAIN\",\"password\":\"ADMIN_PASSWORD\"}'")
        sys.exit(1)
    log("  ✓ admin token valid")

    # ── 3. Collection schema (diagnostics — a mismatched schema explains
    #       'Failed to create record' 400s from the public API) ──
    schema = api("GET", "/api/collections/tier_configs", token=token)
    if schema.get("fields"):
        log("  tier_configs schema:")
        for f in schema["fields"]:
            log(f"    - {f.get('name')} ({f.get('type')}) required={f.get('required')}")
    else:
        log("  WARNING: cannot read tier_configs schema — it may not exist!")
        log("  Create it by re-running the seed script, or in the admin UI.")

    # ── 4. Replace all tier_configs records with the live passwords ──
    existing = api("GET", "/api/collections/tier_configs/records?perPage=200", token=token)
    items = existing.get("items", []) if isinstance(existing, dict) else []
    log(f"  existing records: {len(items)}")
    for rec in items:
        resp = api("DELETE", f"/api/collections/tier_configs/records/{rec['id']}", token=token)
        log(f"    deleted {rec.get('tier')} ({rec['id'][:12]}) -> {resp.get('code', 'ok')}")

    for tier, port, udp in TIERS:
        body = {
            "tier": tier,
            "config": json.dumps({
                "server": DOMAIN,
                "server_port": live[tier]["port"],
                "password": live[tier]["password"],
                "method": live[tier]["method"],
            }),
            "active": True,
            "udp_relay": udp,
        }
        resp = api("POST", "/api/collections/tier_configs/records", body, token=token)
        if resp.get("id"):
            log(f"  ✓ seeded {tier} -> {DOMAIN}:{live[tier]['port']}")
        else:
            log(f"  ✗ FAILED to seed {tier}: {resp}")

    # ── 5. Verify ──
    final = api("GET", "/api/collections/tier_configs/records?perPage=200", token=token)
    items = final.get("items", []) if isinstance(final, dict) else []
    log(f"== final tier_configs: {len(items)} record(s) ==")
    for rec in items:
        try:
            cfg = json.loads(rec.get("config", "{}"))
        except Exception:
            cfg = rec.get("config", {})
        log(f"  {rec.get('tier')}: {cfg.get('server')}:{cfg.get('server_port')} pass={str(cfg.get('password'))[:6]}... udp={rec.get('udp_relay')}")

    log("Done. Clients will refresh automatically via heartbeat within 5 minutes,")
    log("or immediately on next activation.")


if __name__ == "__main__":
    main()
