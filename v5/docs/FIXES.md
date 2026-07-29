
---

## VPS TESTING — ISSUES FOUND & FIXED (2026-07-26)

These were discovered during real deployment to a Voyager VPS (Ubuntu 22.04)
and are now fixed in `v5/server/`.

### S1. Caddy Systemd Service Missing

| Severity | 🔴 Caddy wouldn't start |
|----------|------------------------|
| **File:** | `v5/server/modules/05-caddy.sh` |
| **Issue:** | The module downloaded a custom Caddy binary with rate_limit support, but never created a systemd service file. `systemctl restart caddy` failed with "Unit not found." |
| **Fix:** | Added `create_systemd_service()` that: creates `caddy` user if missing, creates `/var/log/caddy` and `/var/www/html`, writes a proper systemd unit file, and grants `cap_net_bind_service+ep` to the binary so it can bind :80/:443 as non-root. |

### S2. Caddy TLS Certificate Provisioning Failed

| Severity | 🔴 HTTPS broken |
|----------|-----------------|
| **File:** | `v5/server/modules/05-caddy.sh` |
| **Issue:** | Caddy ran as user `caddy` which had no home directory. Caddy tried to write TLS storage to `/home/caddy/.config/caddy/` but got "permission denied." |
| **Fix:** | Removed `ProtectSystem=full` from the service file. Created `/var/lib/caddy` for Caddy's runtime data. Added `Environment=XDG_CONFIG_HOME=/var/lib/caddy` and `Environment=XDG_DATA_HOME=/var/lib/caddy` so Caddy stores TLS certs in a writable location. |

### S3. PocketBase Service Failed with NAMESPACE Error

| Severity | 🔴 PocketBase wouldn't start |
|----------|------------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The service had `ProtectHome=true`, `ProtectSystem=full`, and `PrivateTmp=true`. On this VPS kernel (5.15.0-161-generic), these caused `status=226/NAMESPACE` errors — the kernel rejected namespace operations. |
| **Fix:** | Removed all systemd security hardening directives (`ProtectHome`, `ProtectSystem`, `ReadWritePaths`). The service now uses only `NoNewPrivileges=true` and `PrivateTmp=true`. |

### S4. Admin Token Extraction Failed (Shell Escaping)

| Severity | 🟡 Admin created but collections not seeded |
|----------|---------------------------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The admin creation API response was piped through `echo "$RESP" | python3 -c "..."` which broke when the JWT contained special characters (dots, dashes). The token came back empty, so the script thought "Admin created but could not extract token." |
| **Fix:** | Wrote the API response to a temp file before parsing. Also added a fallback auth flow (`/api/admins/auth-with-password`) if token extraction fails. Then refactored the entire bootstrap into `scripts/seed-pb.py` which uses Python's `json.dump()` to write temp files for all API calls, avoiding shell escaping entirely. |

### S5. Collection Creation Failed with Bash Heredocs

| Severity | 🟡 Collections not created |
|----------|---------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The shell heredocs (`cat > file << 'JSON'`) for the collection schema JSON were inconsistently parsed when passed through SSH. The JSON contained nested quotes and special characters that bash mangled. |
| **Fix:** | Moved all collection creation logic into `scripts/seed-pb.py`. Python writes the JSON payload to a temp file using `json.dump()`, then passes `@file` to curl. This is bulletproof — no shell escaping issues. |

### S6. `generate_codes.sh` Bash Bug (!$double)

| Severity | 🔴 Code generation produced errors |
|----------|-----------------------------------|
| **File:** | `v5/scripts/generate_codes.sh` |
| **Issue:** | The Luhn-mod-N checksum used `double=!$double` to toggle a boolean. In bash, `!$double` triggers history expansion (or fails with `!false: command not found` when history expansion is off). |
| **Fix:** | Replaced with `if $double; then double=false; else double=true; fi`. |

### S7. PocketBase 0.22 JS Hook Compatibility

