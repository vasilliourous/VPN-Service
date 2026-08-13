#!/usr/bin/env bash
# MyVPN Code Generator
# Generates activation codes with Luhn-mod-N checksum.
#
# Code format: MYYYP-XXXX-XXXX-XXXX-C
# Where:
#   MYYYP = MyVPN prefix (static)
#   XXXX  = random alphanumeric segments
#   C     = Luhn-mod-N checksum character
#
# Usage:
#   ./scripts/generate_codes.sh <hub_url> <admin_token> <tier> <count>
#
# Examples:
#   ./scripts/generate_codes.sh https://networkingguides.duckdns.org my-token eco 50
#   ./scripts/generate_codes.sh https://networkingguides.duckdns.org my-token stealth 30
#   ./scripts/generate_codes.sh https://networkingguides.duckdns.org my-token strike 20
#
# Output: Prints codes to stdout AND saves to <tier>-codes.txt
#   Also creates a JSON array for PocketBase import if requested.

set -euo pipefail

# ── Configuration ──
CHARSET="ABCDEFGHJKLMNPQRSTUVWXYZ23456789"  # No I/O/0/1 (avoid ambiguity)
N=${#CHARSET}
PREFIX="MYVPN"
SEGMENTS=3
SEGMENT_LEN=4

# ── Luhn-mod-N checksum calculation ──
# Pure bash implementation (no external deps)
luhn_mod_n_checksum() {
    local code="$1"
    local sum=0
    local double=false
    local val idx

    # Process from right to left
    for (( i = ${#code} - 1; i >= 0; i-- )); do
        char="${code:$i:1}"
        idx=0
        # Find character index in charset
        for (( j = 0; j < N; j++ )); do
            if [ "${CHARSET:$j:1}" = "$char" ]; then
                idx=$j
                break
            fi
        done

        val=$idx
        if $double; then
            val=$(( val * 2 ))
            if [ "$val" -ge "$N" ]; then
                val=$(( val - N + 1 ))
            fi
        fi
        sum=$(( sum + val ))
        # Toggle properly — `double=!$double` would assign the literal string
        # "!false", making every checksum wrong (fixed 2026-08-14 during the
        # first fresh-VPS deployment: all generated codes were rejected as
        # "Invalid code format" by the activation hook).
        if $double; then double=false; else double=true; fi
    done

    local checksum=$(( (N - (sum % N)) % N ))
    echo "${CHARSET:$checksum:1}"
}

# ── Generate a single random segment ──
random_segment() {
    local len=$1 result=""
    for (( i = 0; i < len; i++ )); do
        local idx=$(( RANDOM % N ))
        result="${result}${CHARSET:$idx:1}"
    done
    echo "$result"
}

# ── Generate a full activation code ──
generate_code() {
    # Build the core (PREFIX + segments without delimiter)
    local core="$PREFIX"
    for (( i = 0; i < SEGMENTS; i++ )); do
        core="${core}$(random_segment $SEGMENT_LEN)"
    done

    # Calculate Luhn-mod-N checksum
    local checksum
    checksum=$(luhn_mod_n_checksum "$core")

    # Format with hyphens: MYYYP-XXXX-XXXX-XXXX-C
    local formatted="${PREFIX}-"
    for (( i = 0; i < SEGMENTS; i++ )); do
        local start=$(( (i * SEGMENT_LEN) + ${#PREFIX} ))
        formatted="${formatted}${core:$start:$SEGMENT_LEN}"
        if [ "$i" -lt $((SEGMENTS - 1)) ]; then
            formatted="${formatted}-"
        fi
    done
    formatted="${formatted}-${checksum}"

    echo "$formatted"
}

# ── Validate a code (for testing) ──
validate_code() {
    local code="$1"
    # Remove hyphens
    local cleaned="${code//-/}"
    # Extract checksum (last character)
    local check_char="${cleaned: -1}"
    local core="${cleaned:0:-1}"

    local expected
    expected=$(luhn_mod_n_checksum "$core")
    [ "$check_char" = "$expected" ]
}

# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════

HUB_URL="${1:-}"
ADMIN_TOKEN="${2:-}"
TIER="${3:-}"
COUNT="${4:-}"

if [ -z "$HUB_URL" ] || [ -z "$ADMIN_TOKEN" ] || [ -z "$TIER" ] || [ -z "$COUNT" ]; then
    echo "Usage: $0 <hub_url> <admin_token> <tier> <count>"
    echo ""
    echo "Arguments:"
    echo "  hub_url      Admin hub URL (e.g. https://networkingguides.duckdns.org)"
    echo "  admin_token  PocketBase ADMIN token (JWT) — on the VPS:"
    echo "               grep PB_TOKEN /root/.pb_admin_creds | cut -d= -f2"
    echo "               (NOT /root/.admin_api_token — that is the app-level"
    echo "               ADMIN_API_TOKEN used by the unbind hook, rejected by"
    echo "               PocketBase 0.22 with 401)"
    echo "  tier         eco | stealth | strike"
    echo "  count        Number of codes to generate"
    echo ""
    echo "Environment:"
    echo "  DRY_RUN=1    Print codes to stdout only (skip API import)"
    echo "  EXPIRY_DAYS  Days until codes expire (default: 365)"
    exit 1
fi

# Validate tier
if [ "$TIER" != "eco" ] && [ "$TIER" != "stealth" ] && [ "$TIER" != "strike" ]; then
    echo "Error: Tier must be 'eco', 'stealth', or 'strike'"
    exit 1
fi

# Validate count
if ! [ "$COUNT" -gt 0 ] 2>/dev/null; then
    echo "Error: Count must be a positive integer"
    exit 1
fi

: "${EXPIRY_DAYS:=365}"
: "${DRY_RUN:=0}"

OUTPUT_FILE="${TIER}-codes.txt"
EXPIRES=$(date -d "+${EXPIRY_DAYS} days" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || \
          date -v "+${EXPIRY_DAYS}d" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || \
          echo "unknown")

echo "═══════════════════════════════════════════"
echo " MyVPN Code Generator"
echo "═══════════════════════════════════════════"
echo " Hub URL:      ${HUB_URL}"
echo " Tier:         ${TIER}"
echo " Count:        ${COUNT}"
echo " Expires:      ${EXPIRES} (${EXPIRY_DAYS} days)"
echo " Output:       ${OUTPUT_FILE}"
echo " Checksum:     Luhn-mod-N (charset: ${CHARSET})"
echo "═══════════════════════════════════════════"

# Generate codes
CODES=()
echo "" > "$OUTPUT_FILE"
echo "# MyVPN ${TIER^} Activation Codes" >> "$OUTPUT_FILE"
echo "# Generated: $(date)" >> "$OUTPUT_FILE"
echo "# Expires: ${EXPIRES}" >> "$OUTPUT_FILE"
echo "# Checksum: Luhn-mod-N" >> "$OUTPUT_FILE"
echo "" >> "$OUTPUT_FILE"

echo ""
echo "Generating $COUNT codes..."
echo ""

for (( i = 1; i <= COUNT; i++ )); do
    code=$(generate_code)

    # Validate the generated code
    if ! validate_code "$code"; then
        echo "ERROR: Generated code ${code} failed Luhn check!" >&2
        exit 1
    fi

    CODES+=("$code")
    echo "$code" >> "$OUTPUT_FILE"
    printf "\r  [%3d/%3d] %s" "$i" "$COUNT" "$code"
done
printf "\n\n"

echo "✓ Generated $COUNT codes → ${OUTPUT_FILE}"

# ── Optional: Import to PocketBase via API ──
if [ "$DRY_RUN" = "0" ] && [ -n "$ADMIN_TOKEN" ]; then
    echo ""
    echo "Importing codes to PocketBase at ${HUB_URL}..."

    # Check if codes collection exists
    COLLECTIONS=$(curl -sf -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${HUB_URL}/api/collections" 2>/dev/null || echo "")

    if echo "$COLLECTIONS" | python3 -c "import sys,json; cols=json.load(sys.stdin); print(any(c['name']=='codes' for c in cols['items']))" 2>/dev/null | grep -q "False"; then
        echo "  Creating 'codes' collection..."
        curl -sf -X POST "${HUB_URL}/api/collections" \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            -H "Content-Type: application/json" \
            -d '{
                "name": "codes",
                "type": "base",
                "schema": [
                    {"name": "code", "type": "text", "required": true, "unique": true},
                    {"name": "tier", "type": "select", "required": true, "values": ["eco", "stealth", "strike"]},
                    {"name": "middleman", "type": "text"},
                    {"name": "bound_fingerprint", "type": "text"},
                    {"name": "activated_at", "type": "date"},
                    {"name": "expires_at", "type": "date"},
                    {"name": "suspended", "type": "bool", "default": false},
                    {"name": "unbound_at", "type": "date"},
                    {"name": "unbind_reason", "type": "text"}
                ],
                "indexes": [
                    "CREATE UNIQUE INDEX idx_code ON codes (code)",
                    "CREATE INDEX idx_tier ON codes (tier)",
                    "CREATE INDEX idx_bound ON codes (bound_fingerprint)"
                ]
            }' 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('name',''))" 2>/dev/null || echo "  Collection may already exist"
    fi

    # Import codes
    SUCCESS=0
    FAIL=0
    for code in "${CODES[@]}"; do
        RESP=$(curl -sf -X POST "${HUB_URL}/api/collections/codes/records" \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            -H "Content-Type: application/json" \
            -d "{\"code\": \"${code}\", \"tier\": \"${TIER}\", \"expires_at\": \"${EXPIRES}\"}" 2>/dev/null || echo "FAILED")
        if [ "$RESP" != "FAILED" ]; then
            SUCCESS=$((SUCCESS + 1))
        else
            FAIL=$((FAIL + 1))
            echo "  ✗ Failed to import: ${code}" >&2
        fi
    done

    echo "  Imported: ${SUCCESS}, Failed: ${FAIL}"
    if [ "$FAIL" -eq 0 ]; then
        echo "✓ All codes imported to PocketBase"
    else
        echo "⚠ Some codes failed to import. Check logs above."
    fi
else
    if [ "$DRY_RUN" = "1" ]; then
        echo "  DRY_RUN=1 — skipping PocketBase import"
    else
        echo "  No admin token provided — skipping PocketBase import"
        echo "  Codes saved to ${OUTPUT_FILE} for manual import"
    fi
fi

echo ""
echo "═══════════════════════════════════════════"
echo " Done!"
echo " File: ${OUTPUT_FILE}"
echo " Codes: ${#CODES[@]}"
echo "═══════════════════════════════════════════"
