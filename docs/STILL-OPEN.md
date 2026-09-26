# Still open after this work

What I did **not** finish, and what a successor should know before picking it up.
Ordered by whether I could have validated it here.

---

## Open, and blocked in this environment

### UoT has never carried a real game session

I flipped `ENABLE_UOT` to default-on, and another agent enabled it on the live
host. The transport is proven — a SOCKS5 UDP ASSOCIATE through both the raw 8445
and UoT 8446 paths returns a DNS answer, and the server log shows
`inbound UoT connection to 8.8.8.8:53`.

What is **not** validated is Strike's actual gaming promise: no real game has
ever been run on a school network. Treat "gaming tier" as design intent until
someone does that. If you have access to a school network and a game, this is the
single highest-value thing left.

### Linux TUN elevation — **not a gap; only unvalidated** (corrected 2026-09-26)

**This section previously said "there is no elevation path on Linux". That was true
of the retired Wails client and is FALSE of `client/`.** It was carried over from
`docs/ENGINE-SWAP-ANALYSIS.md` without re-checking the fork — the exact failure mode
this repo keeps re-learning (`docs/README.md` rule 3: the code wins).

The shipping fork is built on Clash Verge Rev, so it **inherits** Verge's elevation
path and needs no helper binary of its own. The chain is:

```
pkexec (sudo fallback)  ->  clash-verge-service-install  ->  root service
                        ->  service runs mihomo as root   ->  TUN works
```

- `crates/…/clash_verge_service_ipc` → `management.rs::elevate()` runs the installer
  under **`pkexec`**, falling back to **`sudo`** when it is absent or exits 127.
  `utils/help.rs::linux_elevator()` probes for `pkexec`. (macOS: `osascript … with
  administrator privileges`; Windows: `Start-Process -Verb RunAs`.)
- `core/service.rs::install_service()` → `invoke_service_install()` →
  `clash-verge-service-install`; the service stages and runs the core from its own
  administrator-approved directory (`stage_approved_core`, digest-pinned).
- `core/runstate/health.rs::tun_capable()` is `self.is_admin || self.service_usable()`,
  with tests `tun_is_capable_when_elevated_even_with_no_service` and
  `tun_is_capable_via_a_ready_service_without_elevation`.

This is the same mechanism that made Clash Rev Meta work in the original 2026-08-03
school test — which is why the project rebuilt on Verge rather than repairing the
retired client (whose hand-rolled elevation was buggy three times, FIXES #17/#40/#41,
and is deliberately not ported: `client/docs/LOGIC-INVENTORY.md` §10).

**The only thing open is running it** — nobody has exercised that chain on real Linux
hardware in a non-root session. See item 4 under "fixable now", which is the same
finding stated as a to-do.

---

## Open, and fixable now

### The client works; a real update has never been INSTALLED

**This section used to say the client port had not started. It has, and it shipped
as 3.0.0.** `client/` has `src-tauri/src/locus/` with activation, device
fingerprinting, a heartbeat, tier→config translation and a hub-mediated updater,
plus a first-run activation gate and a single Connect/Disconnect.

Verified against the **live hub**, not only by unit tests:

- the Luhn checksum agrees with the deployed server (a valid code reaches its
  database lookup; a checksum-broken one is refused as invalid);
- `/api/code-lookup`'s real response deserialises into the client's types, and
  `bound_other` is classified as "not ready";
- a generated tier config is accepted by the real `mihomo` binary;
- **traffic egresses from the VPS** (`170.64.196.179`) rather than the local
  address, and a 5 MB download moved the server's `rx_bytes` by 5,135,262;
- a real heartbeat returns the strike tier's `uot_port` and `udp_relay`;
- **release 3.0.0 is published**: CI built and signed all four platforms, the hub
  fetched and verified them, and both `/api/update` and the heartbeat offer it with
  a signature that verifies against the key compiled into the client.

**What is genuinely still open:**

1. **No client has ever INSTALLED an update.** Every stage is verified — build,
   sign, fetch, publish, serve, and the signature check the install path performs —
   but the installer handoff itself needs Windows or macOS hardware. This is the
   single most valuable thing left to do.
