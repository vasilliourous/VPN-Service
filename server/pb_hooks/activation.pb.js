// Locus Activation Hook — PocketBase 0.22 compatible
// Uses findFirstRecordByData for lookups (simplest API, works across versions)
// Uses newQuery().execute() for SQL operations
// All code inside routerAdd callback (functions not hoisted in goja scope)

routerAdd("POST", "/api/activate", function(e) {
    // ── PocketBase date parsing ──────────────────────────────────────────────
    // CRITICAL: `new Date("2027-09-19 00:00:00.000Z")` returns NaN in goja.
    // The ECMAScript date-string format requires a "T" separator; PocketBase
    // stores a space. Measured against the live hub on 2026-09-19.
    //
    // Every expiry check used to read:
    //     var ed = new Date(exp).getTime(); if (!isNaN(ed) && ed < Date.now()) ...
    // and because the parse ALWAYS returned NaN, the isNaN guard was always
    // taken and the comparison NEVER RAN — so no code ever expired. The guard
    // converted a parse failure into "still valid", the most dangerous default
    // for an expiry check.
    //
    // Defined inside the callback on purpose: goja does not hoist function
    // declarations across scopes, so a file-level helper is invisible here.
    function parsePBDate(value) {
        if (value === null || value === undefined || value === "") return 0;
        if (typeof value === "number") return value;
        var s = String(value).trim();
        if (!s) return 0;
        var direct = new Date(s).getTime();
        if (!isNaN(direct)) return direct;
        var swapped = new Date(s.replace(" ", "T")).getTime();
        if (!isNaN(swapped)) return swapped;
        if (/^[0-9]+$/.test(s)) {
            var n = parseInt(s, 10);
            return s.length <= 10 ? n * 1000 : n;
        }
        return NaN;
    }

    try {
        var data = $apis.requestInfo(e).data;
        var code = (data.code || "").trim();
        var fp = (data.fingerprint || "").trim();
        try { var addr = (e.request().remoteAddr || "").split(":"); var ip = addr[0] || ""; } catch(ex) { var ip = ""; }

        if (!code) return e.json(400, {code:400, message:"Missing code"});
        if (!fp) return e.json(400, {code:400, message:"Missing device fingerprint"});

        // Luhn-mod-N check (32-char charset matching client)
        var s = code.replace(/-/g,"").toUpperCase();
        var c = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789", n = c.length, ok = false, sum = 0, alt = false;
        for (var i = s.length - 2; i >= 0; i--) {
            var idx = c.indexOf(s[i]);
            if (idx === -1) { ok = false; break; }
            var v = idx;
            if (alt) { v *= 2; if (v >= n) v = v - n + 1; }
            sum += v; alt = !alt; ok = true;
        }
        if (ok) ok = ((n - (sum % n)) % n) === c.indexOf(s[s.length - 1]);
        if (!ok) return e.json(400, {code:400, message:"Invalid code format"});

        // Rate limiting — clean old + count recent
        $app.dao().db().newQuery("DELETE FROM activation_attempts WHERE created < datetime('now','-10 minutes')").execute();
        // rateKey is SHA256 hex or IP — strip anything non-alphanumeric for query safety.
        //
        // The "activate_" prefix namespaces this bucket. /api/code-lookup writes
        // "lookup_" keys into the same table, and the two must NOT share a
        // budget: a typo here would otherwise consume the read-only lookup that
        // exists to tell the student WHY the code was rejected. See the matching
        // note in code_lookup.pb.js.
        var rateKey = "activate_" + (fp || ip).replace(/[^a-zA-Z0-9]/g,"_");
        // NOTE: findRecordsByFilter is broken in this PB build (0.22.21) — it
        // returns zero rows for every filter, which silently DISABLED this rate
        // limit entirely (5 attempts / 10 min was never enforced). We count with
        // findRecordsByExpr, which works. Verified live 2026-09-19.
        var recentCount = 0;
        try {
            recentCount = $app.dao().findRecordsByExpr("activation_attempts",
                $dbx.exp("rate_key = {:k}", { k: rateKey })).length;
        } catch (countErr) {
            recentCount = 0; // fail open on a counting error rather than blocking users
        }
        if (recentCount >= 5) return e.json(429,{code:429, message:"Too many attempts"});

        // Log attempt
        var c2 = $app.dao().findCollectionByNameOrId("activation_attempts");
        var att = new Record(c2);
        att.set("ip",ip); att.set("rate_key",rateKey);
        att.set("fingerprint",fp.substring(0,16)+"****");
        att.set("code_attempted",code.substring(0,4)+"****");
        $app.dao().saveRecord(att);

        // Find code — use findFirstRecordByData which is simpler.
        // Lookup uses the canonical hyphenated form (the form codes are seeded
        // in); tolerate any pasted variant just like the heartbeat hook.
        var canonical = (s.length === 15)
            ? s.substring(0,2)+"-"+s.substring(2,6)+"-"+s.substring(6,10)+"-"+s.substring(10,14)+"-"+s.substring(14,15)
            : code;
        // NOTE: findFirstRecordByData THROWS "sql: no rows in result set" when
        // the code is absent — it does not return null. Catching it here turns
        // a perfectly ordinary "no such code" into a clean 404 instead of a 500
        // (the previous `if (!rec)` check was unreachable). See code_lookup.pb.js
        // for the same fix, found live on 2026-09-19.
        var rec = null;
        try {
            rec = $app.dao().findFirstRecordByData("codes", "code", canonical);
        } catch (notFound) {
            rec = null;
        }
        if (!rec) return e.json(404, {code:404, message:"Code not found"});

        // Check binding
        var boundFp = rec.getString("bound_fingerprint");

        // ── Expiry and suspension, checked BEFORE the binding ──
        //
        // These used to live only on the first-activation path, BELOW the
        // `if (boundFp)` re-activation branch that returns early — so a device
        // that was already bound kept re-activating successfully forever, even
        // after its code expired or was suspended. The check was unreachable for
        // exactly the machines it was most likely to matter for.
        //
        // Order is now: expiry → suspension → binding. A lapsed code gets a
        // clear 410 on both paths (the same answer the client already knows how
        // to display), and a suspended code still returns 403 "Code bound to
        // another device" first when the fingerprint differs, so suspension
        // status is not leaked to a probing device.
        var expMs = parsePBDate(rec.get("expires_at"));
        if (!isNaN(expMs) && expMs > 0 && expMs < Date.now()) {
            return e.json(410, {code:410, message:"Code expired"});
        }
        var suspended = rec.getBool("suspended");

        if (boundFp) {
            if (boundFp !== fp) return e.json(403, {code:403, message:"Code bound to another device"});
            if (suspended) return e.json(403, {code:403, message:"Code suspended"});
            // Same-device re-activation: return the current tier config too, so
            // clients can refresh stale connection parameters (see FIXES.md).
            var tierVal2 = rec.getString("tier").replace(/[^a-zA-Z0-9_]/g, "_");
            // findFirstRecordByFilter (NOT findRecordsByFilter — see the rate-limit
            // note above; the list variant returns nothing on this PB build).
            var cfgRec2 = null;
            try { cfgRec2 = $app.dao().findFirstRecordByFilter("tier_configs", "tier = '" + tierVal2 + "'"); } catch (e2) { cfgRec2 = null; }
            var resp2 = {code:200, message:"Already activated", tier:rec.getString("tier"), device_fingerprint:boundFp};
            if (cfgRec2) {
                try { resp2.server_config = JSON.parse(cfgRec2.get("config")); } catch(ex) { resp2.server_config = cfgRec2.get("config"); }
                resp2.udp_relay = cfgRec2.get("udp_relay");
            }
            return e.json(200, resp2);
        }
        if (suspended) return e.json(403, {code:403, message:"Code suspended"});

        // Bind device
        rec.set("bound_fingerprint", fp);
        rec.set("activated_at", new Date().toISOString());
        $app.dao().saveRecord(rec);

        // Clean rate limiting
        $app.dao().db().newQuery("DELETE FROM activation_attempts WHERE rate_key={:key}").bind({key:rateKey}).execute();

        // Get tier config.
        // findFirstRecordByFilter, not findRecordsByFilter: the list variant
        // silently returns zero rows on this PB build, which would hand the
        // client a successful activation with NO server_config — a "connected"
        // app that cannot reach the internet.
        var tierVal = rec.getString("tier").replace(/[^a-zA-Z0-9_]/g, "_");
        var cfgRec = null;
        try { cfgRec = $app.dao().findFirstRecordByFilter("tier_configs", "tier = '" + tierVal + "'"); } catch (e3) { cfgRec = null; }
        var resp = {code:200, message:"Activation successful", tier:rec.getString("tier"), device_fingerprint:fp};
        if (cfgRec) {
            // The tier config is passed through VERBATIM, so the UoT endpoint
            // reaches the client under its stored key "uot_port". That key is a
            // FROZEN wire contract — do not rename it. See the full note in
            // heartbeat.pb.js and FIXES.md entry 29.
            try { resp.server_config = JSON.parse(cfgRec.get("config")); } catch(ex) { resp.server_config = cfgRec.get("config"); }
            resp.udp_relay = cfgRec.get("udp_relay");
        }
        return e.json(200, resp);
    } catch(err) {
        return e.json(500, {code:500, message:err.message || String(err)});
    }
});
