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
PB_BINARY = os.environ.get("PB_BINARY", "/opt/pocketbase/pocketbase")
PB_DATA_DIR = os.environ.get("PB_DATA_DIR", "/opt/pocketbase/pb_data")

# ── PocketBase 0.22.x quirk: admin auth lives at /api/admins, NOT
# /api/collections/_superusers. The `_superusers` collection (and its
# auth-with-password endpoint) only exists in PB >= 0.23. Worse, in 0.22.x
# there is NO public API to create the FIRST admin at all — POST
# /api/collections/_superusers/records returns 404. The only supported
# bootstrap is the CLI: `pocketbase admin create <email> <pass>`.
# This was the cause of every fresh deploy failing to seed any collection
# (observed 2026-09-19: bootstrap aborted, tier_configs/codes/update_config
# never created, but setup.sh still reported success).
ADMIN_LOGIN_PATH = "/api/admins/auth-with-password"
SUPERUSERS_LOGIN_PATH = "/api/collections/_superusers/auth-with-password"

def create_admin_via_cli(email, password):
    """Create the first PB admin using the binary CLI (0.22.x has no API for it).
    Returns True on success. Idempotent-safe: an existing admin makes the CLI
    exit non-zero and we treat that as 'already present'."""
    if not os.path.exists(PB_BINARY):
        log(f"  PB binary not found at {PB_BINARY}; cannot create admin")
        return False
    r = subprocess.run(
        [PB_BINARY, "admin", "create", email, password, "--dir", PB_DATA_DIR],
        capture_output=True, text=True)
    out = (r.stdout or "") + (r.stderr or "")
    if r.returncode == 0:
        log(f"  ✓ Admin created via CLI: {email}")
        return True
    log(f"  CLI admin create: {out.strip()[:200]}")
    return False

def admin_login(email, password):
    """Authenticate as admin, trying the 0.22 (/api/admins) endpoint first and
    falling back to the 0.23+ (_superusers) one. Returns a token or ''."""
    for path in (ADMIN_LOGIN_PATH, SUPERUSERS_LOGIN_PATH):
        resp = api("POST", path, {"identity": email, "password": password})
        tok = resp.get("token") or resp.get("adminToken") or ""
        if tok:
            return tok
    return ""

def api(method, path, data=None):
    """Make PocketBase API request using temp file to avoid shell issues."""
    cmd = ["curl", "-s", "-X", method, f"{API}{path}",
           "-H", "Content-Type: application/json"]
    if TOKEN:
        cmd += ["-H", f"Authorization: {TOKEN}"]
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

# Counts collections that FAILED to create. A silent schema failure used to
# leave the hub looking "successful" while having no tables at all, so clients
# got 500s from the hooks. We now exit non-zero and setup.sh surfaces it.
SCHEMA_ERRORS = 0

TOKEN = ""

