# Locus documentation index

Nineteen documents in a flat directory is only navigable if something says what
each one is. This is that file.

**Two rules for reading anything here:**

1. **Check the status banner.** Several documents describe the **retired** Wails
   client and carry a `⚠️ STATUS: describes the ARCHIVED client` banner. The
   shipping client is `client/`; its docs live in `client/docs/`. A document with
   no banner describes something still live.
2. **The code wins.** Where a document and the code disagree, the code is right
   and the document is a bug. Fix it in the same change.

---

## Start with

| Document | What it is |
|---|---|
| [`CONTEXT.md`](CONTEXT.md) | **Read this first.** What the project is, its history, the repository layout, the naming rules, and the live-data warning. |
| [`STILL-OPEN.md`](STILL-OPEN.md) | What is unfinished or unvalidated, and what was deliberately left alone. |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | ⚠️ Archived-client architecture (Wails + sing-box). The server half is current. |

## Server and operations — all current

| Document | What it is |
|---|---|
| [`DEPLOY.md`](DEPLOY.md) | Deploying the hub to a VPS, from scratch or to a new host. |
| [`OPS.md`](OPS.md) | Running it: health checks, backups, restore, the things you do at 2am. |
| [`POCKETBASE-SETUP.md`](POCKETBASE-SETUP.md) | PocketBase specifics, including the admin console at `/admin/`. |
| [`SECRETS-MANAGEMENT.md`](SECRETS-MANAGEMENT.md) | age-encrypted secrets, what is plaintext, what must never be committed. |
| [`API.md`](API.md) | HTTP API reference for the hub (activation, heartbeat, code lookup, releases). |
| [`RELEASING.md`](RELEASING.md) | Cutting a release: `bump.sh` → tag → CI → publish. **Read the version-authority note below.** |
| [`CI-CD.md`](CI-CD.md) | ⚠️ Describes the workflow that builds the **archived** client. |
| [`FIXES.md`](FIXES.md) | Append-only dated log of every real defect and its fix. **Treat entries as historical** — later entries sometimes correct earlier ones; the corrections are marked. |

## Archived client — banners, reference only

These describe `legacy/wails-client/`. They are kept because they document the
contract the shipping fork must reproduce, not because the code is live.

| Document | What it is |
|---|---|
| [`CLIENT-GUIDE.md`](CLIENT-GUIDE.md) | ⚠️ Build/run guide for the Go + Wails client. |
| [`BACKEND-API.md`](BACKEND-API.md) | ⚠️ The `internal/` package API surface. |
| [`IMPLEMENT.md`](IMPLEMENT.md) | ⚠️ Historical implementation plan (Fyne era). |
| [`UI-AESTHETICS.md`](UI-AESTHETICS.md) | ⚠️ Visual design spec for the Wails UI. |
| [`WAILS-MIGRATION.md`](WAILS-MIGRATION.md) | ⚠️ Fyne → Wails migration history. |
| [`README-legacy-v5.md`](README-legacy-v5.md) | ⚠️ The old `v5/README.md`. Its "definitive version" claim is retired. |

## Analysis and design

| Document | What it is |
|---|---|
| [`ENGINE-SWAP-ANALYSIS.md`](ENGINE-SWAP-ANALYSIS.md) | sing-box → mihomo analysis. **Partly moot by construction**: the fork bundles mihomo, so Track B is settled. Read the correction. |
| [`GAMING-UDP.md`](GAMING-UDP.md) | The UoT (UDP-over-TCP) work for the gaming tier. Built; the open question is whether it has ever carried a real game session. |

## History

| Document | What it is |
|---|---|
| [`history/`](history/) | Curated pre-V5 research: business plan, N4L attacker/defender analysis, blocklist research. Genuinely useful background. |
| [`history/SESSION-*.md`](history/) | Dated session records — what a previous agent did, hit, and left. Decisions and traps, not project documentation. |

---

## Version authority — the thing most likely to mislead you

`VERSION` (repo root) and `server/scripts/bump-version.sh` drive the **archived**
Wails client's version sites and its committed Windows `.syso` resources. They do
**not** version the shipping fork.

The fork versions itself in `client/package.json` and
`client/src-tauri/Cargo.toml`. Reconciling the two is an **open decision**, not an
oversight — see `client/docs/RESTRUCTURE.md`.

**Do not assume `./bump.sh` changes what ships.** It does not.

## Path history

Restructured 2026-09-23: `v5/server/` → `server/`, `v5/console/` →
`server/console/`, `v5/client/` → `legacy/wails-client/`, `v4/` → `legacy/v4/`,
`v5/docs/` + `extra-details/` → `docs/`. All moves used `git mv`, so history is
intact. Documents dated before that date may name the old paths in historical
context — that is correct, not stale.
