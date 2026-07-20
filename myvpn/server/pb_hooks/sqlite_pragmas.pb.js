// Set SQLite WAL mode and busy timeout on PocketBase startup
// Prevents write contention under concurrent heartbeat + activation queries
onBeforeServe(() => {
    const db = $app.dao().db();
    db.exec("PRAGMA journal_mode=WAL;");
    db.exec("PRAGMA busy_timeout=5000;");
    console.log("SQLite pragmas set: WAL + busy_timeout=5000");
});
