# Client release signing — Locus

> **The private key is a single point of failure in both directions.** Losing it
> means no installed client can ever accept another update. Leaking it means
> anyone can publish a build every client will install. Read this before
> touching anything under `.locus-keys/` or the CI signing step.

---

## 1. Why signing exists at all

`tauri-plugin-updater` **mandatorily** verifies a minisign signature over every
downloaded update:

```rust
// tauri-plugin-updater-2.11.0/src/updater.rs:740
verify_signature(&buffer, &self.signature, &self.context.config.pubkey)?;
```

There is no bypass. The `dangerous_*` config flags affect TLS only, not
signature verification. So the plugin can only install a package that was signed
by a key whose public half is compiled into the app.

This is the trade for using the plugin's install path — which handles NSIS on
Windows, `.app`/`.dmg` on macOS, and the Linux package formats correctly. The
retired Wails client hand-rolled that and destroyed an installation (FIXES #22),
so we keep the plugin and pay the cost of key management.

---

## 2. What the plugin actually verifies

Verified by reading `minisign-verify-0.2.5` (the crate the plugin calls), and by
an end-to-end test against this keypair:

- It is **real minisign format**, not raw Ed25519: an Ed25519 key plus an 8-byte
  key ID, and a signature carrying a *global* signature over the trusted comment.
  OpenSSL alone cannot produce this — use the `minisign` tool.
- Both the public key and the signature are **base64-wrapped** before being
  decoded by the plugin. A `.pub` file is embedded directly; a signature file is
  embedded with its text base64'd.
- The key ID in the signature must match the key ID in the public key, or
  verification fails with *"created with a different key"*.

### Proven, not assumed

Both halves were tested before any production code was written:

| Case | Result |
|---|---|
| Artifact signed with the real key, verified via the plugin's own decode+verify path | **OK** |
| Artifact tampered with after signing | **FAILED: signature verification failed** |
| Correct signature, wrong public key | **FAILED: created with a different key** |

A verifier that accepts anything would be worse than none, which is why the
negative cases are recorded here.

---

## 3. The keypair

| | |
|---|---|
| **Public key** | `RWT9eeTiJVDWZDZ8hFQ7TOE6BW7OEwARW+kVEU7m2jIlxcjfiGR9InKW` |
| **Where it lives** | `client/src-tauri/tauri.conf.json` → `plugins.updater.pubkey` |
| **Secret key** | `.locus-keys/locus_update.key` (gitignored) |
| **Where it must go** | GitHub Actions secret `LOCUS_UPDATE_KEY`, **and** an offline copy you control |
| Key ID | `64D65025E2E479FD` (shown in the `.pub` comment line) |

The key has **no passphrase**, deliberately: CI must sign non-interactively. That
makes the secret-store copy the only thing protecting it — treat it like the
production database credentials.

`.locus-keys/` is in the root `.gitignore`. Verify it stays that way:

```sh
git check-ignore -v .locus-keys/locus_update.key
```

---

## 4. Generating a replacement keypair

Only do this if the key is lost or must be rotated — see §5 for the
consequences. Requires the real `minisign` tool (the crate in the registry,
`minisign-verify`, is **verify-only** and cannot sign).

```sh
# Fetch minisign 0.11 (or newer) for your platform
curl -sSL -o minisign.tar.gz \
  https://github.com/jedisct1/minisign/releases/download/0.11/minisign-0.11-linux.tar.gz
tar xzf minisign.tar.gz

# -G generate, -W no passphrase (required for non-interactive CI signing)
./minisign-linux/x86_64/minisign -G -W \
  -p locus_update.pub -s locus_update.key \
  -c "Locus client update signing key"
```

Then, **before shipping anything**:

1. Put the public key (the second line of `locus_update.pub`) into
   `tauri.conf.json` → `plugins.updater.pubkey`.
2. Put the whole `locus_update.key` file into the `LOCUS_UPDATE_KEY` GitHub
   secret.
3. Keep an offline copy. Losing it is not recoverable by any means short of §5.
4. Re-run the sign/verify round trip in §6.

---

## 5. Rotation — the hard constraint

**A client only trusts the public key it was built with.** There is no
server-side mechanism to hand out a new one. So rotating the key means:

- Every client built with the **old** key will refuse every update signed with
  the **new** key. Permanently.
- Those clients can only be moved forward by an update signed with the **old**
  key. If the old key is lost, they can only be updated by reinstalling by hand.

Therefore:

| Situation | Consequence |
|---|---|
| Key **lost**, clients in the field | Those clients can never auto-update again. Recovery is a manual reinstall (a new installer signed with the new key). |
| Key **leaked** | Anyone can publish a malicious build that every client installs. Rotate immediately; accept that old clients need manual reinstalls. |
| Key **leaked and clients small** | Cheaper to rotate early. This is one argument for rotating while the fleet is small. |

**The public key is part of the app's identity.** Changing it is a breaking
change on the same level as changing the app ID, and it must be planned as one.

---

## 6. Verifying a signature by hand

```sh
minisign -V -p locus_update.pub -m locus-windows-amd64.exe -x locus-windows-amd64.exe.sig
```

Expected: `Signature and comment signature verified`.

To test exactly what the plugin does (including the base64 wrapping), build the
harness described in §2 — it base64-encodes the `.sig` and `.pub` files, decodes
them, and calls `minisign_verify::PublicKey::decode` + `verify`. That is the only
test that proves the *plugin* will accept the signature, as opposed to minisign
verifying its own output.

---

## 7. Where signing happens in the pipeline

```
CI build  →  four raw binaries + manifest.json
          →  sign EACH binary with minisign        ← LOCUS_UPDATE_KEY
          →  attach binaries + .sig files to the GitHub Release
                                                    ↓
hub       →  fetch-release.py downloads and verifies format + SHA-256
          →  update_config gains signature_<platform> columns
                                                    ↓
client    →  checks GET /api/update?version=…&platform=…
          →  the hub answers with the DYNAMIC manifest shape:
                 { "version": "2.3.0", "url": "…", "signature": "…" }
          →  UpdaterBuilder::endpoints([our url]) -> check() -> Update
          →  install(), which re-verifies the minisign signature
```

### Why the dynamic manifest shape, and not a hand-built `Update`

The first design attempted to construct a `tauri_plugin_updater::Update`
directly from hub data. **That does not compile**: two of its fields
(`extract_path`, `context`) are private, so a struct literal from outside the
crate is rejected. Only `Updater::check()` produces one.

The workable route uses what the plugin *does* expose:

- `UpdaterBuilder::endpoints(..)` **replaces** the configured endpoint list, so
  a URL built at runtime is honoured (and `tauri.conf.json` can leave
  `endpoints` empty, meaning no stale static manifest can ever be consulted).
- `check()` accepts a **dynamic** response — a single `{version, url, signature}`
  object — which the hub can serve directly, per platform and per entitlement.

So the plugin owns download, mandatory signature verification and the platform
install, while the *decision* stays ours.

Two independent integrity checks on purpose: SHA-256 catches a corrupted or
truncated download; the minisign signature catches a *substituted* artifact,
which a hash published by the same server could never detect.

---

## 8. Related

- `client/docs/UPDATE-ARCHITECTURE.md` — why the updater is hub-mediated
- `docs/RELEASING.md` — publishing a build to the hub
- `docs/OPS.md` → "Client Update System"
- `docs/FIXES.md` — the historical defects this design avoids
