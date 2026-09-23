# CI/CD — REMOVED (was the archived client's pipeline)

> **⚠️ STATUS: the CI/CD pipeline this document described has been REMOVED.**
> `.github/workflows/build.yml` is **deleted**, along with the version-cutting
> scripts it depended on (`bump.sh`, `server/scripts/bump-version.sh`,
> `stamp-syso.py`, `smoke-bump.sh`, `scripts/release-cut.sh`, root `VERSION`,
> and the committed `rsrc_windows_*.syso` resources).
>
> It built and released `legacy/wails-client/` — the **retired** Go + Wails +
> sing-box client — and never built the shipping fork. That mismatch, plus the
> recurring version/tagging failures it caused, is why it was removed.
>
> **There is now no CI for any client.** The shipping client (`client/`, the
> Tauri fork of Clash Verge Rev, mihomo engine) is built locally. Its release
> path is an **open decision** — see `docs/STILL-OPEN.md`.

---

## What still holds (independent of the removed workflow)

These contracts survived the pipeline and are honoured by the **hub** and the
**publish path**, which are still live:

### The release-asset → consumer contract

Whoever builds a release must produce these, because the update machinery on the
hub expects exactly this shape:

| Asset | Consumer | Notes |
|---|---|---|
| `locus-<OS>-<arch>.zip` | humans | Portable: extract and run, no installer |
| `locus-setup-<v>.exe` | humans | Windows installer |
| `locus-setup-<v>-macos-<arch>.dmg` | humans | macOS `.app` |
| raw `locus-<os>-<arch>[.exe]` | **auto-updater** | Must be raw; the updater cannot unpack a zip |
| `manifest.json` | updater + hub | Version + per-platform filename + SHA256 |
| `checksums.sha256` | humans | Hashes of the human-facing deliverables |

**The updater must never be handed an installer.** It always fetches exactly one
named raw executable, and the hub's `fetch-release.py` resolves assets from an
allowlist of the four raw binaries plus `manifest.json` — so installers and DMGs
are ignored by the update pipeline rather than needing to be excluded from it.
Adding a new packaging artefact therefore requires **no change to the hub or to
`update_config`**.

### Publishing never happens inside the build

A build must not be able to put a binary on students' machines by itself.
Publishing to the live hub is a separate, deliberate step
(`server/scripts/publish-release.sh`), and it holds at rollout 0 until verified
on real hardware. See `docs/RELEASING.md`.

---

## Why it was removed (so it is not rebuilt by accident)

The archived client's CI was the source of the repo's tagging confusion:

- It triggered on `main` pushes and `v*` tags but built only the **retired**
  client — so tagging felt like "releasing the product" when it was not.
- The `Create Release` job was gated on `refs/tags/v*`; a branch-only push
  **skipped** it, and GitHub renders a skipped job as a **green check** — seven
  green checks, no release. Re-running the workflow replayed the same skip.
- The pipeline asserted a version spread across six files plus committed Windows
  `.syso` bytes; stale resources broke the 2.1.0 and 2.2.7 releases, and the
  guard built to catch it was bypassed rather than missing (`FIXES.md` 57).
- Broken tags remain: `v2.2.2` points at a commit that can never pass the (now
  deleted) lint gate, and `v2.2.4` never reached origin. They are reachable in
  git history and are listed here so they are not mistaken for releases.

The full removed pipeline (job graph, Wails build tags, artifact table, the
`--no-verify` trap) is preserved in git history at the commit that deleted
`.github/workflows/build.yml`.
