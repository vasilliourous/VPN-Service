# Publishing a Release to the Hub

> **⚠️ STATUS: the old release-cutting machinery has been REMOVED.**
> `scripts/release-cut.sh`, root `bump.sh`, `server/scripts/bump-version.sh`,
> `server/scripts/stamp-syso.py`, `server/scripts/smoke-bump.sh`, root `VERSION`,
> the committed `rsrc_windows_*.syso` resources, and the archived-client CI
> workflow (`.github/workflows/build.yml`) are **deleted**. There is no version
> authority and no automated release-cutting path any more.
>
> What survives here is the **publishing** half — `publish-release.sh`, which
> uploads a build to the live hub and sets the rollout. That is still live.
>
> How the **shipping fork** (`client/`) is versioned and released is an **open
> decision** (see `docs/STILL-OPEN.md`). The old machinery versioned only the
> retired Wails client at `legacy/wails-client/`; it never touched `client/`.

---

## Publishing a build to the hub

Once a release artifact exists on GitHub (or you have the files locally),
publish it to the hub:

```bash
# Hold the rollout at 0 — upload only, offer it to nobody
ROLLOUT_PERCENT=0 server/scripts/publish-release.sh 2.2.7 --from-github

# Or start the rollout (default 5%)
server/scripts/publish-release.sh 2.2.7 --from-github
```

`--from-github` pulls the artifacts for the tag `v<version>` straight from
GitHub (default repo `vasilliourous/VPN-Service`) and verifies the served bytes.
This is the normal path — it exists because hand-uploaded artifacts drift
(`FIXES.md` 41).

**Confirm the hub is serving it:**

```bash
curl -s https://networkingguides.duckdns.org/api/release
```

### ⚠️ Hold the rollout at 0 unless the change was verified on real hardware

A clean compile is not proof that a runtime fix works. For any release
containing a client behaviour change, publish with `ROLLOUT_PERCENT=0`, install
it on a real Windows machine, confirm the fix, and only then raise the rollout.

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
