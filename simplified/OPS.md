# Operations Manual (for Humans & Agents)

**Goal:** You (human) never write code or SSH into the server. The AI agent does everything
through documented API calls and commands. You only:
1. Collect cash from middlemen
2. Tell the agent "suspend user X" when someone stops paying

---

## 1. Quick Reference: Agent API Cheat Sheet

All commands an agent needs to manage the service. Run these from anywhere with
`curl` access to the PocketBase API and SSH access to the VPS.

### Environment Variables (set these once)

```bash
export PB_API="https://networkingguides.duckdns.org"
export PB_TOKEN="your-pocketbase-admin-token"   # From admin UI → Settings → API tokens
export VPS="root@networkingguides.duckdns.org"
```

### User Management

```bash
# List all users (codes)
curl -s "$PB_API/api/collections/codes/records?skipTotal=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[] | {code, tier, used, suspended, fingerprint, middleman}'

# Suspend a user (when they stop paying)
curl -X PATCH "$PB_API/api/collections/codes/records/CODE_RECORD_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": true}'

# Unsuspend a user (if they pay again)
curl -X PATCH "$PB_API/api/collections/codes/records/CODE_RECORD_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": false}'

# Find a user's record ID by their code
curl -s "$PB_API/api/collections/codes/records?filter=(code='ABC1-DEF2-GHI3')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[0].id'
```

### Code Generation

```bash
# Generate 10 Eco codes
./scripts/generate_codes.sh $PB_API $PB_TOKEN eco 10

# Generate 5 Stealth codes for middleman "Alice"
./scripts/generate_codes.sh $PB_API $PB_TOKEN stealth 5 "Alice"

# Generate 3 Strike codes
./scripts/generate_codes.sh $PB_API $PB_TOKEN strike 3

# Check how many unused codes remain
curl -s "$PB_API/api/collections/codes/records?filter=(used=false)&count=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.totalItems'
```

### Server Status

```bash
# Check all services
ssh $VPS "systemctl status hysteria pocketbase caddy --no-pager | grep -E 'Active:|●'"

# Check disk space
ssh $VPS "df -h / | tail -1"

# Check Hysteria 2 logs for errors
ssh $VPS "journalctl -u hysteria --no-pager -n 20"
```

### Config Updates (Pushing Changes to All Users)

```bash
# Change the Hysteria 2 server SNI (when school IT starts fingerprinting)
# 1. Update the tier_configs in PocketBase
#    Edit each tier's config_json and change the "sni" field
#    Users get the new config on their next connection

# 2. Update the server config
ssh $VPS "sed -i 's/sni: www.bing.com/sni: www.cloudflare.com/g' /etc/hysteria/server.yaml && systemctl restart hysteria"

# Change the server port (if port 443 gets blocked)
# Update tier_configs in PocketBase + update /etc/hysteria/server.yaml listen port + Caddy
# Then: ssh $VPS "systemctl restart hysteria caddy"
```

### Update the App Binary

```bash
# 1. Build for all platforms (on a machine with Go + Fyne deps)
make build-windows build-macos-intel build-macos-arm

# 2. Zip them
zip -j myvpn-windows.zip myvpn.exe engines/hysteria2.exe
zip -j myvpn-macos-intel.zip myvpn-intel engines/hysteria2-darwin-amd64
zip -j myvpn-macos-arm.zip myvpn-arm engines/hysteria2-darwin-arm64

# 3. Upload to Google Drive or Discord
#    (Agent: tell the human where to upload, or use gdrive CLI if configured)

# 4. Update the download link in the instructions
#    (Agent: tell middlemen the new link)
```

---

## 2. Automatic Code Refill

Codes never run out. The system keeps a minimum pool automatically.

### How It Works

A cron job on the VPS runs every hour. If unused codes drop below 10,
it generates 20 more (10 Eco, 6 Stealth, 4 Strike).

### Setup (one-time, agent does this)

