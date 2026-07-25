# MyVPN Code Quality Issues & Applied Fixes

> This document catalogs every issue discovered in the V4 codebase and
> server modules during audit and testing — along with applied fixes.
>
> **Status:** Issues marked ✅ are fixed in V5.  
> Issues marked 🟡 are documented but NOT yet fixed (require code changes).

---

## SERVER-SIDE FIXES (modular-vps → v5/server/)

These fixes were discovered during real-world VPS testing on a Voyager VPS.
All are applied in `v5/server/`.

### 1. tcp-brutal Repository URL Changed

| Severity | 🔴 Would fail on fresh deploy |
|----------|-------------------------------|
| **Issue:** | The old repo URL `apernet/tcp-brutal-ng` returns 404 — the repo was renamed to `apernet/tcp-brutal` |
| **Fix:** | Updated all URLs to `https://github.com/apernet/tcp-brutal.git` |
| **File:** | `server/modules/03-brutal.sh` |

### 2. LD_PRELOAD Wrapper Missing from New Repo

| Severity | 🔴 Brutal CC wouldn't activate |
|----------|-------------------------------|
| **Issue:** | The old `tcp-brutal-ng` repo included `brutal-wrap.c`; the new `tcp-brutal` repo doesn't. Without the wrapper, ssserver runs with system default CC (BBR) and Brutal is unused. |
| **Fix:** | Wrote a new wrapper from scratch. The key insight: intercept `accept()` (not `setsockopt(TCP_CONGESTION)`) because ssserver never calls setsockopt for congestion control. The wrapper sets `TCP_CONGESTION=brutal` on each accepted client connection via `setsockopt()`. |
| **File:** | `server/modules/03-brutal.sh` (inline C source compilation) |

### 3. Caddy rate_limit Plugin Missing from APT Repo

| Severity | 🟡 Rate limiting wouldn't work |
|----------|-------------------------------|
| **Issue:** | Installing Caddy from the official APT repo doesn't include the `caddy-ratelimit` plugin, causing the Caddyfile to fail validation. |
| **Fix (original):** | Replaced APT approach with download of pre-built Caddy from `caddyserver.com/api/download?p=github.com/mholt/caddy-ratelimit` — much faster than building with xcaddy. |
| **Fix (V5):** | Same approach, but added `rate_limit` module verification after download (checks `caddy list-modules`). |
| **File:** | `server/modules/05-caddy.sh` |

### 4. Caddyfile Syntax Error: rate_limit as Global Option

| Severity | 🔴 Caddy would fail to start |
|----------|-----------------------------|
| **Issue:** | `rate_limit` was placed as a global option `{ }`. It's actually a **handler directive** that must appear inside the site block. |
| **Fix:** | Moved `rate_limit` blocks inside the site block. Also fixed: removed `alps` TLS option (invalid on modern Caddy), fixed that multiple keys per zone aren't supported (each zone gets one key). |
| **File:** | `server/modules/05-caddy.sh` (inline Caddyfile generation) |

### 5. tc Shell Escaping Bug in ExecStart

| Severity | 🟡 tc wouldn't apply on boot |
|----------|------------------------------|
| **Issue:** | Systemd `ExecStart` with inline shell commands (pipes, redirects) has escaping issues. The tc filter command `tc filter add ... match ip sport $PORT 0xffff flowid $CLASSID` was broken. |
| **Fix:** | Created a dedicated helper script at `/usr/local/bin/myvpn-tc-apply.sh` and call it from the oneshot service. Helper script handles all tc commands. |
| **File:** | `server/modules/04-tc.sh` |

### 6. PocketBase Missing `unzip` Dependency

| Severity | 🟡 PocketBase wouldn't install |
|----------|-------------------------------|
| **Issue:** | PocketBase is distributed as a `.zip` file, but `unzip` wasn't installed. |
| **Fix:** | Added `unzip` to the apt-get install command before download. |
| **File:** | `server/modules/06-pocketbase.sh` |

### 7. Systemd Service Start Ordering

| Severity | 🟡 Services could fail on first boot |
|----------|--------------------------------------|
| **Issue:** | Services were enabled but not started in correct order. Brutal LD_PRELOAD must exist before Stealth service starts. tc rules must exist before Eco/Strike start. |
| **Fix:** | Setup script defers all service starts until after all modules complete. Explicit start ordering in `setup.sh`. |
| **File:** | `server/setup.sh` |

