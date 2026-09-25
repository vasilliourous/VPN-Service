# CI/CD — the client pipeline

> **STATUS: the archived client's pipeline was REMOVED and replaced.**
>
> `build.yml` built and released `legacy/wails-client/` — the **retired** Go +
> Wails + sing-box client — and never built the shipping fork. It is deleted,
> along with the version-cutting scripts it depended on (`bump.sh`,
> `server/scripts/bump-version.sh`, `stamp-syso.py`, `smoke-bump.sh`,
> `scripts/release-cut.sh`, root `VERSION`, and the committed
> `rsrc_windows_*.syso` resources).
>
> **The replacement is `.github/workflows/client.yml`**, which builds, **signs**
> and releases the shipping fork (`client/`, the Tauri fork of Clash Verge Rev).

---

## The client pipeline

**Jobs:** `verify` → `build` (4 platforms) → `release` (tags only).

**Where it must live:** `.github/workflows/` at the **repository root**. GitHub
ignores a workflow placed under `client/.github/`, which is why the fork's
earlier plan to put one there would never have triggered.

**What it produces**, because the hub's publish path resolves these by name and
requires every one of them:

| Asset | Notes |
|---|---|
| `locus-linux-amd64` | raw executables, for the auto-updater |
| `locus-windows-amd64.exe` | |
| `locus-darwin-amd64` | the *filename* says darwin while the *platform key* says `macos_intel` — a frozen asymmetry, not an oversight |
| `locus-darwin-arm64` | |
| **`.sig` for each of the four** | minisign signatures; without them no client can install the update |
| `manifest.json` | version + per-platform filename, SHA-256 and signature |

**Signing is mandatory, not best-effort.** The Tauri updater verifies a minisign
signature over every download and offers no bypass, so a release published
without signatures is installable by nobody while looking perfectly healthy on
the hub. The build fails loudly if `LOCUS_UPDATE_KEY` is absent. See
`client/docs/SIGNING.md`.

**The raw binaries are staged by hand.** `createUpdaterArtifacts` is `false`, so
`tauri build` emits the *installed* forms (NSIS, `.app`) but not the bare
executables the updater consumes. The workflow copies the compiled binary
(`target/<triple>/release/locus[.exe]`) under the name the hub expects.

**Why a partial release is impossible:** `manifest.json` generation fails if any
binary or signature is missing, and a second check verifies every
`locus-*` artifact has a `.sig` before the release is created.

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

### How the new pipeline avoids these

| Old failure | New safeguard |
|---|---|
| A skipped release job rendered as a green check | The release job is the *last* of three and depends on `build`; a skipped tag job cannot look like a completed release because nothing is uploaded |
| Version asserted across six files plus committed binaries | Version is pinned to `client/package.json`, and the tag drives the release name |
| A build that could publish a partial release | `manifest.json` generation fails on any missing binary **or signature**, and every artifact is checked for its `.sig` before the release is created |
| Signing assumed rather than required | The build fails outright if `LOCUS_UPDATE_KEY` is unset, because an unsigned release is installable by nobody |
| `--frozen-lockfile` drift (a package removed without regenerating the lockfile) | `verify` runs `pnpm install --frozen-lockfile` first, so the drift fails in seconds rather than at build time |