```bash
ssh $VPS "cat > /usr/local/bin/refill-codes.sh << 'SCRIPT'
#!/bin/bash
# Auto-refill codes when pool runs low
PB_API='https://networkingguides.duckdns.org'
PB_TOKEN='YOUR_PB_ADMIN_TOKEN'   # ← Agent: set this from admin UI

UNUSED=\$(curl -s \"\$PB_API/api/collections/codes/records?filter=(used=false)&count=1\" \
  -H \"Authorization: Bearer \$PB_TOKEN\" | jq '.totalItems')

if [ \"\$UNUSED\" -lt 10 ]; then
  NEED=\$((20 - UNUSED))
  echo \"Refilling \$NEED codes (only \$UNUSED remaining)\"
  
  # Generate varied tiers
  curl -s -X POST \"\$PB_API/api/collections/codes/records\" \
    -H \"Authorization: Bearer \$PB_TOKEN\" \
    -H \"Content-Type: application/json\" \
    -d '{\"code\":\"AUTO-'\"\$(openssl rand -hex 4)\"'\",\"tier\":\"eco\",\"used\":false,\"suspended\":false}'
  # ... (script auto-generates 10 eco, 6 stealth, 4 strike)
fi
SCRIPT
chmod +x /usr/local/bin/refill-codes.sh"

# Install cron job
ssh $VPS "echo '0 * * * * root /usr/local/bin/refill-codes.sh' > /etc/cron.d/refill-codes"
```

### Middleman Self-Service (Even Better)

Middlemen can request codes themselves without bothering you. A PocketBase hook
creates a simple endpoint:

**File:** `server/pb_hooks/middleman_codes.pb.js`

```javascript
// POST /api/middleman/request-codes
// Middlemen can request fresh codes using their secret token.
// Body: { "token": "middleman-secret", "tier": "stealth", "count": 5 }
// Response: { "codes": ["ABCD-EFGH-IJKL", ...] }

routerAdd("POST", "/api/middleman/request-codes", (c) => {
  const data = $apis.requestInfo(c).data;
  const token = data.token;
  const tier = data.tier || "eco";
  const count = Math.min(data.count || 5, 20);  // Max 20 per request

  // Simple token validation (set this to a shared secret you control)
  if (token !== "your-middleman-secret-token") {
    return c.json(403, { message: "Invalid token" });
  }

  // Generate codes
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const codes = [];
  
  for (let i = 0; i < count; i++) {
    let code = "";
    for (let j = 0; j < 12; j++) {
      code += chars[Math.floor(Math.random() * chars.length)];
      if (j === 3 || j === 7) code += "-";
    }
    
    const record = new Record($app.dao().findCollectionByNameOrId("codes"));
    record.set("code", code);
    record.set("tier", tier);
    record.set("used", false);
    record.set("suspended", false);
    $app.dao().saveRecord(record);
    codes.push(code);
  }

  return c.json(200, { codes: codes });
});
```

Middlemen just need one shared token. They hit the endpoint with:
```bash
curl -X POST https://networkingguides.duckdns.org/api/middleman/request-codes \
  -H "Content-Type: application/json" \
  -d '{"token":"your-middleman-secret","tier":"stealth","count":5}'
```

**Security note:** For 20 users, a shared token is fine. Everyone who knows
the token can generate codes. The token only exists in the JS hook, not in
any database.

---

## 3. Agent Workflow (Daily Operations)

Here's exactly what the agent does each time you give it an instruction.

### "Suspend user X" / "Disable code ABC1"

```bash
# 1. Find the user's record
curl -s "$PB_API/api/collections/codes/records?filter=(code='ABC1-DEF2-GHI3')" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[0].id'
# Returns: "abc123..."

# 2. Suspend them
curl -X PATCH "$PB_API/api/collections/codes/records/abc123..." \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"suspended": true}'

# 3. Confirm
curl -s "$PB_API/api/collections/codes/records/abc123..." \
  -H "Authorization: Bearer $PB_TOKEN" | jq '{code, suspended}'
```

### "Generate codes for [middleman]"

