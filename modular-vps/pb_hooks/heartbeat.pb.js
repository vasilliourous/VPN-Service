// MyVPN Heartbeat Hook (PocketBase 0.22+)
//
// Checks suspension status, signals staged rollout updates,
// and returns refreshed tier config.
//
// PocketBase 0.22 JSVM notes:
//   $app.dao() → $app (direct)
//   c.query() → e.request.url.query()
//   c.json() → e.json()

function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    return val.replace(/\\/g, "").replace(/'/g, "");
}

function hashMod(str, modulus) {
    if (typeof str !== "string" || !str) return 0;
    let hash = 0;
    for (let i = 0; i < str.length; i++) {
        const chr = str.charCodeAt(i);
        hash = ((hash << 5) - hash) + chr;
        hash |= 0;
    }
    return Math.abs(hash) % modulus;
}

routerAdd("GET", "/api/heartbeat", (e) => {
    const code = e.request.url.query().get("code");
    const fingerprint = e.request.url.query().get("fp") || "";

    if (!code) {
        return e.json(400, { code: 400, message: "Missing code" });
    }

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

    // ── Suspension check ──
    if (record.getBool("suspended")) {
        return e.json(403, { code: 403, message: "Account suspended — contact your middleman" });
    }

    const response = {
        status: "ok",
        server_time: new Date().toISOString()
    };

    // ── Staged rollout: read update signal from update_config collection ──
    try {
        const updateRecords = $app.findRecordsByFilter(
            "update_config",
            "active={:active}",
            "", 0, 1,
            { active: true }
        );

        if (updateRecords.length > 0) {
            const updateRec = updateRecords[0];
            const rolloutPct = parseInt(updateRec.getString("rollout_percent") || "0", 10);

            if (rolloutPct > 0) {
                // Determine eligibility: use fingerprint if available, otherwise code hash
                const eligibilityKey = fingerprint || code;
                const bucket = hashMod(eligibilityKey, 100);

                if (bucket < rolloutPct) {
                    const version = updateRec.getString("version");

                    response.update_available = version;
                    response.update_url = updateRec.getString("update_url");
                    response.update_sha256 = updateRec.getString("update_sha256");

                    // Platform-specific download URLs (optional)
                    if (updateRec.get("download_windows")) {
                        response.update_windows = updateRec.get("download_windows");
                    }
                    if (updateRec.get("download_macos_intel")) {
                        response.update_macos_intel = updateRec.get("download_macos_intel");
                    }
                    if (updateRec.get("download_macos_arm")) {
                        response.update_macos_arm = updateRec.get("download_macos_arm");
                    }
                }
            }
        }
    } catch (err) {
        // No update_config collection or no active records — skip
        $app.logger().warn("Update check skipped: " + (err.message || "no config"));
    }

    // ── Return tier config (parameterized query) ──
    const tier = record.getString("tier");
    const safeTier = sanitizeFilter(tier);
    const configs = $app.findRecordsByFilter(
        "tier_configs",
        "tier={:tier} && active={:active}",
        "", 0, 1,
        { tier: safeTier, active: true }
    );

    if (configs.length > 0) {
        response.server_config = configs[0].get("config");
        response.udp_relay = configs[0].get("udp_relay");
    }

    response.tier = tier;

    return e.json(200, response);
});
