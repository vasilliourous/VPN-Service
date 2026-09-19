// Locus Admin API — server-side surface for the web console.
//
// WHY THIS EXISTS: managing the hub previously required SSH + Python + sqlite.
// That is fine for an engineer and hopeless day-to-day. This hook exposes the
// operations an operator actually performs as a small JSON API, so the console
// never needs shell access.
//
// Auth: every route requires the ADMIN_API_TOKEN (from /etc/environment) in the
// JSON body as `admin_token`, or as `X-Admin-Token`. The console holds the token
// in memory only — it is never embedded in the served bundle.
//
// CONVENTIONS (learned the hard way — see FIXES.md 2026-09-19):
//   * Use $app.dao().findFirstRecordByFilter / findRecordsByExpr.
//     findRecordsByFilter returns ZERO rows on this PocketBase build with no
//     error, which silently breaks anything built on it.
//   * Helper functions must be declared INSIDE the handler — file-scope
//     declarations are not visible to routerAdd callbacks.
//   * findFirstRecordByData THROWS (rather than returning null) when absent.
routerAdd("POST", "/api/admin/console", function(e) {
    // ── Inline helpers (file scope is NOT visible here) ──
    function ok(data) {
        var out = {ok: true};
        if (data) { for (var k in data) out[k] = data[k]; }
        return e.json(200, out);
    }
    function bad(status, message) { return e.json(status, {ok: false, message: message}); }
    function nowISO() { return new Date().toISOString(); }

    var CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
    var N = CHARSET.length;

    // Luhn-mod-N checksum over the full body INCLUDING the "RQ" prefix.
    // Must stay byte-identical to the client's luhn.go and the sibling hooks,
    // or generated codes fail validation on the device.
    function checksum(body) {
        var sum = 0, alt = false;
        for (var i = body.length - 1; i >= 0; i--) {
            var v = CHARSET.indexOf(body[i]);
            if (v < 0) return null;
            if (alt) { v = v * 2; if (v >= N) v = v - N + 1; }
            sum += v;
            alt = !alt;
        }
        return CHARSET[(N - (sum % N)) % N];
    }
    function formatCode(body) {
        // body = "RQ" + 12 chars -> RQ-XXXX-XXXX-XXXX-C
        return body.substring(0,2) + "-" + body.substring(2,6) + "-" +
               body.substring(6,10) + "-" + body.substring(10,14) + "-" +
               checksum(body);
    }
    function randomBody() {
        var s = "RQ";
        for (var i = 0; i < 12; i++) {
            s += CHARSET[Math.floor(Math.random() * N)];
        }
        return s;
    }
    function canonical(input, lengthOfBody) {
        // Accepts any pasted variant and returns the stored hyphenated form.
        var s = String(input || "").replace(/-/g, "").toUpperCase();
        if (s.length !== 15) return input;
        return s.substring(0,2) + "-" + s.substring(2,6) + "-" + s.substring(6,10) +
               "-" + s.substring(10,14) + "-" + s.substring(14,15);
    }
    function logEvent(code, event, detail, fingerprint) {
        try {
            var coll = $app.dao().findCollectionByNameOrId("code_events");
            var rec = new Record(coll);
            rec.set("code", code);
            rec.set("event", event);
            rec.set("detail", detail || "");
            rec.set("actor", "console");
            rec.set("fingerprint", fingerprint || "");
            $app.dao().saveRecord(rec);
        } catch (evErr) { /* audit is best-effort; never fail the action for it */ }
    }

    try {
        var body = $apis.requestInfo(e).data;
        var supplied = String(body.admin_token || "").trim();
        if (!supplied) {
            try { supplied = String(e.request().header.get("X-Admin-Token") || "").trim(); } catch (hErr) {}
        }
        var validToken = $os.getenv("ADMIN_API_TOKEN") || "";
        if (!validToken) return bad(500, "ADMIN_API_TOKEN is not configured on the server");
        if (supplied !== validToken) return bad(403, "Invalid admin token");

        var action = String(body.action || "");

        // ─────────────────────────────────────────────────────────────
        // dashboard: counts and recent activity
        // ─────────────────────────────────────────────────────────────
        if (action === "dashboard") {
            var allCodes = $app.dao().findRecordsByExpr("codes", $dbx.exp("id != ''"));
            var bound = 0, suspended = 0, expired = 0, available = 0;
            var byTier = {};
            var expiringSoon = 0;
            var now = Date.now();
            var in30 = now + (30 * 24 * 3600 * 1000);

            for (var i = 0; i < allCodes.length; i++) {
                var c = allCodes[i];
                var t = c.getString("tier") || "unknown";
                byTier[t] = (byTier[t] || 0) + 1;
                var isSusp = c.getBool("suspended");
                var exp = c.get("expires_at");
                var expMs = exp ? new Date(exp).getTime() : 0;
                var isExp = expMs && !isNaN(expMs) && expMs < now;
                if (isSusp) suspended++;
                else if (isExp) expired++;
                else if (c.getString("bound_fingerprint")) bound++;
                else available++;
                if (expMs && !isNaN(expMs) && expMs > now && expMs < in30) expiringSoon++;
            }

            var attempts = $app.dao().findRecordsByExpr("activation_attempts", $dbx.exp("id != ''"));
            var recent = $app.dao().findRecordsByExpr("code_events", $dbx.exp("id != ''"));

            // Newest first, capped — the console only shows a short feed.
            recent.sort(function(a, b) {
                return String(b.get("created") || "").localeCompare(String(a.get("created") || ""));
            });
            var feed = [];
            for (var r = 0; r < recent.length && r < 15; r++) {
                feed.push({
                    code: recent[r].getString("code"),
                    event: recent[r].getString("event"),
                    detail: recent[r].getString("detail"),
                    created: recent[r].getString("created"),
                });
            }

            return ok({
                codes: {
                    total: allCodes.length,
                    available: available,
                    bound: bound,
                    suspended: suspended,
                    expired: expired,
                    byTier: byTier,
                    expiringSoon: expiringSoon,
                },
                attempts: attempts.length,
                recent: feed,
            });
        }

        // ─────────────────────────────────────────────────────────────
        // codes.list: searchable/filterable listing
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.list") {
            var q = String(body.query || "").trim().toUpperCase().replace(/-/g, "");
            var tierFilter = String(body.tier || "").trim();
            var statusFilter = String(body.status || "").trim();
            var mmFilter = String(body.middleman || "").trim().toLowerCase();

            var recs = $app.dao().findRecordsByExpr("codes", $dbx.exp("id != ''"));
            var nowMs = Date.now();
            var out = [];
            for (var j = 0; j < recs.length; j++) {
                var rec = recs[j];
                var codeVal = rec.getString("code");
                var fp = rec.getString("bound_fingerprint");
                var expRaw = rec.get("expires_at");
                var expMs2 = expRaw ? new Date(expRaw).getTime() : 0;
                var isExp2 = expMs2 && !isNaN(expMs2) && expMs2 < nowMs;
                var susp2 = rec.getBool("suspended");

                var status = susp2 ? "suspended"
                           : isExp2 ? "expired"
                           : fp ? "bound"
                           : "available";

                if (q && codeVal.toUpperCase().replace(/-/g, "").indexOf(q) === -1) continue;
                if (tierFilter && rec.getString("tier") !== tierFilter) continue;
                if (statusFilter && status !== statusFilter) continue;
                if (mmFilter && String(rec.getString("middleman") || "").toLowerCase().indexOf(mmFilter) === -1) continue;

                out.push({
                    id: rec.id,
                    code: codeVal,
                    tier: rec.getString("tier"),
                    status: status,
                    bound: !!fp,
                    fingerprint: fp ? fp.substring(0, 12) + "…" : "",
                    suspended: susp2,
                    expires_at: expRaw ? String(expRaw) : "",
                    activated_at: rec.getString("activated_at") || "",
                    middleman: rec.getString("middleman") || "",
                    label: rec.getString("label") || "",
                    notes: rec.getString("notes") || "",
                });
            }
            // Most recent first.
            out.sort(function(a, b) { return (b.activated_at || "").localeCompare(a.activated_at || ""); });
            return ok({total: out.length, codes: out});
        }

        // ─────────────────────────────────────────────────────────────
        // codes.generate
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.generate") {
            var count = parseInt(body.count || "1", 10);
            if (isNaN(count) || count < 1 || count > 500) return bad(400, "count must be between 1 and 500");
            var tier = String(body.tier || "").trim();
            if (!tier) return bad(400, "tier is required");
            var expires = String(body.expires_at || "").trim();
            var middleman = String(body.middleman || "").trim();
            var label = String(body.label || "").trim();
            var notes = String(body.notes || "").trim();

            var collC = $app.dao().findCollectionByNameOrId("codes");
            var created = [], skipped = 0;
            for (var k = 0; k < count; k++) {
                var made = null;
                // Retry on the (astronomically unlikely) collision or any
                // uniqueness error, rather than aborting the whole batch.
                for (var attempt = 0; attempt < 5 && !made; attempt++) {
                    var codeStr = formatCode(randomBody());
                    var exists = null;
                    try { exists = $app.dao().findFirstRecordByData("codes", "code", codeStr); } catch (nf) { exists = null; }
                    if (exists) continue;
                    try {
                        var nr = new Record(collC);
                        nr.set("code", codeStr);
                        nr.set("tier", tier);
                        nr.set("used", false);
                        nr.set("suspended", false);
                        nr.set("bound_fingerprint", "");
                        nr.set("middleman", middleman);
                        nr.set("label", label);
                        nr.set("notes", notes);
                        if (expires) nr.set("expires_at", expires);
                        $app.dao().saveRecord(nr);
                        made = codeStr;
                        created.push(codeStr);
                    } catch (saveErr) {
                        skipped++;
                    }
                }
                if (!made) skipped++;
            }
            logEvent(created.length ? created[0] : "(batch)", "generated",
                     created.length + " x " + tier + (middleman ? " for " + middleman : ""), "");
            return ok({created: created, skipped: skipped, tier: tier});
        }

        // ─────────────────────────────────────────────────────────────
        // codes.suspend / codes.unsuspend
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.suspend" || action === "codes.unsuspend") {
            var target = canonical(body.code);
            if (!target) return bad(400, "code is required");
            var recS = null;
            try { recS = $app.dao().findFirstRecordByData("codes", "code", target); } catch (nf2) { recS = null; }
            if (!recS) return bad(404, "Code not found");
            var wantSuspend = action === "codes.suspend";
            recS.set("suspended", wantSuspend);
            $app.dao().saveRecord(recS);
            logEvent(target, wantSuspend ? "suspended" : "unsuspended",
                     String(body.reason || ""), "");
            return ok({code: target, suspended: wantSuspend});
        }

        // ─────────────────────────────────────────────────────────────
        // codes.unbind — release the device binding
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.unbind") {
            var targetU = canonical(body.code);
            if (!targetU) return bad(400, "code is required");
            var recU = null;
            try { recU = $app.dao().findFirstRecordByData("codes", "code", targetU); } catch (nf3) { recU = null; }
            if (!recU) return bad(404, "Code not found");
            var oldFp = recU.getString("bound_fingerprint");
            if (!oldFp) return bad(400, "That code is not bound to a device");
            var reason = String(body.reason || "Unbound from console").trim();
            recU.set("bound_fingerprint", "");
            recU.set("activated_at", null);
            // These two columns previously did not exist, so the audit trail
            // recorded nothing. They are part of the schema now.
            recU.set("unbound_at", nowISO());
            recU.set("unbind_reason", reason);
            $app.dao().saveRecord(recU);
            logEvent(targetU, "unbound", reason, oldFp.substring(0, 12));
            return ok({code: targetU, previous_fingerprint: oldFp.substring(0, 12) + "…"});
        }

        // ─────────────────────────────────────────────────────────────
        // codes.expire — set or clear an expiry
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.expire") {
            var targetE = canonical(body.code);
            if (!targetE) return bad(400, "code is required");
            var recE = null;
            try { recE = $app.dao().findFirstRecordByData("codes", "code", targetE); } catch (nf4) { recE = null; }
            if (!recE) return bad(404, "Code not found");
            var when = String(body.expires_at || "").trim();
            recE.set("expires_at", when ? when : null);
            $app.dao().saveRecord(recE);
            logEvent(targetE, "expiry-changed", when || "(cleared)", "");
            return ok({code: targetE, expires_at: when});
        }

        // ─────────────────────────────────────────────────────────────
        // codes.update — label / notes / middleman
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.update") {
            var targetUp = canonical(body.code);
            if (!targetUp) return bad(400, "code is required");
            var recUp = null;
            try { recUp = $app.dao().findFirstRecordByData("codes", "code", targetUp); } catch (nf5) { recUp = null; }
            if (!recUp) return bad(404, "Code not found");
            if (body.middleman !== undefined) recUp.set("middleman", String(body.middleman || "").trim());
            if (body.label !== undefined) recUp.set("label", String(body.label || "").trim());
            if (body.notes !== undefined) recUp.set("notes", String(body.notes || "").trim());
            $app.dao().saveRecord(recUp);
            logEvent(targetUp, "updated", "details changed", "");
            return ok({code: targetUp});
        }

        // ─────────────────────────────────────────────────────────────
        // codes.history — per-code event trail
        // ─────────────────────────────────────────────────────────────
        if (action === "codes.history") {
            var targetH = canonical(body.code);
            if (!targetH) return bad(400, "code is required");
            var evs = $app.dao().findRecordsByExpr("code_events",
                $dbx.exp("code = {:c}", { c: targetH }));
            evs.sort(function(a, b) {
                return String(b.get("created") || "").localeCompare(String(a.get("created") || ""));
            });
            var trail = [];
            for (var m = 0; m < evs.length; m++) {
                trail.push({
                    event: evs[m].getString("event"),
                    detail: evs[m].getString("detail"),
                    actor: evs[m].getString("actor"),
                    created: evs[m].getString("created"),
                });
            }
            return ok({code: targetH, events: trail});
        }

        // ─────────────────────────────────────────────────────────────
        // middlemen.list — distinct labels currently in use
        // ─────────────────────────────────────────────────────────────
        if (action === "middlemen.list") {
            var all = $app.dao().findRecordsByExpr("codes", $dbx.exp("id != ''"));
            var seen = {};
            for (var n = 0; n < all.length; n++) {
                var mm = String(all[n].getString("middleman") || "").trim();
                if (mm) seen[mm] = (seen[mm] || 0) + 1;
            }
            var list = [];
            for (var key in seen) list.push({name: key, codes: seen[key]});
            list.sort(function(a, b) { return b.codes - a.codes; });
            return ok({middlemen: list});
        }

        // ─────────────────────────────────────────────────────────────
        // tiers.list / tiers.update — connection settings
        // ─────────────────────────────────────────────────────────────
        if (action === "tiers.list") {
            var tc = $app.dao().findRecordsByExpr("tier_configs", $dbx.exp("id != ''"));
            var tiers = [];
            for (var p = 0; p < tc.length; p++) {
                var cfg = {};
                try { cfg = JSON.parse(tc[p].getString("config")); } catch (parseErr) { cfg = {}; }
                tiers.push({
                    id: tc[p].id,
                    tier: tc[p].getString("tier"),
                    active: tc[p].getBool("active"),
                    udp_relay: tc[p].getBool("udp_relay"),
                    server: cfg.server || "",
                    server_port: cfg.server_port || 0,
                    method: cfg.method || "",
                });
            }
            return ok({tiers: tiers});
        }

        // ─────────────────────────────────────────────────────────────
        // tiers.update — change port/method/active/udp_relay
        // NOTE: the password is deliberately NOT editable here. Changing it
        // silently invalidates every issued client. That stays a deliberate,
        // documented operation (see OPS.md), not a stray click.
        // ─────────────────────────────────────────────────────────────
        if (action === "tiers.update") {
            var tierName = String(body.tier || "").trim();
            if (!tierName) return bad(400, "tier is required");
            var recT = null;
            try { recT = $app.dao().findFirstRecordByData("tier_configs", "tier", tierName); } catch (nf6) { recT = null; }
            if (!recT) return bad(404, "Tier not found");
            var cfgT = {};
            try { cfgT = JSON.parse(recT.getString("config")); } catch (parseErr2) { cfgT = {}; }
            if (body.server !== undefined && String(body.server).trim()) cfgT.server = String(body.server).trim();
            if (body.server_port !== undefined) {
                var port = parseInt(body.server_port, 10);
                if (isNaN(port) || port < 1 || port > 65535) return bad(400, "server_port must be 1-65535");
                cfgT.server_port = port;
            }
            if (body.method !== undefined && String(body.method).trim()) cfgT.method = String(body.method).trim();
            recT.set("config", JSON.stringify(cfgT));
            if (body.active !== undefined) recT.set("active", !!body.active);
            if (body.udp_relay !== undefined) recT.set("udp_relay", !!body.udp_relay);
            $app.dao().saveRecord(recT);
            logEvent("(tier " + tierName + ")", "tier-updated", JSON.stringify(cfgT), "");
            return ok({tier: tierName, config: cfgT});
        }

        // ─────────────────────────────────────────────────────────────
        // releases.get — current update_config, plus what is on disk
        // ─────────────────────────────────────────────────────────────
        if (action === "releases.get") {
            var rel = null;
            try { rel = $app.dao().findFirstRecordByFilter("update_config", "id != ''"); } catch (nf7) { rel = null; }
            var payload = {version: "", rollout_percent: 0, active: false, platforms: {}};
            if (rel) {
                payload.version = rel.getString("version");
                payload.rollout_percent = parseInt(rel.get("rollout_percent") || "0", 10);
                payload.active = rel.getBool("active");
                var platKeys = ["linux", "windows", "macos_intel", "macos_arm"];
                for (var pi = 0; pi < platKeys.length; pi++) {
                    var pk = platKeys[pi];
                    payload.platforms[pk] = {
                        url: rel.getString("download_" + pk) || "",
                        sha256: rel.getString("sha256_" + pk) || "",
                    };
                }
            }
            return ok({release: payload});
        }

        // ─────────────────────────────────────────────────────────────
        // releases.set — point clients at a version / change rollout
        // ─────────────────────────────────────────────────────────────
        if (action === "releases.set") {
            var version = String(body.version || "").trim().replace(/^v/, "");
            if (!version) return bad(400, "version is required");
            if (!/^[0-9]+\.[0-9]+\.[0-9]+/.test(version)) {
                return bad(400, "version must look like 1.2.3");
            }
            var rollout = body.rollout_percent === undefined ? 0 : parseInt(body.rollout_percent, 10);
            if (isNaN(rollout) || rollout < 0 || rollout > 100) return bad(400, "rollout_percent must be 0-100");

            var recR = null;
            try { recR = $app.dao().findFirstRecordByFilter("update_config", "id != ''"); } catch (nf8) { recR = null; }
            if (!recR) {
                var collR = $app.dao().findCollectionByNameOrId("update_config");
                recR = new Record(collR);
            }

            // ── Guard: never advertise a version we cannot serve ──
            //
            // The heartbeat only emits update fields when rollout_percent > 0,
            // and it sends whatever URLs sit in this row. Those URLs are NOT
            // derived from what is on disk — they are written by
            // publish-release.sh (which verifies the served bytes) or by this
            // action. So raising the rollout at a moment when the URLs are
            // empty or stale sends every eligible client to a 404, and a client
            // that cannot download cannot update: the release silently fails
            // for the whole fleet, with no error anywhere but the client log.
            //
            // This has actually happened on this hub: an old row pointed at
            // /updates/1.0.2/* after those test artifacts were deleted.
            //
            // We cannot stat the filesystem from a PocketBase hook (only
            // $os.getenv is exposed), so the check we CAN make is that every
            // platform's URL and hash are present and that the URL version
            // matches the version being advertised.
            if (rollout > 0) {
                var platKeysG = ["linux", "windows", "macos_intel", "macos_arm"];
                var missingG = [];
                for (var gi = 0; gi < platKeysG.length; gi++) {
                    var gk = platKeysG[gi];
                    var gurl = recR.getString("download_" + gk);
                    var gsha = recR.getString("sha256_" + gk);
                    if (!gurl || !gsha) { missingG.push(gk); continue; }
                    // The URL must point at the version being advertised, or a
                    // stale row (e.g. URLs for 1.0.2 while claiming 1.0.0) will
                    // be served as if it were current.
                    if (gurl.indexOf("/updates/" + version + "/") === -1) {
                        missingG.push(gk + " (url is for a different version)");
                    }
                }
                if (missingG.length) {
                    return bad(400,
                        "refusing to offer " + version + " at " + rollout + "% — no usable artifact for: " +
                        missingG.join(", ") +
                        ". Upload all four raw binaries and publish the release first, " +
                        "or clients will be sent to a 404.");
                }
            }

            recR.set("version", version);
            recR.set("rollout_percent", rollout);
            recR.set("active", body.active === undefined ? true : !!body.active);
            $app.dao().saveRecord(recR);
            logEvent("(release " + version + ")", "release-set",
                     "rollout=" + rollout + "% active=" + recR.getBool("active"), "");
            return ok({version: version, rollout_percent: rollout});
        }

        return bad(400, "Unknown action: " + action);
    } catch (err) {
        return e.json(500, {ok: false, message: (err && err.message) ? err.message : String(err)});
    }
});
