// Locus update manifest — the endpoint the desktop client's updater polls.
//
// WHY THIS IS NOT /api/release
//
// /api/release serves the *static* shape: one document listing every platform's
// URL and hash. It is meant for humans, tooling and a last-resort fallback, and
// it deliberately carries no per-device decision.
//
// This endpoint serves one *device*: it answers "what should THIS client
// install?", for a version and a platform given in the query string, in the
// shape the Tauri updater plugin parses:
//
//     { "version": "2.3.0", "url": "https://…", "signature": "…" }
//
// The client points the plugin at this URL at runtime
// (UpdaterBuilder::endpoints), so no static manifest is ever consulted and
// tauri.conf.json needs no endpoints at all.
//
// NO CREDENTIALS — DELIBERATELY
//
// This route is unauthenticated, and that is the point. It is reached by the
// plugin's own HTTP client, which sends no activation code, and more
// importantly it must work for exactly the clients that cannot authenticate:
//
//   - a build broken badly enough that activation fails,
//   - a device that has never activated,
//   - a code that is suspended or expired.
//
// In all three the heartbeat is unreachable, so if updating required the
// heartbeat those clients could never be given the fix that resolves their
// problem — the failure would prevent escaping the failure. (This is the same
// deadlock /api/release was created for; see FIXES.md.)
//
// Because it is unauthenticated it exposes nothing that is not already public:
// only the version, and the download URL that /updates/<version>/ serves openly
// to anyone. No codes, no fingerprints, no fleet counts.
//
// PROTOCOL CONTRACT — 204 vs 200
//
// The Tauri plugin treats **HTTP 204 No Content** as "no update available" and
// returns cleanly. A 200 whose body it cannot parse is raised as an ERROR and
// logged. So "you are up to date" must be 204, not 200-with-an-empty-object —
// returning 200 with no release makes every up-to-date client log a failure on
// every check, which is the noise that hides a real fault.
//
// The `version` and `platform` query parameters are REQUIRED. A request without
// them cannot be answered correctly (we would have to guess the platform, and
// guessing is how a Windows client is handed a Linux binary), so it is refused
// rather than guessed at.

