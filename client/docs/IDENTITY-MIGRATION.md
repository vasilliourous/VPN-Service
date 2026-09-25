# Client identity migration — Locus

> **Status: implemented for the app-data root; the privileged service name is deliberately NOT renamed.**
> Created 2026-09-24 while re-identifying the fork from upstream Clash Verge Rev.
> Read this before "tidying up" any `clash-verge` string in `client/`.

---

## 1. Why this file exists

The fork was copied from Clash Verge Rev v2.5.5 with upstream git history removed.
Only the *branding* was changed at copy time (product name, icon, app id in
`tauri.conf.json`). The **code** still carried upstream's identity in places that
are load-bearing rather than cosmetic, and one of them — `APP_ID` — is the
application data root.

This file records what was changed, what was deliberately left, and why, so the
next person does not re-open the same questions or "fix" an asymmetry that is
holding something up.

---

## 2. `APP_ID` — the app-data root (CHANGED)

`client/src-tauri/src/utils/dirs.rs`:

| | Before | After |
|---|---|---|
| release | `io.github.clash-verge-rev.clash-verge-rev` | `com.locus.client` |
| dev (`verge-dev`) | `io.github.clash-verge-rev.clash-verge-rev.dev` | `com.locus.client.dev` |
| `BACKUP_DIR` | `clash-verge-rev-backup[-dev]` | `locus-backup[-dev]` |

**Why this is not a cosmetic rename.** `app_home_dir()` joins this onto the
platform data directory, and **43 call sites** resolve through it:

- `verge.yaml` (all app settings), `config.yaml`, `profiles.yaml`, `profiles/`,
  `icons/`, `logs/`, `locus-backup/`
- the product state added by this remodelling — activation code, tier, device
  fingerprint, heartbeat counters, pending-update records
- `.encryption_key` (encrypts the WebDAV credentials in `verge.yaml`)
- on Windows: **the owner token file the privileged Service authenticates
  against**, plus the instance lock/record files

So renaming it is a **user-visible state migration**, not a string change.

### 2.1 The one-time adoption (`migrate_legacy_app_data_dir`)

`utils::dirs::migrate_legacy_app_data_dir()` runs from `run()` in `lib.rs`,
**before** the Windows owner repair, the singleton check, and the logger — all of
which resolve paths through the data root, so any of them running first would
operate on the new root before the old contents arrived.

Rules, deliberately conservative because the failure mode is a half-copied config
directory:

- **Only runs when the current root does not exist.** An existing Locus root is
  never touched, so this is safe to call on every launch and is a no-op after the
  first successful run.
- **Renames, never copies.** `fs::rename` is atomic within a filesystem, so a
  crash cannot leave two partially-populated roots. A cross-filesystem rename
  *fails*, and that failure is reported — it is not degraded into a recursive
  copy, because a truncated profile set that the app then rewrites is worse than
  no migration at all.
- **A failure is non-fatal.** The app starts with an empty data root and the user
  re-activates; the legacy directory is left intact for a manual move. Refusing
  to launch would be strictly worse.
- **Candidates are listed newest-first** in `LEGACY_APP_IDS`, and the first that
  exists wins. Only one legacy root is expected per build flavour; the list
  exists so a future identity change does not require restructuring the call.

`LEGACY_APP_IDS` holds **historical literals** and must not be replaced with the
current `APP_ID` — the migration would then have nothing to look for.

### 2.2 Windows: what the adoption does and does not fix

On Windows the Service authenticates by comparing the **owner SID of the app data
root** against the caller's, and by an owner token file *inside* the root
(`core/owner_identity.rs`, `repair_app_data_root_owner`). Consequences:

- The adopted directory carries the token with it, and the directory owner is
  unchanged, so the migration keeps the Service working.
- If ownership is wrong, `repair_app_data_root_owner` already repairs it, and it
  now runs **after** the adoption rather than before.
