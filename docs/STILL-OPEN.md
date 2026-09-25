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

### Linux TUN elevation (Track A)

Unchanged by this work. There is no elevation path on Linux — direct mode is
forced and the helper binary is not shipped, so TUN cannot be created in a
non-root session. The gap is now declared honestly instead of surfacing as an
opaque engine error, but it is not fixed. See `docs/ENGINE-SWAP-ANALYSIS.md`.

---

## Open, and fixable now

### The client port has not started — `client/` is an untailored upstream copy

**This is the largest open item in the project.** `client/` is a **branding-only
copy of Clash Verge Rev v2.5.5** — the name, icon, and app id say Locus; the code
is upstream. Verified: the string `locus` appears nowhere in
`client/src-tauri/src/`, and the `src-tauri/src/locus/` module tree specified in
`client/docs/LOGIC-INVENTORY.md` does not exist.

None of the following Locus behaviour exists in the client yet:

- activation (`/api/activate`, Luhn code validation, code lookup pre-check)
- heartbeat loop, backoff, grace
- tier payload → mihomo config
- device fingerprint
- the Locus update path (hybrid: hub `update_config` + public `/api/release`)
- product state storage on top of Verge's app dir

The plan for all of it is written (`client/docs/LOGIC-INVENTORY.md`,
`ARCHITECTURE.md`, `UPDATE-ARCHITECTURE.md`), including what to debloat from
upstream and which contracts to port verbatim from `legacy/wails-client/`. What is
missing is the implementation.

**Why it matters beyond "unfinished":** every doc that calls `client/` "the
shipping client" means "the directory that *will* ship." Nothing in the repo
executes Locus logic on a client today. Until this port is done, the Locus client
does not exist as software — do not test against it, and do not "fix" upstream
behaviour as though it were ours.

### Fork versioning and release path — an open decision

The old version/release machinery is **deleted** (root `VERSION`, `bump.sh`,
`server/scripts/bump-version.sh`, `stamp-syso.py`, `smoke-bump.sh`,
`scripts/release-cut.sh`, the committed `rsrc_windows_*.syso`, and
`.github/workflows/build.yml`). It versioned and released the retired Wails client
and never the shipping fork.

What is left undecided:

- **What owns the fork's version.** `client/package.json`,
  `client/src-tauri/tauri.conf.json`, and `client/src-tauri/Cargo.toml` each carry
  `2.5.5` today, with no rule tying them together.
- **Whether a root `VERSION` is reintroduced** for the fork, or the fork versions
  itself in its own manifests.
- **How a release is built and tagged** now that no CI exists. Publishing to the
  hub (`server/scripts/publish-release.sh`) still works and takes the version as
  an argument, but nothing *produces* the artifacts.

This is a **correctness dependency of the updater**, not just hygiene: the update
comparison runs against build-embedded version metadata, so drift makes the
updater mis-decide (`client/docs/UPDATE-ARCHITECTURE.md` §4).

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
