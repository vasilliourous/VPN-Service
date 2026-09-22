#!/usr/bin/env bash
# write-admin-credentials.sh — assemble every effective credential into ONE
# file on the VPS for the operator.
#
# Why: after a deploy, the credentials are scattered across
#   /root/.tier_passwords       (shadowsocks passwords)
#   /root/.pb_admin_creds       (PocketBase admin email/pass/token)
#   /root/.admin_api_token      (hub admin endpoints)
#   /root/.fetch_link_secret    (release-fetch HMAC key)
#   /etc/environment            (ADMIN_API_TOKEN, again)
#   /root/.b2-creds             (backups)
# and the only way to answer "what is the Strike password on this box?" was to
# remember which of six paths to cat. This writes the answer once.
#
# Called by setup.sh at the END of a deploy (after all secrets exist), and
# safe to re-run by hand at any time:
#   /root/server/scripts/write-admin-credentials.sh
#
# Output: /root/locus-credentials.txt  (chmod 600)
#
# It ALWAYS reports on the fleet-divergence question, because the file is the
# operator's last chance to notice that this host invented its own passwords
# before they walk away.
set -uo pipefail

OUT="/root/locus-credentials.txt"
PASS_FILE="/root/.tier_passwords"
PB_CREDS="/root/.pb_admin_creds"
ADMIN_TOKEN_FILE="/root/.admin_api_token"
FETCH_SECRET_FILE="/root/.fetch_link_secret"
B2_CREDS="/root/.b2-creds"

now() { date -u '+%Y-%m-%d %H:%M:%SZ'; }

# ── Read helpers: report the SOURCE of each value, not just the value ──
# Provenance matters more than the value here. "Where did this come from?"
# is the question that tells you whether the host is in lockstep with the
# fleet or has quietly forked.

get_val() {  # get_val FILE VARNAME -> echoes value or "" if absent
    local f="$1" k="$2"
    [ -f "$f" ] || { echo ""; return; }
    sed -n "s/^${k}=//p" "$f" | head -1
}

env_val() {  # read from /etc/environment
    sed -n "s/^$1=//p" /etc/environment 2>/dev/null | head -1
}

# Tier passwords: env (fleet, from secrets.env.age) beats the local file.
# This mirrors 02-shadowsocks.sh exactly -- if the two ever disagree, the
# module is the authority because that is what the running server loaded.
ECO_PASS="${ECO_PASS:-}";       ECO_SRC="environment (secrets.env.age)"
STEALTH_PASS="${STEALTH_PASS:-}"; STEALTH_SRC="environment (secrets.env.age)"
STRIKE_PASS="${STRIKE_PASS:-}"; STRIKE_SRC="environment (secrets.env.age)"
for t in ECO STEALTH STRIKE; do
    eval "cur=\${${t}_PASS}"
    if [ -z "$cur" ]; then
        v="$(get_val "$PASS_FILE" "${t}_PASS")"
        eval "${t}_PASS=\"\$v\""
        eval "${t}_SRC=\"${PASS_FILE}\""
    fi
done

PB_EMAIL="$(get_val "$PB_CREDS" PB_ADMIN_EMAIL)"
PB_PASS="$(get_val "$PB_CREDS" PB_ADMIN_PASS)"
PB_TOKEN="$(get_val "$PB_CREDS" PB_TOKEN)"
[ -z "$PB_EMAIL" ] && PB_EMAIL="${PB_ADMIN_EMAIL:-}"
[ -z "$PB_PASS" ]  && PB_PASS="${PB_ADMIN_PASS:-}"

ADMIN_TOKEN="$(cat "$ADMIN_TOKEN_FILE" 2>/dev/null || true)"
[ -z "$ADMIN_TOKEN" ] && ADMIN_TOKEN="$(env_val ADMIN_API_TOKEN)"

