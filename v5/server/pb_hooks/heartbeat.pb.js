// MyVPN Heartbeat Hook — PocketBase 0.22 compatible
// Changed from GET to POST to avoid leaking activation codes in server access logs.
// Code and fingerprint are sent in the JSON body, not URL query parameters.
routerAdd("POST", "/api/heartbeat", function(e) {
    try {
        var data = $apis.requestInfo(e).data;
        var code = (data.code || "").trim();
        var fingerprint = (data.fingerprint || "").trim();

        if (!code) return e.json(400, {code:400, message:"Missing code"});

        // Normalize the lookup code to the canonical hyphenated form
        // ("RQ-XXXX-XXXX-XXXX-C" — the form codes are seeded in). Clients
        // should send the canonical form, but tolerating any pasted variant
        // keeps heartbeat (suspension checks, update signals) alive for
        // legacy installs that stored an unformatted code.
        var s = code.replace(/-/g,"").toUpperCase();
        var canonical = (s.length === 15)
            ? s.substring(0,2)+"-"+s.substring(2,6)+"-"+s.substring(6,10)+"-"+s.substring(10,14)+"-"+s.substring(14,15)
            : code;

        // Use findFirstRecordByData (same approach as activation hook — confirmed working on PB 0.22.21)
        var record = $app.dao().findFirstRecordByData("codes", "code", canonical);
        if (!record) return e.json(404, {code:404, message:"Code not found"});

        if (record.getBool("suspended")) return e.json(403, {code:403, message:"Account suspended — contact your middleman"});

        var response = {status:"ok", server_time:new Date().toISOString()};

        // Staged rollout check — read from update_config
        try {
            var updateRecs = $app.dao().findRecordsByFilter("update_config", "active=true", "", 0, 1);
            if (updateRecs.length > 0) {
                var u = updateRecs[0];
                var pct = parseInt(u.get("rollout_percent") || "0", 10);
                if (pct > 0) {
                    // Simple hash for eligibility
                    var key = fingerprint || code, hash = 0;
                    for (var i = 0; i < key.length; i++) { hash = ((hash << 5) - hash) + key.charCodeAt(i); hash |= 0; }
                    var bucket = Math.abs(hash) % 100;
                    if (bucket < pct) {
                        response.update_available = u.get("version");
                        response.update_url = u.get("update_url");
                        response.update_sha256 = u.get("update_sha256");
                        if (u.get("download_linux")) response.update_linux = u.get("download_linux");
                        if (u.get("download_windows")) response.update_windows = u.get("download_windows");
                        if (u.get("download_macos_intel")) response.update_macos_intel = u.get("download_macos_intel");
                        if (u.get("download_macos_arm")) response.update_macos_arm = u.get("download_macos_arm");
                    }
                }
            }
        } catch(ex) { /* no update_config collection */ }

        // Newest record wins (defends against duplicate records from older
        // seed runs — see FIXES.md). Inline filter value — {:param} binding
        // is unreliable on PB 0.22.
        var tier = record.getString("tier");
        var tierVal = tier.replace(/[^a-zA-Z0-9_]/g, "_");
        var cfgRecs = $app.dao().findRecordsByFilter("tier_configs", "tier='"+tierVal+"'", "-created", 1, 0);
        var cfgRec = cfgRecs.length > 0 ? cfgRecs[0] : null;
        if (cfgRec) {
            try { response.server_config = JSON.parse(cfgRec.get("config")); } catch(ex) { response.server_config = cfgRec.get("config"); }
            response.udp_relay = cfgRec.get("udp_relay");
        }
        response.tier = tier;

        return e.json(200, response);
    } catch(err) {
        return e.json(500, {code:500, message:err.message || String(err)});
    }
});
