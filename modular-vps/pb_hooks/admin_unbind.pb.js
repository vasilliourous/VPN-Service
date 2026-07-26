// MyVPN Admin Unbind Hook (PocketBase 0.22+)
// Allows an admin to clear a device fingerprint from a code,
// enabling the code to be reactivated on a new device.
// Requires a valid admin API token.
//
// PocketBase 0.22 JSVM notes:
//   $app.dao() → $app (direct)
//   $apis.requestInfo(c) → e.requestInfo()
//   $app.dao().saveRecord() → $app.save()
//   c.json() → e.json()

function sanitizeFilter(val) {
    if (typeof val !== "string") return "";
    return val.replace(/\\/g, "").replace(/'/g, "");
}

routerAdd("POST", "/api/admin/unbind-code", (e) => {
    const body = e.requestInfo().body;
    const adminToken = body.admin_token || "";
    const code = (body.code || "").trim();
    const reason = (body.reason || "Requested by admin").trim();

    // ── Verify admin token ──
    const validToken = $os.getenv("ADMIN_API_TOKEN") || "change-me-in-production";
    if (adminToken !== validToken) {
        return e.json(403, { code: 403, message: "Invalid admin token" });
    }

    if (!code) {
        return e.json(400, { code: 400, message: "Missing code" });
    }

    // ── Look up the code (parameterized query) ──
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

    // ── Check if code is actually bound ──
    const boundFp = record.getString("bound_fingerprint");
    if (!boundFp) {
        return e.json(400, { code: 400, message: "Code is not bound to any device" });
    }

    // ── Log the unbind for audit ──
    const auditFp = boundFp.substring(0, 8) + "****";
    $app.logger().info(
        `Admin unbind: code=${safeCode.substring(0, 4)}****, ` +
        `old_fingerprint=${auditFp}, reason=${reason}`
    );

    // ── Clear the binding ──
    record.set("bound_fingerprint", "");
    record.set("device_fingerprint_hash", "");
    record.set("activated_at", null);
    record.set("unbound_at", new Date().toISOString());
    record.set("unbind_reason", reason);
    $app.save(record);

    return e.json(200, {
        code: 200,
        message: "Code unbound successfully. Can now be activated on a new device.",
        tier: record.getString("tier"),
        middleman: record.getString("middleman") || ""
    });
});
