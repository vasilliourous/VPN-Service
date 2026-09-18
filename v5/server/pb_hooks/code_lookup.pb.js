// Locus Code Lookup Hook — PocketBase 0.22 compatible
//
// READ-ONLY pre-check for activation codes. The client calls this from the
// activation screen so a student learns whether their code is actually
// recognised BEFORE committing to the full /api/activate round-trip (which
// binds the device and can take up to 30s).
//
// GUARANTEES:
//   * NEVER binds a device, NEVER writes to the codes collection.
//   * Shares the same Luhn-mod-N precheck and canonical-code lookup as
//     /api/activate, so the two endpoints cannot disagree about what a code is.
//   * Reuses the activation_attempts table for rate limiting, so it cannot be
//     used as an unbounded code-enumeration oracle.
//
// Response shape (always HTTP 200 for a well-formed request so the client can
// distinguish "code is bad" from "transport failed"):
//   {status: "ok"|"unbound"|"bound_this_device"|"bound_other"|
//            "suspended"|"expired"|"not_found", tier?, expires_at?, message?}
//
// NOTE: deployed by copying this file to /opt/pocketbase/pb_hooks/ (see OPS.md).
// PocketBase must be restarted for a new hook file to register.
routerAdd("POST", "/api/code-lookup", function(e) {
    try {
        var data = $apis.requestInfo(e).data;
        var code = (data.code || "").trim();
        var fp = (data.fingerprint || "").trim();
        try { var addr = (e.request().remoteAddr || "").split(":"); var ip = addr[0] || ""; } catch(ex) { var ip = ""; }

        if (!code) return e.json(400, {status:"not_found", message:"Missing code"});
        if (!fp) return e.json(400, {status:"not_found", message:"Missing device fingerprint"});

        // ── Luhn-mod-N check (32-char charset, mirroring the client) ──
        // Same algorithm as activation.pb.js — a malformed code is answered
        // locally without touching the database.
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
        if (!ok) return e.json(200, {status:"not_found", message:"Invalid code format"});

        // ── Rate limiting (shared with activation attempts) ──
        // Keeps this from becoming an enumeration oracle. Uses the same table
        // and threshold as /api/activate.
        $app.dao().db().newQuery("DELETE FROM activation_attempts WHERE created < datetime('now','-10 minutes')").execute();
        var rateKey = (fp || ip).replace(/[^a-zA-Z0-9]/g,"_");
        var recents = $app.dao().findRecordsByFilter("activation_attempts","rate_key='"+rateKey+"'","",0,0);
        if (recents.length >= 10) {
            return e.json(429, {status:"not_found", message:"Too many attempts — please wait a few minutes"});
        }
        // Log the attempt. NB: unlike /api/activate we do NOT delete this row
        // afterwards, so lookups are counted too.
        var c2 = $app.dao().findCollectionByNameOrId("activation_attempts");
        var att = new Record(c2);
        att.set("ip",ip); att.set("rate_key",rateKey);
        att.set("fingerprint",fp.substring(0,16)+"****");
        att.set("code_attempted",code.substring(0,4)+"****");
        $app.dao().saveRecord(att);

        // ── Canonical-form lookup (identical to activation.pb.js) ──
        var canonical = (s.length === 15)
            ? s.substring(0,2)+"-"+s.substring(2,6)+"-"+s.substring(6,10)+"-"+s.substring(10,14)+"-"+s.substring(14,15)
            : code;
        var rec = $app.dao().findFirstRecordByData("codes", "code", canonical);
        if (!rec) return e.json(200, {status:"not_found", message:"Code not found"});

        // ── Describe the code WITHOUT binding anything ──
        var tier = rec.getString("tier");
        var exp = rec.get("expires_at");
        var resp = {status:"ok", tier:tier};
        if (exp) { resp.expires_at = exp; }

        if (rec.getBool("suspended")) {
            resp.status = "suspended";
            resp.message = "This code has been suspended — contact support";
            return e.json(200, resp);
        }
        if (exp) {
            var ed = new Date(exp).getTime();
            if (!isNaN(ed) && ed < Date.now()) {
                resp.status = "expired";
                resp.message = "This code has expired";
                return e.json(200, resp);
            }
        }

        var boundFp = rec.getString("bound_fingerprint");
        if (boundFp) {
            if (boundFp === fp) {
                resp.status = "bound_this_device";
                resp.message = "Already activated on this device";
            } else {
                resp.status = "bound_other";
                resp.message = "This code is already in use on another device";
            }
            return e.json(200, resp);
        }

        resp.status = "unbound";
        resp.message = "Ready to activate";
        return e.json(200, resp);
    } catch(err) {
        // Transient/internal failure — the client treats a 5xx as "unknown" and
        // falls back to the activate-time check, so this must not look like a
        // definitive "code not found".
        return e.json(500, {status:"unknown", message:err.message || String(err)});
    }
});
