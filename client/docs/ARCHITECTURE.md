# Client fork — architecture and rework plan

> **Status:** design document, pre-implementation.
> **Fork base:** Clash Verge Rev `v2.5.5` (`22e3f1ac`), copied into `client/`, history removed.
> **Reference implementation:** the retired Wails client at `legacy/wails-client/` (formerly
> `v5/client/`). It is *reference only* — it does not ship — but three years of
> field bugs are recorded in its `FIXES.md` and half of this document exists to make
> sure the fork does not re-earn them.

---

## 1. Why this document exists

The fork is not "Clash Verge with the UI trimmed". It is a different product built on
Verge's core-management machinery. Three things make the difference large enough to
need written architecture:

1. **The updater is completely different.** Verge uses the Tauri updater plugin against
   a signed static manifest. Locus uses a hub-mediated `update_config` record with
   per-platform SHA-256s, a rollout percentage, and a one-shot code-bearing download
   link. Section 5 is the full reconciliation; it is the single most consequential
   design decision in the fork.
2. **Verge has no concept of a user.** There is no account, no code, no device, no
   entitlement. Everything Locus needs — activation, tiers, heartbeat, grace — is
   additive, and must not fight Verge's "profiles are the source of truth" model.
3. **The old client is a specification.** Its `updatecfg` package documents a
   three-layer contract between publish scripts, the hub hook, and the client, where
   a *rename* between layers is load-bearing for already-deployed clients. A rewrite
   that "simplifies" that rename breaks every client in the field. That is exactly the
   kind of thing a fresh implementation gets wrong, so it is written down here.

---

## 2. What the fork inherits (verified, not assumed)

Counts and paths verified against the tree in `client/`.

| Area | Location | Size | Notes |
|---|---|---|---|
| Rust app shell | `src-tauri/src/` | ~1.4 MB source | Tauri 2 |
| Core management | `src-tauri/src/core/` | 724 KB | manager, service, tray, updater, runstate |
| Config | `src-tauri/src/config/` | 176 KB | `verge.rs` (`IVerge`), `draft.rs`, `profiles.rs` |
| Commands | `src-tauri/src/cmd/` | 108 KB | 84 registered in `lib.rs` |
| Features | `src-tauri/src/feat/` | 132 KB | |
| **Frontend** | `src/` | 4.5 MB | React 19 + MUI + i18next |
| Workspace crates | `crates/` | 7 crates | see §3 |

**Pages** (`src/pages/`, 3,086 lines total):

| Page | Lines | Fate |
|---|---|---|
| `profiles.tsx` | 999 | **cut** |
| `unlock.tsx` | 411 | **cut** |
| `home.tsx` | 398 | keep, rework |
| `connections.tsx` | 340 | **cut** |
| `logs.tsx` | 207 | **cut** |
| `proxies.tsx` | 199 | keep (node selection) |
| `_layout.tsx` | 179 | keep, rework |
| `settings.tsx` | 121 | keep, gut |
| `rules.tsx` | 104 | **cut** |
| `_navigation.tsx` | 78 | rewrite |
| `_theme.tsx` | 32 | keep |
| `_routers.tsx` | 18 | rewrite |

**Upstream identity removed already** (`client/` state as of this document): package
name `locus-client`, Cargo package `locus`, license `UNLICENSED`, bundle id
`com.locus.client`, product name `Locus`, deep-link scheme `locus`, UA fallback
`locus-client`, upstream CI and git hooks deleted. The Tauri updater plugin block was
**removed from `tauri.conf.json`** — see §5.

---

## 3. Build and dependency reality

- **Toolchain:** `rust-toolchain.toml` pins **1.98.1**; `Cargo.toml` declares
  `edition = "2024"` (needs ≥1.85). Node 24 + pnpm work. `package.json` scripts are
  `web:build` (tsc + vite) and `web:dev`; `build` invokes the Tauri CLI.
- **Workspace members:** `src-tauri` + `crates/{clash-verge-draft, clash-verge-media-unlock,
  clash-verge-logging, clash-verge-signal, tauri-plugin-clash-verge-sysinfo,
  clash-verge-i18n, clash-verge-limiter}`.
- **Sidecar, not a library:** `externalBin: ["sidecar/verge-mihomo",
  "sidecar/verge-mihomo-alpha"]` — the mihomo core ships as a bundled executable.
  This is why the engine question is settled by construction: the fork *is* a mihomo
  client, and `legacy/wails-client`'s `ENGINE-SWAP-ANALYSIS.md` Track B is moot.