### 8. Backup B2_BUCKET Not Exported

| Severity | 🟡 Backup script wouldn't receive env vars |
|----------|--------------------------------------------|
| **Issue:** | `B2_BUCKET` was set but not exported, so the backup script couldn't read it. |
| **Fix:** | Added `set -a` (allexport) before reading env vars. |
| **File:** | `server/modules/07-backups.sh` |

---

## CLIENT-SIDE ISSUES (v4/) — DOCUMENTED, NOT YET FIXED

These are known issues in the V4 Go client code. They don't prevent use but
should be addressed before production release.

### C1. Heartbeat Sends Code & Fingerprint in GET Query Params

| Severity | 🔴 Security |
|----------|-------------|
| **File:** | `v4/internal/heartbeat/heartbeat.go:197` |
| **Issue:** | `GET /api/heartbeat?code=X&fp=Y` — the activation code and fingerprint appear in query parameters. These are logged by Caddy (access logs), potentially exposed in browser history (if debugging), and visible in `Referer` headers. |
| **Fix:** | Change to `POST` with JSON body, or encode as `Authorization` header. Requires mirror update in `heartbeat.pb.js` to read from header/body instead of query params. |

### C2. Fingerprint Fallback UUID is Broken

| Severity | 🔴 Would crash on edge case |
|----------|-----------------------------|
| **File:** | `v4/internal/activation/fingerprint.go:103` |
| **Issue:** | If all hardware sources fail AND `crypto/rand.Read()` fails, the code does `fmt.Sprintf("fallback-%d", sync.Once{})` which prints the `sync.Once` struct — not a UUID string. This is extremely unlikely on modern OSes (rand.Read failure is near-impossible) but would cause a panic or unparseable UUID. |
| **Fix:** | Use `uuid.New().String()` from `github.com/google/uuid` instead of manual generation, or simplify to a timestamp-based UUID format that doesn't depend on crypto/rand. |

### C3. sing-box Binary Search Path Is Hardcoded

