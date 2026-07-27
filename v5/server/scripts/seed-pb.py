#!/usr/bin/env python3
"""
PocketBase bootstrap: creates admin, collections, seeds data.
Runs on a fresh PocketBase install. Idempotent — safe to re-run.
Called by 06-pocketbase.sh after PocketBase starts.
"""
import json, subprocess, os, sys, time

API = "http://127.0.0.1:8090"
DOMAIN = os.environ.get("DOMAIN", "")
if not DOMAIN:
    sys.exit("DOMAIN environment variable required")
ADMIN_CREDS = "/root/.pb_admin_creds"
ADMIN_EMAIL = f"admin@{DOMAIN}"

def api(method, path, data=None):
    """Make PocketBase API request using temp file to avoid shell issues."""
    cmd = ["curl", "-s", "-X", method, f"{API}{path}",
           "-H", "Content-Type: application/json"]
    if data is not None:
        tf = f"/tmp/pbapi_{os.getpid()}.json"
        with open(tf, "w") as f:
            json.dump(data, f)
        cmd += ["-d", f"@{tf}"]
    r = subprocess.run(cmd, capture_output=True, text=True)
    try:
        return json.loads(r.stdout)
    except json.JSONDecodeError:
        return {"error": r.stdout[:200], "raw": r.stdout[:200]}

def log(msg):
    print(msg)

# ── Step 1: Create or authenticate admin ──
if os.path.exists(ADMIN_CREDS):
    with open(ADMIN_CREDS) as f:
        for line in f:
            if "=" in line and not line.startswith("#"):
                k, v = line.strip().split("=", 1)
                os.environ[k] = v
    token = os.environ.get("PB_TOKEN", "")
    # Verify token still works
    test = api("GET", "/api/collections")
    if test.get("items") is not None:
        log("✓ Existing admin credentials valid")
        token_valid = True
    else:
        log("Existing token expired — will re-create admin")
        token_valid = False
else:
    token_valid = False

if not token_valid:
    # Create admin (no auth required on fresh DB)
    admin_pass = subprocess.run(
        ["openssl", "rand", "-base64", "24"],
        capture_output=True, text=True).stdout.strip()
    resp = api("POST", "/api/admins", {
        "email": ADMIN_EMAIL,
        "password": admin_pass,
        "passwordConfirm": admin_pass,
    })
    token = resp.get("token", resp.get("adminToken", ""))
    if not token:
        log(f"Admin creation returned: {resp.get('message', '')}")
        # Try auth with password
        resp = api("POST", "/api/admins/auth-with-password", {
            "identity": ADMIN_EMAIL,
            "password": admin_pass,
        })
        token = resp.get("token", "")
    if not token:
        log("Could not obtain admin token. Trying existing creds...")
        sys.exit(1)
    log(f"✓ Admin created: {ADMIN_EMAIL}")
    # Write with restrictive permissions atomically (0o600 = owner read/write only)
    fd = os.open(ADMIN_CREDS, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(f"# MyVPN PocketBase Admin Credentials\n"
                f"PB_ADMIN_EMAIL={ADMIN_EMAIL}\n"
                f"PB_ADMIN_PASS={admin_pass}\n"
                f"PB_TOKEN={token}\n")

# ── Step 2: Create collections ──
collections = [
    ("codes", [
        {"name":"code","type":"text","required":True,"unique":True},
        {"name":"tier","type":"text","required":True},
        {"name":"used","type":"bool"},
        {"name":"suspended","type":"bool"},
        {"name":"bound_fingerprint","type":"text"},
        {"name":"expires_at","type":"date"},
        {"name":"activated_at","type":"date"},
        {"name":"middleman","type":"text"},
    ]),
    ("tier_configs", [
        {"name":"tier","type":"text","required":True,"unique":True},
        {"name":"config","type":"text","required":True},
        {"name":"active","type":"bool"},
        {"name":"udp_relay","type":"bool"},
    ]),
    ("activation_attempts", [
        {"name":"ip","type":"text"},
        {"name":"rate_key","type":"text"},
        {"name":"fingerprint","type":"text"},
        {"name":"code_attempted","type":"text"},
    ]),
    ("update_config", [
        {"name":"version","type":"text","required":True},
        {"name":"rollout_percent","type":"number"},
        {"name":"active","type":"bool"},
        {"name":"update_url","type":"text"},
        {"name":"update_sha256","type":"text"},
            {"name":"download_linux","type":"text"},
            {"name":"download_windows","type":"text"},
            {"name":"download_macos_intel","type":"text"},
            {"name":"download_macos_arm","type":"text"},
    ]),
]

rules = {
    "listRule": "@request.auth.admin = true",
    "viewRule": "@request.auth.admin = true",
    "createRule": "@request.auth.admin = true",
    "updateRule": "@request.auth.admin = true",
    "deleteRule": "@request.auth.admin = true",
}

for name, schema in collections:
    # Check if collection already exists
    existing = api("GET", "/api/collections")
    found = any(c.get("name") == name for c in existing.get("items", []))
    if found:
        log(f"  {name}: already exists")
        continue
    # Create it
    body = {"name": name, "type": "base", "schema": schema}
    body.update(rules)
    resp = api("POST", "/api/collections", body)
    if resp.get("id"):
        log(f"  ✓ {name} created")
    else:
        log(f"  {name}: {resp.get('message', 'unknown')}")

# ── Step 3: Seed update_config ──
resp = api("POST", "/api/collections/update_config/records", {
    "version": "1.0.0",
    "rollout_percent": 0,
    "active": True,
})
log(f"  update_config: {'seeded' if resp.get('id') else resp.get('message', '')}")

# ── Step 4: Seed tier configs ──
# Priority: 1) Already in os.environ (from decrypted secrets via setup.sh)
#           2) /root/.tier_passwords file (from 02-shadowsocks.sh)
pw_file = "/root/.tier_passwords"
tiers_seeded = 0
pw_from_file = False

# Check if any tier passwords are already set in environment
for t_name in ["ECO_PASS", "STEALTH_PASS", "STRIKE_PASS"]:
    if os.environ.get(t_name, ""):
        pw_from_file = True  # actually from env, but indicates they're available
if not pw_from_file and os.path.exists(pw_file):
    with open(pw_file) as f:
        for line in f:
            if "=" in line and not line.startswith("#"):
                k, v = line.strip().split("=", 1)
                os.environ[k] = v
    pw_from_file = True

if pw_from_file:
    for t, port, udp in [("eco", 8443, False), ("stealth", 8444, False), ("strike", 8445, True)]:
        pw = os.environ.get(f"{t.upper()}_PASS", "")
        if not pw:
            log(f"  {t}: no password found — skipping")
            continue
        config_str = json.dumps({
            "server": DOMAIN, "server_port": port,
            "password": pw, "method": "aes-256-gcm",
        })
        resp = api("POST", "/api/collections/tier_configs/records", {
            "tier": t, "config": config_str, "active": True, "udp_relay": udp,
        })
        log(f"  {t}: {'seeded' if resp.get('id') else resp.get('message', '')}")
        tiers_seeded += 1
else:
    log("  No tier passwords found in environment or /root/.tier_passwords — skipping tier config seeding")
    log("  This is expected on a fresh deploy without secrets.env.age.")
    log("  Run 02-shadowsocks.sh first or provide secrets.env.age with tier passwords.")

log("✓ Bootstrap complete")