- **Git dependencies (five, all reachable only over the network):**

  | Crate | Pin | Note |
  |---|---|---|
  | `clash_verge_service_ipc` | `tag = v2.7.3` | privileged service IPC; external repo |
  | `tauri-plugin-mihomo` | `rev = 17c7757…` | pinned by us (was `branch = main`) |
  | `tracing-estuary` | `rev = dbfce03…` | pinned by us (was `branch = main`) |
  | `tray-icon` | `rev = 5750c67…` | **`[patch.crates-io]`** — a fork of a fork (macOS 27 click fix) |
  | `sysproxy` | `branch = main` | **left floating deliberately** — see below |

  **On `sysproxy`:** pinning it in `src-tauri/Cargo.toml` produced *two* copies of the
  crate at the same commit, because `clash_verge_service_ipc` (which we cannot edit)
  hard-depends on it via `branch = main`. Two copies means two distinct `sysproxy`
  types in the graph, which is a silent misbehaviour risk. The pin was reverted.
  **Reproducibility therefore comes from `Cargo.lock` (which records the exact
  revision), not from the manifest — CI must use `--locked`/`--frozen`.**

- **`removeUnusedCommands: true`** in `tauri.conf.json` prunes Rust commands not
  reachable from the frontend. Consequence for the debloat: removing a page
  automatically removes its backend surface, so backend shrinkage follows the UI.
  This is the mechanism that makes "cut a page" cheap, and it is also why deleting
  pages must be done in a specific order (§6).

---

## 4. Target product

A single-purpose VPN client: paste a code, get a tunnel. Everything else is gone.

**Kept surfaces**

| Surface | Behaviour |
|---|---|
| Activation | code entry, client-side Luhn + prefix/charset validation, then `POST /api/activate` |
| Home | connect/disconnect, current status, tier badge, grace/expiry, update prompt |
| Nodes | the tier's proxy group; manual node choice only if a tier exposes more than one |
| Settings | minimal: language, theme, autostart, TUN on/off, diagnostics export |

**Cut surfaces:** profiles, subscriptions, rule editing, connections inspector, logs
viewer, media-unlock, WebDAV/local backup, Monaco editor, DNS editor, hotkeys, UWP tool.

**Non-goals:** multi-profile support, provider/subscription sync, per-rule routing
exposure, anything that assumes the user understands Clash.

---

## 5. → detailed updater reconciliation

The most important section. **See `UPDATE-ARCHITECTURE.md`** — it is long enough to
deserve its own file, and it is the design the rest of the rework depends on.

Summary of the decision: **the Tauri updater plugin is removed, not configured.** Locus's
existing hub-mediated update path is ported to Rust almost verbatim, because it is
already the contract every deployed client speaks.

---

## 6. Debloat plan

Order matters. Pages are deleted before their components, components before their
hooks, hooks before their commands, and each step is verified with `pnpm run web:build`
(which runs `tsc --noEmit`) and `cargo check`.

### 6.1 Frontend

1. **Navigation first.** Rewrite `src/pages/_navigation-meta.ts` and
   `src/pages/_navigation.tsx` to only the kept items (home, proxies, settings).
   This is the cheapest step and it immediately reveals what is orphaned.
2. **Delete pages:** `profiles.tsx`, `unlock.tsx`, `connections.tsx`, `logs.tsx`,
   `rules.tsx`. Update `_routers.tsx` (it maps over `navItems`, so it follows
   automatically).
3. **Delete components/hooks** that only those pages reach. Expect the largest cuts in
   `src/components/profile/**`, `src/components/unlock/**`,
   `src/components/connection/**`, `src/components/rule/**` (110 `.tsx` files total).
4. **Dependencies to remove from `package.json`:** `@monaco-editor/react`,
   `@dnd-kit/*` (4 packages), `@tanstack/react-virtual` (if only used by the cut
   list views), `react-markdown`/`remark-gfm`/`rehype-raw` (changelog + profile docs).
   **Monaco is the single largest win:** it emits `editor.api` 2.65 MB + `vs` 1.27 MB
   (~1.0 MB gzipped combined).
5. **i18n:** 13 locales (`ar de en es fa id jp ko ru tr tt zh zhtw`) → the ones you
   actually support. `en/settings.json` is 706 lines and will shrink to a fraction once
   the settings page is gutted; `en/profiles.json` (213) disappears entirely.
