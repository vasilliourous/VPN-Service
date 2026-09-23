# ARCHIVED — the retired Locus client

This directory is the **retired Wails client**: Go + Wails v2 + Vue 3, tunnelling
through **sing-box**. It shipped as `v5/client/` until 2026-09-23 and is no longer
the product.

**The shipping client is `client/` at the repository root** — a fork of
[Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev) v2.5.5
(Tauri 2 + Rust, React frontend) which tunnels through **mihomo**.

## Why this is kept

Not as a fallback and not as backup code. It is kept because it encodes the
**behavioural contract** the fork must reproduce, in executable form:

| File | Encodes |
|---|---|
| `internal/activation/luhn_test.go` | Activation-code Luhn-mod-N checksum |
| `internal/activation/lookup_test.go` | Code-lookup semantics |
| `internal/updatecfg/updatecfg_test.go` | The three-layer `update_config` field contract **against the live hub and publish scripts** |
| `internal/uotkey/contract_test.go`, `uotkey_test.go` | UoT key derivation |
| `internal/storage/storage_test.go` | Atomic writes, 0700/0600 perms, 3-deep backup rotation |
| `internal/updater/version_test.go`, `manifest_test.go` | Version comparison, manifest parsing |
| `internal/buildinfo/buildinfo_test.go` | Build metadata |
| `version_consistency_test.go` | Version drift across every site |
| `internal/manager/lifecycle_test.go`, `watchdog_test.go` | Core lifecycle; disconnect must not orphan the engine |

**Port the behaviour before reimplementing it.** A passing test here is a
specification; a passing test in the fork is the implementation. Do not delete a
test because the fork "does it differently" — decide first whether the contract
changed, and if it did, say so where the hook or hub is changed too.

`internal/updatecfg` is the sharpest example. Its test reads
`server/pb_hooks/heartbeat.pb.js` and `server/scripts/publish-release.sh` **as
text** and asserts the field names agree. That guard spans three languages
(goja JS, Go, Rust) and is the reason FIXES #31 cannot recur silently.

## Version

This tree is **completely stale** and no longer carries a maintained version. The
root `VERSION` file and the tooling in `server/scripts/bump-version.sh` that used
to keep *this* tree's version sites (`main.go`, `buildinfo.go`, `wails.json`,
`frontend/package.json`) and its committed Windows `.syso` resources consistent
have been **deleted**, along with the `.syso` files and
`.github/workflows/build.yml`.

The version strings frozen in this tree are whatever they last said and are
**not** authoritative for anything. The shipping fork versions itself in
`client/package.json` and `client/src-tauri/Cargo.toml`; reconciling the fork's
own sites is an open decision (`docs/STILL-OPEN.md`). Nothing bumps anything.

## Building it

The Go toolchain is **not** installed on this host and is not on `PATH`. See
`docs/OPS.md` for the bootstrap procedure. Building requires a Go 1.22+ toolchain
plus the Wails environment; the frontend must be built before
`go build -tags frontend`.

**The checks below no longer work — the scripts they name are deleted:**

```sh
# ⚠️ NONE OF THESE EXIST ANY MORE
./bump.sh --dry-run
python3 server/scripts/stamp-syso.py "$(cat VERSION)" --check
bash server/scripts/smoke-bump.sh
```

## What was retired, and what was not

Retired with this tree: the Wails/sing-box engine path, the elevation helpers
(`elevate_*.go` — buggy three times over, and the fork's service owns this now),
and the hand-rolled updater installer mechanics.

**Still live and shared, not part of the archive:** `server/` (the hub),
`server/console/` (the admin console), `scripts/` (code generation), and the
publish chain in `server/scripts/`.
