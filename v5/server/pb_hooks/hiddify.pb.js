// MyVPN Hiddify Links Hook — PocketBase 0.22 compatible
// Generates Hiddify-compatible ss:// subscription links for testing
// without requiring the dedicated MyVPN client.
//
// Endpoints:
//   GET /api/hiddify?code=CODE           — returns ss:// link for the code's tier
//   GET /api/hiddify?code=CODE&all=1     — returns ss:// links for ALL active tiers
//   GET /api/hiddify?code=CODE&sub=1     — returns a Hiddify subscription (base64 list)
//
// Usage from any HTTP client (curl, browser, Hiddify import):
//   curl "https://networkingguides.duckdns.org/api/hiddify?code=RQ-XXXX-XXXX-XXXX-X"

function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    return val.replace(/\\/g, "").replace(/'/g, "");
}

routerAdd("GET", "/api/hiddify", function(e) {
    try {
        var req = e.request();
        var code = req.url.query().get("code") || "";
        var allTiers = req.url.query().get("all") === "1";
        var subFormat = req.url.query().get("sub") === "1";

        if (!code) return e.json(400, {code: 400, message: "Missing code parameter"});

        // ── Look up the code (canonical RQ-XXXX-XXXX-XXXX-C form, tolerating
        // any pasted variant, same as the activation/heartbeat hooks) ──
        var s = code.replace(/-/g,"").toUpperCase();
        var canonical = (s.length === 15)
            ? s.substring(0,2)+"-"+s.substring(2,6)+"-"+s.substring(6,10)+"-"+s.substring(10,14)+"-"+s.substring(14,15)
            : code;
        var safeCode = sanitizeFilter(canonical);
        var records = $app.findRecordsByFilter(
            "codes",
            "code={:code}",
            "", 0, 1,
            { code: safeCode }
        );
        if (records.length === 0) return e.json(404, {code: 404, message: "Code not found"});
        var record = records[0];

        // ── Check suspension & expiry ──
        if (record.getBool("suspended")) return e.json(403, {code: 403, message: "Code suspended"});
        var exp = record.get("expires_at");
        if (exp) { var ed = new Date(exp).getTime(); if (!isNaN(ed) && ed < Date.now()) return e.json(410, {code: 410, message: "Code expired"}); }

        var userTier = record.getString("tier");

        // ── Resolve server domain ──
        var allCfgs = $app.findRecordsByFilter("tier_configs", "active={:active}", "", 0, 0, { active: true });
        var domain = "";
        if (allCfgs.length > 0) {
            try {
                var sampleCfg = JSON.parse(allCfgs[0].get("config"));
                if (sampleCfg.server && sampleCfg.server !== "0.0.0.0") domain = sampleCfg.server;
            } catch(ex) {}
        }
        if (!domain) {
            domain = req.url.host().split(":")[0];
            if (!domain || domain.match(/^\d+\.\d+\.\d+\.\d+$/)) domain = "networkingguides.duckdns.org";
        }

        // ── Collect configs ──
        var configs = [];
        if (allTiers) {
            configs = allCfgs;
        } else {
            var tierCfgs = $app.findRecordsByFilter(
                "tier_configs",
                "tier={:tier} && active={:active}",
                "", 0, 1,
                { tier: sanitizeFilter(userTier), active: true }
            );
            if (tierCfgs.length > 0) configs = [tierCfgs[0]];
        }

        // ── Build ss:// links ──
        var links = [];
        for (var i = 0; i < configs.length; i++) {
            var cfg;
            try { cfg = JSON.parse(configs[i].get("config")); } catch(ex) { cfg = configs[i].get("config"); }
            if (typeof cfg === "string") { try { cfg = JSON.parse(cfg); } catch(ex) { continue; } }
            if (!cfg || !cfg.password || !cfg.method || !cfg.server_port) continue;

            var tierName = configs[i].get("tier") || "unknown";
            var server = domain;
            var port = cfg.server_port;
            var method = cfg.method;
            var password = cfg.password;
            var udp = configs[i].get("udp_relay") || false;

            var userInfo = method + ":" + password;
            var encoded = btoa(userInfo);
            var tag = tierName.charAt(0).toUpperCase() + tierName.slice(1) + " - MyVPN";
            var ssLink = "ss://" + encoded + "@" + server + ":" + port + "#" + encodeURIComponent(tag);

            var sipObj = { method: method, password: password, server: server, server_port: port };
            if (udp) sipObj.mode = "tcp_and_udp";
            var sipEncoded = btoa(JSON.stringify(sipObj));
            var sipLink = "ss://" + sipEncoded + "@" + server + ":" + port + "#" + encodeURIComponent(tag);

            links.push({
                tier: tierName,
                remark: tag,
                ss_link: ssLink,
                sip008_link: sipLink,
                server: server,
                server_port: port,
                method: method,
                udp_relay: udp
            });
        }

        if (links.length === 0) return e.json(404, {code: 404, message: "No active tier configs found"});

        // ── Subscription format (base64-encoded list of proxy URLs for Hiddify import) ──
        if (subFormat) {
            var proxyLines = [];
            for (var j = 0; j < links.length; j++) proxyLines.push(links[j].ss_link);
            var subContent = proxyLines.join("\n");
            var subB64 = btoa(subContent);
            e.response().header().set("Content-Type", "text/plain; charset=utf-8");
            e.response().header().set("Subscription-Userinfo", "download=0; upload=0; total=0; expire=0");
            return e.json(200, {
                code: 200,
                subscription_base64: subB64,
                subscription_plain: subContent,
                links: links,
                _note: "Import the subscription_base64 or subscription_plain into Hiddify/Sing-box/Shadowrocket"
            });
        }

        // ── Standard JSON response ──
        var response = { code: 200, message: "Success", tier: userTier, links: links };
        if (links.length === 1) {
            response.ss_link = links[0].ss_link;
            response.sip008_link = links[0].sip008_link;
        }
        return e.json(200, response);
    } catch(err) {
        return e.json(500, {code: 500, message: err.message || String(err)});
    }
});
