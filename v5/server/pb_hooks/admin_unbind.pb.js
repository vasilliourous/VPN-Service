// MyVPN Admin Unbind Hook — PocketBase 0.22 compatible
routerAdd("POST", "/api/admin/unbind-code", function(e) {
    try {
        var body = $apis.requestInfo(e).data;
        var adminToken = (body.admin_token || "").trim();
        var code = (body.code || "").trim();
        var reason = (body.reason || "Requested by admin").trim();

        // ADMIN_API_TOKEN must be set in /etc/environment on the VPS
        var validToken = $os.getenv("ADMIN_API_TOKEN") || "";
        if (!validToken) return e.json(500, {code:500, message:"ADMIN_API_TOKEN not configured"});
        if (adminToken !== validToken) return e.json(403, {code:403, message:"Invalid admin token"});
        if (!code) return e.json(400, {code:400, message:"Missing code"});

        var record = $app.dao().findFirstRecordByData("codes", "code", code);
        if (!record) return e.json(404, {code:404, message:"Code not found"});

        var boundFp = record.getString("bound_fingerprint");
        if (!boundFp) return e.json(400, {code:400, message:"Code is not bound to any device"});

        // Log the unbind for audit
        var auditFp = boundFp.substring(0, 8) + "****";
        $app.logger().info("Admin unbind: code=" + code.substring(0,4) + "****, old_fingerprint=" + auditFp + ", reason=" + reason);

        // Clear the binding
        record.set("bound_fingerprint", "");
        record.set("activated_at", null);
        record.set("unbound_at", new Date().toISOString());
        record.set("unbind_reason", reason);
        $app.dao().saveRecord(record);

        return e.json(200, {
            code: 200,
            message: "Code unbound successfully.",
            tier: record.getString("tier"),
            middleman: record.getString("middleman") || ""
        });
    } catch(err) {
        return e.json(500, {code:500, message:err.message || String(err)});
    }
});
