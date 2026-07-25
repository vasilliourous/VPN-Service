// MyVPN Activation Hook
//
// Validates activation codes with Luhn-mod-N checksum,
// binds to hardware fingerprint, enforces rate limiting.
//
// IMPORTANT: PocketBase's JavaScript runtime (goja) does NOT have
// Node.js crypto. This hook uses pure-JS for checksums and uses
// the fingerprint string directly for rate limiting (it's already
// SHA256-hashed client-side before transmission).

// ── Luhn-mod-N checksum validation ──
// Character set: uppercase letters + digits (36 chars, no I/O/0/1 ambiguity)
// Standard Luhn-mod-N: double value, if >= N subtract (N-1), sum all digits
function luhnModNCheck(code) {
    if (typeof code !== "string") return false;
    const cleaned = code.replace(/-/g, "").toUpperCase();
    if (cleaned.length < 2) return false;

    const chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ";
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

routerAdd("POST", "/api/activate", (c) => {
    const data = $apis.requestInfo(c).data;
    const code = (data.code || "").trim();
    const clientFingerprint = (data.fingerprint || "").trim();
    const ip = $apis.requestInfo(c).remoteAddress;

    // ── Validate required fields ──
    if (!code) {
        return c.json(400, { code: 400, message: "Missing code" });
    }
    if (!clientFingerprint) {
        return c.json(400, { code: 400, message: "Missing device fingerprint" });
    }

    // ── Luhn-mod-N client-side checksum validation ──
    if (!luhnModNCheck(code)) {
        return c.json(400, { code: 400, message: "Invalid code format — checksum failed" });
    }

    // ── Rate limiting: fingerprint-keyed (fingerprint is already SHA256), IP as fallback ──
    const rateKey = clientFingerprint || ip.replace(/\./g, "_");
    const rateLimit = 5;        // 5 attempts
    const rateWindow = 10;      // 10 minutes

    // Clean old attempts
    $app.dao().db().exec(
        "DELETE FROM activation_attempts WHERE created < datetime('now', '-10 minutes')"
    );

    // Check current count
    const recentAttempts = $app.dao().findRecordsByFilter(
        "activation_attempts",
        "rate_key={:key} && created >= datetime('now', '-10 minutes')",
        "", 0, 0,
        { key: rateKey }
    );

    if (recentAttempts.length >= rateLimit) {
        return c.json(429, {
            code: 429,
            message: "Too many activation attempts. Try again in 10 minutes."
        });
    }

    // ── Log this attempt ──
    const attempt = $app.dao().createRecord("activation_attempts");
    attempt.set("ip", ip);
    attempt.set("rate_key", rateKey);
    attempt.set("fingerprint", clientFingerprint.substring(0, 16) + "****");
    attempt.set("code_attempted", code.substring(0, 4) + "****");
    $app.dao().saveRecord(attempt);

    // ── Look up the code (parameterized query) ──
    const safeCode = sanitizeFilter(code);
    const records = $app.dao().findRecordsByFilter(
        "codes",
        "code={:code}",
        "", 0, 1,
        { code: safeCode }
    );

    if (records.length === 0) {
        return c.json(404, { code: 404, message: "Code not found" });
    }

    const record = records[0];

    // ── Check if code is already bound to a device ──
    const existingFp = record.getString("bound_fingerprint");
    if (existingFp) {
        // Code already bound; verify it matches this device
        if (existingFp !== clientFingerprint) {
            return c.json(403, { code: 403, message: "Code is already bound to another device" });
        }
        // Same device re-activating — allow
        return c.json(200, {
            code: 200,
            message: "Device already activated",
            tier: record.getString("tier"),
            device_fingerprint: existingFp
        });
    }

    // ── Check if code is suspended ──
    if (record.getBool("suspended")) {
        return c.json(403, { code: 403, message: "Code is suspended" });
    }

    // ── Check if code is expired ──
    const expiresAt = record.getDateTime("expires_at");
    if (expiresAt && expiresAt.time().getTime() < Date.now()) {
        return c.json(410, { code: 410, message: "Code has expired" });
    }

    // ── Bind code to device fingerprint ──
    // The fingerprint was already SHA256-hashed by the client before sending.
    // We store it as-is — we don't re-hash (that would double-hash).
    record.set("bound_fingerprint", clientFingerprint);
    record.set("activated_at", new Date().toISOString());
    $app.dao().saveRecord(record);

    // ── Clean up rate limiting attempts for this key ──
    $app.dao().db().exec(
        "DELETE FROM activation_attempts WHERE rate_key={:key}",
        { key: rateKey }
    );

    // ── Return tier config ──
    const tier = record.getString("tier");
    const safeTier = sanitizeFilter(tier);
    const configs = $app.dao().findRecordsByFilter(
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

    return c.json(200, response);
});
