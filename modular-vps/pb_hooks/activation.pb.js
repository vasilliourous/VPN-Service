// MyVPN Activation Hook (PocketBase 0.22+)
//
// Validates activation codes with Luhn-mod-N checksum,
// binds to hardware fingerprint, enforces rate limiting.
//
// PocketBase 0.22 JSVM notes:
//   $app.dao() → $app (direct), $app.dao().db() → $app.db()
//   $apis.requestInfo(c) → e.requestInfo()
//   $app.dao().saveRecord() → $app.save()
//   $app.dao().createRecord() → new Record(collection)
//   c.json() → e.json()
//   c.query() → e.request.url.query()

// ── Luhn-mod-N checksum validation ──
// Character set: uppercase letters + digits (36 chars, no I/O/0/1 ambiguity)
// Standard Luhn-mod-N: double value, if >= N subtract (N-1), sum all digits
function luhnModNCheck(code) {
    if (typeof code !== "string") return false;
    const cleaned = code.replace(/-/g, "").toUpperCase();
    if (cleaned.length < 2) return false;

    const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
    const n = chars.length;
    let sum = 0;
    let alternate = false;

    for (let i = cleaned.length - 1; i >= 0; i--) {
        const idx = chars.indexOf(cleaned[i]);
        if (idx === -1) return false;

        let val = idx;
        if (alternate) {
            val *= 2;
            if (val >= n) val = val - n + 1;
        }
        sum += val;
        alternate = !alternate;
    }
    return (sum % n) === 0;
}

function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    return val.replace(/\\/g, "").replace(/'/g, "");
}

routerAdd("POST", "/api/activate", (e) => {
    const body = e.requestInfo().body;
    const code = (body.code || "").trim();
    const clientFingerprint = (body.fingerprint || "").trim();
    const ip = e.remoteIP();

    // ── Validate required fields ──
    if (!code) {
        return e.json(400, { code: 400, message: "Missing code" });
    }
    if (!clientFingerprint) {
        return e.json(400, { code: 400, message: "Missing device fingerprint" });
    }

    // ── Luhn-mod-N client-side checksum validation ──
    if (!luhnModNCheck(code)) {
        return e.json(400, { code: 400, message: "Invalid code format — checksum failed" });
    }

    // ── Rate limiting: fingerprint-keyed (fingerprint is already SHA256), IP as fallback ──
    const rateKey = clientFingerprint || ip.replace(/\./g, "_");
    const rateLimit = 5;        // 5 attempts
    const rateWindow = 10;      // 10 minutes

    // Clean old attempts
    $app.db().newQuery(
        "DELETE FROM activation_attempts WHERE created < datetime('now', '-10 minutes')"
    ).execute();

    // Check current count
    const recentAttempts = $app.findRecordsByFilter(
        "activation_attempts",
        "rate_key={:key} && created >= datetime('now', '-10 minutes')",
        "", 0, 0,
        { key: rateKey }
    );

    if (recentAttempts.length >= rateLimit) {
        return e.json(429, {
            code: 429,
            message: "Too many activation attempts. Try again in 10 minutes."
        });
    }

    // ── Log this attempt ──
    const attemptCol = $app.findCollectionByNameOrId("activation_attempts");
    const attempt = new Record(attemptCol);
    attempt.set("ip", ip);
    attempt.set("rate_key", rateKey);
    attempt.set("fingerprint", clientFingerprint.substring(0, 16) + "****");
    attempt.set("code_attempted", code.substring(0, 4) + "****");
    $app.save(attempt);

    // ── Look up the code (parameterized query) ──
    const safeCode = sanitizeFilter(code);
    const records = $app.findRecordsByFilter(
        "codes",
        "code={:code}",
        "", 0, 1,
        { code: safeCode }
    );

    if (records.length === 0) {
        return e.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];

    // ── Check if code is already bound to a device ──
    const existingFp = record.getString("bound_fingerprint");
    if (existingFp) {
        // Code already bound; verify it matches this device
        if (existingFp !== clientFingerprint) {
            return e.json(403, { code: 403, message: "Code is already bound to another device" });
        }
        // Same device re-activating — allow
        return e.json(200, {
            code: 200,
            message: "Device already activated",
            tier: record.getString("tier"),
            device_fingerprint: existingFp
        });
    }

    // ── Check if code is suspended ──
    if (record.getBool("suspended")) {
        return e.json(403, { code: 403, message: "Code is suspended" });
    }

    // ── Check if code is expired ──
    const expiresAt = record.get("expires_at");
    if (expiresAt) {
        const expiryDate = new Date(expiresAt).getTime();
        if (!isNaN(expiryDate) && expiryDate < Date.now()) {
            return e.json(410, { code: 410, message: "Code has expired" });
        }
    }

    // ── Bind code to device fingerprint ──
    // The fingerprint was already SHA256-hashed by the client before sending.
    // We store it as-is — we don't re-hash (that would double-hash).
    record.set("bound_fingerprint", clientFingerprint);
    record.set("activated_at", new Date().toISOString());
    $app.save(record);

    // ── Clean up rate limiting attempts for this key ──
    $app.db().newQuery(
        "DELETE FROM activation_attempts WHERE rate_key={:key}"
    ).bind({ key: rateKey }).execute();

    // ── Return tier config ──
    const tier = record.getString("tier");
    const safeTier = sanitizeFilter(tier);
    const configs = $app.findRecordsByFilter(
        "tier_configs",
        "tier={:tier} && active={:active}",
        "", 0, 1,
        { tier: safeTier, active: true }
    );

    const response = {
        code: 200,
        message: "Activation successful",
        tier: tier,
        device_fingerprint: clientFingerprint
    };

    if (configs.length > 0) {
        response.server_config = configs[0].get("config");
        response.udp_relay = configs[0].get("udp_relay");
    }

    return e.json(200, response);
});