- If the Service was installed against the OLD root, the transport health check
  reports `ReinstallRequired` / `ForceReinstallRequired` and the app asks for a
  reinstall. That is the existing self-heal path (`core/service.rs`), not
  something this migration has to handle specially.

### 2.3 Migration checklist for a real install

1. First launch under `com.locus.client` adopts the legacy directory; confirm the
   log/stderr line `[locus] adopted pre-Locus application data from …`.
2. Confirm the activation code / tier survived (the client should not ask to
   activate again).
3. On Windows with the Service installed, confirm core start works; if it asks to
   reinstall the Service, that is expected and is a one-click fix.
4. Confirm `.encryption_key` moved with the directory — otherwise stored WebDAV
   credentials will fail to decrypt.

---

## 3. What was deliberately NOT renamed

### 3.1 The privileged service name (upstream, and load-bearing)

`core/service.rs` opens the service via
`clash_verge_service_ipc::WINDOWS_SERVICE_NAME`. That constant belongs to the
**pinned upstream crate** `clash-verge-service-ipc` (tag `v2.7.3`), which also
ships the service binary itself (`clash-verge-service`, `-install`, `-uninstall`,
fetched by `scripts/prebuild.mjs`).

Renaming it means forking that crate and its binaries, i.e. owning a privileged
Windows service end to end. That is a **separate decision with its own risk
profile**, not part of a branding pass. Until it is taken, the service keeps its
upstream name and the app keeps its upstream identity to the service.

User-visible effect: **none**. The service name appears in `services.msc` and in
the app's own diagnostic strings, not in the product surface.

### 3.2 Rust crate names, identifiers, and paths

`clash_verge_i18n`, `clash_verge_logging`, `clash_verge_draft`,
`clash_verge_signal`, `clash_verge_limiter`, `clash_verge_service_ipc`,
`tauri-plugin-clash-verge-sysinfo`, the `crates/` workspace members, sidecar
filenames (`verge-mihomo`, `verge-mihomo-alpha`), the mihomo IPC socket
(`verge-mihomo.sock`), the tray id (`clash-verge-rev-tray`), temp-file prefixes,
and the runtime/check config filenames (`clash-verge.yaml`, `clash-verge-check.yaml`).

These are internal identifiers. Renaming them is churn with no product benefit and
several failure modes (see §3.1 for the worst one). The rule from
`client/AGENTS.md` applies: **minimal diffs; do not rename inherited identifiers
unless the change requires it.**

---

## 4. Still upstream, and should be fixed on sight

These are user-visible and are *not* load-bearing:

| Where | What | Note |
|---|---|---|
| `src-tauri/tauri.conf.json` | deep-link scheme `locus` | already correct |
| `src-tauri/src/utils/init.rs` | `DEEP_LINK_SCHEMES = ["clash", "clash-verge"]`, `clash-verge.desktop` | Linux deep-link registration |
| `src-tauri/src/utils/resolve/scheme.rs` | accepts `clash` / `clash-verge` schemes | must match §above |
| `src-tauri/src/utils/network.rs` | `clash-verge/v{version}` User-Agent | |
| `src-tauri/src/utils/schtasks.rs` | `clash-verge-task-{user,admin}.xml` | Windows scheduled tasks |
| `src-tauri/src/utils/macos_launch_guard.rs` | `Contents/MacOS/clash-verge` in install-location checks | **must match the real bundle binary name** |
| `src/pages/settings.tsx` | `t.me/clash_verge_re` Telegram link | |
| `src/components/setting/mods/update-viewer.tsx` | `https://locus.app/releases/...` | placeholder domain — needs a real decision |

The deep-link scheme and the bundle binary name are a **matched pair**: changing
one without the other breaks deep links and the macOS launch guard respectively.
Check both sides before editing.

---

## 5. Related

- `client/docs/ARCHITECTURE.md` — what to cut from upstream and where Locus logic goes
- `client/docs/LOGIC-INVENTORY.md` — the modules to write
- `docs/STILL-OPEN.md` — open decisions, including fork version authority
- `docs/FIXES.md` — the historical defect log
