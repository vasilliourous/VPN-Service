# Locus documentation index

`docs/` holds the **live** project documentation. Retired-client material is
separated into [`archive/`](archive/) and pre-V5 research into [`history/`](history/),
so what remains at this level is current. This file says what each one is.

**Three rules for reading anything here:**

1. **Check the status banner.** Documents about the **retired** Wails client carry
   a `⚠️ STATUS: describes the ARCHIVED client` banner; documents about deleted
   machinery carry a `⚠️ STATUS: ... REMOVED` banner. The client that will ship is
   `client/` — but note it is currently a **branding-only copy of Clash Verge Rev
   with no Locus logic yet** (see `STILL-OPEN.md`), and its *spec* docs live in
   `client/docs/`. A document with no banner describes something still live.
2. **Nothing current lives in `archive/` or `history/`.** Those are reference
   material. If you are orienting yourself, you do not need them.
3. **The code wins.** Where a document and the code disagree, the code is right
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
| [`UPDATE-SYSTEM.md`](UPDATE-SYSTEM.md) | **How an update reaches a client, end to end** — publishing, the `/api/update` endpoint, the two integrity gates, and what is verified vs not. |
| [`RELEASING.md`](RELEASING.md) | ⚠️ Publishing a build to the hub (still live). The old release-cutting path is removed. |
| [`CI-CD.md`](CI-CD.md) | ⚠️ REMOVED — the pipeline that built the archived client is deleted. Kept for the surviving asset/consumer contract. |
| [`FIXES.md`](FIXES.md) | Append-only dated log of every real defect and its fix. **Treat entries as historical** — later entries sometimes correct earlier ones; the corrections are marked. |

## Archived client — moved to `archive/`, reference only

These describe `legacy/wails-client/`. They were moved into [`archive/`](archive/)
so the top level of `docs/` holds only live documents. They are kept because they
document the contract the shipping fork must reproduce, not because the code is live.

| Document | What it is |
|---|---|
| [`archive/CLIENT-GUIDE.md`](archive/CLIENT-GUIDE.md) | ⚠️ Build/run guide for the Go + Wails client. |
| [`archive/BACKEND-API.md`](archive/BACKEND-API.md) | ⚠️ The `internal/` package API surface. |
| [`archive/IMPLEMENT.md`](archive/IMPLEMENT.md) | ⚠️ Historical implementation plan (Fyne era). |
| [`archive/UI-AESTHETICS.md`](archive/UI-AESTHETICS.md) | ⚠️ Visual design spec for the Wails UI. |
| [`archive/WAILS-MIGRATION.md`](archive/WAILS-MIGRATION.md) | ⚠️ Fyne → Wails migration history. |
| [`archive/README-legacy-v5.md`](archive/README-legacy-v5.md) | ⚠️ The old `v5/README.md`. Its "definitive version" claim is retired. |

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

## Version authority — REMOVED, and an open decision

The old version machinery is **deleted**: root `VERSION`, `bump.sh`,
`server/scripts/bump-version.sh`, `stamp-syso.py`, `smoke-bump.sh`,
`scripts/release-cut.sh`, the committed `rsrc_windows_*.syso` resources, and the
archived-client CI workflow. They drove **only** the retired Wails client at
`legacy/wails-client/`, never the shipping fork — that mismatch, plus the tags
and releases it produced for a client nobody ships, is why it was removed.

**There is no version authority now.** The shipping fork carries a version in
`client/package.json` and `client/src-tauri/Cargo.toml` (currently `2.5.5`),
which do not agree with each other or with anything else by rule — reconciling
them is an **open decision**, not an oversight. See `docs/STILL-OPEN.md`
("Fork versioning and release path") and `client/docs/RESTRUCTURE.md`.

**Do not assume any script bumps what ships.** None does; there is no such script.

## Path history

Restructured 2026-09-23: `v5/server/` → `server/`, `v5/console/` →
`server/console/`, `v5/client/` → `legacy/wails-client/`, `v4/` → `legacy/v4/`,
`v5/docs/` + `extra-details/` → `docs/`. All moves used `git mv`, so history is
intact. Documents dated before that date may name the old paths in historical
context — that is correct, not stale.

Filenames changed in the merge, so a few old names are **dead** — search for the
new one instead:

| Old name (dead) | Current file |
|---|---|
| `GOTCHAS-FROM-THIS-WORK.md` | `docs/history/SESSION-GOTCHAS.md` |
| `extra-details/` | `docs/` (contents merged) |
| `v5/docs/` | `docs/` |

Later, the archived-client **version/release machinery was deleted** (root
`VERSION`, `bump.sh`, the bump/syso scripts, `release-cut.sh`, the `.syso`
resources, `.github/workflows/build.yml`). References to those are historical,
not instructions — see "Version authority" above.
