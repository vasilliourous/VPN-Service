// Heartbeat handler — validates token, checks suspension,
// rotates token, and returns remote commands if any.
routerAdd("POST", "/api/heartbeat", (c) => {
    const auth = c.requestHeader("Authorization");
    const fingerprint = c.requestHeader("X-Device-Fingerprint");

    if (!auth || !fingerprint) {
        return c.json(401, { "code": 401, "message": "Unauthorized" });
    }

    // Extract and verify token
    const token = auth.replace("Bearer ", "");
    const secret = $os.getenv("TOKEN_SECRET") || "change-me-in-production";
    const parts = token.split(".");
    if (parts.length !== 2) {
        return c.json(401, { "code": 401, "message": "Invalid token" });
    }

    const payload = parts[0];
    const signature = parts[1];
    const expectedSig = $security.hmacSHA256(payload, secret);
    if (signature !== expectedSig) {
        return c.json(401, { "code": 401, "message": "Invalid token signature" });
    }

    // Decode payload
    let tokenData;
    try {
        tokenData = JSON.parse(atob(payload));
    } catch (e) {
        return c.json(401, { "code": 401, "message": "Invalid token payload" });
    }

    // Check expiry (grace period allows expired tokens for up to 7 days)
    const now = Date.now();
    if (tokenData.exp && tokenData.exp < now) {
        return c.json(401, { "code": 401, "message": "Token expired" });
    }

    // Look up the activation code by fingerprint
    const record = $app.dao().findFirstRecordByFilter("activation_codes",
        `bound_device_id = {:fp} && active = true`, { fp: fingerprint });
    if (!record) {
        return c.json(404, { "code": 404, "message": "Device not found" });
    }

    // Check suspension
    if (record.get("suspended")) {
        const reason = record.get("suspended_reason") || "Account suspended";
        return c.json(200, {
            "status": "suspended",
            "plan": record.get("plan"),
            "token": "",
            "protocols": [],
            "commands": [],
            "message": reason
        });
    }

    // Generate new rotated token
    const newTokenPayload = JSON.stringify({
        fingerprint: fingerprint,
        code: record.get("code"),
        exp: now + 24 * 60 * 60 * 1000
    });
    const newToken = btoa(newTokenPayload) + "." + $security.hmacSHA256(newTokenPayload, secret);

    // Return heartbeat response
    return c.json(200, {
        "status": "active",
        "plan": record.get("plan"),
        "token": newToken,
        "protocols": [
            {
                "id": "hysteria2",
                "display_name": "Speed Mode",
                "binary_name": "speedmode",
                "config": { "server": "your-server:443", "auth": "your-auth" },
                "weight": 1
            }
        ],
        "commands": [],
        "message": ""
    });
});