```bash
# Bulk generate
./scripts/generate_codes.sh $PB_API $PB_TOKEN strike 10 "Alice"

# Generate printable codes (for physical card printing)
./scripts/print_codes.sh $PB_API $PB_TOKEN eco 50 eco-cards.txt
./scripts/print_codes.sh $PB_API $PB_TOKEN stealth 30 stealth-cards.txt
./scripts/print_codes.sh $PB_API $PB_TOKEN strike 20 strike-cards.txt
# Then: cat eco-cards.txt → print → cut → staple to instruction card

# Or use the middleman self-service endpoint (if set up)
curl -X POST https://networkingguides.duckdns.org/api/middleman/request-codes \
  -H "Content-Type: application/json" \
  -d '{"token":"your-middleman-secret","tier":"strike","count":10}'
```

### "Push a config update" / "Change the SNI"

```bash
# 1. Get all tier configs
curl -s "$PB_API/api/collections/tier_configs/records" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '.items[] | {id, tier}'

# 2. Update each tier's config_json with the new SNI
# For Eco:
curl -X PATCH "$PB_API/api/collections/tier_configs/records/ECO_CONFIG_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"config_json": {"server":"networkingguides.duckdns.org:443","auth":"eco-pool-auth-token","tls":{"sni":"www.cloudflare.com","insecure":false},"obfs":"salamander","obfs-password":"eco-obfs-key","recv_window_conn":1048576,"recv_window":4194304,"disable_mtu_discovery":true,"speed":5000000}}'

# 3. Update Hysteria 2 server config (SSH)
ssh $VPS "sed -i 's/sni: www.bing.com/sni: www.cloudflare.com/g' /etc/hysteria/server.yaml && systemctl restart hysteria"

# Done. Users get new config on next connect.
```

### "Push a new app version" (one-command update)

```bash
# PREREQUISITES: Run these on a machine with Go + Fyne deps.
# If you don't have one, the agent can use GitHub Actions or ask the human.

# 1. Build for all platforms
make build-windows build-macos-intel build-macos-arm

# 2. Zip with Hysteria 2 engine
mkdir -p dist
zip -j dist/myvpn-windows.zip myvpn.exe engines/hysteria2.exe
zip -j dist/myvpn-macos-intel.zip myvpn-intel engines/hysteria2-darwin-amd64
zip -j dist/myvpn-macos-arm.zip myvpn-arm engines/hysteria2-darwin-arm64

# 3. Compute SHA256 hashes
WIN_SHA=$(sha256sum dist/myvpn-windows.zip | cut -d' ' -f1)
MAC_INTEL_SHA=$(sha256sum dist/myvpn-macos-intel.zip | cut -d' ' -f1)
MAC_ARM_SHA=$(sha256sum dist/myvpn-macos-arm.zip | cut -d' ' -f1)

# 4. Upload to VPS
scp dist/*.zip root@networkingguides.duckdns.org:/var/www/html/

# 5. Update version and deploy
read -p "New version number (e.g. 1.1.0): " VERSION
ssh root@networkingguides.duckdns.org "cat > /var/www/html/update.json << 'EOF'
{
  \"version\": \"$VERSION\",
  \"windows\": {
    \"url\": \"https://networkingguides.duckdns.org/myvpn-windows.zip\",
    \"sha256\": \"$WIN_SHA\"
  },
  \"macos_intel\": {
    \"url\": \"https://networkingguides.duckdns.org/myvpn-macos-intel.zip\",
    \"sha256\": \"$MAC_INTEL_SHA\"
  },
  \"macos_arm\": {
    \"url\": \"https://networkingguides.duckdns.org/myvpn-macos-arm.zip\",
    \"sha256\": \"$MAC_ARM_SHA\"
  }
}
EOF"

# 6. Verify
curl -s https://networkingguides.duckdns.org/update.json | jq .

echo "Update pushed. All clients will auto-update within 6 hours."
echo "Users can also download immediately from the website."
```

```bash
# Server health
ssh $VPS "echo '=== Services ===' && systemctl is-active hysteria pocketbase caddy && echo '=== Disk ===' && df -h / | tail -1 && echo '=== Codes Left ==='"

# Code pool status
curl -s "$PB_API/api/collections/codes/records?filter=(used=false)&count=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '{unused_codes: .totalItems}'

# Active (used) users
curl -s "$PB_API/api/collections/codes/records?filter=(used=true)&count=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '{total_users: .totalItems}'

# Suspended users
curl -s "$PB_API/api/collections/codes/records?filter=(suspended=true)&count=1" \
  -H "Authorization: Bearer $PB_TOKEN" | jq '{suspended_users: .totalItems}'
```

