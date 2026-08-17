
---

## CLIENT APP PASS — UPDATE FLOW WIRED, CODE NORMALIZATION, STORAGE RECOVERY (2026-08-17)

Client-side fix pass (no server re-deploy needed for the client fixes; the two
hook edits are hardening and deploy with the next hook sync):

**1. Update flow was a dead end — now wired end-to-end.**
The `updater` package (crash-safe two-phase swap) was fully implemented but
NOTHING called `PerformUpdate`: the UI's "Update vX available" button only
re-ran `CheckForUpdate` and never downloaded anything. Students would see the
button forever with no update ever applying.

- `app.go`: heartbeat + manual check now record the update signal
  (`recordUpdateSignal` → `a.lastUpdate`), and a new frontend-bound
  `App.ApplyUpdate()` runs `PerformUpdate` in the background (5 min timeout,
  serialized by `upMu`), emitting `update:status` events
  (`downloading → verifying → applying → applied | failed`). On success the
  app quits ~1.5s later so the forked new binary takes over. Re-applying the
  already-running version (staged rollouts re-advertise every heartbeat) is
  rejected with "Already running the latest version".
- Frontend: `bridge.applyUpdate()`, `update:status` listener, store
  `updatePhase`/`updateMessage` state, and the MainScreen button now applies
  the update with a live spinner + "Downloading…/Restarting…" label.
- Note: after an update the app does NOT auto-reconnect the tunnel (the new
  binary starts disconnected; student clicks Connect). Follow-up candidate.

**2. Activation-code normalization (heartbeat 404 risk).**
The server looks up codes formatting-sensitively (`findFirstRecordByData` on
the seeded hyphenated form), but the client stored whatever the user typed.
The frontend auto-formats with hyphens so the happy path worked, but any
unformatted/lowercase stored code (old clients, direct pastes) 404'd every
heartbeat — killing suspension checks and update signals.

- `activation.NormalizeCode()` added; `App.Activate` stores + heartbeats the
  canonical `MYVPN-XXXX-XXXX-XXXX-C` form; `Startup` self-heals legacy stored
  codes by rewriting storage.
- Hardening: both `activation.pb.js` and `heartbeat.pb.js` now normalize the
  lookup code to the canonical form before `findFirstRecordByData`.