FETCH_SECRET=""
[ -f "$FETCH_SECRET_FILE" ] && FETCH_SECRET="$(cat "$FETCH_SECRET_FILE" 2>/dev/null || true)"
[ -z "$FETCH_SECRET" ] && FETCH_SECRET="${RELEASE_FETCH_SECRET:-}"

B2_ID="$(get_val "$B2_CREDS" B2_APPLICATION_KEY_ID)"
B2_KEY="$(get_val "$B2_CREDS" B2_APPLICATION_KEY)"
B2_BUCKET="$(get_val "$B2_CREDS" B2_BUCKET)"

# ── Divergence detection ──
# setup.sh knows the truth and passes it in; do not try to INFER provenance
# from file contents. An earlier draft guessed "not from secrets" whenever
# PB_ADMIN_PASS was absent from the environment, which is false on every
# normal deploy (setup.sh exports it, this script runs afterwards in a
# different shell) and would have cried wolf every single time.
#
# Only these two inputs can indicate divergence, and both are set by the
# module that actually performed the generation:
#   TIER_PASSWORDS_GENERATED=1   (02-shadowsocks.sh, ALLOW_GENERATED_TIER_PASSWORDS=1)
#   ADMIN_TOKEN_GENERATED=1      (setup.sh, ALLOW_GENERATED_ADMIN_TOKEN=1)
DIVERGED=0
GENERATED_LIST=""
if [ "${TIER_PASSWORDS_GENERATED:-0}" = "1" ]; then
    DIVERGED=1
    GENERATED_LIST="${GENERATED_LIST}  - Shadowsocks tier passwords (eco/stealth/strike)\n"
fi
if [ "${ADMIN_TOKEN_GENERATED:-0}" = "1" ]; then
    DIVERGED=1
    GENERATED_LIST="${GENERATED_LIST}  - ADMIN_API_TOKEN\n"
fi

# Provenance is DECLARED, not guessed. setup.sh sets PB_ADMIN_PASS in the
# environment it exports to modules; if this script is run by hand later, that
# variable is simply absent and we say "not declared" rather than inventing a
# verdict.
if [ -n "${PB_ADMIN_PASS:-}" ]; then
    PB_SRC="secrets.env.age (declared by setup.sh)"
else
    PB_SRC="${PB_CREDS} (run by hand — provenance not declared)"
fi

