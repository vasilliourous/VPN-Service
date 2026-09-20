# What this conversation did

Four tasks, in order. Everything below landed in **one squashed commit,
`4791381 "Another Version!"`** — the message carries no reasoning, so this file
is the only record of why. Nothing here is project background; it is what
happened in this conversation.

| # | Task | Outcome |
|---|---|---|
| 1 | Learn the project | Read all project memory + repo; verified build/tests |
| 2 | Update all the docs | 9 files, ~660 insertions, factual corrections |
| 3 | Verify the server end-to-end | **Found and fixed one real defect** |
| 4 | Make deploys zero-touch | Defaults flipped, two self-inflicted bugs caught |

Files I created: `stamp-batch.py`. Flags I added: `FIRST_BATCH*`,
`ENABLE_UOT` default flip, `SKIP_CONSOLE`.

---

## 1. Learning the project

Read the memory index plus the repo. Two things worth recording because they
were wrong in memory and cost time:

- **The bound code's fingerprint is a full 64-char SHA-256**, not the 16-char
  prefix (`6b3e0b06bf604dc7`) recorded in the `live-data-do-not-delete` memory
  note. I tested with the prefix and concluded device binding was broken. It was
  not — the server was right and my test was wrong. The real value is
  `6b3e0b06bf604dc789803598db51f162ab99ba988298367b57409e0fca5ab302`.
- **v5-client-architecture memory was stale** on two fields: it said the hub was
  `114.23.136.59` and codes were `MYVPN-*`. Both wrong — it is `170.64.196.179`
  and `RQ-*`. The newer `session-2026-09-19-summary` and
  `vps-deployment-credentials` notes were correct.

Verified locally: 9 Go packages, ~7,700 lines across 38 files; frontend builds
(28 modules); console builds and its `/admin/assets/` base guard passes; the four
pure-Go test packages pass; `gofmt -l` flagged only `internal/tray/tray.go`.

---

## 2. Docs rewrite (9 files)

Every change was a factual correction, not a rewrite for style. The specific
wrong claims I found and fixed:

| Claim I found | Reality |
|---|---|
| Host `114.23.136.59` / `134.199.155.166` | `170.64.196.179`; both old hosts retired |
| Wails 2.9.1 | 2.12.0 |
| sing-box 1.10.0 | 1.12.1 |
| "2 platform bundles" | 4 bundles + raw per-platform artifacts + manifest |
| "6 backend packages" | 8 under `internal/` (measured: 38 files, ~7,700 lines) |
| "~4,200 lines / 19 files" | same measurement correction |
| "SSH rate-limited" (ufw) | fail2ban; `ufw limit 22/tcp` is a known lockout |
| "both platform zips" in CI | 4 targets; `webkit2_41` tag on Linux |
| `/update.json` as a health check | stale placeholder; updater reads `update_config` |

**Also redacted the live admin API token** from `v5/CONTEXT.md` and
`v5/docs/SECRETS-MANAGEMENT.md` (replaced with a generation hint plus a note that
it persists in git history). Both files were tracked-but-unmodified, so the token
was never in a commit — rotation is optional, which I said rather than implying
it was urgent.

Added the PocketBase traps to `POCKETBASE-SETUP.md` (they had only existed in
`FIXES.md`), a hook-installation section that was missing entirely, the
`code_events` collection, `update_config`'s per-platform columns, and the admin
console across five docs.

