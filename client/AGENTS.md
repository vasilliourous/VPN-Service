# Agent Guidelines — Locus client

Instructions for AI coding agents working in `client/`, the Locus desktop client.

This directory is a **fork of [Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev)
v2.5.5** (Tauri 2 + Rust backend, React + TypeScript frontend). It is the
shipping Locus client. The predecessor Wails client is archived at
`legacy/wails-client/` and is **not** the product; see that directory's
`ARCHIVED.md` before reading it as current.

## Where to start

- `docs/ARCHITECTURE.md` — inherited surfaces, the debloat cut list, and where
  Locus logic goes
- `docs/LOGIC-INVENTORY.md` — module-by-module spec of the Locus code to write
- `docs/UPDATE-ARCHITECTURE.md` — the update path and why it is hybrid
- `docs/RESTRUCTURE.md` — repo layout and the (now open) version-authority question
- Repo-wide rules: **there is no root `AGENTS.md`.** The repo-level context lives
  in the root `README.md` and `docs/CONTEXT.md` (read `docs/README.md` first).

## Rules

1. **Verify against the live system, not the docs.** Several inherited documents
   describe the archived Wails client. Where a doc and the code disagree, the
   code wins and the doc gets fixed in the same change.

2. **Comments state constraints, not narration.** Write a comment only for a
   non-obvious constraint the code cannot express; never restate what the code
   does. This fork carries a lot of inherited comment bulk — do not add to it.

3. **Minimal diffs.** Match surrounding style, naming, and comment density. Do
   not restructure working code, rename inherited identifiers, or bump
   dependencies unless the change requires it.

4. **Do not "tidy up" the two load-bearing asymmetries.** They are contracts
   with already-deployed clients and the publish tooling, not oversights:
   the `update_<platform>` / `download_<platform>` rename between the
   `update_config` record and the heartbeat response, and the `macos_intel` /
   `macos_arm` platform keys versus `locus-darwin-amd64` / `locus-darwin-arm64`
   artifact filenames. See `docs/UPDATE-ARCHITECTURE.md`.

5. **`removeUnusedCommands: true` in `tauri.conf.json`.** Tauri prunes Rust
   commands unreachable from the frontend, so backend surface shrinks *after* UI
   removal. Delete pages before their backend, and never hand-edit
   `generate_handlers()` in `src-tauri/src/lib.rs`.

6. **Build order is mandatory.** `node scripts/prebuild.mjs` must run before any
   Rust build; it populates the gitignored `src-tauri/sidecar/` and
   `src-tauri/resources/`. `pnpm run web:build` must pass after any frontend
   deletion.

7. **Dependency pinning goes in `Cargo.lock`, not the manifests.** Pinning a git
   dependency that an external crate also depends on by branch creates two
   copies at the same commit under different source URLs. Use `--locked` in CI.

8. **Language and commits.** Code, comments, and commit messages in English.
   Conventional Commits (e.g. `fix(sysproxy): …`).

9. **No performative artifacts.** No verification checklists, no "Testing"
   filler. Real evidence instead: reproduction steps, failure output.

10. **Licence.** This fork is `UNLICENSED` and derived from GPL-3.0-only
    upstream. Record attribution and licence obligations before any distribution;
    do not assume the inherited `LICENSE` file still describes this tree.