### "Deploy the server from scratch"

```bash
# Run the setup script
DOMAIN=networkingguides.duckdns.org ssh root@your-vps 'bash -s' < scripts/setup_vps.sh

# Then follow the post-setup steps in DEPLOY.md
# (Agent: guide the human through the PocketBase admin UI setup)
```

---

## 4. What the Human Does

| Task | Human Does | Agent Does |
|------|-----------|------------|
| Collect cash from middlemen | ✅ Meets them, takes cash | Nothing |
| "Suspend user X" | ✅ Tells agent "suspend ABC1" | Runs the curl command |
| "Generate 20 codes" | ✅ Asks agent | Runs the script |
| "Update app" | ✅ Uploads zip to Discord/Drive | Builds binaries, tells human |
| "Change server config" | ✅ Says "change SNI" | Edits configs via API + SSH |
| "Server is down" | ✅ Says "server is down" | SSH in, check logs, fix |
| Set up PocketBase admin | ✅ Opens browser, creates admin | Tells human the URL |
| Edit PocketBase collections | ✅ Uses the admin UI | Documents the schema |
| VPS is compromised | ✅ Destroys VPS, creates new one | Re-runs setup script |

---

## 5. Middlemen Operations (No Agent Needed)

Middlemen can fully self-service if you set up the hook above:

```bash
# Middleman requests 5 Stealth codes
curl -X POST https://networkingguides.duckdns.org/api/middleman/request-codes \
  -H "Content-Type: application/json" \
  -d '{"token":"your-middleman-secret","tier":"stealth","count":5}'

# Response:
# { "codes": ["A3X9-K7M2-R4P1", "B5Y0-L8N3-S6Q2", ...] }
```

They text these codes to buyers. The app handles the rest.

---

## 6. Update Push (How the Agent Ships New Versions)

Since there's no auto-updater, the agent follows this process:

### For a config change (no new binary needed)

1. Agent updates `tier_configs` entries in PocketBase via API
2. Optionally updates `/etc/hysteria/server.yaml` via SSH
3. Users get the new config on their **next connection** (activation re-fetches config)

### For a new app binary

1. Agent builds binaries (on a machine with Go + Fyne deps, or uses CI)
2. Agent zips them with the Hysteria 2 engine
3. Agent uploads to Google Drive (or tells the human to)
4. Agent posts the new link in the middlemen chat
5. Middlemen tell users to download the new version

### For a Hysteria 2 engine update

1. Agent downloads the new Hysteria 2 binary from GitHub releases
2. Agent zips it with the app for the next build
3. Same distribution process as app update

---

## 7. PocketBase Schema Reference (for Agent API calls)

### `codes` collection

| Field | Type | API Example |
|-------|------|-------------|
| `code` | text | `"ABC1-DEF2-GHI3"` |
| `tier` | text | `"eco"`, `"stealth"`, `"strike"` |
| `used` | bool | `true` / `false` |
| `suspended` | bool | `true` / `false` |
| `fingerprint` | text (optional) | SHA256 hash, set by app |
| `middleman` | text (optional) | `"Alice"` |

**API filter examples:**
```bash
# Unused codes
?filter=(used=false)

# Suspended users
?filter=(suspended=true)

# All Strike users
?filter=(tier='strike')

# Used codes by middleman Alice
?filter=(middleman='Alice' && used=true)
```

### `tier_configs` collection

| Field | Type | API Example |
|-------|------|-------------|
| `tier` | text (unique) | `"eco"` |
| `config_json` | json | Full Hysteria 2 client config |
| `active` | bool | `true` |

**Update a tier config:**
```bash
curl -X PATCH "$PB_API/api/collections/tier_configs/records/CONFIG_ID" \
  -H "Authorization: Bearer $PB_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"config_json": { ... updated config ... }}'
```