6. **The "Clash Verge" string sweep** (115 remaining hits, all in `src/locales/*`).
   **These are not a blind find-and-replace.** A large share name the *privileged
   system service* in user-facing errors, e.g. `en/layout.json`:
   > "The Clash Verge service version does not match the version required by this app…"
   > "TUN mode needs the Clash Verge system service to take over network traffic."

   Decide the user-facing name for that component (suggestion: **"the Locus network
   service"**) and apply it per-string. Getting this wrong produces error messages
   students cannot act on, which is the "permission failures reported as opaque engine
   errors" bug (FIXES #18) in a new costume.

### 6.2 Rust

1. `crates/clash-verge-media-unlock` — remove from workspace and delete; `feat/` and
   `cmd/media_unlock_checker` follow.
2. `cmd/profile.rs`, `cmd/save_profile.rs`, `cmd/webdav.rs`, `cmd/backup.rs`,
   `feat/profile.rs`, `feat/backup.rs` — removal candidates once profiles are cut.
   **Careful:** the tier config is applied *through* the profile/config pipeline
   (§7), so keep `core/manager/config.rs` and whatever it needs, even though the
   profile *UI* is gone.
3. `cmd/uwp.rs`, `feat/`-adjacent `win_uwp.rs` — the WSL/UWP loopback exemption. Decide
   whether to keep; it is a Windows-only helper that can improve compatibility.
4. Re-run `cargo check` after each removal; `removeUnusedCommands` handles command
   deregistration, not dead modules.

### 6.3 Security tightening (not deletion)

`capabilities/migrated.json` and `capabilities/desktop-capability` currently grant:

- `fs:scope` `allow: ["**"]`, twice (read and write)
- `assetProtocol` scope `"**"`, `requireLiteralLeadingDot: false`
- `http:default` with allow `https://*/*` and `http://*/*`

The fork needs `fs` scoped to its own app dir and the sidecar/core dirs, `http` scoped
to the hub host, and an asset-protocol scope that is not the whole filesystem. This is
the same class of bug as the `admin_console` hook-order issue (a too-broad grant, not a
missing feature), and it is a **shipping requirement, not cleanup** — an unrestricted
`fs` scope in a WebView app is a local-privilege footgun.

---

## 7. Adding the Locus logic

### 7.1 Where it goes

| Concern | New location | Do not put it in |
|---|---|---|
| Code validation + activation | `src-tauri/src/locus/activation.rs` | `cmd/` (keep commands thin) |
| Heartbeat loop | `src-tauri/src/locus/heartbeat.rs` | `core/` (Verge's; keeps upstream parity) |
| Product state persistence | `src-tauri/src/locus/store.rs` | `config/verge.rs` |
| Tier → mihomo config | `src-tauri/src/locus/tier.rs` | `enhance/` |
| Update client | `src-tauri/src/locus/update/` | `core/updater.rs` (delete that) |
| Commands | `src-tauri/src/cmd/locus.rs` | — |

The `locus/` module is the product; everything Verge-provided stays where it is so the
seam is obvious in review. `IVerge` (`config/verge.rs`) gets the product fields added
**additively** — it is already all `Option<T>` with serde defaults, so
`activation_code`, `tier`, `device_fingerprint` slot in without new machinery.

### 7.2 Contracts to port (from the old client, verbatim)

**Activation** — `POST /api/activate`:

```
request:  { "code": "...", "fingerprint": "..." }
response: { code, message, tier, device_fingerprint,
            server_config: { server, server_port, password, method, server_port_uot },
            udp_relay }
```

Status codes are handled distinctly: a used code on the same fingerprint succeeds; on a
different fingerprint it is 403; suspended is 403 with a distinct message. Validation is
**client-side first** — Luhn checksum plus hub-supplied charset/prefix — so a typo does
not cost a round trip. The hub supplies the alphabet and prefix; **do not hardcode
them** (the old client exposes `GetCodeCharset`/`GetCodePrefix` for this reason).

**Heartbeat** — constants verified in `internal/heartbeat/heartbeat.go`:

| Constant | Value |
|---|---|
| `MinInterval` | 5 minutes |
| `MaxInterval` | 2 hours |
| `GracePeriod` | 7 days |
| Backoff | `min(MinInterval * 2^failures, MaxInterval)`, resets to min on success |

The loop sends jitter, and the response carries the update signal (§5). Grace is
computed from `last_heartbeat_ok`; a device that never succeeded gets the full window
from first launch.

**Tier config shape** (`tier_configs` on the hub, read by `activation.pb.js` and
`heartbeat.pb.js`):

```
{ tier, server, server_port, password, method, udp_relay, uot_port }
```

**The `uot_port` naming trap (FIXES #29):** the hub field is `uot_port`, but the client
has always received it under the key `server_port_uot`. The hub hook renames it on the
way out, and the client's `ServerConfig.UnmarshalJSON` accepts both. This rename is
**load-bearing for deployed clients** and must be reproduced exactly — the same class
of hazard as the `download_* → update_*` rename in §5.

### 7.3 Where the tier config is applied

The tier payload becomes a mihomo config and is handed to the **existing** Verge
config pipeline in `core/manager/config.rs`:

```
update_config_forced() → validate_and_apply() → apply_config() | apply_config_by_service()
                                              → reload_or_restart()
```

Use this path rather than writing a second one. It brings config validation, the
service-vs-sidecar staging decision, and reload-vs-restart policy for free. The tier
config is generated into the same location a "profile" would occupy, which is why the
profile *pipeline* survives even though the profile *UI* does not (§6.2 item 2).

### 7.4 Frontend additions

- First-run **activation gate**: the app renders `ActivationScreen` and nothing else
  until `IsActivated`. Keep the old client's UX detail that an *already-used code on the
  same device* succeeds — students reinstall and re-paste, and a rejection there reads
  as "my code is dead".
- **Status indicator** states: disconnected / connecting / connected / degraded /
  grace-expiring. The old client's `degradedStreakMax = 5` (~50 s of a stubbornly
  broken tunnel before handing control back to the student) is a good default.
- Tier badge, grace countdown, and an update prompt that cannot be dismissed into a
  dead end (see `UPDATE-ARCHITECTURE.md` §"the code deadlock").

---

## 8. Reference: bugs the old client paid for

From `legacy/wails-client` and its `FIXES.md` (60 numbered entries). These are the ones
the fork can plausibly re-earn:

| # | Bug | Fork risk |
|---|---|---|
| 10 | `findRecordsByFilter` returns `[]` silently in PB 0.22.21 — update gate was a no-op | Server-side; but the **client-side** symptom ("we never saw an update") is the reason to log update decisions explicitly in the fork |
| 11 | Per-platform checksums missing → every platform but one failed verification | The ported updater must carry all four platforms |
| 12 | Version gate could **downgrade** (`"1.9.0" > "1.10.0"` as strings) | Port `CompareVersions` semantics, not string compare |
| 17 | `isElevated()` returned `true` unconditionally on Unix | Verge's service layer replaces this, but do not re-add hand-rolled elevation |
| 18 | Permission failures surfaced as opaque engine errors | Applies directly to the "Clash Verge service" i18n strings (§6.1 item 6) |
| 20/45 | Version duplicated across 5–6 files, nothing kept them in sync | The fork adds *three* more version sites (`package.json`, `tauri.conf.json`, two `Cargo.toml`s). Needs the drift gate |
| 22 | Binary-swap failures could destroy the installation | Verge's NSIS handles install; do not reintroduce a hand-rolled swap |
| 40/41 | Elevation handoff misread a normal auto-connect as a failed relaunch; `GetTokenInformation` called with 4 args instead of 5 | Adopt Verge's service elevation wholesale; do not port any of the old client's elevation code |
| 46–48 | Publishing required an operator machine; 1 MB floor rejected a valid release; one-shot link reuse | Server-side; see `UPDATE-ARCHITECTURE.md` |
| 49–53 | Orphaned engine on disconnect; "already running" dead end; watchdog escalating after disconnect | **The most portable bugs.** Verge's `core/manager/lifecycle.rs` has `start_core`/`stop_core`/`restart_core`; map the old client's symptoms onto it and test that disconnect cannot leave a running core |
| 54–56 | Updater staged into CWD; download handle still open at rename; unreadable error | Verge's updater is replaced, so these vanish — *provided* §5 is implemented as written |
| 59 | The guard ran after the expensive work, and after the tag | CI ordering; relevant to the fork's release workflow |

---

## 9. Open decisions

1. **User-facing name for the privileged service** (i18n sweep, §6.1 item 6).
2. **Whether node selection is exposed at all.** If every tier is one server, the
   proxies page can go too, which cuts a further ~199 lines plus its components.
3. **Telegram channel link** in `src/pages/settings.tsx` still points at
   `t.me/clash_verge_re`.
4. **`locus.app` placeholder URLs** (docs/repo/release links) — confirm the real domain
   or remove the buttons.
5. **TUN on/off in settings vs. always-on.** Verge defaults to system-proxy mode;
   the old client was TUN-only. This changes the failure modes students see.
6. **Linux support level.** ~2% of clients; the fork builds for it, but is it tested
   or best-effort?

---

## 10. Verification strategy

- **Frontend:** `pnpm run web:build` (runs `tsc --noEmit`) after every deletion.
  Currently green on the un-debloated tree.
- **Rust:** `cargo check` / `cargo clippy` locally after the toolchain gap is closed
  (needs `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`, `libsoup-3.0-dev`,
  `libjavascriptcoregtk-4.1-dev`). **Local `cargo build` currently fails** on this host
  for missing dev headers, not for code reasons.
- **CI is the real gate:** the Tauri build for three platforms, with `--locked`, on
  GitHub Actions (§11 of the parent plan).
- **Parity tests** ported from the old client's Go tests where they encode contract
  behaviour: Luhn, version comparison, tier-key decoding (`server_port_uot` /
  `uot_port`), grace-period arithmetic, and the artifact filename contract.