| Severity | 🔴 Activation endpoint returns 400 |
|----------|-----------------------------------|
| **File:** | `v5/server/pb_hooks/activation.pb.js`, `heartbeat.pb.js`, `admin_unbind.pb.js` |
| **Issue:** | PocketBase 0.22.21 changed the JavaScript VM API. The hooks were originally written for the 0.21 API (`$app.dao()`, `$apis.requestInfo()`). On 0.22.21, `$app.dao()` still works via compatibility shim, but `$app.dao().db().exec()` and `$app.dao().findRecordsByFilter()` with `{:param}` syntax had degraded behavior. |
| **Fix:** | **✅ Fixed 2026-07-26.** Hooks updated to use working API patterns for PocketBase 0.22.21: |
| **Changes made:** | • `$apis.requestInfo(c).data` → `$apis.requestInfo(e).data` (compat shim still works)<br>• `$app.dao().db().exec()` → `$app.dao().db().newQuery().execute()`<br>• `$app.dao().findRecordsByFilter()` with `{:param}` → `$app.dao().findFirstRecordByData()` for single-record lookups<br>• `$app.dao().findCollectionByNameOrId()` + `new Record()` + `$app.dao().saveRecord()` for creating records<br>• `e.remoteIP` → `e.request().remoteAddr.split(":")[0]` (remoteIP not exposed in 0.22.21 router events)<br>• `const`/`let` → `var` (goja compatibility)<br>• Helper functions must be defined inside the `routerAdd` callback (goja doesn't hoist declarations into callback scope)<br>• Rate limiting filter: inline string with sanitization instead of `{:param}` syntax (filter parser changed)<br>• `sqlite_pragmas.pb.js` removed — `on()` hook API removed in 0.22, WAL mode set automatically by PocketBase |

### S8. Smoke Test IP Detection Off by One

| Severity | 🟡 False positive warning |
|----------|---------------------------|
| **File:** | `v5/server/scripts/smoke-test.sh` |
| **Issue:** | The smoke test detects the VPS IP using `ip route show default | awk '{print $3}'` which returns the **gateway** IP (e.g., `114.23.136.1`), not the interface IP (`114.23.136.47`). The DNS match check then fails even when DNS is correct. |
| **Fix:** | Changed to `ip -4 addr show | grep -oP 'inet \K[0-9.]+' | grep -v '^127\\.' | head -1` to get the actual interface IP. |

---

### S9. Secrets Decryption via Process Substitution Fails Silently

| Severity | 🔴 All SS passwords auto-generated instead of using stable secrets |
|----------|-------------------------------------------------------------------|
| **File:** | `v5/server/setup.sh`, `v5/server/restore.sh` |
| **Issue:** | Both `setup.sh` and `restore.sh` used `source <(age -d ...)` (bash process substitution) to load decrypted secrets. In non-interactive SSH sessions — specifically when running via `ssh root@host "/root/server/setup.sh"` — the process substitution would appear to succeed (exit 0, "✓ Secrets decrypted" printed) but **the variables would not actually be loaded into the current shell**. This caused all downstream modules to fall through to auto-generated passwords instead of using the stable credentials from `secrets.env.age`. The symptom: `DOMAIN` was empty despite "Secrets decrypted" having printed, causing a hard fail at the DOMAIN check. |
| **Root cause:** | `source <(command)` is a bash extension that works unreliably in certain SSH environments. The process substitution forks a subprocess, and `source` may read from an already-closed pipe under some shell configurations. Ubuntu's `/bin/sh` is dash, which does not support process substitution at all — but even under bash the behavior was inconsistent. |
| **Fix:** | Replaced `source <(age -d ...)` in both files with a two-step temp-file approach: `age -d ... > /tmp/.secrets-$$.env` followed by `source /tmp/.secrets-$$.env`. This is POSIX-compatible, works in all shell environments, and is easy to debug (the temp file can be inspected). Also added an `age` binary check + auto-install via `apt-get` before decryption, so a fresh VPS without `age` pre-installed will still work. |