# ── Step 1: Create or authenticate admin ──
# NOTE: read the saved token into a LOCAL variable. This block used to copy
# every key from /root/.pb_admin_creds straight into os.environ, which silently
# CLOBBERED PB_ADMIN_PASS (sourced from secrets.env.age by setup.sh) with the
# possibly-stale value recorded in the file. That made the "is the env password
# authoritative?" check below always see the file's value, so a wrong recorded
# password could never be repaired — the script would keep "resetting" the
# admin to the same wrong value. (Diagnosed live 2026-09-19.)
saved_token = ""
if os.path.exists(ADMIN_CREDS):
    try:
        with open(ADMIN_CREDS) as f:
            for line in f:
                if line.startswith("PB_TOKEN="):
                    saved_token = line.strip().split("=", 1)[1]
    except OSError:
        saved_token = ""
    TOKEN = saved_token
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
    # Resolve the admin password. Precedence:
    #   1. PB_ADMIN_PASS from the environment (sourced from secrets.env.age) —
    #      this is authoritative, since it is what the operator has recorded.
    #   2. The value already recorded in /root/.pb_admin_creds.
    #   3. A freshly generated random password (last resort).
    #
    # We track whether the value came from the environment, because only then
    # may we OVERWRITE the server-side password. Reading a stale value out of
    # creds and pushing it back is what made a mismatch permanent — the earlier
    # code reset the live admin to whatever the (wrong) creds file held, then
    # re-wrote that same wrong value, so re-running never converged.
    env_pass = os.environ.get("PB_ADMIN_PASS", "")
    admin_pass = env_pass
    if not admin_pass and os.path.exists(ADMIN_CREDS):
        try:
            with open(ADMIN_CREDS) as f:
                for line in f:
                    if line.startswith("PB_ADMIN_PASS="):
                        admin_pass = line.strip().split("=", 1)[1]
        except OSError:
            pass
    if not admin_pass:
        admin_pass = subprocess.run(
            ["openssl", "rand", "-base64", "24"],
            capture_output=True, text=True).stdout.strip()
        log("  WARN: PB_ADMIN_PASS not provided — generated a random password")

    # 1. Can we log in with the resolved password?
    token = admin_login(ADMIN_EMAIL, admin_pass)

    # 2. No admin yet -> create it with the resolved password.
    if not token:
        if create_admin_via_cli(ADMIN_EMAIL, admin_pass):
            token = admin_login(ADMIN_EMAIL, admin_pass)
        if not token:
            resp = api("POST", "/api/collections/_superusers/records", {
                "email": ADMIN_EMAIL,
                "password": admin_pass,
                "passwordConfirm": admin_pass,
            })
            token = resp.get("token", resp.get("adminToken", "")) or \
                admin_login(ADMIN_EMAIL, admin_pass)

    # 3. Admin exists but our password does not match. Only force-reset when the
    #    password came from the environment (the operator's intended value) —
    #    overwriting a live password with a possibly-stale creds value is worse
    #    than reporting the mismatch.
    if not token and env_pass:
        log("  Admin password does not match PB_ADMIN_PASS — resetting to the intended value")
        subprocess.run(
            [PB_BINARY, "admin", "update", ADMIN_EMAIL, env_pass,
             "--dir", PB_DATA_DIR],
            capture_output=True, text=True)
        admin_pass = env_pass
        token = admin_login(ADMIN_EMAIL, admin_pass)

    if not token:
        log(f"Could not authenticate admin {ADMIN_EMAIL}. "
            "Set PB_ADMIN_PASS to the correct value, or reset it with: "
            f"{PB_BINARY} admin update {ADMIN_EMAIL} <newpass> --dir {PB_DATA_DIR}")
        sys.exit(1)
    log(f"✓ Admin ready: {ADMIN_EMAIL}")
    # Write with restrictive permissions atomically (0o600 = owner read/write only)
    fd = os.open(ADMIN_CREDS, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(f"# Locus PocketBase Admin Credentials\n"
                f"PB_ADMIN_EMAIL={ADMIN_EMAIL}\n"
                f"PB_ADMIN_PASS={admin_pass}\n"
                f"PB_TOKEN={token}\n")
    TOKEN = token

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
        # Administrative metadata. `unbound_at`/`unbind_reason` were already
        # being written by admin_unbind.pb.js but did NOT exist in the schema,
        # so PocketBase silently discarded them — the audit trail looked
        # implemented but recorded nothing. `notes`/`label` back the admin
        # console (who the code belongs to, free-form remarks).
        {"name":"unbound_at","type":"date"},
        {"name":"unbind_reason","type":"text"},
        {"name":"label","type":"text"},
        {"name":"notes","type":"text"},
    ]),
    # Append-only history of administrative and lifecycle actions, so the
    # console can show "what happened to this code" without guessing.
    ("code_events", [
        {"name":"code","type":"text","required":True},
        {"name":"event","type":"text","required":True},
        {"name":"detail","type":"text"},
        {"name":"actor","type":"text"},
        {"name":"fingerprint","type":"text"},
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
        # macOS columns: heartbeat.pb.js reads these. They were missing from the
        # schema, so every macOS client silently received no update signal at
        # all — the hook's get() returned undefined and the field was omitted.
        {"name":"download_macos_intel","type":"text"},
        {"name":"download_macos_arm","type":"text"},
        # Per-platform checksums. A single update_sha256 cannot describe four
        # different binaries, and the updater refuses to apply an update whose
        # SHA256 is empty — so without these, every non-matching platform would
        # download and then fail verification.
        {"name":"sha256_linux","type":"text"},
        {"name":"sha256_windows","type":"text"},
        {"name":"sha256_macos_intel","type":"text"},
        {"name":"sha256_macos_arm","type":"text"},
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
        # Reconcile columns. A collection created by an older deploy can be
        # missing fields added later — e.g. update_config gained the macOS
        # download columns, without which macOS clients silently received no
        # update signal. Only ever ADD missing fields; never remove or retype,
        # so this cannot destroy existing data.
        cur = api("GET", f"/api/collections/{name}")
        cur_schema = cur.get("schema") or []
        have = {f.get("name") for f in cur_schema}
        missing = [f for f in schema if f["name"] not in have]
        if missing:
            merged = cur_schema + missing
            resp = api("PATCH", f"/api/collections/{name}", {"schema": merged})
            if resp.get("id"):
                log(f"    + added column(s): {', '.join(f['name'] for f in missing)}")
            else:
                log(f"    ✗ could not add {', '.join(f['name'] for f in missing)}: {resp.get('message', resp)}")
                SCHEMA_ERRORS += 1
        continue
    # Create it
    body = {"name": name, "type": "base", "schema": schema}
    body.update(rules)
    resp = api("POST", "/api/collections", body)
    if resp.get("id"):
        log(f"  ✓ {name} created")
    else:
        log(f"  ✗ {name}: {resp.get('message', 'unknown')}")
        SCHEMA_ERRORS += 1

# ── Step 3: Seed update_config (idempotent upsert) ──
# This used to POST unconditionally, so every re-deploy appended another row
# (observed 2026-09-19: a second full setup.sh run left 2 rows). The client
# reads the FIRST match, so duplicates are mostly harmless today — but they
# make rollout state ambiguous and grow unbounded across deploys. Mirror the
# tier_configs approach: update the newest record if one exists, else create.
uc_existing = api("GET", "/api/collections/update_config/records?perPage=100")
uc_items = uc_existing.get("items", []) if isinstance(uc_existing, dict) else []
uc_body = {
    "version": "1.0.0",
    "rollout_percent": 0,
    "active": True,
}
if len(uc_items) > 1:
    uc_items.sort(key=lambda r: r.get("created", ""))
    for dup in uc_items[:-1]:
        api("DELETE", f"/api/collections/update_config/records/{dup['id']}")
        log(f"  update_config: removed duplicate {dup['id'][:12]}")
if uc_items:
    uc_id = uc_items[-1]["id"]
    resp = api("PATCH", f"/api/collections/update_config/records/{uc_id}", uc_body)
    log(f"  update_config: {'updated ' + uc_id[:12] if resp.get('id') else 'UPDATE FAILED: ' + str(resp.get('message', resp))}")
else:
    resp = api("POST", "/api/collections/update_config/records", uc_body)
    log(f"  update_config: {'seeded' if resp.get('id') else 'CREATE FAILED: ' + str(resp.get('message', resp))}")

    # ── Step 4: Seed tier configs ──
    # udp_relay + uot_port advertise the sing-box UoT endpoint to clients.
    # That endpoint is part of the DEFAULT deployment now
    # (02-shadowsocks.sh: ENABLE_UOT defaults to 1), so the default here
    # matches. Set ENABLE_UOT=0 in BOTH places to turn it off — advertising
    # uot_port without the service running sends Strike game UDP to a dead
    # port until the client times out and falls back to raw UDP.
    # Without UoT, UDP flows via standard ss UDP — the plain
    # shadowsocks-rust server does not implement sing-box's proprietary
    # UDP-over-TCP and RSTs it (observed 2026-08-01).
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
    uot_enabled = os.environ.get("ENABLE_UOT", "1") != "0"
    uot_port = int(os.environ.get("UOT_PORT", "8446"))
    for t, port in [("eco", 8443), ("stealth", 8444), ("strike", 8445)]:
        pw = os.environ.get(f"{t.upper()}_PASS", "")
        if not pw:
            log(f"  {t}: no password found — skipping")
            continue
        udp = uot_enabled and t == "strike"
        cfg_dict = {
            "server": DOMAIN, "server_port": port,
            "password": pw, "method": "aes-256-gcm",
        }
        if udp:
            cfg_dict["uot_port"] = uot_port
        config_str = json.dumps(cfg_dict)
        body = {"tier": t, "config": config_str, "active": True, "udp_relay": udp}

        # ── Idempotent upsert ──
        # IMPORTANT: always POSTing new records created duplicates on re-runs,
        # and activation/heartbeat read the FIRST match — stale passwords were
        # served to clients after re-deploys (see FIXES.md).
        existing = api("GET", f"/api/collections/tier_configs/records?filter=(tier='{t}')&perPage=100")
        items = existing.get("items", []) if isinstance(existing, dict) else []
        # Delete older duplicates, keeping only the newest record for the tier.
        if len(items) > 1:
            items.sort(key=lambda r: r.get("created", ""))
            for dup in items[:-1]:
                resp = api("DELETE", f"/api/collections/tier_configs/records/{dup['id']}")
                log(f"  {t}: removed duplicate record {dup['id'][:12]} ({resp.get('code', 'ok')})")
        if items:
            rid = items[-1]["id"]
            resp = api("PATCH", f"/api/collections/tier_configs/records/{rid}", body)
            log(f"  {t}: {'updated ' + rid[:12] if resp.get('id') else 'UPDATE FAILED: ' + str(resp.get('message', resp))}")
        else:
            resp = api("POST", "/api/collections/tier_configs/records", body)
            log(f"  {t}: {'seeded' if resp.get('id') else 'CREATE FAILED: ' + str(resp.get('message', resp))}")
        tiers_seeded += 1
else:
    log("  No tier passwords found in environment or /root/.tier_passwords — skipping tier config seeding")
    log("  This is expected on a fresh deploy without secrets.env.age.")
    log("  Run 02-shadowsocks.sh first or provide secrets.env.age with tier passwords.")

if tiers_seeded == 0:
    log("  ✗ No tier configs were seeded.")
    SCHEMA_ERRORS += 1

log("✓ Bootstrap complete")

if SCHEMA_ERRORS:
    log(f"✗ Bootstrap finished with {SCHEMA_ERRORS} error(s) — the hub is NOT usable.")
    log("  Collections missing or unseeded; hooks will fail with 500s.")
    sys.exit(1)
