// Public release manifest — the update path that does NOT require a code.
//
// WHY THIS EXISTS: every other way of learning about a new build runs through
// POST /api/heartbeat, which requires a bound activation code. That is fine
// when the client works. It is useless in exactly the situation where an update
// matters most:
//
//   - the installed build is bad enough that activation fails, or
//   - the device has never activated, or
//   - the code is suspended/expired and the operator's fix is "ship a new build".
//
// In all three the client cannot reach the heartbeat, so it can never be told
// that a fix exists — a deadlock the failure mode itself prevents escaping.
//
// This route answers one question with no credentials: "what is the current
// public release, and where do I get it?" It is deliberately NOT a replacement
// for the heartbeat:
//
//   - It carries no per-device decision. There is no rollout percentage
//     anywhere any more (removed 2026-09), so this is no longer a difference
//     from the heartbeat — but it remains the credential-free path, which is
//     why it exists.
//   - It exposes NO per-device, per-code or fleet data. Only the published
//     version and its public download URLs, which are already served openly
//     from /updates/* anyway.
//   - It is rate-limited by Caddy like the other public endpoints.
routerAdd("GET", "/api/release", function(e) {
    // Inline helpers only: file-scope declarations are not visible inside
    // routerAdd callbacks in this PocketBase build (see FIXES.md 2026-09-19).
    function field(rec, name) {
        try { var v = rec.get(name); return (v === null || v === undefined) ? "" : String(v); } catch (err) { return ""; }
    }

    try {
        var u = null;
        try {
            // findFirstRecordByFilter, NOT findRecordsByFilter — the list
            // variant silently returns zero rows on this PocketBase build.
            u = $app.dao().findFirstRecordByFilter("update_config", "active = true");
        } catch (noRow) {
            u = null;
        }

        if (!u) {
            // No release is published. This is a normal state (nothing has been
            // shipped yet) and must be distinguishable from an error, so it
            // returns 200 with an explicit empty marker rather than 404.
            return e.json(200, { ok: true, published: false });
        }

        var out = {
            ok: true,
            published: true,
            version: field(u, "version"),
            platforms: {},
        };
        var keys = ["linux", "windows", "macos_intel", "macos_arm"];
        for (var i = 0; i < keys.length; i++) {
            var k = keys[i];
            var url = field(u, "download_" + k);
            if (!url) continue;
            // signature is included because the client's updater verifies one
            // mandatorily: a platform present here without a signature is an
            // artifact nobody can install, and the client needs to be able to
            // tell that apart from "no release at all".
            out.platforms[k] = {
                url: url,
                sha256: field(u, "sha256_" + k),
                signature: field(u, "signature_" + k),
            };
        }
        // rollout_percent is intentionally NOT returned: it is fleet policy,
        // and this endpoint is unauthenticated.

        // Short cache: the manifest changes only on publish, and a cached
        // response keeps a struggling network from hammering the hub.
        try { e.response().header().set("Cache-Control", "public, max-age=300"); } catch (hErr) {}
        return e.json(200, out);
    } catch (err) {
        return e.json(500, { ok: false, message: (err && err.message) ? err.message : String(err) });
    }
});