2. **The client has only been RUN on Linux.** Windows and macOS are built and
   signed by CI but have never executed on real hardware. Given four
   Windows-specific CI bugs already found from Linux, expect runtime surprises.
3. **A mid-session tier CHANGE has never been exercised.** The code path exists
   (`store::tier_changed`) but no heartbeat has carried a changed tier in practice.
4. **The Linux TUN elevation path is untested** — the fork inherits Verge's
   pkexec→sudo escalation and service, but nobody has run it. Linux is ~2% of
   clients and last in priority.

### ~~The `proxies` page is still in the client~~ — REMOVED in 3.1.0 (2026-09-26)

Resolved by deletion rather than replacement. `pages/proxies.tsx` and the whole
`components/proxy/` tree (~4,900 lines) are gone, and `proxies` is no longer a
navigation entry.

The one genuinely useful thing in there — **live latency**, which a student asking
"is my connection good?" wants — was *not* carried over, and that is the gap this
section leaves behind. The Connection screen shows live up/down speed and the traffic
graph, which answer "is it working?" but not "is it fast?". A latency readout is still
worth adding, and `test_delay` still exists to back it.

### ~~Deferred: the Verge UI a student still meets~~ — DONE in 3.1.0 (2026-09-26)

**This entire section is superseded.** It measured the problem — "8 of ~203 front-end
files mention Locus at all" — and posed four open questions. All four were answered and
the work is finished:

| Surface | 3.1.0 state |
|---|---|
| Activation gate | Locus, rebranded (`UI-AESTHETICS.md` palette) |
| Nav | **two tabs: Connection and Account** |
| Proxies / Logs / Settings | **deleted** — 69 files, ~17,600 lines |
| Home cards | replaced by the Connection screen |
| Locale text | zero Verge product-name strings in all 13 languages |

Answers, for the record:

1. **Node selection does not exist.** One server per tier, so the ~4,900-line proxy
   tree went. Nothing was kept "in case".
2. **Logs was deleted rather than replaced.** A "Report a problem" affordance that
   gathers what support needs is still the better answer, and is now the *only* gap
   this section leaves behind — a student with a problem has the Account screen's
   device ID and nothing to send with it.
3. **Entitlements vs internals:** TUN-on, sysproxy, core choice, ports and DNS are
   internals with **no UI at all**. Locus decides them. Only language, theme,
   update-check and refresh became student-facing, inside Account.
4. **The home cards did not shrink — they were deleted.** Live speed, the traffic graph
   and the connection state live on Connection; the session totals live on Account.

What replaced the connective tissue: `components/connection/use-connection.ts` (the
tunnel state machine), `use-traffic-summary.ts` (one place the traffic numbers are
computed) and `tier-badge.tsx`. The backend was not touched — profiles, config
generation, `enhance`, the service/sidecar decision and `locus::tier` all still drive
`locus_connect` exactly as before.

**Still missing from this area:** there is no Locus logo asset in the repo.
`src-tauri/icons/*` is the Clash Verge Rev mark; `UI-AESTHETICS.md` §4 asks for a 48×48
`#2EA86A` shield. The activation screen shows the wordmark alone rather than
substituting the old logo, deliberately — drawing a brand mark needs a human decision.

### Fork versioning — RESOLVED (2026-09-26)

This was an open decision; it is settled.

**The client is 3.0.0**, its own Locus line, not Clash Verge's `2.5.5`. The
reasoning is worth keeping, because the trap was real:

```
2.5.5 vs 2.2.6 -> strictly newer? true     (publishing 2.5.5 moves the fleet up)
2.2.9 vs 2.5.5 -> strictly newer? false    (after which every 2.2.x is REFUSED)
```

The client reported the version it inherited from Verge while the hub served
2.2.x. Publishing the inherited number would have pushed the fleet above the hub's
numbering permanently, with no server-driven downgrade to recover from — a manual
reinstall per device would be the only fix.

**Three version sites must agree**:
`client/src-tauri/Cargo.toml`, `client/package.json`,
`client/src-tauri/tauri.conf.json`. That agreement is enforced by
`client/src-tauri/tests/version_consistency.rs`, which reads them from disk. Drift
is a correctness dependency of the updater, not hygiene: a build whose reported
version disagrees with its bundled one either refuses the update that would fix it
or silently declines every release, and neither says so.