| Severity | 🟡 Breaks on non-standard installs |
|----------|------------------------------------|
| **File:** | `v4/internal/helper/main.go:435-443` |
| **Issue:** | The helper service searches for sing-box in only 4 hardcoded paths. If the binary is installed elsewhere (e.g., custom path, user's home), the helper won't find it. |
| **Fix:** | Add `PATH` environment search, a config file option, or pass the path via IPC command. |

### C4. No Certificate Pinning in Heartbeat

| Severity | 🟡 Security (planned feature) |
|----------|-------------------------------|
| **File:** | `v4/internal/heartbeat/heartbeat.go` |
| **Issue:** | Architecture docs specify "cert-pinned TLS" but the heartbeat uses a standard `http.Client` with no certificate pinning. A compromised CA could MITM the heartbeat. |
| **Fix:** | Use `crypto/tls.Config` with `VerifyConnection` hook to pin the server's certificate or public key. |

### C5. commandExists() Uses Wrong Search Path

| Severity | 🟡 Diagnostics would be wrong |
|----------|-------------------------------|
| **File:** | `v4/internal/gui/diagnostics.go:95-101` |
| **Issue:** | `commandExists()` checks `os.Stat(name)` (cwd) and `os.Stat("/usr/bin/"+name)` — not the actual system `PATH`. `exec.LookPath()` is the correct approach. |
| **Fix:** | Use `exec.LookPath("sing-box")` instead of manual path checks. |

### C6. Zero Tests

| Severity | 🟡 Reliability |
|----------|----------------|
| **Location:** | Entire `v4/` codebase |
| **Issue:** | There are zero `*_test.go` files. The memory file mentions `activation_test.go` exists, but it doesn't. Luhn-mod-N validation, fingerprint generation, heartbeat logic, and updater crash detection all need tests. |
| **Fix:** | Add unit tests. At minimum: `luhn_test.go` (known-good codes, codes with wrong checksums, edge cases), `fingerprint_test.go` (Linux source collection with mock files), `storage_test.go` (atomic writes, concurrent access). |

### C7. Unused Dependency in go.mod

| Severity | 🟡 Dead weight |
|----------|----------------|
| **File:** | `v4/go.mod` |
| **Issue:** | `go.mod` lists `github.com/shadowsocks/go-shadowsocks2` but the app uses sing-box (spawned as subprocess), not the Go Shadowsocks library. This dependency is unused. |
| **Fix:** | Remove the dependency. |

### C8. Activation Endpoint Path Inconsistency Risk

| Severity | 🟡 Would silently fail |
|----------|------------------------|
| **File:** | `v4/internal/activation/activation.go` vs `server/pb_hooks/activation.pb.js` |
| **Issue:** | Client POSTs to `/api/activate`. The JS hook registers `routerAdd("POST", "/api/activate", ...)`. These match in the current code, but if either changes independently, the mismatch would be silent (no compile-time check — it's a runtime HTTP route). |
| **Fix:** | Define the endpoint path as a shared constant (not possible across Go/JS) or document it as a hard contract (see `docs/API.md`). |

### C9. No go.sum File

| Severity | 🟡 Reproducibility |
|----------|-------------------|
| **Location:** | `v4/` root |
| **Issue:** | No `go.sum` committed. Running `go mod tidy` on different dates could resolve different dependency versions, leading to non-reproducible builds. |
| **Fix:** | Run `go mod tidy` once and commit the `go.sum`. |

### C10. TUN Package Design Fragmentation

| Severity | 🟡 Architectural |
|----------|------------------|
| **Issue:** | Three overlapping implementations for the same job: (a) `tunnel/tunnel.go` implements TUN directly via `exec.Command`, (b) `manager/process.go` includes `HelperClient` that talks to the privileged helper via IPC, (c) `helper/main.go` is the privileged daemon. It's unclear which path is actually used at runtime. |
| **Fix:** | Consolidate: the helper service should be the only path for TUN creation. The `tunnel` package can be removed or reduced to just kill switch + DNS helpers. |

---

## OBSERVATIONS (Non-Issues)

These are not bugs but important things to know.

### Architecture Notes

| Observation | Detail |
|-------------|--------|
| TCP-over-TCP risk | The client uses sing-box which creates a TUN interface. All traffic goes through the TUN → Shadowsocks TCP tunnel. This creates TCP-over-TCP, which can cause performance issues on lossy links (each packet loss causes both TCP stacks to back off). Mitigation: sing-box has tun `auto_route` and the server uses aggressive CC (Brutal for Stealth). The N4L school network is generally low-loss, so this is acceptable. |
| UDP over TCP | Strike tier supports UDP via `udp_over_tcp` in sing-box. This wraps UDP datagrams in TCP streams for games that need UDP. Latency-sensitive UDP games (Fortnite, Valorant) should be tested. |
| Brutal is unfair | Brutal CC is designed to be unfair. On a shared VPS, a Stealth user can drown out Eco and even Strike traffic. This is intentional product differentiation. If the VPS runs other services, they will be impacted. |
| No IPv6 | The TUN interface explicitly does not handle IPv6. The sing-box config drops IPv6 traffic. This could leak IPv6 traffic or cause connectivity issues on IPv6-preferred networks. |
| Grace period is client-enforced | The 7-day grace period is calculated by the client based on `last_heartbeat_ok` timestamp. A malicious client could bypass this. Acceptable for the threat model (paying students, not nation-state attackers). |

### Performance Notes

| Observation | Detail |
|-------------|--------|
| Sing-box memory | ~50MB idle, ~100-200MB under load. Fine for 2GB VPS shared across 20 users. |
| Caddy memory | ~30MB base + ~5MB per concurrent connection. |
| PocketBase memory | ~20MB base for SQLite. Scales well. |
| ss-rust memory | ~5MB per instance, no per-connection overhead (single-threaded event loop). |
| TCP Brutal CPU | Near-zero CPU overhead (kernel module). The LD_PRELOAD wrapper is negligible. |

### Security Notes

| Observation | Detail |
|-------------|--------|
| All ports open | UFW allows 8443/8444/8445/tcp and 8445/udp globally. No IP whitelisting. This is required because students connect from school IPs that change. Acceptable risk — the Shadowsocks protocol provides encryption+auth. |
| No rate limiting on Strike UDP | If Strike users abuse UDP (e.g., DDoS), they could impact VPS performance. Mitigation: tc caps at 200 Mbps, and the client base is small. |
| Admin token in env | `ADMIN_API_TOKEN` is stored in `/etc/environment`. A compromised VPS leaks this token. Rotate periodically. |
| TLS termination at Caddy | Caddy terminates TLS, then proxies to PocketBase over plain HTTP on localhost. An attacker with localhost access (e.g., through another exploited service on the VPS) could intercept API traffic. |
