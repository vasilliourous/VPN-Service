# Repo restructure: separating client, server, and legacy

> Records the layout change and everything that must be repointed. The old layout
> tangle (`v4/`, `v5/client/`, `v5/server/`, `v5/console/`, root `scripts/`, root
> `bump.sh`) is the reason this is written down rather than done ad hoc.

---

## 1. Target layout

```
VPN-Service/
├─ client/                     # the fork (Clash Verge Rev base) — was created this session
│  ├─ src/  src-tauri/  crates/  packages/  scripts/
│  ├─ Cargo.toml  Cargo.lock  package.json  pnpm-lock.yaml
│  └─ docs/                    # ARCHITECTURE, UPDATE-ARCHITECTURE, LOGIC-INVENTORY
├─ server/                     # was v5/server/
│  ├─ pb_hooks/  modules/  scripts/  templates/
│  └─ console/                 # was v5/console/
├─ legacy/
│  ├─ wails-client/            # was v5/client/ — frozen oracle, reference only
│  └─ v4/                      # was v4/
├─ docs/                       # was v5/docs/ + extra-details/ merged
├─ shared/contracts/           # tier + update_config schemas read by both sides
├─ scripts/                    # deploy / publish tooling (repointed)
├─ .github/workflows/          # single CI location (see §4)
└─ vendor/                     # gitignored; upstream reference if ever re-cloned
```

**Why `legacy/wails-client` rather than deleting it:** its Go tests encode contract
behaviour (Luhn, version comparison, the `uot_port` rename, artifact names, grace
arithmetic) that the fork must reproduce. `LOGIC-INVENTORY.md` §9 lists the specific
test files to port. Deleting it before those are ported loses the specification.

---

## 2. Steps, in order

Uses `git mv` so history follows.

1. `v5/server/` → `server/`
2. `v5/console/` → `server/console/`
3. `v5/client/` → `legacy/wails-client/`
4. `v4/` → `legacy/v4/`
5. `v5/docs/` + `extra-details/` → `docs/` (resolve name collisions explicitly;
   `extra-details/` holds `WHAT-WE-DID.md`, `STILL-OPEN.md`, `GOTCHAS-FROM-THIS-WORK.md`,
   `SESSION-UPDATE-ARC.md`, `README.md`)
6. `v5/CONTEXT.md`, `v5/README.md` → `docs/` (review first; they describe the old
   layout and will be stale)
7. `v5/VERSION` → decisions below
8. Delete the now-empty `v5/`
9. Create `shared/contracts/` and move the contract definitions there (see §3)
10. Handle the stray tracked file at repo root literally named
    `whale resume 01a06fdc-….md` — decide whether to keep, rename, or untrack

---

## 3. Version authority

**Decision: the repo `VERSION` file remains the single authority for the client.**

The fork introduces *more* version sites than the old client had:

| Site | Current value |
|---|---|
| repo `VERSION` | (authority) |
| `legacy/wails-client` `main.go` + `wails.json` + `package.json` | frozen |
| `client/package.json` | `2.5.5` |
| `client/src-tauri/tauri.conf.json` | `2.5.5` |
| `client/Cargo.toml` (workspace) | — |
| `client/src-tauri/Cargo.toml` | `2.5.5` |