**3. Storage: corrupt file now recovers from backups instead of deactivating.**
`storage.New` previously moved a corrupt `storage.json` aside and started
fresh — silently deactivating the device (and the code is then "bound to
another device" server-side, a support nightmare). The `.bak.0..2` rotation
existed but was never used for recovery.

- `New()` now tries `tryRestoreBackups()` (newest valid `.bak.N` first, JSON +
  consistency checked) before the fresh-start fallback; restored via the same
  atomic save path.
- `RestoreFromBackup()` simplified to reuse the same logic.
- New tests: `storage_test.go` (corrupt→restore keeps activation, no-backup→
  fresh start moves file aside, invalid backups skipped, rotation pruning).

**4. Frontend fixes.**
- `MainScreen.vue`: removed dead `await emit('connect')` error handling —
  Vue 3 `emit()` returns void, so the "failed connect" branch could never
  fire. Connect failures surface through the global error toast (store path),
  which already worked. Also removed the unused `check-update` emit/handler.
- `app.go`: bound methods (`Activate`, `Connect`, `GetStatus`, `IsActivated`,
  `GetDiagnostics`, `disconnect`, `startHeartbeatLoop`) now nil-guard via
  `notReady()` when Startup fails part-way — a broken startup shows a clear
  message instead of panicking inside the Wails binding.

**Verified:** `go build ./...` + `go vet` + `go test ./...` (incl. new storage
tests) green on linux, windows-amd64 and darwin-arm64 cross-compile;
`vue-tsc --noEmit` + `vite build` clean; frontend/dist rebuilt and committed.

**Deploy:** client fixes ship with the next client build. The two `pb_hooks`
edits are safe to deploy anytime (normalization is a no-op for canonical
codes); restart pocketbase or rely on hook hot-reload.

---

## GAMING UDP — P0 CODE LANDED (UNTESTED) (2026-08-14)

The P0 transport change from `v5/docs/GAMING-UDP.md` is implemented but
**untested** (no test environment). What landed:

- `v5/server/modules/02-shadowsocks.sh`: gated `ENABLE_UOT=1` section —
  installs sing-box server (v1.12.1) with a Strike-creds shadowsocks inbound
  on `UOT_PORT` (default 8446), `udp_over_tcp: true`, systemd unit
  `sing-box-uot.service`. Additive; 8443/8444/8445 and TCP path untouched.
- `v5/server/scripts/seed-live.py` + `seed-pb.py`: with `ENABLE_UOT=1`,
  strike tier_configs gains `"uot_port"` + `udp_relay=true`; otherwise
  identical to before (raw UDP).
- Client (`process.go`, `app.go`, `heartbeat.go`, `activation.go`,
  `storage.go`): when the tier advertises `uot_port` (UDPRelay &&
  ServerPortUOT > 0), the generated config adds a `proxy-uot` outbound
  (`udp_over_tcp: true`) and a `network: udp` route rule; TCP stays on the
  standard outbound. No advertised port → config byte-identical to before.
- Verified: `go build`/`go vet` green on linux + windows cross-compile.
  Shell modules NOT executed (no VPS).

**Deploy (when testing resumes):** `ENABLE_UOT=1` on setup.sh re-run +
`ENABLE_UOT=1` on seed-live.py. Disable: stop unit + drop uot_port from
tier_configs. Full validation checklist in `v5/docs/GAMING-UDP.md` §P1.

---

## GAMING UDP — CONSOLIDATED CHANGE PLAN (2026-08-14)

The Strike-tier gaming-UDP failure chain (raw SS UDP → hostile school-network
UDP policy; UoT removed because shadowsocks-rust RSTs SagerNet UoT) is
consolidated into an implementation plan at `v5/docs/GAMING-UDP.md`.

**Core change (P0, not yet implemented):** carry game UDP inside the TCP
tunnel via SagerNet UDP-over-TCP, with a **server that implements it**
(additive sing-box server instance on a new port is the leading option —
shadowsocks-rust can't do UoT and RSTs it). The server choice is an open
decision with a tradeoff matrix in the plan (§5a: sing-box server vs Xray
SS+uot vs shadowsocks-rust+udp2raw vs raw-UDP status quo); the deciding
interop test can't run until testing resumes. P1 validation experiments
(VPS-direct byte-exact LiteNetLib probe, packet-rate ladder, server
aliveness) are deferred until testing is possible. See the plan doc for the
full file-change map and sequencing.

---

## MACOS RE-ENABLED — UNSIGNED BUILD (2026-08-14)

Round-2 culling dropped macOS (unsigned builds were deemed unusable).
Revisited when it became clear macOS is ~50% of the target market; decision:
ship the **unsigned** build with a documented Gatekeeper workaround (right-click
→ Open, or `xattr -cr /Applications/MyVPN.app`). Full end-user instructions
will live on the separate download site.

What was restored (all previously removed in the culling commits):
- `tunnel.go`: `darwinTUN`, `killSwitchDarwin` (pfctl), `setDNSDarwin`
  (networksetup), darwin switch cases + interface assertion
- `activation/fingerprint_darwin.go`: self-contained darwin fingerprint
  (networksetup / ioreg), mirroring the linux/windows layout
- `updater/update_darwin.go`: darwin binary swap + fork (`swapDarwin`/`forkDarwin`)
- `darwin_link.go`: `UTType` cgo shim (Wails + Xcode 26 SDK linker fix)
- `manager/process.go`: elevation case back to `linux, darwin`
- `updater.go` + `heartbeat.go`: `DownloadURLMacOSIntel/ARM`,
  `UpdateMacOSIntel/ARM` fields + darwin case in `PlatformDownloadURL`
- `server/pb_hooks/heartbeat.pb.js`: `update_macos_intel`/`arm` field emission
- `.github/workflows/build.yml`: macOS-amd64 + macOS-arm64 matrix entries,
  sing-box darwin download case, release-table macOS rows + unsigned note
- `v5/client/Makefile`: `build-macos-intel` / `build-macos-arm` + build-all

**Verified:** `go build ./...` and `go vet` pass for darwin arm64 + amd64
(cross-compile), plus linux + windows. The full `wails build` (frontend
embed) must be validated on a macOS runner (CI) before shipping a release.

---

## FRESH VPS DEPLOY 2026-08-14 — BUGS FOUND & FIXED (134.199.155.166)

First deploy to the new VPS (Ubuntu 22.04, 1vCPU/1GB, DigitalOcean syd1).
DNS for networkingguides.duckdns.org now points at the new IP. The deploy
surfaced a stack of real bugs — all fixed in this round:

| # | Area | Bug | Fix |
|:-:|------|-----|-----|
| D1 | `00-env.sh` | Hard RAM gate (<1GB FAIL) rejected a 957MB-class VPS that runs the whole stack fine | Fail only <512MB; <1GB is a warning |
| D2 | `05-caddy.sh` | `list-modules | grep -q` under `set -euo pipefail`: grep -q closes the pipe → caddy SIGPIPE → pipefail turns the check into a FALSE NEGATIVE (a good binary was rejected) | Use `grep -c` (consumes all input); fixed in 3 places |
| D3 | `05-caddy.sh` | caddyserver.com download API ignores the version pin (asked 2.8.4, got 2.11.4) and occasionally returns a build WITHOUT the plugin | Added deterministic xcaddy fallback build; keeps API fast-path |
| D4 | `07-backups.sh` | `ensure_sqlite3` called before its definition → "command not found", setup died | Function moved above the Main section |
| D5 | `setup.sh` | Post-module service-start list only started shadowsocks-* — tc-*-cap services (and sing-box-uot) were never started on fresh deploys → caps silently unenforced | Start list now includes tc-*-cap + sing-box-uot (when ENABLE_UOT=1) |
| D6 | `02-shadowsocks.sh` UoT | sing-box REJECTS `"network": "tcp_and_udp"` (shadowsocks-rust syntax) and rejects `"udp_over_tcp"` on the INBOUND (unknown field) | Both omitted — sing-box defaults to tcp+udp and handles UoT magic-domain connections automatically |
| D7 | `seed-pb.py` | `api()` never sent the Authorization header → admin token "verification" always failed, then old `/api/admins` endpoint (removed in PB 0.22) → bootstrap never created collections | Global TOKEN threaded through api(); PB 0.22 `_superusers` endpoints; saved-password re-auth fallback |
| D8 | `seed-live.py` | Same PB 0.22 admin migration needed (`/api/admins` → `_superusers`) | Same fix |
| D9 | seed schemas | `update_config` still declared `download_macos_*` fields | Removed (macOS dropped) |
| D10 | `scripts/generate_codes.sh` | `double=!$double` in bash assigns the literal string `!false` → EVERY generated code had a wrong Luhn checksum → all codes rejected as "Invalid code format" by the client AND the activation hook | Proper toggle `if $double; then double=false; else double=true; fi` |
| D11 | code generator docs | Instructions passed `/root/.admin_api_token` (app-level token) — PocketBase 0.22 rejects it (401). The generator needs the PB admin JWT | Docs + setup.sh hint now use `grep PB_TOKEN /root/.pb_admin_creds \| cut -d= -f2` |

**Deployment notes (streamlined process):**
- Update DNS BEFORE running setup.sh — `00-env.sh` hard-fails if the domain doesn't resolve.
- `scp -r v5/server age-key.txt root@VPS:/root/server/` — age-key.txt must sit alongside setup.sh.
- `ENABLE_UOT=1 DOMAIN=… ./setup.sh` installs the sing-box UoT endpoint (port 8446) + seeds `uot_port` via seed-pb.py.
- Caddy per-IP rate limits are tight for school NAT (activate 5/10min/IP, heartbeat 1/10s/IP, api 100/10s/IP) — fine for the current client cadence; revisit if multiple students share one egress IP.
- The old VPS's PocketBase DB (activation codes) remains in B2 (`vpsvpnbackup`, backups up to 2026-08-10 02:00 UTC) — decision 2026-08-14: fresh start, do NOT restore.

**Verified live:** 23/23 smoke checks; activation 200 with `uot_port:8446`; heartbeat 200 with config refresh; TLS 200; all 10 services active; backup timer hourly.

---

## STRIKE GAMING LATENCY — BUFFERBLOAT CONTROL (2026-08-14)

Gaming speed = latency + jitter, not Mbps (school RTT 51ms is already good).
The real server-side win was bufferbloat: the HTB tier classes used plain
FIFO queues, so when a tier ran at full bandwidth (e.g. someone downloading
at 200 Mbps), queuing added latency spikes to game traffic on the same tier.

Changes (applied live + in modules):

- `04-tc.sh` helper: **fq_codel leaf qdisc under every HTB class**
  (parent 1:10/1:20/1:30, handles 10:/20:/30:) — per-flow fair queuing +
  CoDel AQM keeps latency flat under load (the standard BBR pairing).
  UoT traffic (port 8446, default class 1:30) benefits too.
- `01-bbr.sh` 91-tcp-tune.conf: `tcp_slow_start_after_idle = 0` (games that
  alternate quiet/burst don't re-slow-start every round) +
  `tcp_mtu_probing = 1`.
- `04-tc.sh` module bugs fixed while doing this:
  - helper script is now ALWAYS rewritten (was skip-if-exists → a stale
    helper persisted on deployed VPSs and helper updates never applied)
  - filter flush moved to the module main (once, before applying all three
    tiers) — the per-call flush design would have left only the LAST tier's
    filter; repeated adds without any flush duplicate filters (both hit
    during the 2026-08-14 apply)

Verified live: 3 filters, fq_codel under all 3 classes, sysctls applied.

Expected effect: latency stays flat on Strike when the tier is saturated;
game RTT (already ~51ms school → Sydney) unchanged at low load.

---

## NEW VPS — VOYAGER-PROBLEM REGRESSION TEST (2026-08-14)

Tested 134.199.155.166 against every failure class that plagued the old
Voyager VPS. Results:

| Old-Voyager problem | Test | Result |
|---------------------|------|:------:|
| IP drift (domain pointed at dead IP, cached clients broke) | duckdns resolves to 134.199.155.166 from VPS + public resolvers | ✅ |
| tc caps evaporate on kernel update / reboot | **Full reboot** → all 10 services active, exactly 3 tc filters attached post-boot | ✅ |
| "Can't validate the SS leg on the VPS" (TUN captured own egress) | sing-box client on the VPS → :8445 → curl example.com → **HTTP 200 in 89ms** | ✅ |
| UoT RST'd by shadowsocks-rust (~300ms) | sing-box client `udp_over_tcp` → :8446 → DNS query to 1.1.1.1:53 → **reply returned (2 answers)** — first working UoT round-trip on this stack | ✅ |
| Backups silently stopped (old box's last backup 2026-08-10) | B2 shows 24 fresh `.db.gz` backups from Aug 13-14; timer active; checksum verified | ✅ |
| `source <(age -d …)` secrets silently failing | setup.sh decrypts via age-key.txt temp-file path; tier passwords match configs + tier_configs | ✅ |
| PB hooks broken → generic 400 on activation | Activation returns proper "Missing code" 400; heartbeat 200; health 200; TLS 200 | ✅ |
| "Empty tier_configs" red herring | tier_configs populated (eco/stealth/strike, strike has uot_port) | ✅ |
| B2 CLI syntax drift / restore-path bugs | b2 CLI 4.7.1 installed; backup upload + checksum verification working | ✅ |
| Admin-password drift after restore | PB admin JWT in /root/.pb_admin_creds valid (seeds + API calls succeed) | ✅ |

**Remaining (needs the user's environment):** real SCP:SL game session on
the school network (P1 in GAMING-UDP.md). Also note: school network shapes
downloads per-flow (~2.5 Mbps/flow; 25 Mbps aggregate with parallel flows,
113 Mbps up) — a last-mile policy, not a VPS defect.

---

## HISTORY ARCHIVE — CURATED ORIGINALS RESTORED (2026-08-14)

After the culling rounds 1–2 deleted `originals/` wholesale, review flagged
that the business model and N4L threat research were unique knowledge with no
living-doc equivalent. The still-relevant docs were restored from git history
(`72eec8a^:originals/`) into a clearly-labelled archive:

- `v5/docs/history/Business-Plan.md` — middleman distribution, market vs xVPN
- `v5/docs/history/attacker-perspective.md` — insider-threat / covert-telemetry analysis
- `v5/docs/history/defender-perspective.md` — defense-in-depth research
- `v5/docs/history/comprehensive-vpn-blocklist.md` — N4L/Palo Alto blocklist research
- `v5/docs/history/school-vpn-blocking-implementation.md` — N4L network-level blocking
- `v5/docs/history/Architectural-Plan.md` — original v3 Hysteria 2 plan (context)

**Not restored** (stays in git history only): `Actionable-Plan.md` (4,771-line
superseded action plan), `V2-ACTION-PLAN.md`, `V2-BOTTLENECK-REFERENCE.md`,
and the entire `v3/` + `simplified/` alternative doc sets.

`v5/docs/history/README.md` indexes everything and gives the `git show`
commands for anything still only in history.

---

## REPO CULLING ROUND 2 — STREAMLINED LAYOUT (2026-08-13)

Continuation of the 2026-08 cleanup (round 1 removed `v3/`, `simplified/`,
`originals/`, `v5/legacy/`). Round 2 consolidates duplicates and drops dead
platform support. All removed files are recoverable from git history.

| # | Change | Rationale |
|:-:|--------|-----------|
| C1 | `modular-vps/` deleted | Stale duplicate of `v5/server/` (untouched by commits since 2026-07-27). `v5/server/` is the one and only server deployment directory. Its `hiddify.pb.js` hook was moved into `v5/server/pb_hooks/`. |
| C2 | Root `CONTEXT.md` deleted | Superseded by `v5/CONTEXT.md` — one context document. |
| C3 | `v5/scripts/` deleted | Duplicate of root `scripts/` (which holds the newer hardened `generate_codes.sh`). Root `scripts/` is canonical; all docs updated to `./scripts/…`. |
| C4 | macOS (darwin) support dropped from client | Unsigned builds are unusable on macOS (no Apple signing/notarization). **REVERSED 2026-08-14** — macOS was ~50% of market, so the unsigned build is shipped with a documented Gatekeeper workaround (right-click → Open / `xattr -cr`). See "MACOS RE-ENABLED" below. |
| C5 | Fingerprint files made self-contained | `fingerprint.go` (shared) + `fingerprint_darwin.go` deleted; shared logic (hashSources, UUID fallback, ValidateFingerprint, caching) folded into `fingerprint_linux.go` and `fingerprint_windows.go`. `update_unix.go` → `update_linux.go` (linux-only, macOS gone). |
| C6 | `POCKETBASE-SETUP.md` leaked admin token | Five hardcoded `ADMIN_API_TOKEN` values replaced with `YOUR_ADMIN_TOKEN` placeholder. |

**Docs updated:** root `README.md`, `v5/README.md`, `v5/CONTEXT.md`,
`v5/docs/ARCHITECTURE.md`, `BACKEND-API.md`, `CLIENT-GUIDE.md`, `CI-CD.md`,
`API.md`, `OPS.md`, `DEPLOY.md`, `POCKETBASE-SETUP.md`, `IMPLEMENT.md`,
`WAILS-MIGRATION.md`, `.github/workflows/build.yml`, `v5/client/Makefile`.

---

## DEPLOY/RESTORE — PLUG-N-PLAY HARDENING (2026-08-01)

Validated a full blank-VPS deploy + B2 restore live (114.23.136.59). The deploy
path was already one-command; the restore path had gaps, all fixed:

| # | Area | Issue | Fix |
|:-:|------|-------|-----|
| R1 | `restore.sh` | Latest-backup detection used `b2 ls --long "$BUCKET" backups/` — plain bucket names fail on the current CLI ("Invalid B2 URI") | `b2 ls --recursive "b2://$BUCKET/backups/"` filtered to `.db.gz`, sorted |
| R2 | `restore.sh` | Download/checksum used deprecated `b2 download-file-by-name` / `b2 file-info` (exit 1 + error output on success paths) | Current syntax: `b2 file download b2://…` / `b2 file info b2://…` |
| R3 | `restore.sh` | Restored `data.db` over a fresh-seeded DB without removing `-wal`/`-shm` — stale WAL can be replayed against the restored file | `rm -f data.db-wal data.db-shm` before chown |
| R4 | `restore.sh` | Restored DB's admin password may predate the secrets file (old auto-generated era) → login fails with documented creds | Auto-run `./pocketbase admin update "$PB_ADMIN_EMAIL" "$PB_ADMIN_PASS"` (while PB stopped) |
| R5 | `restore.sh` | `pocketbase-backup.timer` has `Requires=pocketbase.service` — stopping PB (part of restore) **stops the timer too** (Requires propagates stops, not starts); it stayed inactive after restore | Explicit `systemctl restart pocketbase-backup.timer` + `is-active` verification after start |
| R6 | `restore.sh` | Codes-count check used the API with a nonexistent token file → always 0/unknown (collections are admin-only) | Direct `sqlite3 … "SELECT COUNT(*) FROM codes"` |
| R7 | `restore.sh` | No hook-load verification after restore | POST `/api/activate {}` must return 400 "Missing code" (generic 400 = hook load error) |
| R8 | `07-backups.sh` | `sqlite3` not installed → backup script silently used the direct-copy fallback (inconsistent backups under write load) | Module now installs `sqlite3` so `.backup` (safe) is always used |
| R9 | `07-backups.sh` | Timer enable/restart swallowed errors with `|| true` | Loud enable/restart + `is-active` verification with manual-fix instructions |

**Docs updated:** `DEPLOY.md` (post-deploy checklist now includes backup timer +
backup log + update.json + the Requires= gotcha; disaster-recovery section
describes the full one-command flow), `OPS.md` (restore runbook rewritten with
the verified manual steps + current b2 syntax + admin password alignment +
timer restart), `v5/CONTEXT.md` (agent rules 7–9: timer gotcha, admin password
alignment, b2 URI syntax).

**Verdict:** a blank Ubuntu 22.04 VPS + repo + `age-key.txt` is fully plug-n-play:
`setup.sh` for fresh deploy (auto-runs the smoke test), `restore.sh` for
migration/disaster recovery — both validated live 2026-08-01.

---

## STEALTH TIER — TCP BRUTAL REMOVED, BACK TO BBR (2026-08-01)

**Decision:** Stealth no longer uses the `tcp-brutal` kernel module + LD_PRELOAD
wrapper. The aggressive rate-based CC filled buffers and caused bufferbloat/jitter
on the school network when it throttled, and the Linux `tcp-brutal` module is not
as robust or powerful as Hysteria 2's built-in Brutal CC. Stealth now runs plain
**BBR** like the other tiers, with a **tc HTB cap of 100 Mbps** (class `1:20`,
port 8444) replacing the old 48 Mbps Brutal target rate.

**Changes:**
- `v5/server/modules/03-brutal.sh` — **deleted** (kernel module build/load,
  DKMS registration, `brutal-wrap.so` LD_PRELOAD wrapper, 48 Mbps target).
- `v5/server/modules/02-shadowsocks.sh` — removed `write_brutal_dropin()`
  (the `LD_PRELOAD=/usr/local/lib/brutal-wrap.so` drop-in for the Stealth
  systemd service); Stealth service description is now "BBR, 100 Mbps tc cap".
- `v5/server/modules/04-tc.sh` — Stealth now gets a tc class `1:20` @ `100mbit`
  and a `tc-stealth-cap.service` oneshot (alongside Eco `1:10` and Strike `1:30`).
- `v5/server/setup.sh` — `03-brutal.sh` removed from the module chain; summary
  line updated to "Stealth port: 8444 (BBR, 100 Mbps tc)".
- `v5/server/scripts/smoke-test.sh` — Brutal module check replaced with a
  Stealth tc class (`1:20`) check; tc verification now covers 8443/8444/8445.
- Docs updated: root `README.md`, `v5/CONTEXT.md`, `v5/docs/ARCHITECTURE.md`,
  `v5/docs/DEPLOY.md`, `v5/docs/OPS.md` (tier tables, diagrams, deploy
  checklist, ops checks, kernel-update guidance).
- Historical references in `v3/`, `v4/`, `simplified/`, `originals/`,
  `modular-vps/` and root `CONTEXT.md` are left untouched (reference-only).

**Ops note:** no kernel modules are maintained anymore. After a kernel update,
re-apply the caps with `systemctl restart tc-eco-cap tc-stealth-cap tc-strike-cap`.
The old Brutal entries below this one are historical records of the earlier design.

---

## CI LINT — ERRCHECK FIXES (Wails Migration)

Found when the Wails CI pipeline first ran `golangci-lint` on the client module.
All fixes are behavior-neutral (`_ =` acknowledges best-effort/cleanup errors).

| # | File | Line | Fix |
|:-:|------|:----:|-----|
| L1 | `v5/client/internal/storage/storage.go` | 159 | `f.Sync()` → `_ = f.Sync()` (best-effort fsync) |
| L2 | `v5/client/internal/updater/updater.go` | 165 | `u.restoreBackup(...)` → `_ = ...` (cleanup after swap failure; original error already returned) |
| L3 | `v5/client/internal/updater/updater.go` | 364 | `os.Chmod(...)` → `_ = ...` (post-revert permission set) |
| L4 | `v5/client/internal/manager/process.go` | 261 | `m.cmd.Wait()` → `_ = ...` (graceful shutdown goroutine) |
| L5 | `v5/client/internal/manager/process.go` | 409 | `cmd.Wait()` → `_ = ...` (startup probe after immediate exit) |
| L6 | `v5/client/internal/tunnel/tunnel.go` | 140 | `iptables -F OUTPUT` → `_ = ...Run()` (best-effort rule removal) |
| L7 | `v5/client/internal/tunnel/tunnel.go` | 163 | `pfctl -F all` → `_ = ...Run()` (best-effort) |
| L8 | `v5/client/internal/tunnel/tunnel.go` | 164 | `pfctl -d` → `_ = ...Run()` (best-effort) |
| L9 | `v5/client/internal/tunnel/tunnel.go` | 257 | `t.Stop()` → `_ = t.Stop()` (rollback on TUN setup failure) |
| L10 | `v5/client/internal/tunnel/tunnel.go` | 294 | `t.Stop()` → `_ = t.Stop()` (rollback on TUN setup failure) |

Also fixed siblings not flagged by the linter: `f.Close()` in the same storage block,
and the `linuxTUN.Stop()` best-effort cleanup loop.

---

## CI LINT — REMAINING ERRCHECK + UNUSED FIXES (2026-07-31)

`golangci-lint run ./...` (v2.12.2, default linters — the CI `lint` job) still
failed after the L1–L10 batch. 26 more issues fixed, all behavior-neutral
(`_ =` acknowledgements; two genuinely dead functions removed):

| # | File | Fix |
|:-:|------|-----|
| L11 | `v5/client/app.go` | `a.mgr.Stop()` → `_ = ...` (disconnect) |
| L12 | `v5/client/app.go` | `a.store.SetHeartbeat(...)` → `_ = ...` (heartbeat callback) |
| L13 | `v5/client/app.go` | `a.store.SetHeartbeatFailure(...)` → `_ = ...` (heartbeat callback) |
| L14 | `v5/client/internal/activation/activation.go` | `defer resp.Body.Close()` → `defer func() { _ = ... }()` |
| L15 | `v5/client/internal/heartbeat/heartbeat.go` | `defer resp.Body.Close()` → closure |
| L16 | `v5/client/internal/heartbeat/heartbeat.go` | **Removed dead `getInterval()`** (unused) |
| L17 | `v5/client/internal/manager/process.go` | `defer conn.Close()` → closure (helper IPC client) |
| L18 | `v5/client/internal/manager/process.go` | `os.Remove(configPath)` → `_ = ...` (stop cleanup) |
| L19 | `v5/client/internal/storage/storage.go` | `os.Remove(tmpPath)` → `_ = ...` (save failure cleanup) |
| L20 | `v5/client/internal/storage/storage.go` | `os.Remove(oldest)` → `_ = ...` (backup rotation) |
| L21 | `v5/client/internal/tunnel/tunnel.go` | `netsh ... delete rule` → `_ = ...Run()` (Windows kill switch off) |
| L22 | `v5/client/internal/tunnel/tunnel.go` | `networksetup ... Ethernet` → `_ = ...Run()` (macOS DNS fallback) |
| L23 | `v5/client/internal/tunnel/tunnel.go` | `ifconfig down` → `_ = ...Run()` (darwinTUN.Stop) |
| L24 | `v5/client/internal/updater/updater.go` | 5× `defer {Body,f}.Close()` → closures (download/checksum/copy) |
| L25 | `v5/client/internal/updater/updater.go` | 13× `os.Remove(...)` → `_ = ...` (cleanup paths in PerformUpdate/downloadBinary/CheckOnStartup) |
| L26 | `v5/client/internal/updater/updater.go` | **Removed dead `cleanupSentinelFiles()`** (unused) |
| L27 | `v5/client/internal/updater/recover.go` | 6× `os.Remove(...)` → `_ = ...` (sentinel/marker cleanup) |
| L28 | `v5/client/internal/updater/update_windows.go` | 2× `os.Remove(oldPath)` → `_ = ...` (.old cleanup in swapWindows) |

Also ran `gofmt -w` across the module (pre-existing alignment drift in const/var/struct
blocks — cosmetic only) and synced `frontend/package.json` to the committed
`package-lock.json` (`vite ^6.4.3` → `^5.4.21`) so CI `npm install` is
deterministic. Verified green locally: `golangci-lint run ./...` (0 issues),
`go vet ./...`, `go build -tags frontend` (linux/windows/darwin amd64+arm64),
`go test ./...`, and a clean `npm ci && npm run build`.

---

## CI/RUNTIME — MISSING WAILS BUILD TAGS + BROKEN JS BRIDGE (2026-07-31)

**Symptom:** Windows binary built by CI showed the Wails error dialog
*"Wails applications will not build without the correct build tags"* and
opened https://wails.io/docs/guides/manual-builds/.

**Root cause 1 — build tags:** Wails v2 selects its app implementation with
build tags (`internal/app/app_default_*.go` is `//go:build !dev && !production
&& !bindings`). The CI workflow compiled with only `-tags frontend` (which just
selects the embedded asset FS), so the *stub* implementation shipped: a binary
that shows the error dialog. `wails build` adds `desktop,production` itself;
raw `go build` must pass them explicitly.

**Fix:** `.github/workflows/build.yml` now uses `-tags "frontend desktop production"`
in the lint job (`go vet`, compile check) and the 4-platform build matrix.

**Root cause 2 — broken frontend bridge:** `frontend/src/lib/bridge.ts` called
`window.runtime.Call('GetVersion', …)`, but Wails v2.9's runtime does NOT expose
`Call` on `window.runtime` (only Log/Window/Events/etc.), and method names must
be qualified (`main.App.GetVersion`) per the binding DB. Every UI action would
have thrown after the tags were fixed.

**Fix:** `bridge.ts` now calls `window.go.main.App.<Method>(…)` (the bindings map
the backend injects at startup — the generated `wailsjs/` files are only optional
IDE helpers) and keeps `window.runtime.EventsOn/EventsOff` for events.

**Verified:** Windows binary built with the real tags embeds
`-tags=frontend,desktop,production` (per `go version -m`) and no longer contains
the stub error string. Frontend rebuilds cleanly. Linux desktop build requires
WebKitGTK headers locally (CI installs `libgtk-3-dev libwebkit2gtk-4.1-dev` —
includes `pkg-config`); macOS builds require CGO on a macOS runner (matrix
already sets `cgo: "1"` for macOS).

### Follow-up (2026-07-31) — Linux lint failure: missing `webkit2_41` tag

The first green push still failed the `Lint & Vet` job on ubuntu-latest. Cause:
Wails' Linux desktop cgo code selects the WebKitGTK version via a build tag —

```c
#cgo !webkit2_41 pkg-config: webkit2gtk-4.0
#cgo webkit2_41 pkg-config: webkit2gtk-4.1
```

ubuntu-latest (24.04) ships only WebKitGTK **4.1** (`libwebkit2gtk-4.1-dev`),
so the tag-less build looked for `webkit2gtk-4.0` and failed at the pkg-config
step. The `wails` CLI does not auto-add this tag in v2.9.1 — it must be passed
manually. **Fix:** `.github/workflows/build.yml` adds `webkit2_41` to the lint
job (`go vet` / compile check) and the Linux matrix entry; macOS/Windows keep
`frontend desktop production` (the tag is linux-only). Docs and `main.go`
comments updated to mention `-tags "frontend desktop production webkit2_41"`
for Ubuntu 24.04+ builds.

---

## WINDOWS RUNTIME — BLANK POWERSHELL FLASH + INVISIBLE APP (2026-07-31)

**Symptom:** launching the Windows exe popped a blank PowerShell window, then
"nothing happened".

**Root cause 1 — PowerShell console flash:** the device fingerprint collector
(`internal/activation/fingerprint_windows.go`) spawns `powershell.exe` 3×
(Get-NetAdapter, Win32_DiskDrive, Win32_ComputerSystemProduct). A GUI-subsystem
parent spawning console-subsystem children gets a **visible console window per
child** on Windows.

**Fix:** all PowerShell spawns now run via `runHidden()` with
`syscall.SysProcAttr{HideWindow: true}`.

**Root cause 2 — invisible app:** `main.go` set `StartHidden: true`, but Wails
v2.9.1 has **no system tray API** (verified: no `SystemTray` in `pkg/runtime` /
`pkg/options`) and this app never creates a tray icon — the `tray:show` /
`tray:quit` listeners in `setupSystemTray` are dormant hooks nothing emits.
Result: the app ran completely invisible ("nothing happened"). Wails v2.9 also
has no close-to-hide interception, so closing the window quits.

**Fix:** removed `StartHidden` (window is shown on launch); documented the
dormant tray hooks and close-quits behaviour in code comments and docs.

**Also fixed:** `manager/process_windows.go` `newProcAttr()` now sets
`HideWindow: true` so sing-box (console subsystem) doesn't flash a console when
the user connects. Docs (`ARCHITECTURE.md`, `CLIENT-GUIDE.md`,
`UI-AESTHETICS.md`) updated to match.

---

## MACOS BUILD — UNDEFINED `_OBJC_CLASS_$_UTType` (2026-07-31)

**Symptom:** both macOS matrix jobs (`Build macOS-amd64`, `Build macOS-arm64`)
failed at the "Build client app" step with a final-link error:

```
Undefined symbols for architecture arm64: "_OBJC_CLASS_$_UTType"
```

**Root cause:** Wails v2.9.1's darwin frontend uses `UTType` for file dialogs
(`WailsContext.m:575,659`, `UTType typeWithFilenameExtension:`) but its cgo
LDFLAGS only link `Foundation`, `Cocoa`, `WebKit`. On older SDKs the
`UniformTypeIdentifiers` framework was re-exported transitively; the
Xcode 26 / macOS 26 SDK on `macos-latest` removed that implicit linkage.

**Fix:** `v5/client/darwin_link.go` (`//go:build darwin`) adds the missing
framework to the final link:

```go
#cgo LDFLAGS: -framework UniformTypeIdentifiers
```

cgo flags from the main package are included in the final link step, so this
covers both `wails build` and manual `go build` on amd64/arm64. Non-darwin
builds are unaffected (build tag). Upstream Wails v2.10+ fixes this properly
in the darwin package itself.

---

## WINDOWS RUNTIME — BLACK WINDOW FLASHES THEN APP DIES (2026-07-31)

**Symptom:** the window (black) flashes for ~1 second then closes; no process
remains in Task Manager. Wails runs `OnStartup` in a goroutine with no
recovery, and `wailsruntime.LogFatal` calls `os.Exit(1)` — for a GUI build
(no console) any startup failure or panic dies **completely silently**.

**Likely trigger:** a corrupt `storage.json` (e.g. left by the earlier
invisible/crashed sessions) → `storage.New` failed → `LogFatal` → `os.Exit(1)`
about one second after launch (the visible gap = the hidden PowerShell
fingerprint calls). The black window is the WebView mid-load when the process
dies.

**Fixes (make failures impossible to hide):**
1. `internal/storage/storage.go` — `New()` is now **self-healing**: an
   unreadable/corrupt `storage.json` is moved aside
   (`storage.json.corrupt-<unix-ts>`) and a fresh store is created; config-dir
   failures fall back to the OS temp dir. A bad JSON file can no longer brick
   startup.
2. `main.go` — `os.Stderr` and the standard logger are redirected to
   `%APPDATA%\myvpn\myvpn.log` (rotated at 1MB). Panics, `log.Fatal` and
   `log.Printf` output are now captured on GUI builds with no console.
3. `app.go` — `Startup` wraps its body in `recover()` (logs the panic to
   `myvpn.log` and keeps the window alive) and the storage failure path uses
   `LogError` instead of `LogFatal` (no more `os.Exit(1)`).
4. `main.go` — `wails.Run` errors still exit, but the message lands in
   `myvpn.log` instead of a null console.

**Diagnosis path for future Windows issues:** run the exe, then read
`%APPDATA%\myvpn\myvpn.log` — any panic stack or startup error will be there.

### Follow-up (2026-07-31) — exit-path instrumentation

With the log in place, a fresh CI build (run 42 — all jobs green incl. macOS)
still flashed and died with ONLY `MyVPN starting (version main)` in the log —
no panic, no `wails.Run` error. Since go-webview2 `log.Fatalf`s on WebView2
env/controller failure (which would have been logged), WebView2 init succeeded;
the death is either a native crash, a browser-process failure, or an external
kill (school-managed machines: AV/AppLocker). Added stage markers to the log:

- `DOM ready — webview loaded the UI` (new `OnDomReady` hook in `main.go`)
- `Startup complete (activated=...)` (end of `App.Startup`)
- `Shutting down MyVPN...` (`App.Shutdown` — present iff the app exited via
  the normal window-close path)

Combined with the Windows Event Viewer (Application log → "Application Error"
for `myvpn.exe`, showing the faulting module), the next run identifies the
exact dying stage.

### Follow-up 2 (2026-07-31) — WAILS v2.9.1 → v2.12.0 (go-webview2 crash fixes)

The instrumented log showed `MyVPN starting` → `DOM ready — webview loaded the
UI` and then **silent death**, with `Startup complete` and `Shutting down`
never logged — i.e. the process died right when the Vue app started calling
bound Go methods over the WebView2 JS↔Go IPC. Wails v2.9.1 bundles
`go-webview2 v1.0.10` (2023-10), which predates the upstream crash fixes:

| go-webview2 | Fix |
|-------------|-----|
| v1.0.12 | infinite recursion fix |
| v1.0.13 | overlapped I/O error on long JS scripts |
| v1.0.16 | **panic when sending long data from JS to Go** |
| v1.0.19 | COM error handling |
| v1.0.20/21 | **random crashes** |

**Fix:** upgraded `github.com/wailsapp/wails/v2` **v2.9.1 → v2.12.0** (needs
only Go 1.22, so CI is unchanged) which bundles **go-webview2 v1.0.22** with
all of the above. Verified: Windows build (real tags), golangci-lint 0 issues,
`go vet`, `go test` all green. The darwin `UTType` shim and the linux
`webkit2_41` tag remain required in v2.12.0 (confirmed in its source).

---

## SERVER-SIDE — STALE/EMPTY tier_configs REJECTS CLIENTS (2026-07-31)

**Symptom:** with the app running (as admin), Connect worked but the tunnel
died instantly: `inbound/tun[tun-in]: ... wsasend: An existing connection was
forcibly closed by the remote host` (both upload and download) to
`114.23.136.59:8445`. TCP connects fine (all ss ports open) — the ssserver
**rejects the Shadowsocks handshake**: wrong password/method.

**Investigation (verified from outside):**
- DNS: `networkingguides.duckdns.org` → **114.23.136.59** (older docs say
  .47 — the VPS IP changed; docs updated).
- All three tier passwords from `secrets.env.age` were tested against the live
  ss servers with a real sing-box client — **all three authenticate**
  (HTTP 200 through the tunnel).
- **Correction (with SSH access, the story simplified):** the live
  `tier_configs` collection was **correct all along** — all three records
  match `/etc/shadowsocks/*.json` exactly, one record per tier. The earlier
  "empty collection" conclusion was wrong: the collection's API rules are
  admin-only (`@request.auth.admin = true`), so unauthenticated list/create
  requests returned misleading empty/generic-400 responses.
- **The real problem is the CLIENT's stale stored config**: the device was
  activated on the PREVIOUS PocketBase instance (before the Jul 28 re-setup
  replaced the data dir and rotated the ss passwords). The current client
  never refreshes stored connection parameters:
  - the heartbeat hook returns `server_config`, but the client ignored it,
  - re-activation short-circuits client-side ("Already activated" returns
    before any server call),
  - so the app kept connecting with an old password → ssserver RST.

**Fixes (repo + deployed to VPS):**
- `v5/server/pb_hooks/activation.pb.js` + `heartbeat.pb.js` — always pick the
  **newest** `tier_configs` record; the "Already activated" response now
  includes `server_config`. NOTE: `{:param}` binding is unreliable in
  `findRecordsByFilter` on PB 0.22 — filters use sanitized inline values.
- `v5/server/scripts/seed-pb.py` — tier seeding is an **idempotent upsert**
  (delete older duplicates, PATCH existing or POST new).
- `v5/server/scripts/fix-tier-configs.py` — repair script for the VPS
  (ground-truth passwords from `/etc/shadowsocks/*.json`).
- Client (`v5/client/app.go`): heartbeat responses now **apply
  `server_config`** (self-heal within one heartbeat once a client with this
  fix runs).
- Client (`v5/client/internal/manager/process.go`): sing-box stderr captured
  and surfaced in Connect errors ("TUN interface creation was denied — run
  MyVPN as administrator: ... Access is denied.").

**Live-VPS actions performed (2026-07-31, authorized):** deployed both hooks
to `/opt/pocketbase/pb_hooks/` (+ `/root/server/pb_hooks/`), restarted
PocketBase, verified the heartbeat route serves the correct `server_config`
(end-to-end test with a real code: eco → `networkingguides.duckdns.org:8443`
+ matching password). Client unblock: run the new build (heartbeat refresh)
or delete `%APPDATA%\myvpn\storage.json` and re-enter the activation code.

---

## WINDOWS RUNTIME — CONNECT SPINS FOREVER (Process.Signal(0) BROKEN) (2026-08-01)

**Symptom:** after the server fix, Connect starts sing-box (correct password,
TUN created) but the button spins for minutes with no new log lines — while
the tunnel actually runs in the background.

**Root cause:** the manager checked process liveness with
`cmd.Process.Signal(syscall.Signal(0))` everywhere (startup probe, IsRunning,
State, healthLoop, already-running check). On **Windows**, `Process.Signal`
only supports `Kill` (TerminateProcess) — any other signal returns
`syscall.EWINDOWS` ("not supported by windows") — verified in Go's
`src/os/exec_windows.go`. So every check reported "dead":
- the 500 ms startup probe concluded sing-box exited, called `cmd.Wait()`,
  which **blocks while sing-box is alive** → `Connect()` never returns → the
  UI spinner runs forever;
- closing the app then spawned a second `cmd.Wait()` in `stopLocked`, which
  errors immediately ("Wait was already called"), skipping the kill → an
  **orphaned sing-box.exe** kept running.

**Fix (`v5/client/internal/manager/process.go`):** replaced ALL signal-based
liveness checks with a cross-platform **exited channel**: a goroutine owns
`cmd.Wait()` and `close(exited)` when the process exits; `processAlive()`
selects on that channel. Applied to the startup probe (select vs 500 ms
timer), IsRunning, State, healthLoop (incl. the restart path — each restarted
process gets a fresh channel), stopLocked (waits on the existing channel; no
second Wait), and Start's already-running check.

**Tests added (`internal/manager/process_test.go`):** `TestLifecycle` (start →
running → auto-exit detected → crashed → stop → stopped) and
`TestImmediateExit` (probe surfaces sing-box stderr, e.g. "Access is denied").
Both pass. Windows build, golangci-lint, vet all green.

**Note for users of the previous build:** after closing the hung app, kill any
leftover `sing-box.exe` in Task Manager before running the new build.

---

## WINDOWS RUNTIME — CONNECTS BUT NO TRAFFIC (dial i/o timeout) (2026-08-01)

**Symptom:** with the fixed build, the UI reports Connected, but
whatismyip.com still shows the real IP and sing-box logs repeated
`inbound/tun[tun-in]: dial tcp 114.23.136.59:8445: i/o timeout` (5 s dial
timeouts every ~60-130 s). The heartbeat (direct, not via TUN) still works.

**Server verified healthy from an outside host:** all three ss ports open;
strike password authenticates (HTTP 200 through the tunnel in ~60 ms).
So the server is NOT the problem.

**Most likely cause — stale host state, not config:** the same sing-box config
reached the server in an earlier session (RST after connect = connection
established). The sessions in between (old builds with the broken shutdown)
**orphaned sing-box.exe processes and left the `myvpn0` TUN adapter with
stale routes/WFP filters**; a fresh sing-box then routes its own dial into the
dead TUN state → timeout. (The config already sets
`route.auto_detect_interface: true`, which binds the outbound to the physical
interface — so a clean host should not loop.)

**Diagnostics added (`app.go`):** the report now includes
`Server: <addr> reachable|UNREACHABLE (...)` — a 3 s TCP dial to the configured
server from the app (before/without the tunnel). This distinguishes:
- `UNREACHABLE` ⇒ the network blocks the ss port (e.g. different WiFi);
- `reachable` while the tunnel still times out ⇒ stale TUN/routes on the host
  (cleanup below).

**User-side cleanup (one-time, after the old builds):**
1. Close MyVPN; in Task Manager end ALL `sing-box.exe` processes.
2. As admin: `netsh interface show interface` → find `myvpn0` →
   `netsh interface delete interface myvpn0` (if present).
3. Reboot (clears routes + WFP filters), then Connect again.
4. Check Diagnostics: `Server: … reachable` + `Engine: running`.

### Follow-up — strict_route disabled (2026-08-01)

New diagnostics (VPN ON) showed the app's OWN dial failing at
`lookup networkingguides.duckdns.org: i/o timeout` — i.e. with the TUN up,
**DNS and everything else through the tunnel dies**, while direct Shadowsocks
(Hiddify, no TUN) works and the server is verified healthy. The prime suspect
was `strict_route: true` in the generated sing-box config: on Windows it
installs WFP filters that "strictly block all connections not from the TUN" —
a misfiring filter also blocks sing-box's own outbound and DNS, which matches
"connects but cuts out the internet" exactly.

**Fix:** `generateConfig` now sets `strict_route: false` (omitted from JSON;
`auto_route` + `auto_detect_interface` remain — still a full tunnel). If the
tunnel still fails after the cleanup+reboot, set `MYVPN_DEBUG=1` before
launching (switches sing-box to debug logging) and send the log — it shows the
dial's interface binding and route decisions.

### Follow-up 2 — DNS loop: final must be dns-tunnel (2026-08-01)

After disabling strict_route the tunnel egressed (ping 1.1.1.1 worked) but
**domains never resolved**. Root cause: `generateConfig` set
`dns.final = "dns-direct"`, whose server had `detour: "direct"` — the direct
outbound's DoH traffic is routed back into the TUN by auto_route → **DNS loop**
("dial tcp: lookup …: i/o timeout" while the tunnel itself was fine). This
also explains "the app has never worked": the proxy outbound was fine all
along; DNS was always looping.

**Fix:** `dns.final` is now `"dns-tunnel"` (DoH via the default proxy outbound
→ through the tunnel); `dns-direct` is kept but unused. Verified: Windows
build, lint, vet, manager tests green; docs (ARCHITECTURE.md, CLIENT-GUIDE.md)
updated to match.

### Follow-up 3 — DNS query loopback: explicit detour required (2026-08-01)

With `final: dns-tunnel` (no detour), sing-box flooded
`DNS query loopback in transport[dns-tunnel]` for every query. Verified in
`sing-box v1.10.0 route/router.go`: a DNS server with an **empty detour**
dials via `dialer.NewRouter(router)` — the transport's own connection is
re-routed by the route rules back into the DNS handler, and sing-dns's
context-based loop detection fires (`sing-dns client.go`).

**Fix:** `dns-tunnel` now has an explicit **`detour: "proxy"`** — the DoH dial
goes straight through the Shadowsocks outbound (`dialer.NewDetour`), bypassing
the router entirely. `TestGeneratedConfig` asserts the detour; all tests,
Windows build, lint, vet green.

### Follow-up 4 — THE missing piece: `sniff: true` on the TUN inbound (2026-08-01)

Verified empirically on the VPS (root, real TUN): without sniffing, DNS
queries were routed to the **shadowsocks outbound** (`outbound connection to
10.0.0.2:53` via proxy) — the `{"protocol": "dns", "outbound": "dns-out"}`
rule NEVER matched. Source: `route/router.go:856` gates ALL sniffing (which
sets `metadata.Protocol` for rule matching) behind
`metadata.InboundOptions.SniffEnabled` — i.e. the TUN inbound's `"sniff": true`
option. Every official sing-box TUN example includes it; our config never did
— this single omission explains every DNS failure variant (direct detour loop,
transport loopback, and the plain "no DNS at all" behavior).

With `"sniff": true` the VPS test confirmed the full interception pipeline:
`sniﬀed packet protocol: dns` → `match protocol=dns => dns-out` →
`dns: exchange example.com`. (The ss leg could not be validated on the VPS
itself — a TUN client on the ss server host captures the server's own egress —
but the tunnel was already proven working from remote hosts.)

**Fix:** TUN inbound now sets `"sniff": true`. `TestGeneratedConfig` asserts
it. Windows build, lint, vet, tests green; docs updated.

### Follow-up 5 — server-domain resolution loop (sing-box issue #2207) (2026-08-01)

Even with sniff + detour, every query failed instantly with
`DNS query loopback in transport[dns-tunnel]`. Root cause (confirmed via
sing-box issue #2207, closed as fixed upstream but still present in v1.10):
the Shadowsocks outbound's server is the **domain**
`networkingguides.duckdns.org` — when the DNS transport dials the DoH via the
proxy, the proxy must resolve that domain, and the resolution re-enters the
DNS system → the transport's own context (tagged `dns-tunnel`) hits the loop
detection in sing-dns.

**Fix (the reporter-confirmed workaround):** a DNS rule sending ALL
sing-box-initiated (outbound) resolution DIRECT:
`{"outbound": ["any"], "server": "dns-direct"}`. Plus a route rule excluding
the resolved VPN-server IP from the tunnel (`ip_cidr: <server>/32` → direct;
resolved at config generation, before the TUN exists) so a captured ss
connection egresses physically instead of looping.

**VPS validation (root, real TUN, exact config):** `sniffed protocol: dns` →
`match => dns-out` → exchange; DoH routed via the ss outbound; the server
domain resolved DIRECT (`lookup succeed: 114.23.136.59`, NOERROR) — **zero
loopback errors**. (The ss leg itself can't be validated on the VPS — the TUN
on the ss-server host captures the server's own egress — but the tunnel is
proven working from remote hosts.)

### Follow-up 6 — bind_interface: force the ss dial onto the physical NIC (2026-08-01)

With the DNS pipeline fixed, every exchange still timed out at 10 s — the DoH
through the tunnel never completes on the user's Windows machine, while the
same path works from remote hosts in ~350 ms (verified: DoH query through the
tunnel returns HTTP 200 + a valid DNS response) and Hiddify (no TUN) works on
the same machine. Conclusion: with the TUN up, sing-box's OWN Shadowsocks
dial is still being captured by auto_route on Windows — `auto_detect_interface`
is not sufficient there.

**Fix:** the proxy outbound now gets an explicit **`bind_interface`** — the
physical NIC is detected at Connect time, BEFORE the TUN exists
(`interface_windows.go`: hidden PowerShell `Get-NetRoute` for the 0.0.0.0/0
alias; `interface_unix.go`: `ip route show default`). A socket bound to the
physical interface cannot be captured by the TUN, so the ss connection always
egresses directly. `app.go` Connect passes the detected interface into
`manager.Config.BindInterface`; `generateConfig` emits
`"bind_interface"` on the shadowsocks outbound.

### Follow-up 7 — sing-box 1.10.0 → 1.12.1: THE fix (2026-08-01)

Despite every config safeguard (sniff, DNS detour, outbound:any rule,
server-IP rule, bind_interface), the ss connection STILL timed out with the
TUN up — on both the user's Windows machine AND the Linux VPS. Meanwhile
**Hiddify (also sing-box + TUN + Shadowsocks) worked on the same machine** —
its core runs a much newer sing-box.

**Validation on the VPS with sing-box 1.12.1** (root, real TUN, the 1.12
config format): `curl https://example.com` through the tunnel returned
**HTTP 200 in 84 ms** — the first end-to-end success ever, even in the
self-host scenario.

**Changes:**
- Engine bumped **1.10.0 → 1.12.1** (`.github/workflows/build.yml`,
  `engines/README.md`). 1.11 added "Improve tun compatibility" (3 fixes);
  1.12 refactored the DNS servers and route rules.
- Config rewritten for the 1.12 format: DNS servers use
  `{"type": "https", "server": "1.1.1.1", "server_port": 443, "detour": ...}`
  (no more `"address"`), the `dns-out` special outbound is gone (replaced by
  the route rule action `{"protocol": "dns", "action": {"type": "dns"}}`), and
  the server-IP exclusion uses `{"action": {"type": "route", "outbound":
  "direct"}}`.
- `TestGeneratedConfig` updated and passing; Windows build, lint, vet green.

### Follow-up 8 — REMOVED bind_interface: align with Hiddify exactly (2026-08-01)

The 1.12 config parsed and ran, but the ss dial STILL timed out with
`bind_interface: "Wi-Fi"`. Evidence review: the ONLY session where the ss dial
reached the server (23:35) had NO bind; Hiddify (works on the same machine)
uses NO bind — only `auto_detect_interface`; and sing-box's Windows interface
name resolution for `bind_interface` is unverified (an unresolvable name
silently breaks every dial — the exact observed symptom).

**Fix:** `bind_interface` is REMOVED from both outbounds. The direct outbound
is kept non-empty via `"connect_timeout": "10s"` (1.12 rejects DNS detours to
empty direct outbounds). The generated config now matches the Hiddify pattern:
no binds, `auto_detect_interface`, `default_domain_resolver`, `hijack-dns`
action, sniff, DoH through the tunnel. Verified: parses and runs on the VPS
(process alive, TUN up, zero FATAL), tests pass, Windows build + lint green.

### Follow-up (2026-07-31) — macOS link failure: missing `UniformTypeIdentifiers` framework

Linux/Windows builds then passed, but both macOS matrix jobs failed at the
final link with:

```
Undefined symbols for architecture arm64:
  "_OBJC_CLASS_$_UTType"
ld: symbol(s) not found for architecture arm64
```

Wails v2.9.1's darwin code (`WailsContext.m`) uses `UTType` but only links
`-framework Foundation -framework Cocoa -framework WebKit`. On older SDKs,
UniformTypeIdentifiers was re-exported transitively and the symbol resolved
implicitly; **Xcode 26 / macOS 26 SDK removed that implicit linkage**, so the
link broke. (Checked: v2.13.0 does not add the framework either — and it
requires Go 1.25, so bumping Wails was not an option on Go 1.22.)

**Fix:** added `v5/client/darwin_link.go` — a `//go:build darwin` cgo shim in
the main package with `#cgo LDFLAGS: -framework UniformTypeIdentifiers`. cgo
flags from the main package are passed to the final link, so this fixes both
macOS architectures and both `wails build` and manual `go build` paths. No
workflow change needed.

---

### Follow-up 9 — REMOVED udp_over_tcp: sing-box's UDP-over-TCP is proprietary (2026-08-01)

**Symptom:** client says Connected and DNS resolves, but pages hang ("nothing
loads properly"). The log shows successful DNS exchanges and DoH through the
tunnel (`outbound/shadowsocks[proxy]: outbound connection to 1.1.1.1:443`),
but every UDP/QUIC flow dies:

```
outbound/shadowsocks[proxy]: outbound UoT packet connection to 142.251.12.84:443
connection: packet download closed: read tcp ...->114.23.136.59:8445:
  wsarecv: An existing connection was forcibly closed by the remote host.
```

(Repeated ~300ms apart — Chrome/Edge QUIC retry storm. Hiddify mobile VPN
mode and Hiddify desktop System Proxy work with the same server; Hiddify
desktop VPN mode fails the same way.)

**Root cause:** `generateConfig` emitted
`"udp_over_tcp": { "enabled": true, "version": 2 }` for tiers with
`udp_relay` (Strike). But sing-box's UDP-over-TCP is a **proprietary SagerNet
protocol** — magic domains `sp.udp-over-tcp.arpa` (v1) / `sp.v2.udp-over-tcp.arpa`
(v2) — **not** the Shadowsocks SIP003 UDP-over-TCP. The deployed server is
shadowsocks-rust, which does not implement it and RSTs every UoT connection.
The TCP data path was fine all along; the failure was dead UDP plus browser
QUIC retry loops. (This was also observed earlier the same day in
`seed-live.py`'s notes: "every UoT conn RST after ~300ms".)

**Fix:**
- `v5/client/internal/manager/process.go` — `udp_over_tcp` is no longer
  emitted for any tier. UDP goes **raw** (standard ss UDP; Strike server runs
  `tcp_and_udp`), works wherever the network allows UDP, and browsers fall
  back to TCP where it doesn't (N4L school WiFi). `UDPOverTCPConfig` type and
  `Outbound.UDPOverTCP` field removed; `Config.UDPRelay` is now informational
  only.
- `v5/server/scripts/seed-pb.py`, `fix-tier-configs.py` — strike seeds
  `udp_relay: false` (it historically flipped on client UoT). `seed-live.py`
  already had this.
- **Live DB (pending):** tier_configs → strike must have `udp_relay: false`.
  Clients self-heal via the heartbeat server_config refresh within one beat
  (no rebuild needed for existing installs).
- Revisit UoT only if a **sing-box server** is deployed for a tier.

---

### Follow-up 10 — Pre-flight guard: refuse a second sing-box on the same TUN (2026-08-01)

**Symptom:** after the UoT fix, the client still misbehaved ("a variety of
issues"). The log showed TWO engine startups in one session:

```
23:55:42 sing-box log level: debug          ← leftover sing-box.exe (orphaned
23:55:45 sing-box log level: debug          ← fresh Connect spawn
         inbound/tun[tun-in]: started at myvpn0   ← ×2 — two instances, one TUN
```

**Root cause:** orphaned `sing-box.exe` processes from earlier broken builds
(and/or other VPN apps like Hiddify) survive on Windows. A fresh Connect then
spawns a second engine; both instances share the `myvpn0` wintun adapter —
packets are delivered to both, routes fight, and failures look random.

**Fix (`v5/client/internal/manager/`):**
- New `foreignSingBoxRunning()` (Windows: `tasklist /FI "IMAGENAME eq sing-box.exe"`;
  Unix: `pgrep -x sing-box`) — true when an untracked sing-box process exists.
- `Start()` now refuses with a clear message ("another sing-box process is
  already running — close it (Task Manager) and retry") instead of stacking a
  second engine. Only runs on user-initiated Connect (never in the health-loop
  restart path, and never when our own process is tracked and alive).
- Note: this also blocks Connect while Hiddify's sing-box core is running —
  intentional (two TUN VPNs at once is invalid), the message says so.

**One-time user cleanup (still required for machines with orphaned engines):**
1. Task Manager → end ALL `sing-box.exe`.
2. `netsh interface show interface` → if `myvpn0` exists:
   `netsh interface delete interface myvpn0` (admin).
3. Reboot (clears stale routes/WFP filters).

---

## VPS TESTING — ISSUES FOUND & FIXED (2026-07-26)

These were discovered during real deployment to a Voyager VPS (Ubuntu 22.04)
and are now fixed in `v5/server/`.

### S1. Caddy Systemd Service Missing

| Severity | 🔴 Caddy wouldn't start |
|----------|------------------------|
| **File:** | `v5/server/modules/05-caddy.sh` |
| **Issue:** | The module downloaded a custom Caddy binary with rate_limit support, but never created a systemd service file. `systemctl restart caddy` failed with "Unit not found." |
| **Fix:** | Added `create_systemd_service()` that: creates `caddy` user if missing, creates `/var/log/caddy` and `/var/www/html`, writes a proper systemd unit file, and grants `cap_net_bind_service+ep` to the binary so it can bind :80/:443 as non-root. |

### S2. Caddy TLS Certificate Provisioning Failed

| Severity | 🔴 HTTPS broken |
|----------|-----------------|
| **File:** | `v5/server/modules/05-caddy.sh` |
| **Issue:** | Caddy ran as user `caddy` which had no home directory. Caddy tried to write TLS storage to `/home/caddy/.config/caddy/` but got "permission denied." |
| **Fix:** | Removed `ProtectSystem=full` from the service file. Created `/var/lib/caddy` for Caddy's runtime data. Added `Environment=XDG_CONFIG_HOME=/var/lib/caddy` and `Environment=XDG_DATA_HOME=/var/lib/caddy` so Caddy stores TLS certs in a writable location. |

### S3. PocketBase Service Failed with NAMESPACE Error

| Severity | 🔴 PocketBase wouldn't start |
|----------|------------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The service had `ProtectHome=true`, `ProtectSystem=full`, and `PrivateTmp=true`. On this VPS kernel (5.15.0-161-generic), these caused `status=226/NAMESPACE` errors — the kernel rejected namespace operations. |
| **Fix:** | Removed all systemd security hardening directives (`ProtectHome`, `ProtectSystem`, `ReadWritePaths`). The service now uses only `NoNewPrivileges=true` and `PrivateTmp=true`. |

### S4. Admin Token Extraction Failed (Shell Escaping)

| Severity | 🟡 Admin created but collections not seeded |
|----------|---------------------------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The admin creation API response was piped through `echo "$RESP" | python3 -c "..."` which broke when the JWT contained special characters (dots, dashes). The token came back empty, so the script thought "Admin created but could not extract token." |
| **Fix:** | Wrote the API response to a temp file before parsing. Also added a fallback auth flow (`/api/admins/auth-with-password`) if token extraction fails. Then refactored the entire bootstrap into `scripts/seed-pb.py` which uses Python's `json.dump()` to write temp files for all API calls, avoiding shell escaping entirely. |

### S5. Collection Creation Failed with Bash Heredocs

| Severity | 🟡 Collections not created |
|----------|---------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The shell heredocs (`cat > file << 'JSON'`) for the collection schema JSON were inconsistently parsed when passed through SSH. The JSON contained nested quotes and special characters that bash mangled. |
| **Fix:** | Moved all collection creation logic into `scripts/seed-pb.py`. Python writes the JSON payload to a temp file using `json.dump()`, then passes `@file` to curl. This is bulletproof — no shell escaping issues. |

### S6. `generate_codes.sh` Bash Bug (!$double)

| Severity | 🔴 Code generation produced errors |
|----------|-----------------------------------|
| **File:** | `v5/scripts/generate_codes.sh` |
| **Issue:** | The Luhn-mod-N checksum used `double=!$double` to toggle a boolean. In bash, `!$double` triggers history expansion (or fails with `!false: command not found` when history expansion is off). |
| **Fix:** | Replaced with `if $double; then double=false; else double=true; fi`. |

### S7. PocketBase 0.22 JS Hook Compatibility

| Severity | 🔴 Activation endpoint returns 400 |
|----------|-----------------------------------|
| **File:** | `v5/server/pb_hooks/activation.pb.js`, `heartbeat.pb.js`, `admin_unbind.pb.js` |
| **Issue:** | PocketBase 0.22.21 changed the JavaScript VM API. The hooks were originally written for the 0.21 API (`$app.dao()`, `$apis.requestInfo()`). On 0.22.21, `$app.dao()` still works via compatibility shim, but `$app.dao().db().exec()` and `$app.dao().findRecordsByFilter()` with `{:param}` syntax had degraded behavior. |
| **Fix:** | **✅ Fixed 2026-07-26.** Hooks updated to use working API patterns for PocketBase 0.22.21: |
| **Changes made:** | • `$apis.requestInfo(c).data` → `$apis.requestInfo(e).data` (compat shim still works)<br>• `$app.dao().db().exec()` → `$app.dao().db().newQuery().execute()`<br>• `$app.dao().findRecordsByFilter()` with `{:param}` → `$app.dao().findFirstRecordByData()` for single-record lookups<br>• `$app.dao().findCollectionByNameOrId()` + `new Record()` + `$app.dao().saveRecord()` for creating records<br>• `e.remoteIP` → `e.request().remoteAddr.split(":")[0]` (remoteIP not exposed in 0.22.21 router events)<br>• `const`/`let` → `var` (goja compatibility)<br>• Helper functions must be defined inside the `routerAdd` callback (goja doesn't hoist declarations into callback scope)<br>• Rate limiting filter: inline string with sanitization instead of `{:param}` syntax (filter parser changed)<br>• `sqlite_pragmas.pb.js` removed — `on()` hook API removed in 0.22, WAL mode set automatically by PocketBase |

### S8. Smoke Test IP Detection Off by One

| Severity | 🟡 False positive warning |
|----------|---------------------------|
| **File:** | `v5/server/scripts/smoke-test.sh` |
| **Issue:** | The smoke test detects the VPS IP using `ip route show default | awk '{print $3}'` which returns the **gateway** IP (e.g., `114.23.136.1`), not the interface IP (`114.23.136.47`). The DNS match check then fails even when DNS is correct. |
| **Fix:** | Changed to `ip -4 addr show | grep -oP 'inet \K[0-9.]+' | grep -v '^127\\.' | head -1` to get the actual interface IP. |

---

### S9. Secrets Decryption via Process Substitution Fails Silently

| Severity | 🔴 All SS passwords auto-generated instead of using stable secrets |
|----------|-------------------------------------------------------------------|
| **File:** | `v5/server/setup.sh`, `v5/server/restore.sh` |
| **Issue:** | Both `setup.sh` and `restore.sh` used `source <(age -d ...)` (bash process substitution) to load decrypted secrets. In non-interactive SSH sessions — specifically when running via `ssh root@host "/root/server/setup.sh"` — the process substitution would appear to succeed (exit 0, "✓ Secrets decrypted" printed) but **the variables would not actually be loaded into the current shell**. This caused all downstream modules to fall through to auto-generated passwords instead of using the stable credentials from `secrets.env.age`. The symptom: `DOMAIN` was empty despite "Secrets decrypted" having printed, causing a hard fail at the DOMAIN check. |
| **Root cause:** | `source <(command)` is a bash extension that works unreliably in certain SSH environments. The process substitution forks a subprocess, and `source` may read from an already-closed pipe under some shell configurations. Ubuntu's `/bin/sh` is dash, which does not support process substitution at all — but even under bash the behavior was inconsistent. |
| **Fix:** | Replaced `source <(age -d ...)` in both files with a two-step temp-file approach: `age -d ... > /tmp/.secrets-$$.env` followed by `source /tmp/.secrets-$$.env`. This is POSIX-compatible, works in all shell environments, and is easy to debug (the temp file can be inspected). Also added an `age` binary check + auto-install via `apt-get` before decryption, so a fresh VPS without `age` pre-installed will still work. |
