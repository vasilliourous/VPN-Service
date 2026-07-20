// Permanent device binding — one code, one device, forever.
// If the app is deleted and reinstalled on the same device,
// the same code works again because the hardware fingerprint is identical.
// A different device with the same code → rejected.
//
// Server can suspend a binding without destroying it.
// Suspended devices get "suspended" status in heartbeat → client disconnects
// but can re-activate (receive a new token) when unsuspended.

const CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const BASE = CHARSET.length;

function luhnModN(code) {
    let sum = 0;
    let double = false;
    for (let i = code.length - 1; i >= 0; i--) {
        let val = CHARSET.indexOf(code[i].toUpperCase());
        if (val === -1) return -1;
        if (double) {
            val *= 2;
            if (val >= BASE) val = Math.floor(val / BASE) + (val % BASE);
        }
        sum += val;
        double = !double;
    }
    return (BASE - (sum % BASE)) % BASE;
}

function validateCode(code) {
    code = code.replace(/^MYVPN-/, "").replace(/-/g, "");
    if (code.length !== 17) return false;
    const checkChar = code[code.length - 1];
    const data = code.slice(0, -1);
    const expectedCheck = CHARSET[luhnModN(data)];
    return checkChar.toUpperCase() === expectedCheck;
}

routerAdd("POST", "/api/collections/activations/records", (c) => {
    const body = c.requestInfo().body;
    const code = body.code;
    const fingerprint = c.requestHeader("X-Device-Fingerprint");

    if (!code || !fingerprint) {
        return c.json(400, { "code": 400, "message": "Missing code or device fingerprint" });
    }

    // Validate Luhn checksum client-side was done, but verify server-side too
    if (!validateCode(code)) {
        return c.json(400, { "code": 400, "message": "Invalid activation code format" });
    }

    // Look up the activation code
    const record = $app.dao().findFirstRecordByFilter("activation_codes",
        `code = {:code} && active = true`, { code });
    if (!record) {
        return c.json(404, { "code": 404, "message": "Invalid or expired code" });
    }

    // Check expiry
    const expires = record.get("expires");
    if (expires && new Date(expires) < new Date()) {
        return c.json(410, { "code": 410, "message": "Code expired" });
    }

    const boundDevice = record.get("bound_device_id");

    if (!boundDevice || boundDevice === "") {
        // Code is fresh — permanently bind to this device
        record.set("bound_device_id", fingerprint);
        $app.dao().saveRecord(record);
    } else if (boundDevice === fingerprint) {
        // Same device re-activating (e.g. after app reinstall)
        const suspended = record.get("suspended");
        if (suspended) {
            const reason = record.get("suspended_reason") || "Your account has been suspended. Contact your middleman.";
            return c.json(403, { "code": 403, "message": reason });
        }
        // Device is recognized — allow re-activation
    } else {
        // Different device — reject permanently
        return c.json(403, { "code": 403, "message": "This code is already bound to another device" });
    }

    // Generate a token
    const secret = $os.getenv("TOKEN_SECRET") || "change-me-in-production";
    const tokenPayload = JSON.stringify({
        fingerprint: fingerprint,
        code: code,
        exp: Date.now() + 24 * 60 * 60 * 1000
    });
    const token = btoa(tokenPayload) + "." + $security.hmacSHA256(tokenPayload, secret);

    // Return token and plan
    return c.json(200, {
        "token": token,
        "plan": record.get("plan")
    });
});