FIXES #20/#45 were both "the version was maintained by hand across N files and drifted".
The fork makes it worse, so CI must enforce it: a workflow step that reads `VERSION`,
compares against the four fork sites, and **fails the build on mismatch** — the same
pattern as the existing Wails `version_consistency_test.go` and the `.syso` staleness
gate (FIXES #57–60).

**This is a correctness dependency of the updater**, not just hygiene: the update
comparison runs against build-embedded version metadata, so a drifted version makes the
updater mis-decide (see `UPDATE-ARCHITECTURE.md` §4).

Stamped (generated) at build time, not hand-edited:
- `client/package.json` `version`
- `client/src-tauri/tauri.conf.json` `version`
- `client/src-tauri/Cargo.toml` `version`
- `main.version` / buildinfo equivalent

---

## 4. CI layout constraint (important)

**GitHub only runs workflows from `.github/workflows/` at the repository root.**
A workflow placed in `client/.github/workflows/` will **never trigger** — which is why
upstream's 19 workflows were deleted during the fork cleanup (`client/.github/` is gone).

Consequences:

- The fork's CI lives at `.github/workflows/client.yml` (repo root).
- It must set `defaults.run.working-directory: client` (or per-step), and
  `cache-dependency-path: client/pnpm-lock.yaml`.
- Use `paths:` filters so server-only changes do not trigger a full Tauri build for
  three platforms.
- Keep the existing Wails workflow only while `legacy/wails-client` still needs to
  build; retire it once the fork ships. Do **not** leave two client workflows both
  producing "the client" with no way to tell which is current.

Required CI additions (verified against the fork's manifests):

| Need | Value |
|---|---|
| Rust | `rust-toolchain.toml` → **1.98.1** (`edition = "2024"` requires ≥1.85) |
| Node + pnpm | Node 20/24, `pnpm install --frozen-lockfile` |
| Rust locking | **`--locked`** — five git deps, three pinned only by `Cargo.lock` |
| Linux deps | `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`, `libsoup-3.0-dev`, `libjavascriptcoregtk-4.1-dev`, `libappindicator3-dev`, `librsvg2-dev`, `patchelf`, `pkg-config` |
| Windows | NSIS + WebView2 (`src-tauri/webview2.*.json`) |
| macOS | both `aarch64` and `x86_64` targets |
| Caching | `Swatinem/rust-cache` (cold Rust builds are 10–20 min/leg) |
| Gates | `cargo clippy -D warnings`, `cargo test`, version-drift check |

---

## 5. Files that must be repointed

This is the step that breaks most quietly. Every known path consumer:

| File | Depends on | Action |
|---|---|---|
| `client/Makefile` | `$(realpath ../VERSION)` | repoint to the repo `VERSION` location |
| `bump.sh` (root) | `v5/VERSION` + Wails sites | retarget to the fork's four sites |
| `scripts/release-cut.sh` | Wails/tag flow | retarget or retire |
| `scripts/publish-release.sh` | artifact paths, `manifest.json` | retarget to the fork's artifacts |
| `scripts/publish-update.sh` | **already broken** (see below) | retire; `publish-release.sh` is correct |
| `scripts/generate_codes.sh`, `print_codes.sh` | hub only | unaffected |
| `server/modules/*.sh` | internal paths | verify after the move |
| `setup.sh` / `deploy-console.sh` | **deploys from `/root/server/`, not the repo** | documented drift trap — re-verify staging sync |
| `docs/STILL-OPEN.md` | the `md5sum` drift-check snippet | repoint (`/root/server/pb_hooks` vs repo) |
| `.github/workflows/build.yml` | `v5/client/wails.json`, `v5/VERSION`, version grep | repoint to legacy or retire |

**`publish-update.sh` is already known-broken** and is listed in `STILL-OPEN.md` as such:
it writes no `sha256_<platform>` columns (clients refuse an empty hash) and its macOS
URLs (`locus-macos-amd64`) do not match CI's filenames (`locus-darwin-amd64`) → 404.
Do not "fix and keep" both publishers; the two-publisher divergence is the direct cause
of FIXES #31. Retire it.

---

## 6. `shared/contracts/`

Both sides need the same definitions, and divergence between them is the root cause of
several recorded bugs. Candidates to move here (source of truth, with copies generated
or asserted rather than hand-maintained):

- Platform identifiers and artifact filenames
  (`LOGIC-INVENTORY.md`, `UPDATE-ARCHITECTURE.md` §2.2)
- The `update_config` record ↔ heartbeat response field rename table
  (`download_*` → `update_*`)
- The tier config shape, including the `uot_port` / `server_port_uot` alias
- Heartbeat constants (interval bounds, grace period, backoff)
- Which of these are read by the **Go legacy client**, **the Rust fork**, and
  **the PB hooks** — a `pb_hooks` file cannot import Rust, so the contract must be
  machine-checkable rather than merely documented

Note the constraint: PocketBase hooks run in goja (JS), the fork is Rust, the legacy
client is Go. A shared *file* is fine for documentation and for the Rust/Go sides;
the hook side needs the values inlined **plus** a test that asserts the inlined copy
matches. That mismatch is exactly FIXES #29 and #31.

---

## 7. Verified build baseline (2026-09-23)

The fork compiles clean on Linux after installing the Tauri dev deps. Recorded so a
successor can tell "the tree is broken" from "my environment is incomplete".

**System packages required (Ubuntu/Debian):**

```
pkg-config build-essential libssl-dev
libglib2.0-dev libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev
libjavascriptcoregtk-4.1-dev libappindicator3-dev librsvg2-dev patchelf
```

Symptom when they are missing: `error: failed to run custom build command for
glib-sys v0.18.1`. Note a runtime `libglib-2.0.so.0` is **not** sufficient — the
`.pc` file and headers come from `libglib2.0-dev`.

**Critical: `node scripts/prebuild.mjs` must run before any Rust build.** It downloads
the sidecars and geo data into `src-tauri/sidecar/` and `src-tauri/resources/`, both of
which are **gitignored** (`src-tauri/.gitignore`). Without them:

```
error: failed to run custom build command for `locus v2.5.5`
resource path `sidecar/clash-verge-service-x86_64-unknown-linux-gnu` doesn't exist
```

What prebuild fetches: `verge-mihomo` + `verge-mihomo-alpha` (from MetaCubeX/mihomo
releases), the three `clash-verge-service*` binaries (from `clash-verge-service-ipc`
v2.7.3), and `country.mmdb`, `GeoLite2-ASN.mmdb`, `geosite.dat`, `geoip.dat`
(from MetaCubeX/meta-rules-dat).

**Verified commands and results:**

| Command | Result |
|---|---|
| `pnpm install --frozen-lockfile` | pass |
| `pnpm run web:build` (tsc + vite) | pass, 3–5 s |
| `node scripts/prebuild.mjs` | pass, fetches sidecars |
| `cargo check --manifest-path src-tauri/Cargo.toml` | **pass**, `Finished dev profile` in ~34 s |
| `cargo clippy --manifest-path src-tauri/Cargo.toml` | **pass**, zero warnings |
| `cargo metadata --locked` | pass |

**Gotcha — stale macro artifacts:** a Rust build interrupted *before* the system deps
were installed leaves a truncated `target/debug/deps/libgtk3_macros-*.so`. The next
build then fails with `error[E0786]: found invalid metadata files for crate
gtk3_macros`. Fix: delete the stale artifacts (`find target -name 'libgtk3_macros*'
-delete`, likewise `libgdk3_macros*`) — `cargo clean` also works but costs a full
rebuild. This is an artifact of the environment, not of the tree.

---

## 8. Risks

| Risk | Mitigation |
|---|---|
| A path consumer is missed and fails silently at release time | The table in §5 is exhaustive as of writing; grep for `v5/` and `v4/` repo-wide after the move and confirm zero hits |
| `git mv` on a directory with an untracked `client/` sibling confuses staging | Commit or explicitly stage `client/` before moving; verify `git status` is clean of surprises first |
| Two client workflows both build "the client" | Retire the Wails one when the fork ships; never both |
| `legacy/wails-client` still referenced by docs as the current client | Update `docs/` in the same change; stale docs are how the next agent gets misled |
| The move breaks the `md5sum` drift check against `/root/server/` | Re-run the check immediately after the move |
