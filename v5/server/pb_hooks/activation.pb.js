// MyVPN Activation Hook — PocketBase 0.22 compatible
// Uses findFirstRecordByData for lookups (simplest API, works across versions)
// Uses newQuery().execute() for SQL operations
// All code inside routerAdd callback (functions not hoisted in goja scope)
routerAdd("POST", "/api/activate", function(e) {
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
        // rateKey is SHA256 hex or IP — strip anything non-alphanumeric for query safety
        var rateKey = (fp || ip).replace(/[^a-zA-Z0-9]/g,"_");
        // rateKey is SHA256 hex or IP — already sanitized to [a-zA-Z0-9_]
        // Using findRecordsByFilter with inline value because PocketBase 0.22
        // doesn't support {:param} binding in this API — the value is safe via regex.
        var recents = $app.dao().findRecordsByFilter("activation_attempts","rate_key='"+rateKey+"'","",0,0);
        if (recents.length >= 5) return e.json(429,{code:429, message:"Too many attempts"});

        // Log attempt
        var c2 = $app.dao().findCollectionByNameOrId("activation_attempts");
        var att = new Record(c2);
        att.set("ip",ip); att.set("rate_key",rateKey);
        att.set("fingerprint",fp.substring(0,16)+"****");
        att.set("code_attempted",code.substring(0,4)+"****");
        $app.dao().saveRecord(att);

        // Find code — use findFirstRecordByData which is simpler
        var rec = $app.dao().findFirstRecordByData("codes", "code", code);
        if (!rec) return e.json(404, {code:404, message:"Code not found"});

        // Check binding
        var boundFp = rec.getString("bound_fingerprint");
        if (boundFp) {
            if (boundFp !== fp) return e.json(403, {code:403, message:"Code bound to another device"});
            return e.json(200, {code:200, message:"Already activated", tier:rec.getString("tier"), device_fingerprint:boundFp});
        }
        if (rec.getBool("suspended")) return e.json(403, {code:403, message:"Code suspended"});
        var exp = rec.get("expires_at");
        if (exp) { var ed = new Date(exp).getTime(); if (!isNaN(ed) && ed < Date.now()) return e.json(410,{code:410,message:"Code expired"}); }

        // Bind device
        rec.set("bound_fingerprint", fp);
        rec.set("activated_at", new Date().toISOString());
        $app.dao().saveRecord(rec);

        // Clean rate limiting
        $app.dao().db().newQuery("DELETE FROM activation_attempts WHERE rate_key={:key}").bind({key:rateKey}).execute();

        // Get tier config using findFirstRecordByData (simplest cross-version API)
        var cfgRec = $app.dao().findFirstRecordByData("tier_configs", "tier", rec.getString("tier"));
        var resp = {code:200, message:"Activation successful", tier:rec.getString("tier"), device_fingerprint:fp};
        if (cfgRec) {
            try { resp.server_config = JSON.parse(cfgRec.get("config")); } catch(ex) { resp.server_config = cfgRec.get("config"); }
            resp.udp_relay = cfgRec.get("udp_relay");
        }
        return e.json(200, resp);
    } catch(err) {
        return e.json(500, {code:500, message:err.message || String(err)});
    }
});
