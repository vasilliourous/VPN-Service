// Locus Heartbeat Hook — PocketBase 0.22 compatible
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
        // NOTE: it THROWS "sql: no rows in result set" for an unknown code rather
        // than returning null, so the `if (!record)` check below is only reached
        // once the throw is caught. Without the try/catch an unknown code
        // surfaced as HTTP 500 instead of a clean 404 (found live 2026-09-19).
        var record = null;
        try {
            record = $app.dao().findFirstRecordByData("codes", "code", canonical);
        } catch (notFound) {
            record = null;
        }
        if (!record) return e.json(404, {code:404, message:"Code not found"});

        if (record.getBool("suspended")) return e.json(403, {code:403, message:"Account suspended — contact your middleman"});

        var response = {status:"ok", server_time:new Date().toISOString()};

        // Update signal — read from update_config.
        //
        // There is NO rollout percentage. An active release is advertised to
        // every client that heartbeats; `active` is the single off switch.
        //
        // The percentage was removed deliberately (2026-09): it existed to
        // limit how many devices saw a bad build, because the client has no
        // rollback beyond the installer's own atomicity. The cost was a fleet
        // where a low percentage means the operator's own test device is
        // probably outside the bucket — so a working updater looks broken, and
        // the usual outcome was a misdiagnosis rather than safety. With it gone,
        // "publish" and "offer" are the same act, and `active` stops offering.
        //
        // IMPORTANT: use findFirstRecordByFilter, NOT findRecordsByFilter.
        // In this PocketBase build (0.22.21) findRecordsByFilter silently
        // returns ZERO rows for every collection and filter, even `1=1` on a
        // populated table, with no error raised. That made the update gate a
        // permanent no-op: no client was ever offered an update. Both
        // findFirstRecordByFilter and findRecordsByExpr work reliably (verified
        // live 2026-09-19), including on bool fields.
        try {
            var u = null;
            try {
                u = $app.dao().findFirstRecordByFilter("update_config", "active = true");
            } catch (noRow) {
                u = null; // no active row — nothing to advertise
            }
            if (u && u.get("version")) {
                response.update_available = u.get("version");

                // Generic fallback fields. update_url/update_sha256 are the
                // legacy single-platform pair and describe the LINUX binary
                // only, so they are kept for older clients and must not be
                // treated as describing the device's own platform.
                if (u.get("update_url")) response.update_url = u.get("update_url");
                if (u.get("update_sha256")) response.update_sha256 = u.get("update_sha256");

                // Per-platform URL and checksum. The client verifies the bytes
                // it actually downloads, so a single update_sha256 is not enough
                // — it can only ever match one platform, and the updater refuses
                // to apply an update with an empty hash.
                //
                // The field NAMES are renamed on the way out: the record stores
                // download_<platform>, the response carries update_<platform>.
                // That rename is historical and load-bearing — deployed clients
                // read update_linux, so the hook must keep translating. Do not
                // "simplify" it to emit download_* directly.
                var platKeysH = ["linux", "windows", "macos_intel", "macos_arm"];
                for (var hi = 0; hi < platKeysH.length; hi++) {
                    var hk = platKeysH[hi];
                    if (u.get("download_" + hk)) response["update_" + hk] = u.get("download_" + hk);
                    if (u.get("sha256_" + hk)) response["update_sha256_" + hk] = u.get("sha256_" + hk);
                    // The minisign signature. The Tauri updater verifies one
                    // MANDATORILY, so a release advertised without this cannot be
                    // installed — the client would download and then fail. This
                    // hook is the path that carries it; /api/update serves the
                    // same data to the plugin directly.
                    if (u.get("signature_" + hk)) response["update_signature_" + hk] = u.get("signature_" + hk);
                }
            }
        } catch(ex) { /* update_config missing or unreadable — skip updates */ }

        // Server config for this tier.
        // Same findRecordsByFilter caveat as above — findFirstRecordByFilter is
        // the working call. The previous version relied on a lookup that could
        // return nothing, which would omit server_config from the heartbeat and
        // leave clients unable to refresh their tunnel settings.
        var tier = record.getString("tier");
        var tierVal = tier.replace(/[^a-zA-Z0-9_]/g, "_");
        var cfgRec = null;
        try {
            cfgRec = $app.dao().findFirstRecordByFilter("tier_configs", "tier = '" + tierVal + "'");
        } catch (noCfg) {
            cfgRec = null;
        }
        if (cfgRec) {
            // ─── FROZEN WIRE CONTRACT — DO NOT RENAME ───────────────────────
            // The tier's config JSON is passed through VERBATIM, so the UoT
            // endpoint reaches the client under its stored key, "uot_port".
            //
            // That key is load-bearing and cannot be changed: clients already
            // in the field read "uot_port", and an unknown-to-them key is a
            // SILENT no-op rather than an error — the exact failure that made
            // UDP-over-TCP dead fleet-wide until 2026-09-19 (the client struct
            // declared only "server_port_uot"). The client now tolerates both
            // spellings via internal/uotkey, but the hub must keep emitting
            // "uot_port" because older builds cannot be updated retroactively.
            //
            // See FIXES.md entry 29 and internal/uotkey for the full history.
            // ────────────────────────────────────────────────────────────────
            try { response.server_config = JSON.parse(cfgRec.get("config")); } catch(ex) { response.server_config = cfgRec.get("config"); }
            response.udp_relay = cfgRec.get("udp_relay");
        }
        response.tier = tier;

        return e.json(200, response);
    } catch(err) {
        return e.json(500, {code:500, message:err.message || String(err)});
    }
});
