// Rate limit activation attempts per IP
const ATTEMPT_LIMIT = 5;
const WINDOW_MINUTES = 10;

routerAdd("POST", "/api/collections/activations/records", (c) => {
    const ip = c.requestHeader("X-Forwarded-For") || c.remoteIP();

    const recent = $app.dao().findRecordsByFilter("activation_attempts",
        `ip="${ip}" && created > "${new Date(Date.now() - WINDOW_MINUTES * 60000).toISOString()}"`);

    if (recent.length >= ATTEMPT_LIMIT) {
        return c.json(429, { "code": 429, "message": "Too many attempts. Try again later." });
    }

    $app.dao().saveRecord($app.dao().createRecord("activation_attempts", {
        ip: ip,
        created: new Date().toISOString()
    }));

    return c.next();
});