umask 077
{
cat <<EOF
╔══════════════════════════════════════════════════════════════════════╗
║  LOCUS — DEPLOYED CREDENTIALS                                          ║
║  Generated $(now)
╚══════════════════════════════════════════════════════════════════════╝

Host:    $(hostname)   ($(ip -4 addr show 2>/dev/null | grep -oP 'inet \K[\d.]+' | grep -v '^127\.' | head -1))
Domain:  ${DOMAIN:-<unset in this shell>}

This file is chmod 600 and lives only on this VPS. It is a CONVENIENCE COPY:
the authoritative source for anything shared across deployments is
secrets.env.age in the repo. If the two disagree, fix secrets.env.age.

EOF

if [ "$DIVERGED" = "1" ]; then
cat <<'EOF'
╔══════════════════════════════════════════════════════════════════════╗
║  ⚠  THIS HOST GENERATED ITS OWN CREDENTIALS                          ║
║                                                                      ║
║  It no longer matches the fleet. Clients activated against another   ║
║  hub will NOT work here, and vice versa.                             ║
║                                                                      ║
║  Write the generated values below back into secrets.env.age —        ║
║  see docs/SECRETS-MANAGEMENT.md -> "Generated credentials            ║
║  write-back". Until you do, stop and record them somewhere safe.     ║
╚══════════════════════════════════════════════════════════════════════╝
EOF
printf 'Generated in this run:\n'
printf "%b" "$GENERATED_LIST"
printf '\n'
fi

cat <<EOF
──────────────────────────────────────────────────────────────────────
 SHADOWSOCKS TIER PASSWORDS  (paste these into Clash Verge Rev)
──────────────────────────────────────────────────────────────────────
ECO     (port 8443, tcp_only)      ${ECO_PASS}
                                   source: ${ECO_SRC}
STEALTH (port 8444, tcp_only)      ${STEALTH_PASS}
                                   source: ${STEALTH_SRC}
STRIKE  (port 8445, tcp_and_udp)   ${STRIKE_PASS}
                                   source: ${STRIKE_SRC}

STRIKE UDP-over-TCP endpoint:      port ${UOT_PORT:-8446}
  (sing-box only — Clash/mihomo DO implement udp-over-tcp v2;
   point port: 8446 with udp-over-tcp: true. Pointing it at 8445
   does NOT fall back and breaks UDP silently.)

  cipher: aes-256-gcm  (all tiers)

──────────────────────────────────────────────────────────────────────
 POCKETBASE ADMIN  (console at /_/ , admin API)
──────────────────────────────────────────────────────────────────────
Email:      ${PB_EMAIL}
Password:   ${PB_PASS}
            source: ${PB_SRC}
JWT token:  ${PB_TOKEN:-<not recorded>}
  (A JWT EXPIRES. The password above is the durable credential; the token
   in /root/.pb_admin_creds is a cache. If API calls 401, re-auth:
     curl -s -X POST http://127.0.0.1:8090/api/admins/auth-with-password \\
       -H 'Content-Type: application/json' \\
       -d '{"identity":"'\"\$PB_EMAIL\"'","password":"'\"\$PB_PASS\"'"}')

  NOTE: PocketBase 0.22 rejects ADMIN_API_TOKEN for record access. Use this
  JWT for scripting; use the token below only for /api/admin/* endpoints.

──────────────────────────────────────────────────────────────────────
 ADMIN CONSOLE  (https://<domain>/admin/)
──────────────────────────────────────────────────────────────────────
Paste the token below into the console's token field.
ADMIN_API_TOKEN:              ${ADMIN_TOKEN:-<not found>}

──────────────────────────────────────────────────────────────────────
 RELEASE FETCH  (one-shot publish trigger links)
──────────────────────────────────────────────────────────────────────
RELEASE_FETCH_SECRET:         ${FETCH_SECRET:-<not found>}

──────────────────────────────────────────────────────────────────────
 BACKBLAZE B2  (backups)
──────────────────────────────────────────────────────────────────────
B2_APPLICATION_KEY_ID:        ${B2_ID:-<not configured>}
B2_APPLICATION_KEY:           ${B2_KEY:-<not configured>}
B2_BUCKET:                    ${B2_BUCKET:-<not configured>}

──────────────────────────────────────────────────────────────────────
 WHERE EACH OF THESE ACTUALLY LIVES  (raw paths)
──────────────────────────────────────────────────────────────────────
Tier passwords        ${PASS_FILE}
PocketBase creds      ${PB_CREDS}
Admin API token       ${ADMIN_TOKEN_FILE}   (also /etc/environment)
Fetch link secret     ${FETCH_SECRET_FILE}
B2 credentials        ${B2_CREDS}

──────────────────────────────────────────────────────────────────────
 WHAT IS NOT IN THIS FILE
──────────────────────────────────────────────────────────────────────
Activation codes. They are CUSTOMER DATA, not deployment credentials. They
live in the PocketBase database, are issued through the admin console, and
are backed up hourly to B2. NEVER bulk-delete or regenerate them.
See docs/OPS.md -> "Codes & Clients".
EOF
} > "$OUT"

chmod 600 "$OUT"
echo "[credentials] ✓ Wrote ${OUT} (chmod 600)"
if [ "$DIVERGED" = "1" ]; then
    echo "[credentials] ⚠  This host generated credentials — do the write-back:"
    echo "[credentials]    docs/SECRETS-MANAGEMENT.md -> \"Generated credentials write-back\""
fi
exit 0