**No root `VERSION` file.** The fork versions itself in its own manifests.

**Releases are built by CI** — see `docs/RELEASING.md` and `docs/UPDATE-SYSTEM.md`.
Tag → CI builds, signs and releases → the hub fetches → publishes.

### ~~`publish-update.sh` (repo root) has two real defects~~ — RESOLVED BY RETIREMENT (2026-09-23)

The script is now `legacy/publish-update.sh.broken`, with both defects documented
in its header. It was never live: nothing has been published through it, and
`update_config` was at rollout 0 with empty URLs.

1. **It writes no `sha256_<platform>` columns.** Verified: zero matches. It emits
   an `assets: {…}` object, which is not an `update_config` column.
2. **Its macOS URLs point at filenames CI does not produce.**
   `locus-macos-amd64` / `locus-macos-arm64` vs CI's `locus-darwin-amd64` /
   `locus-darwin-arm64` → 404.

`publish-release.sh` is the correct path and does verify served bytes.

### ~~Correct the `internal/updatecfg` doc comment~~ — DONE (2026-09-23)

It stated that `publish-update.sh` writes `update_linux`/`update_windows` while
`publish-release.sh` writes `download_*`, causing every platform to fetch the
Linux binary. **That was not accurate**: both scripts write `download_*`, and
`publish-update.sh` contains zero `update_linux`/`update_windows` occurrences.

Corrected in three places: the `internal/updatecfg` package comment, the
`updatecfg_test.go` header, and `FIXES.md` #31 (which carried the same claim).
`publish-update.sh` itself is retired to `legacy/publish-update.sh.broken` with
the correction in its header. The package remains a valid shared contract
definition — only the stated cause of the bug was wrong.

### Delete my backup files once the guard is confirmed good

```
/root/data.db.pre-updateconfig-fix-1789795235
/root/admin_console.pb.js.pre-guard-1789795273
/root/admin_console.pb.js.staged-pre-guard-1789795273
```

---

## Things I checked and deliberately left alone

Recording these so they are not re-investigated:

- **`shadowsocks-eco` WARN lines** — `decrypt length failed` from AWS-range IPs.
  Internet scanners probing open ports; they cannot complete the AEAD handshake.
  Benign, and the volume is low.
- **A 502 in the Caddy log** — my own PocketBase restart during the hook deploy.
- **`/update.json`** — a stale placeholder written by `05-caddy.sh`. The updater
  reads `update_config`. Not a fault, but do not use it as a health check.
- **The truncated fingerprint** in the `live-data-do-not-delete` memory note — I
  flagged it but did not correct the memory. If you rely on that value, use the
  full 64-char SHA-256 from `docs/history/SESSION-GOTCHAS.md` §1.
- **Making it so a future deploy cannot advertise unresolvable update URLs** — I
  added a guard for the *record* (`releases.set` checks URLs and hashes are
  present and version-matched), but the hook cannot stat the filesystem
  (PocketBase exposes only `$os.getenv`), so it cannot confirm the files exist.
  Publishing still verifies served bytes (`publish-release.sh`); that is the real
  guarantee, and the guard is defence in depth for the console path.

---

## If you are the next agent here

1. Run `git show --stat 4791381` to see exactly what this conversation touched.
2. Read `docs/history/SESSION-GOTCHAS.md` before testing against the live hub — it will
   save you the two false diagnoses I made.
3. Remember `setup.sh` deploys from `/root/server/`, not from the repo. Editing a
   file here changes nothing until the staging copy is updated. Check drift:

```bash
for f in activation admin_console admin_unbind code_lookup heartbeat hiddify release; do
  a=$(md5sum /opt/pocketbase/pb_hooks/$f.pb.js 2>/dev/null | cut -d' ' -f1)
  b=$(md5sum /root/server/pb_hooks/$f.pb.js 2>/dev/null | cut -d' ' -f1)
  [ "$a" = "$b" ] && echo "$f in sync" || echo "$f DRIFT"
done
```