routerAdd("GET", "/api/update", function(e) {
    // Inline helpers only: file-scope declarations are not visible inside
    // routerAdd callbacks in this PocketBase build (see FIXES.md 2026-09-19).
    function field(rec, name) {
        try { var v = rec.get(name); return (v === null || v === undefined) ? "" : String(v); } catch (err) { return ""; }
    }

    // Splits a version into numeric segments and a pre-release tag, so
    // "1.10.0" > "1.9.0" (string comparison gets that backwards) and
    // "2.0.0" > "2.0.0-rc1".
    //
    // This mirrors locus::update::version on the client. Both exist because the
    // hub must not advertise a downgrade even if update_config is stale, and the
    // client must not accept one even if the hub is wrong. Two independent
    // checks on the same rule is the point, not duplication.
    function parseVersion(raw) {
        var s = String(raw || "").trim().replace(/^[vV]/, "");
        // Build metadata is ignored for ordering, and must be stripped before
        // the pre-release split or "1.0.0+build5" would rank below "1.0.0".
        var plus = s.indexOf("+");
        if (plus >= 0) s = s.substring(0, plus);
        var pre = "";
        var dash = s.indexOf("-");
        if (dash >= 0) { pre = s.substring(dash + 1); s = s.substring(0, dash); }

        var nums = [];
        var parts = s.split(".");
        for (var i = 0; i < parts.length; i++) {
            var n = parseInt(parts[i], 10);
            if (isNaN(n)) break;
            nums.push(n);
        }
        return { nums: nums, pre: pre };
    }

    // Returns true only when `candidate` is STRICTLY newer than `current`.
    //
    // Refusing anything that is not strictly newer makes a stale or rolled-back
    // update_config row harmless. There is no server-driven downgrade in this
    // system, so a mistaken advertisement would otherwise downgrade the fleet.
    function isNewer(candidate, current) {
        if (!candidate || !current) return false;
        var a = parseVersion(candidate);
        var b = parseVersion(current);
        var width = Math.max(a.nums.length, b.nums.length);
        for (var i = 0; i < width; i++) {
            var av = i < a.nums.length ? a.nums[i] : 0;
            var bv = i < b.nums.length ? b.nums[i] : 0;
            if (av !== bv) return av > bv;
        }
        // Equal numbers: a release outranks a pre-release of the same version.
        if (a.pre === b.pre) return false;
        if (a.pre === "") return true;
        if (b.pre === "") return false;
        return a.pre > b.pre;
    }

    // 204 is a clean "nothing to install" for the Tauri plugin. Returning 200
    // with an empty body would be parsed as an error and logged on every check.
    function noUpdate() {
        try { e.response().header().set("Cache-Control", "no-store"); } catch (hErr) {}
        e.response().writeHeader(204);
        return e.response();
    }

    try {
        var query = e.request().url.query();
        var runningVersion = String(query.get("version") || "").trim().replace(/^[vV]/, "");
        var platform = String(query.get("platform") || "").trim();

        // Refuse rather than guess. A missing platform cannot be answered
        // safely: choosing one for the caller is how a Windows client receives
        // a Linux binary and fails its checksum.
        if (!runningVersion || !platform) {
            return e.json(400, {
                ok: false,
                message: "version and platform query parameters are required",
            });
        }

        var allowed = ["linux", "windows", "macos_intel", "macos_arm"];
        if (allowed.indexOf(platform) === -1) {
            return e.json(400, { ok: false, message: "unknown platform: " + platform });
        }

        var u = null;
        try {
            // findFirstRecordByFilter, NOT findRecordsByFilter — the list
            // variant silently returns zero rows on this PocketBase build, with
            // no error, which would make this endpoint a permanent no-op.
            u = $app.dao().findFirstRecordByFilter("update_config", "active = true");
        } catch (noRow) {
            u = null;
        }

        if (!u) return noUpdate();

        var version = field(u, "version");
        if (!version) return noUpdate();

        // The no-downgrade gate. Runs before anything else so a stale row can
        // never reach the client.
        if (!isNewer(version, runningVersion)) return noUpdate();

        var url = field(u, "download_" + platform);
        var sha256 = field(u, "sha256_" + platform);
        var signature = field(u, "signature_" + platform);

        // Every one of these is required, and a partial offer is treated as no
        // offer at all rather than as a broken one.
        //
        // The Tauri plugin verifies the signature MANDATORILY and has no bypass,
        // so an unsigned artifact cannot be installed. Advertising it would make
        // the client download tens of megabytes only to fail verification. And a
        // missing checksum is refused for the same reason it is on the client:
        // an artifact that cannot be verified must never be fetched.
        if (!url || !sha256 || !signature) {
            // Log loudly. This is an operator error — a row was published
            // without signing, or before signing existed — and the symptom
            // otherwise is "updates silently never arrive", which is the
            // hardest class of fault to diagnose.
            console.log("[locus] /api/update: refusing to advertise " + version +
                        " for " + platform + " — missing " +
                        (url ? "" : "url ") + (sha256 ? "" : "sha256 ") +
                        (signature ? "" : "signature ") + "(publish it with a signature)");
            return noUpdate();
        }

        // The shape the plugin's `check()` parses: a dynamic release object.
        //
        // `portable`/`notes` are omitted: the plugin does not require them, and
        // the in-app prompt shows the version and links to the hub instead.
        var out = {
            version: version,
            url: url,
            signature: signature,
        };

        // Short cache so a struggling network does not hammer the hub, but not
        // for long: an operator who stops offering an update expects it to stop
        // promptly.
        try { e.response().header().set("Cache-Control", "public, max-age=120"); } catch (hErr) {}

        // sha256 is returned alongside even though the plugin ignores it: the
        // client records it, and it makes the response self-describing for
        // anyone debugging with curl.
        out.sha256 = sha256;
        return e.json(200, out);
    } catch (err) {
        // A 5xx is a transport-shaped failure to the client, which means "could
        // not ask" rather than "no update" — and those must stay distinguishable.
        return e.json(500, { ok: false, message: (err && err.message) ? err.message : String(err) });
    }
});
