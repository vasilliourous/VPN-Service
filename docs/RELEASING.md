# Publishing a Release to the Hub

> **⚠️ STATUS: the old release-cutting machinery is REMOVED, and a new path
> exists.** `scripts/release-cut.sh`, root `bump.sh`, `bump-version.sh`,
> `stamp-syso.py`, `smoke-bump.sh`, root `VERSION`, the committed
> `rsrc_windows_*.syso`, and `.github/workflows/build.yml` are **deleted** — they
> versioned the retired Wails client and never the fork.
>
> **What replaced them:**
> - CI at `.github/workflows/client.yml` (**repo root** — GitHub ignores a workflow
>   under `client/.github/`). A `v*` tag builds, **signs** and releases all four
>   platforms plus `manifest.json`.
> - The client versions itself: `client/src-tauri/Cargo.toml`, `client/package.json`
>   and `client/src-tauri/tauri.conf.json` must agree, enforced by
>   `client/src-tauri/tests/version_consistency.rs`. There is no root `VERSION`.
>
> **Read `UPDATE-SYSTEM.md` for the full pipeline**, and `client/docs/SIGNING.md`
> for key custody. The short version:

```
1. Bump the version in all three sites, so they agree.
2. git tag -a vX.Y.Z && git push origin vX.Y.Z
     → CI builds + signs all four platforms, creates the GitHub Release
3. Fetch:  console Releases → "Fetch & publish",  or
           POST /api/admin/fetch-release {"version":"X.Y.Z"}
     → the hub pulls from GitHub, verifies format + SHA-256 + signature
4. Publish: writes update_config. The console does 3 and 4 in one button.
```

> **Fetch and publish are two steps.** Forgetting step 4 leaves the artifacts
> served while `/api/release` still advertises the old version — nothing looks
> broken, updates simply do not happen.
>
> **There is no rollout percentage.** Publishing IS offering; `active` is the only
> off switch, and there is no server-driven downgrade.

---

## Publishing a build to the hub

Two routes, both starting from a GitHub Release that CI created:

**The console (normal).** Releases → enter the version → **Fetch & publish**. The
hub pulls the four binaries and their signatures straight from GitHub, verifies
each one's format, SHA-256 and signature, and writes `update_config`. Nothing
large travels from a browser.

**The CLI**, for a hand-built or hotfixed binary that is not on a GitHub Release:

```bash
# Validate first — fetches from GitHub, touches nothing
DRY_RUN=1 server/scripts/publish-release.sh 3.1.0 --from-github

# Publish. There is no rollout to set; publishing is offering.
PB_ADMIN_EMAIL=admin@networkingguides.duckdns.org PB_ADMIN_PASS=... \
  server/scripts/publish-release.sh 3.1.0 --from-github
```

Omitting `--from-github` uses files from `RELEASE_DIR` (default
`./release-artifacts`). The script **refuses to publish** when an artifact is under
1MB (a truncated download or Git LFS pointer), when a hash disagrees with
`manifest.json`, or when a signature is missing.

### Why signatures are not optional

The Tauri updater verifies a minisign signature over every download and offers no
bypass. A release published without one is **installable by nobody**, while looking
perfectly healthy from the hub's side. `fetch-release.py`, `publish-release.sh` and
`releases.set` all refuse it — the checks exist so this fails at publish time rather
than as "updates silently never arrive". See `client/docs/SIGNING.md`.

### Confirm the hub is serving it

```bash
curl -s https://networkingguides.duckdns.org/api/release
curl -s "https://networkingguides.duckdns.org/api/update?version=2.2.0&platform=linux"
server/scripts/verify-release.sh 3.1.0      # read-only; hashes the SERVED bytes
```

`verify-release.sh` is the right tool: it checks the `update_config` row **and**
re-downloads each artifact to confirm the served bytes match the recorded hash.

### ⚠️ There is no rollout, and no automatic downgrade

Publishing **is** offering. `active` (boolean) is the only off switch, and
`releases.set` refuses to activate a release whose artifacts are missing, unsigned,
or point at a different version.

**Stopping only stops OFFERING** — clients that already updated stay updated. A bad
build can only be fixed by publishing a higher version, which the possibly-broken
client must then successfully fetch. So **test on real hardware before publishing**,
not before widening a rollout, because there is no rollout to widen.

---

## Related

- `docs/API.md` — the `/api/release` contract the hub serves.
- `docs/SECRETS-MANAGEMENT.md` — how server credentials are stored and rotated.
- `server/scripts/publish-release.sh` — pushing a built release to the hub.
- `server/scripts/fetch-release.py` — the GitHub-fetch path used by the console
  publish hook.
- `server/scripts/verify-release.sh` — confirm `/updates/<version>/` and the
  `update_config` row agree.

---

## History — the removed release-cutting path (for context only)

The following is kept because it explains why certain files disappeared and
which failure modes the descendants of this project already hit. **None of the
commands below work any more** — the scripts they name are deleted.

The old flow was, in order:

1. `./bump.sh <arg> --no-verify` — rewrote six version-bearing files in
   `legacy/wails-client/` and stamped `rsrc_windows_{amd64,arm64}.syso` via
   `stamp-syso.py`. It deliberately did not touch git.
2. A stale-artifact gate read the version back out of the committed `.syso`
   bytes and refused to tag if they did not match root `VERSION`.
3. `git add -A && commit && tag -a v<version>`, then push `main` **and** the tag
   (via `scripts/release-cut.sh`).
4. CI built four platforms on the tag and created the GitHub Release.

Failures worth remembering if a successor reintroduces any of this:

- **A committed Windows resource can be stale while every text check passes.**
  The `go:generate` *directive* said `2.2.8`; the committed `.syso` *artifact*
  said `2.2.6`. Happened at 2.1.0 (stamped `2.0.0`, product `MyVPN`) and again at
  2.2.7. This is why the gate eventually read the version out of the binary.
- **`--no-verify` skipped the artifact update, not just the tests.** Fixed by
  making stamping run before the flag was consulted. (`FIXES.md` 57.)
- **A skipped CI job renders as a green check.** The `Create Release` job was
  gated on `refs/tags/v*`; a branch-only push skipped it and showed seven green
  checks with no release. Re-running the workflow replayed the same skip —
  the fix was to push the tag.
- Root `VERSION` and this tooling drove **only** the archived Wails client. The
  shipping fork was never wired into it. That mismatch is the reason the
  machinery was removed rather than repaired.