**I mangled `v5/README.md` mid-task** — an edit dropped a closing code fence and
left a stray tree fragment. Caught it with a fence-balance check and repaired it.
The check (`grep -c '^```'`, must be even) is worth reusing.

---

## 3. Server audit — the one real defect

Full read-only sweep of `170.64.196.179`. Result: **in working order.** All
services active *and* enabled, smoke test 23/23, `PRAGMA integrity_check` ok,
TLS valid 89 days, 11 real codes intact.

### `update_config` advertised downloads that did not exist

The row claimed `version 1.0.0` while every URL and hash pointed at
`/updates/1.0.2/*` — and **`/var/www/updates/` was completely empty**. All four
advertised artifacts returned 404.

Latent, not active: `rollout_percent = 0` meant no client was offered the update
(verified — the heartbeat returned no `update_*` keys). But it was armed: raising
the rollout even 1% would send every eligible client to a 404, and a client that
cannot download cannot update, so the release would fail silently fleet-wide.

Cause: leftover test data. The `1.0.2` artifacts were uploaded during the earlier
update-system verification, then cleaned up; the `update_config` row was not.
`publish-release.sh` is correct — it verifies served bytes before writing.

Fixed twice:
1. Cleared the stale URLs (DB backed up first) → honest `1.0.0 / rollout 0 / no URLs`.
2. Added a guard in `releases.set` (in `admin_console.pb.js`) refusing any
   rollout > 0 unless all four platform URLs **and** hashes are present, and each
   URL points at the version being advertised. Verified both bypass paths are
   blocked and staging at 0% still works.

The exact refusal text is still in the hook at line ~685: `"refusing to offer " +
version + " at " + rollout + "% — no usable artifact for: …"`.

### Backup artifacts I left on the host

- `/root/data.db.pre-updateconfig-fix-1789795235` — DB before the URL clear
- `/root/admin_console.pb.js.pre-guard-1789795273` — hook before the guard
- `/root/admin_console.pb.js.staged-pre-guard-1789795273` — same, staging copy

Safe to delete once the guard is confirmed good. I deployed the hook to **both**
`/opt/pocketbase/pb_hooks/` and the `/root/server/` staging copy (md5-matched),
because `setup.sh` deploys from staging.

### Other audit findings — none needed action

- **`shadowsocks-eco` "errors"** were internet scanners (`decrypt length failed`
  from AWS-range IPs). Harmless.
- **A 502 in the Caddy log** was my own PocketBase restart during the hook deploy.
- **A 5xx/404 log scan returned nothing** — the log had rotated.
- **My "activate doesn't rate-limit" result was a bad test**: I used a
  Luhn-invalid code, which is rejected *before* the rate-limit stage. Both limits
  work (activate 429s on the 6th; lookup on the 11th). Side effect worth knowing:
  malformed codes are unthrottled by design.
- **The box had rebooted** since the prior session (uptime 2h11m). Boot
  resilience confirmed: swap in fstab, all 12 units enabled, tc caps as
  `RemainAfterExit` oneshots, BBR in `sysctl.d`.
- **8090/8091 correctly firewalled** from the internet; 8443/8445/8446 open.

---

## 4. Zero-touch deploy

Intent: a fresh `setup.sh` produces a complete hub with no follow-up flags.

| Area | Before | After |
|---|---|---|
| Strike UoT (8446) | `ENABLE_UOT=0` opt-in | **default on** in all five places that touch it |
| 8446 in firewall | never opened | opened TCP+UDP when UoT is on |
| Admin console | `warn` + skip | **required** — missing bundle fails the deploy |
| Release uploader | `warn` + skip | **required** |
| First-batch codes | none | `FIRST_BATCH=<n>` (+ `_MIDDLEMAN`, `_EXPIRES`), minted once |
| Post-deploy banner | a to-do list | what was actually deployed |

### Two bugs I introduced and caught before shipping

1. **Opt-out flags did not propagate.** `run_module` passed only `DOMAIN` and
   `FORCE` inline, and `setup.sh` read `ENABLE_UOT` without exporting it — so
   `ENABLE_UOT=0` was honoured by the summary while the module still installed
   the service. Fixed by resolving the toggles once and both exporting *and*
   forwarding them. Verified with a propagation harness across three cases.
2. **`08-firewall.sh` never opened 8446** — UoT would have listened locally and
   been unreachable, with clients sitting on the advertised port.

Also hardened `deploy_console`: extract to a temp dir and validate *before*
replacing the live console (a corrupt bundle used to extract straight into
`/var/www/admin`, leaving a half-written SPA); check `index.html` exists and
references `/admin/assets/`. Tested against a real build plus a deliberately
wrong-level tarball (correctly caught).

**Verified on the live host** that the already-staged bundle passes all three new
guards and byte-matches the deployed console — so the stricter module would not
break a re-run there.

---

## What changed afterwards (not mine)

Two of my areas were later superseded. Recording it so nothing reads as current
when it is not:

- **The browser uploader I hardened was retired** in `338c189`. The hub now pulls
  releases from GitHub (`fetch-release.py`, `locus-fetch.service`), which removes
  the PocketBase body-size problem rather than working around it.
  `release_upload.py`, `locus-upload.service` and `artifact.ts` are gone.
- **`FIXES.md` was truncated** from 1,769 lines to 69 by `15e7630` (entries 1-26
  lost), then restored in `ec3b129`. If you see a large `FIXES.md` diff, check
  whether entries were *dropped* rather than added.

Still standing from my work: the zero-touch defaults, the `releases.set` guard,
the console deploy guards, and `stamp-batch.py`.
