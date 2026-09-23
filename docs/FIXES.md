## RELEASE MACHINERY REMOVED, NOT REPAIRED (2026-09, current)

The archived Wails client's version/release machinery was **deleted** rather than
fixed a further time: root `VERSION`, `bump.sh`,
`server/scripts/bump-version.sh`, `stamp-syso.py`, `smoke-bump.sh`,
`scripts/release-cut.sh`, the committed `rsrc_windows_*.syso` resources, and
`.github/workflows/build.yml`.

Rationale: every entry below in this file that ends "…and the gate that knew"
describes a guard on a pipeline that built a client nobody ships. It versioned only
`legacy/wails-client/`, never the fork, and its tags were the source of repeated
confusion. `legacy/wails-client/` is now a stale, reference-only logic oracle; the
shipping fork's versioning and release path are an **open decision**
(`docs/STILL-OPEN.md`).

**Entries below this line are historical.** Where they name `bump.sh`, `VERSION`,
`release-cut.sh`, `stamp-syso.py`, `smoke-bump.sh`, `.syso`, or `build.yml`, those
files no longer exist. The *lessons* (a committed artifact can be stale while every
text check passes; `--no-verify` skipped the artifact update; a skipped CI job looks
green) remain worth knowing if any of this is ever rebuilt.

---

## RELEASE TAGGING — THE .syso WENT STALE AGAIN, AND THE GATE THAT KNEW (2026-09-20)

CI failed the 2.2.7 release in **Check version consistency**:

```
rsrc_windows_amd64.syso FileVersion = "2.2.6", want "2.2.7"
rsrc_windows_arm64.syso ProductVersion = "2.2.6", want "2.2.7"
```

The reaction this invites is "rerun `go generate` and push" — which is what the
first fix did. That is the wrong read, because the same thing had already
happened once and the guard built to stop it was bypassed, not missing. Fixing
only the artifact would have guaranteed a third occurrence.

### 57. `release-cut.sh` passed `--no-verify`, which skipped the artifact update too

`bump-version.sh` regenerated the `.syso` and re-read them as a self-check — that
gate was added after 2.1.0 shipped resources stamped `2.0.0` / product `MyVPN`.
But `--no-verify` short-circuited the script **before** that block, and
`scripts/release-cut.sh` line 20 passed `--no-verify` unconditionally. So the
automated release path — the one that actually cuts tags — never regenerated
anything, and warned only in text a reader could scroll past.

Evidence in the history: `cd4dbc0` (2.2.6) changed 8 files including both `.syso`;
`916aec7` (2.2.7) changed only 6, with no resources. Nothing noticed until CI ran.

This is the second-order version of the 2.1.0 bug. There, the guard did not exist.
Here it existed and the fast path routed around it — which is worse, because it
looks protected.

### 58. The fix depended on a Go toolchain that the release host does not have

The documented remedy was `cd v5/client && go generate -tags windows`. That needs
a Go install and network access to fetch `go-winres`, and this machine has no
system Go — so "regenerate the artifacts" was advice that could not be followed
where releases are actually cut. A step that is awkward to run is a step that
gets skipped, which is how the artifacts went stale twice.

It does not need a toolchain. A patch-level bump changes only four bytes: the two
UTF-16LE display strings after `FileVersion`/`ProductVersion`, and the packed
`VS_FIXEDFILEINFO` block whose `dwFileVersionMS/LS` and `dwProductVersionMS/LS`
carry the same number in binary. Missing the second one is the subtle failure —
the Properties *string* would read the new version while Windows' own version
comparisons and the installer still see the old one.

`v5/server/scripts/stamp-syso.py` rewrites both with the standard library. Its
output is **byte-for-byte identical** to `go-winres`' — verified in both
directions against the real 2.2.6 and 2.2.7 artifacts, and against the historical
2.2.6 bytes recovered from `cd4dbc0`. It refuses (exit 3) rather than guessing on
a version-width change such as `2.9.9` → `2.10.0`, where the fixed-size fields no
longer fit and `go generate` really is required.

### 59. The guard ran after the expensive work, and after the tag

Even when it fired, `TestCommittedSysoMatchesRepoVersion` ran deep inside the
`lint` job, producing an error that reads like a Go test failure rather than
"your artifacts are stale" — which is exactly how the 2.2.7 report was misread as
a generation-command problem. And it only ever ran **after** a tag had been
pushed, so the tag was already public and wrong.

Three changes, at three points in the pipeline:

| Where | What |
|---|---|
| `bump-version.sh` | The `.syso` stamping now runs **before** the `--no-verify` branch, unconditionally, with no toolchain. `--no-verify` skips the *test* gate, never the artifact update. |
| `release-cut.sh` | Re-checks the committed resources with `stamp-syso.py --check` and **refuses to commit or tag** a stale tree. Toolchain-free, so it cannot be skipped for want of one. |
| `build.yml` | The same check runs first among the version guards, so a stale artifact is attributed to the artifact and not to a failing test. |

### 60. The regression suite did not cover the path that broke

`smoke-bump.sh` ran every case with `--no-verify` and explicitly documented that
the `.syso` regeneration was "covered by CI, not by this smoke test" — so the
exact combination that failed (no-verify, plus no toolchain) had no offline test.
It now seeds a **real** `.syso` pair, stamps it down to the scratch version, and
asserts under `GO_BIN=definitely-not-a-real-go` that a bump still moves the
artifacts, that the packed `VS_FIXEDFILEINFO` fields move with the strings, that
the brand text survives, and that `--check` reports stale without writing.
`smoke-bump` went from 21 assertions to 33.

---

## CLIENT UPDATE — THE PORTABLE BINARY COULD NOT REPLACE ITSELF (2026-09-20)

Field report, immediately after 2.2.3 was published:

```
⚠ Update to 2.2.3 failed: download failed: cannot rename downloaded file: rename
C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp
C:\Users\Hello\Downloads\locus-windows-amd64.exe.new:
The process cannot access the file because it is being used by another process.
```

The download was fine — it reached the rename at the very end, after the
checksum had passed. The rename is the step that installs the new binary, and it
could not run. This was not a fluke of one machine: it is a structural
consequence of how the portable build was deployed.

### 54. The updater staged its download in whatever directory the app was launched from

`PerformUpdate` resolved its working directory as
`filepath.Dir(os.Executable())`. For a portable copy that is wherever the user
unzipped the zip, and the most common place a student runs a downloaded
executable from is **Downloads**.

Downloads is the most heavily observed directory on Windows. Defender,
SmartScreen, the search indexer, cloud-sync clients and download managers all
open a newly written `.exe` within milliseconds of it appearing. The updater
wrote `locus-windows-amd64.exe.new.tmp` and then tried to rename it *in that
same directory*, so it was aiming at the one folder most likely to have something
else holding a handle on the file.

Nothing here is a bug in the rename itself: renaming a file that another process
has open genuinely cannot succeed on Windows. The mistake was choosing a
directory the application does not own as the place to perform an install.

### 55. The download handle was still open at rename time

Independently of any third party, the code raced its own file descriptor:

```go
f, err := os.OpenFile(tmpPath, ...)
defer func() { _ = f.Close() }()   // runs at function RETURN
...
os.Rename(tmpPath, path)           // ...so the handle is still open here
```

`defer` runs when the function returns, which is *after* the rename. On Windows
a file with an open handle cannot be renamed, so this was guaranteed to fail
whenever the timing lined up. The reported error blamed "another process"; in
some of those cases the other process was Locus.

### 56. A transient blocker was never retried, and the error was unreadable

Two smaller defects made the same failure worse:

- `os.Rename` was called once. The holders described above are transient by
  nature — a scanner that opened the file the instant it appeared, a sync client
  finishing up — and a short bounded retry would have ridden most of them out.
- The surfaced error was the raw `os.Rename` output: two absolute paths, a
  Windows status phrase, and no indication of which process was involved or that
  retrying after closing a File Explorer window would work.

### The fix

**Install into a directory the application owns.** A new package
`v5/client/internal/install` resolves where this copy of Locus lives and how it
was deployed:

| Platform | Install location | Rationale |
|---|---|---|
| Windows | `%ProgramFiles%\Locus`, else `%LOCALAPPDATA%\Programs\Locus` | Owned directory; the client already runs elevated (its manifest sets `requireAdministrator`) |
| macOS | `/Applications/Locus.app` | A `.app` is the unit macOS replaces |
| Linux | `~/.local/bin`, else `~/.local/share/locus/bin` | Deliberately unintrusive — nothing system-wide is written |

Anything else resolves as **portable**, which is a supported deployment rather
than a failure — the launched directory is still used, just with a private
staging subdirectory inside it (`<install-dir>/.locus-staging/`). A portable copy
run from Downloads therefore keeps working: the download no longer lands on a
filename that the folder's watchers are sitting on.

The staging directory is deliberately *not* the system temp directory.
`os.Rename` is only atomic within a single filesystem, and `%TEMP%` is frequently
a different volume from the install location — renaming across volumes degrades
to a copy, which is neither atomic nor safe for a running executable.

Also fixed:

- **The handle is closed explicitly before the rename** (the `defer` stays as a
  safety net for error paths), and a close failure is now reported rather than
  silently proceeding into a rename that cannot succeed.
- **Transient rename failures retry** with a bounded backoff — 5 attempts,
  100→1600 ms (~3.1 s total). Permanent failures (missing path, read-only
  filesystem, cross-device) are classified and returned immediately, because
  waiting on them converts a fast, clear error into a slow one.
- **The error is actionable**: it names the file that was blocked, says another
  program is holding it, gives a platform-specific remedy, and states that the
  verified download was left intact so retrying is safe.
- **AppImage-style read-only mounts are detected** and reported as their own
  mode (`immutable`), since elevation cannot help there and the correct advice is
  to replace the bundle.
- **Diagnostics report the install location and mode**, so "update failed" is
  answerable from a support report instead of requiring a reproduction.

### Installers, and the portable build stays

Real installers now ship alongside the portable zips, which continue to be
published and supported:

- **Windows** — Inno Setup (`build/windows/locus.iss`), installs to
  `%ProgramFiles%\Locus`, registers an uninstaller, and removes the staging and
  rollback directories on uninstall.
- **macOS** — a real `Locus.app` bundle inside a `.dmg`
  (`build/macos/make-dmg.sh`), which installs into `/Applications` and carries a
  README explaining the unsigned-app first-launch steps.
- **Linux** — portable only, by design. `os.Rename` over a running binary is
  legal on Linux, so in-place update already worked and there is no reason to
  write anything system-wide.

No signing is performed on macOS; Gatekeeper still blocks the first launch, which
remains a separate known gap.

### Verified

- `internal/install` and `internal/updater` tests pass; the regression tests were
  each run against the reverted implementation to confirm they fail:
  `TestResolveNeverStagesInTheLaunchedDirectoryForInstalledCopies` reports
  "staging dir is the install dir itself; downloads would race directory watchers
  again", and `TestIsRetryableRenameError` caught a real defect in the first
  draft of the classifier (the Windows phrases were gated behind
  `runtime.GOOS == "windows"`, so the exact message from the field report was
  classified *permanent* when tested off Windows).
- `gofmt` clean; `GOOS=windows go vet -tags "desktop production"` clean;
  `golangci-lint` clean with the same linters CI uses (it caught one dead helper
  in the new package — the same class of failure that killed the 2.2.2 tag).
- The manifest generator was checked against the full post-change filename set:
  all four raw binaries still resolve, and neither the installer nor the DMG is
  picked up as a platform artifact.
- `fetch-release.py` needs no change: it resolves assets by name from an
  allowlist of the four binaries plus `manifest.json`, so the new installer
  assets on the GitHub Release are ignored rather than breaking the all-or-
  nothing check.

### Not fixed by this

The Windows rename fix is verified by inspection and by classification tests, but
**not on a real Windows machine** — this host cannot run one. Confirming the
update end to end on an installed copy is still required, and a portable copy
running from Downloads is the case worth testing explicitly.

---

## CLIENT TUNNEL/RECOVERY — NORTON KILLS THE SOCKET; THREE STATE BUGS IT EXPOSED (2026-09-20)

Field report: the client connected for ~5 seconds, then showed "Repairing…",
then "⚠ Tunnel could not be recovered automatically". Clicking the button while
"Repairing…" appeared to disconnect, but the next Connect failed with
"⚠ tunnel is already running" and only restarting the app recovered it. A
terminal window flashed on every Connect and kept flashing.

### Root cause of the tunnel drop: a third-party network filter (Norton)

The tunnel itself is fine. The hub was verified healthy (8445/8446 listening, UoT
live), and with the AV's protections disabled the tunnel holds. With them enabled
Windows tears the Shadowsocks socket down a few seconds after the adapter comes
up:

```
ERROR dns: exchange failed for login.live.com. IN A:
  read tcp 192.168.68.52:52716->170.64.196.179:8445:
  wsarecv: An established connection was aborted by the software in your host machine.
```

"Aborted by the software in your host machine" is a local teardown, not a network
timeout, and it kills every in-flight DNS query at once — the signature of a
filter driver detaching the connection rather than a path problem. It lands at
first-connect, exactly when Windows floods the brand-new TUN adapter with
telemetry, which is when a security filter is most likely to interfere.

**This is NOT fixed in code.** It is an environment interaction (the same class
as the earlier `strict_route` WFP finding) and must be surfaced honestly to the
user rather than silently "repaired" forever. What IS fixed is everything the
failure exposed: the client now stops churning, stops leaking engines, and stops
being a dead end when this happens. A user-facing note that Norton's network
filter must exclude the Locus engine belongs with the release.

Worth recording: the clients' DNS log showed `ncc.avast.com` and
`filerep-replica-win.ff.avast.com` resolving on a machine the user reports as
running **Norton**. Those are the machine's OWN background traffic (Locus merely
resolves DNS for it), so their presence is not evidence about which product is
filtering. An earlier diagnosis in this pass asserted "Avast" from those domain
names and was wrong — do not infer the filter vendor from the log's DNS names.

### 49. Disconnect did not stop the engine (the orphan behind every other symptom)

`app.disconnect()` began with `if !a.connected { return "Already disconnected" }`.
The watchdog's auto-disconnect calls this **after** the UI has already flipped to
Disconnected, or while the student is tapping Disconnect during a repair — so
`a.connected` was already false, the function returned early, and **`mgr.Stop()`
was never called**. sing-box kept running, untracked by the UI.

`a.connected` is a UI flag. It is not evidence about the process; only the
manager knows that. `Stop()` is already a safe no-op when nothing is tracked
(it cancels the context and removes the config file), so skipping it saved
nothing and leaked an engine.

**Fix:** stop unconditionally. `Stop()` is called on every disconnect path, and a
stop failure is logged and surfaced instead of swallowed (a failed stop is
exactly the condition that breaks the next Connect).

### 50. "⚠ tunnel is already running" was a dead end, and the cleanup that fixes it was unreachable

`Start()` opened with `if m.processAlive() { return "tunnel is already running" }`,
and the auto-clean block (`killForeignEngines` + `removeStaleTUN`) sat *below* it.
So in the one state where cleanup was needed — a live engine we do not track —
the guard fired first and the cleanup never ran. With bug 49 leaving orphans,
this became a guaranteed dead end whose only escape was restarting the app.

Two further problems in the same function:

- `m.cmd`/`m.exited` were published **after** `cmd.Start()` returned. In that
  window `processAlive()` reported false while a child was alive, so a concurrent
  `Stop()` took the "already stopped" path and returned without killing anything
  — manufacturing the orphan directly.
- `processAlive()` is only our *belief*. It can be stale in both directions.

**Fix:** reconcile tracking with reality before deciding (drop tracking if the
tracked process is gone), then treat only a LIVE, TRACKED engine as "already
running". An untracked engine is reclaimed through the cleanup path instead of
refused. `m.cmd`/`m.exited` are now published **before** `Start()`, with a
rollback if `Start()` fails, closing the race at its source.

### 51. The watchdog escalated forever, and restarted engines after disconnect

Three separate problems:

1. **`escalate()` had no notion of ownership.** `StopWatchdog()` closed the stop
   channel, but a probe cycle already in flight ran to completion and restarted
   the engine. Combined with 49/50 that is precisely the observed sequence —
   `watchdog: tunnel unrecovered after 5 tries — disconnecting` followed by four
   more sing-box startups, each one a terminal flash and a new orphan.
2. **The probe could not distinguish "no engine" from "engine up but broken".**
   Both returned a bare error, and `escalate` was reached for both — so a
   disconnected tunnel caused recovery to *spawn* an engine the user had not
   asked for.
3. **`watchdog: stopping engine for full reset: invalid argument`** — the
   teardown itself errored, so even the escalation path was failing silently.

**Fix:** a sentinel `errEngineNotRunning` (wrapped with `%w`, checked via
`errors.Is`, so the classification survives wrapping); `runProbeCycle` reports a
missing engine as `stopped` instead of escalating; `escalate` re-checks
`watchdogActive()` at the top and between each rung, so stopping the tunnel
mid-ladder abandons the ladder.

The probe's real limitation is now documented in the code rather than implied:
step 3 dials the server ADDRESS at host level, which proves the server is
reachable — not that the tunnel is carrying payloads. An in-tunnel probe is the
correct fix and is tracked separately; `ProbeTunnel` no longer claims more than
it measures.

### 52. A terminal window flashed on every Connect

`sing-box` was spawned with `HideWindow: true`, so the engine itself was fine.
Every **helper** was not: `tasklist`, `netsh`, `taskkill` and `powershell` were
spawned with a bare `exec.Command`, and a Windows console-subsystem child of a
GUI process gets a **new console allocated for it** — a window that appears and
vanishes. `foreignSingBoxRunning()` and `removeStaleTUN()` run on every `Start()`
(so: flash on Connect), and the watchdog's escalation called them again per
retry (so: continuous flashing that only stopped when the app was closed).

**Fix:** one `hiddenCommand(name, args...)` constructor applying `CREATE_NO_WINDOW`
on Windows, with `hiddenRun`/`hiddenOutput` wrappers, and **every** spawn in the
package routed through it — including `killProcessGroup`'s `taskkill` and the
PowerShell elevation call. This bug had been fixed one call site at a time and
kept returning; one constructor is what makes it unrepeatable.

Also in this pass: sing-box's `stdout`/`stderr` were attached to `os.Stdout`/
`os.Stderr`. This is a GUI binary with **no console**, so that both allocated a
console for the child (another flash source) and produced no readable
diagnostics. Both streams now go to the client log via a `managerLogWriter`
(`[sing-box]`-prefixed), while stderr is still captured in the bounded buffer
used to report startup failures.

### 53. "Repairing…" was a disabled button, so there was no way to stop a repair

The UI showed `Repairing…` on a button with `primaryDisabled = true`, and
`toggleConnection()` fell through a "button is disabled, but guard anyway" branch
to `return` — doing nothing. So a student watching an unwinnable repair (Norton
tearing the tunnel down every 10 seconds) had no way to stop it, and the disabled
styling made the app look as though it had disconnected.

**Fix:** while repairing, the button is an explicit **Stop repairing** in danger
styling and actually disconnects. `primaryDisabled` now blocks only a
double-tap during an in-flight connect.

### Tests (and how they were validated)

New `internal/manager/lifecycle_test.go`. The pre-existing watchdog test
**tolerated** a failing probe (`t.Logf`, not `t.Fatal`), which is why an unsound
probe could ship; these assert behaviour instead.

Critically, each test was run against the **reverted** fix to confirm it fails:

- `TestEscalateIsInertWhenWatchdogStopped` → **FAILS** with the `escalate` guard
  removed (`escalate must be inert once the watchdog has been stopped`). This is
  the test that pins the "engines restart after disconnect" bug.
- Two earlier drafts were **discarded because they passed with the fix reverted**
  and therefore proved nothing: one could not reach the orphan state (the probe
  reads `m.cmd`, which the test had cleared), and one omitted the retained
  `tunCfg` that recovery needs to actually spawn anything. Recorded here because
  a green test that cannot fail is worse than no test.

Verified: `gofmt` clean, `GOOS=windows go vet -tags "desktop production"` clean,
linux build clean, frontend build clean, `go test ./internal/manager/...` green
(existing tests plus the new ones).

---



Publishing a release used to run through the operator's browser. The admin
console held four file pickers, hashed each file in JavaScript, and POSTed it to
a dedicated Python uploader (`scripts/release_upload.py` + `locus-upload.service`
on `127.0.0.1:8091`) because PocketBase refuses request bodies somewhere between
1 MB and 5 MB and exposes no multipart API to hooks, and this Caddy build has no
upload handler.

That worked, and every guard in it was earned. But it made the hub depend on the
operator's laptop, its upload bandwidth and its browser to ship a release — for
bytes that CI had **already** published to a GitHub Release. The hub can fetch
them itself.

### 46. Publishing required an operator's machine to be the transport

The specific problems, none of which were bugs in isolation:

- **The transport was the weakest link.** A release could not ship from a phone,
  from a CI job, or from a box with a bad uplink. The bytes travelled
  laptop → hub over a home/school connection for no reason; GitHub had them
  already, and the hub is closer to GitHub than the operator is.
- **The size problem existed only because of the direction.** The 200 MB
  upload cap, the Content-Length *and* stream double-check, the browser-side
  hashing, the progress bars, the `~5 MB` PocketBase body cap that forced a
  whole second service to exist — all of it was workaround for pushing 30 MB
  *into* a 454 MB box. Fetching *out* from the hub has none of those
  constraints, so retiring the uploader deleted the entire class of problem
  rather than managing it.
- **Two writers, two field-name layers.** `publish-release.sh` (CLI) and the
  uploader/console wrote the same `update_config` columns from different code
  paths. Entry 31 exists because one of them wrote field names the hub does not
  read. Fewer writers is fewer ways to disagree.

**What replaced it.** `scripts/fetch-release.py` (+ `templates/locus-fetch.service`,
same loopback port) downloads the artifacts for a version **straight from its
GitHub Release**, and the new `releases.publish` hook action writes
`update_config` from the hashes the service computed on disk. Caddy routes
`/api/admin/fetch-release` and `/api/admin/fetch-link` to it; `05-caddy.sh`
retires `locus-upload` on deploy so the new unit can bind the port.

Deliberate carry-overs, so the guards that were earned are not lost:

- **Assets are resolved by name from the release manifest**, never by
  constructing a URL — GitHub's asset naming stays out of the contract, exactly
  as `publish-release.sh` already did.
- **The platform/format check moved to the server.** The console used to refuse
  a Windows `.exe` dropped into the Linux slot (`v5/console/src/lib/artifact.ts`).
  Deleting the upload UI would have deleted that check, so it now lives in the
  fetcher (`verify_artifact_kind`: ELF / PE / Mach-O, plus arm64-vs-x86_64 for
  macOS). Verified against a real release: a PE in the Linux slot and an arm64
  Mach-O in the Intel slot are both refused.
- **All-or-nothing.** Every platform plus `manifest.json` must be present, and
  the manifest must parse as JSON. A partial release is refused rather than
  published with nothing for those clients to download.
- **`publish-release.sh` stays** — it is now the only way to publish a
  hand-built or hotfixed binary that is not on a GitHub Release.

### 47. A 1 MB binary floor silently rejected a valid release

The fetcher inherited the uploader's "anything under 1 MB is a truncated
download or a Git LFS pointer" rule and applied it to every asset. `manifest.json`
is a legitimate 719 bytes, so the first real end-to-end fetch failed with
`downloaded only 719 bytes ... not a binary` — after downloading all four
correct binaries.

The floor is a guard against truncated *executables*, and a JSON manifest is not
one. It now applies to binaries only; the manifest is required to be non-empty
and to parse as JSON, which is the check that actually matters for it (it is
what proves the bytes came from the same build CI hashed). Caught by running the
fetcher against the real `v2.2.1` release before deploying, which is the only
reason it was not discovered live and blamed on GitHub.

### 48. A one-shot link must be single-use, and the version must be inside the signature

Fetches are triggered manually, and a fetch should be triggerable from somewhere
that does not hold the admin token — a phone, a CI job. That needs a link, and a
link that can be replayed is a permanent grant the moment it leaks into a chat
log, a browser history or a CI log.

So a link is `?version&nonce&exp&sig` with `sig = HMAC-SHA256(secret, version|nonce|exp)`:

- **Single use.** The nonce is burned on the first successful verification; a
  replay is `409`, not a second fetch.
- **Expiring.** 15 minutes by default (`FETCH_LINK_TTL`).
- **The version is inside the signed payload**, not just present in the query
  string — so a link minted for `2.2.1` cannot be edited to fetch `9.9.9`. A
  signature that covers only the nonce would leave that open, and it is the
  obvious thing to try first.
- **Minting requires the token.** A link cannot mint another link, or one
  leaked URL becomes an unlimited grant.

All four were verified live against the hub, including the failure paths: a
replayed link, a tampered signature, a version swapped under a valid signature,
an expired link, and an unauthenticated mint attempt.

### Verified live on the hub (2026-09-19)

- Fetch of `v2.2.1` from GitHub: all four binaries + manifest in **4.8 s**, formats
  detected `ELF` / `PE` / `Mach-O x86_64` / `Mach-O arm64`, hashes identical to
  the manifest's declared values.
- All four artifacts downloaded back **through Caddy** hash to the recorded
  values.
- `releases.publish` wrote `update_config` = 2.2.1 with all four `download_*`
  URLs and `sha256_*`; `/api/release` went from
  `{"platforms":{},"version":"2.2.0"}` (the broken state of entry 41) to four
  fully-populated platforms.
- A real bound code's heartbeat at 100% delivered `update_available`, all four
  `update_<platform>` URLs **and** all four per-platform
  `update_sha256_<platform>` hashes.
- Refusals, all live: unknown tag, `../etc` as a version, an incomplete
  artifact set, and `rollout > 0` on an unpublished version.
- `smoke-test.sh`: **23 passed / 0 failed / 0 warnings**. Real codes untouched
  (11 codes, 1 bound).
- Rollout left at **0** afterwards — a version nobody is offered yet is the
  correct resting state.

---

## ELEVATION (CONT'D) — THE TOKEN CHECK ITSELF WAS BROKEN; UPDATES NEEDED A CODE (2026-09-19)

## RELEASE TOOLING — VERSION DRIFT AND A PUBLISH THAT WROTE AN UNUSABLE ROW (2026-09-19, later pass)

`release.pb.js` implements `GET /api/release` — the update path that needs no
activation code. It exists for exactly the situation where an update matters
most: the installed build is broken enough that activation fails, or the code is
suspended, or the device never activated. In all three the heartbeat is
unreachable, so without this route the client can never be told that a fix has
been published. The failure prevents escaping the failure.

It was written, committed and reviewed. It was never copied to the host. In
production the route returned `{"code":404,"message":"Not Found."}`.

**Why nothing caught it:** hooks were deployed by hand, so there were two
sources of truth — the repo and `/opt/pocketbase/pb_hooks/` — with no
reconciliation between them. Nothing compared the two, and no check asked the
live hub whether its own routes worked. A route that 404s is not an error
anywhere; it is just a 404.

**Fix:** `v5/server/scripts/hooks-sync.sh`. It hashes each hook in the repo
against the host copy, uploads only what differs (to a temp name, moved into
place atomically, so a partial file is never loaded), restarts PocketBase, waits
for it to answer rather than sleeping a fixed amount, then verifies: service
active, no new `-p err` journal lines, and `GET /api/release` returning 200 with
valid JSON. A restart that leaves the hub broken now fails the deploy instead of
passing silently. It also runs standalone as `--check` for drift detection.

**Verified live:** the dry run named `release.pb.js` as the one hook missing and
the other six as current — so the repo and host really were in sync apart from
this. After deploy, `/api/release` returns 200.

### 44. `publish-release.sh` wrote `update_config` for a release with no artifacts

The live `update_config` row before this fix:

```
version=2.2.0  rollout=0  active=1
download_linux=''  download_windows=''  download_macos_intel=''  download_macos_arm=''
sha256_linux=''
```

Every URL and hash was an empty string. The script had been run with an empty
`RELEASE_DIR`: it warned once per missing platform, found no usable artifacts,
and **wrote the row anyway**. The result is a release that is advertised as
active with zero platforms — visible publicly as:

```
GET /api/release -> {"ok":true,"platforms":{},"published":true,"version":"2.2.0"}
```

The client handles this as `no_asset` rather than a crash, so the practical
effect is that no client can ever fetch this release while the hub reports that
one is published.

**Fix — three guards plus a source that cannot be got wrong:**

- `--from-github` fetches this version's assets from its GitHub Release by tag.
  The version argument selects the release, so there is no directory for an
  operator to point at the wrong place. Verified against the real `v2.2.0`
  release: all five assets fetched, and all four hashes matched CI's
  `manifest.json`.
- No usable artifacts at all → refuse, exit 4, `update_config` untouched.
- An incomplete platform set → refuse unless `ALLOW_PARTIAL=1`, which says
  loudly which platforms are being dropped. Dropping one is invisible from the
  operator's seat and shows up later as those students stuck on an old build.
- `--help` was parsed as the version (it died with "release dir not found"), and
  a non-version argument was accepted and used as a tag. Both fixed.
- `fail()` used `%s`, so every multi-line refusal printed the literal characters
  `\n`. The most important messages were the least readable. Now `%b`.

`smoke-publish.sh` (21 checks, `DRY_RUN=1`, no network, no hub changes) asserts
every refusal path, including that the script does not reach `update_config`.

**Not fixed, deliberately:** the live 2.2.0 row still has empty `download_*`
fields. 2.2.0 is superseded by 2.2.1, so it is left alone — republishing it is a
separate decision, not a side effect of this work. Until 2.2.1 is published,
`/api/release` advertises 2.2.0 with no platforms.

### 45. The version was maintained by hand across six files, and the lockfile had already drifted

The client version lives in six places that different tools read
independently. Bumping them by hand has already produced a shipped defect: in
2.1.0 the `go:generate` DIRECTIVE was bumped but the committed
`rsrc_windows_*.syso` were not regenerated, so the Windows executables reported
version "2.0.0" and product name "MyVPN" in their Properties tab. Every
text-based check passed — a correct directive says nothing about the artifact it
already produced.

Found while writing the replacement: **`frontend/package-lock.json` was rooted at
"2.0.0" while `package.json` said "2.2.0"**, and nothing checked the lockfile at
all, so it had drifted across two releases. Harmless only because CI runs
`npm install` rather than `npm ci` and ships built output — a latent hard CI
failure for whoever tightened that step.

**Fix:** `v5/server/scripts/bump-version.sh`. It rewrites all six, regenerates
the Windows resources, then re-reads the version OUT of the compiled `.syso`
before declaring success — the only check that can catch a stale artifact. It
refuses a dirty tree so a failed run is recoverable with `git checkout --`, and
it stops before any git command.

`bump.sh` at the repo root is the same thing with the next version computed
automatically from the current one.

Two guards, each **proven to fail on the defect it targets** rather than merely
passing on a clean tree:

- `TestFrontendLockfileVersionMatchesRepoVersion` checks both the document root
  and `packages[""]`, which drift independently.
- `TestNoUnaccountedCopyOfVersion` inverts the question the other guards ask.
  They assert the copies *we know about* agree; none can notice a NEW copy
  appearing. This greps the tree for the running version and requires every hit
  to be in a maintained file — so the seventh copy becomes a build failure
  instead of a silent divergence.

`smoke-bump.sh` (21 checks) runs the whole thing against a throwaway tree,
including that a deliberately introduced seventh copy is caught and named.


### 41. `GetTokenInformation` was called with four arguments instead of five

Entry 40 fixed the handoff logic, but the retest surfaced the deeper defect: the
error persisted even when Windows *had* elevated the process. The cause was in
the elevation probe itself.

The Win32 signature is

```c
BOOL GetTokenInformation(HANDLE TokenHandle,
                         TOKEN_INFORMATION_CLASS TokenInformationClass,
                         LPVOID TokenInformation,
                         DWORD TokenInformationLength,      // <-- omitted
                         PDWORD ReturnLength);
```

Five parameters. The Go P/Invoke passed four, omitting the buffer LENGTH, so:

- `&retLen`'s *pointer value* was consumed as the buffer size, and
- the real `ReturnLength` out-parameter read an unrelated stack slot.

The call could still return success while never writing to the `elevated`
buffer — which read as zero, i.e. "not elevated". **Every** caller therefore saw
`isElevated() == false`, including processes Windows had genuinely elevated,
which is why "Run as administrator" made no difference: the app was not
elevation-checking, it was reading uninitialised memory.

Verified the constant was NOT the problem before changing anything:
`TokenElevation = 20` is correct (confirmed against Microsoft Learn and
pinvoke.net). Worth recording, because the obvious-looking first guess
(`TokenElevationType = 18` being the intended value) would have been a
plausible-looking "fix" that changed nothing.

Fix: pass all five arguments with `unsafe.Sizeof(elevated)`, and treat a
`ReturnLength` that is not 4 as "cannot determine" rather than trusting an
unwritten buffer. Silently reading zero as "not elevated" is what turned a
malformed call into a confident, wrong error message.

### 42. Updates were only reachable through an activation code — a deadlock

Raised by the user, and correct: "if the updates only work when connected, how
can connection problems be updated?" The literal answer is better than it
sounds, but the design had a real hole.

**What was already true:** the heartbeat — which carries the update signal — is
a plain HTTPS call to the hub and does NOT require the tunnel. It runs whenever
the app is *activated*, connected or not. So a broken tunnel alone never blocked
an update.

**What was actually broken:** every update path began at
`GET /api/heartbeat`, which requires a bound activation code. Three real
situations therefore had no way to receive a fix at all:

1. the installed build is bad enough that activation fails;
2. the device has never activated;
3. the code is suspended or expired and the operator's remedy is a new build.

In each case the thing that is broken is the thing that would deliver the
repair. The only escape was the `--revert` CLI flag, which needs a terminal.

Fix, in two parts:

- **`GET /api/release`** (new hook, `pb_hooks/release.pb.js`) — a credential-free
  public manifest answering only "what version is published, and where". It
  deliberately does NOT expose `rollout_percent` (fleet policy), any per-device
  data, or any per-code data; the download URLs it returns are already served
  openly from `/updates/*`. Returns `published:false` rather than 404 when
  nothing is released, so "nothing published" stays distinguishable from
  "cannot ask".
- **Client fallback** (`internal/updater/manifest.go`) — `CheckForUpdate` falls
  back to that manifest when the heartbeat is unavailable *or* no code is bound,
  marks the result accordingly, and adopts it so `ApplyUpdate` works. Staged
  rollout is intentionally not honoured on this path (it is per-device and
  needs a fingerprint); the reason string says the check came from the public
  list.
- **Activation screen** gained a "Check for a newer version" affordance, so a
  device that cannot activate is not stranded with no way to install a fix.
  Previously `MainScreen` (which holds the update button) was not rendered at
  all while unactivated.

Tests: `manifest_test.go` covers parsing, the empty-manifest case, a
missing-platform asset, HTTP/JSON/`ok:false` failures, and — guarding the same
class of bug as the `server_port_uot`/`uot_port` mismatch — that the decoder
matches the exact key shape the hook emits. A field-name drift here would
silently report "no update" to every client.

**Not yet deployed.** The hook requires an SSH deploy to the live hub; until
then `/api/release` returns 404 and the client fallback degrades to reporting
the original heartbeat failure, which is the correct behaviour.

---

## ELEVATION — "ELEVATED COPY DID NOT HAVE PERMISSION" ON EVERY CONNECT (2026-09-19)

### 40. The elevation handoff mistook a normal auto-connect for a failed relaunch
Reported from a real Windows machine: pressing Connect raised the UAC prompt,
the user approved it, and the app replied

> Locus needs administrator permission to connect, but the elevated copy did not
> have permission. Close it and relaunch as Administrator…

and doing exactly that — launching elevated — produced the same message. A
second, apparently unrelated symptom accompanied it: an intermittent
`application is not ready — restart Locus`, curable only by pressing retry
several times, after which the app relaunched and showed the elevation error.

Four independent defects combined to produce that experience.

**(a) Elevation was inferred from the wrong flag.** `Connect()` decided "I am
the elevated copy that failed to elevate" from the presence of `--autoconnect`.
But `--autoconnect` is a legitimate user-facing flag and is *also* set on the
handoff, so an ordinary auto-connect run was indistinguishable from a relaunch
that had failed. The guard then refused to connect and printed the permission
error — including on processes that **were** elevated, which is why "Run as
administrator" changed nothing.

Fixed by giving the handoff a dedicated marker, `--elevated-attempt`, that is
never set by anything else. `--autoconnect` now means only "connect when ready".

**(b) The flags accumulated across handoffs.** `relaunchElevated` appended
unconditionally:

```go
allArgs := append([]string(nil), os.Args[1:]...)  // carries existing args
allArgs = append(allArgs, argsToAdd...)           // appends the same flag again
```

so a second handoff produced `--autoconnect --autoconnect` (and grew with each
attempt), and `--elevation-attempt=N` was appended rather than replaced, leaving
a stale count in place. Fixed with `mergeArgs`, which replaces a flag of the same
name instead of duplicating it.

**(c) Elevation got no second chance and no bound.** A single failed handoff was
terminal, with no retry counter — while the actual failure mode (a slow UAC
consent, an antivirus scan of the new process) is transient. Now bounded at
three attempts, after which the message tells the user to use "Run as
administrator" directly instead of relaunching in a loop.

**(d) The real cause of "application is not ready".** Startup launched the
auto-connect goroutine and relied on a fixed 1-second sleep to outrun its own
initialisation. On a cold elevated launch — WebView2 initialising, the new
process being scanned — `Startup` could still be running when the sleep expired,
so `Connect()` hit the not-ready guard and told the user to restart. Retrying
won the race often enough to look like a flaky crash rather than a race.

Fixed by publishing a `ready` flag **before** the goroutine starts, so readiness
is guaranteed rather than timed, and by distinguishing the two cases in
`notReady()`: a transient "still starting" (accurate, retried automatically, no
longer shown as an error) versus a recorded `startupErr` (where restarting is
genuinely the right advice).

Also fixed, found while verifying (a): the shipped `rsrc_windows_*.syso` carries
`requestedExecutionLevel="requireAdministrator"`, so a normally launched copy is
*always* elevated. Any user-visible elevation error therefore means the manifest
was bypassed or the binary is not the one being run — worth knowing before
debugging the token check itself.

Tests: `elevation_flags_test.go` covers arg merging (no duplication across
repeated handoffs, valued flags replaced, input not mutated) and, as a direct
regression test for the report, that `--autoconnect` alone is **not** treated as
an elevated relaunch. Writing them caught a real inconsistency in the first
revision of the fix: `hasFlag` compared the bare flag name while the attempt
constant included its `=`, so the valued form never matched.

---

## TIERS PAGE — STALE WARNING CONTRADICTED THE STRIKE DESIGN (2026-09-19)

### 27. The Web UI told operators to switch off the feature Strike exists for

The Tiers page carried this for every tier:

> **UDP relay** must stay off for this server: shadowsocks-rust does not
> implement sing-box's UDP-over-TCP, and leaving it on makes UDP traffic fail.

It was written 2026-08-01 and was **correct at the time**. The layout then was:
`udp_relay` unconditionally put `udp_over_tcp: true` on the client's shadowsocks
outbound, aimed at the ordinary shadowsocks-rust port. shadowsocks-rust does not
implement SagerNet's UoT protocol (magic domains `sp.udp-over-tcp.arpa`), so
every UoT dial was RST about 300ms later and UDP broke — FIXES.md Follow-up 9.

Two things changed on 2026-08-14 and the warning was never revisited:

1. **`udp_relay` no longer acts alone.** The client's condition is
   `uotEnabled := cfg.UDPRelay && cfg.ServerPortUOT > 0` — an AND. With no
   `uot_port` in the tier config, setting the flag is a no-op, so the old
   failure mode ("turning this on breaks UDP") became unreachable.
2. **The server changed.** `enable-uot.sh` installs a **sing-box** listener on
   port 8446, which *does* implement UoT. That was the whole conclusion of
   `GAMING-UDP.md`: "UoT was never the wrong protocol — it was the wrong server."

So the two fields now mean different things — `udp_relay` *declares* the tier
carries UDP; `uot_port` names the *mechanism*. The warning told operators to
disable the declaration that makes Strike's gaming feature work, and it would
have become actively harmful the moment someone enabled UoT properly.

### 28. `uot_port` was invisible and uneditable in the Web UI

`tiers.list` returned only `server`, `server_port` and `method` from the tier's
config JSON — `uot_port` was parsed over but never surfaced, and `tiers.update`
had no way to set it. An operator could therefore toggle the half of the switch
they could see while having no sight of, or control over, the half that decides
whether it does anything.

**Fixes:** `tiers.list` now returns `uot_port`; `tiers.update` accepts it
(0–65535, `0`/absent removes it) and is optional so an older console build keeps
working. The Tiers page gained a `UoT port (0 = none)` field and two
disagreement warnings — flag on without a port (a confusing no-op), and a port
set without the flag (an endpoint nobody is told about).

The replacement text states the real constraint: both switches are required, the
port must point at a sing-box UoT listener (not 8443/8444/8445), and on this
deployment no tier should set one because the UoT endpoint is not running.

### Verified live

- Web UI redeployed — asset `index-BC96z_48.js`.
- Hook deployed to `/opt/pocketbase/pb_hooks/` **and** the `/root/server/`
  staging copy; PocketBase restarted; previous hook backed up to
  `/root/admin_console.pb.js.bak.*`.
- `tiers.list` now returns `uot_port` for all three tiers, and the values are
  `0` for eco, stealth **and strike** — i.e. the live hub genuinely has no UoT
  endpoint, so the corrected warning describes the real state rather than
  guessing.

### Superseded later the same day

> **CORRECTION (2026-09-19, later pass).** The paragraph that stood here said the
> endpoint had "never run on the current host". That was already false when it
> was written: `787934c`/`78d193c` enabled UoT on `170.64.196.179` and the
> listener is live on TCP+UDP 8446 (verified by direct `ss -lntup` and by
> `systemctl is-active sing-box-uot`).
>
> **But the feature is still dead, for a different reason — entry 29 below.**
> The client parses `server_port_uot`; the hub emits `uot_port`. So `udp_relay`
> and `uot_port` are both set on Strike and *nothing on the client reads either
> one*. The server side was never the missing piece. See entry 29.

---

## UDP-OVER-TCP WAS NEVER CONNECTED, AND THE .syso SHIPPED STALE (2026-09-19, later pass)

### 29. The hub sent `uot_port`; the client only ever read `server_port_uot`

UoT was enabled on the live hub and the listener verified open on TCP+UDP 8446
(entry 28's successor, commit `78d193c`). That verification was real and
correct — and the feature still did nothing, because of a field name.

The hub stores the endpoint inside the tier's config JSON under `uot_port` and
passes that JSON through verbatim from both `/api/activate` and
`/api/heartbeat`. Confirmed against the live host:

```
$ sqlite3 /opt/pocketbase/pb_data/data.db \
    "SELECT tier, udp_relay, config FROM tier_configs"
strike|1|{...,"server_port":8445,...,"uot_port":8446}

$ curl -s -XPOST .../api/heartbeat -d '{"code":"RQ-...","fingerprint":"..."}'
{"server_config":{...,"server_port":8445,"uot_port":8446},"udp_relay":true,...}
```

The client declared only `server_port_uot` on all three of
`activation.ServerConfig`, `heartbeat.ServerConfig` and
`storage.ServerConfig`. Go's `encoding/json` **ignores unknown keys without
raising an error**, so:

```
ServerPortUOT == 0        (always)
uotEnabled := cfg.UDPRelay && cfg.ServerPortUOT > 0   →   false   (always)
```

`manager/process.go` therefore never appended the `proxy-uot` outbound. The
mechanism behind the Strike gaming tier was dead on every build, on every
platform, for the entire time it was advertised as working. `process.go` even
carried a comment asserting "LIVE SINCE 2026-09-19", which described intent
rather than behaviour.

**Why nothing caught it.** Both sides were individually correct, and each was
tested in isolation. No test asserted that the client deserialises the key the
server actually emits. A grep for `uot_port` in the client *did* match — in
comments — which is exactly the kind of hit that makes a human conclude the
wiring is fine.

**Fix — the wire key is frozen.** The hub must keep sending `uot_port`, because
already-deployed clients read that literal and a rename is a silent no-op to
them, not an error. So the tolerance lives on the client:

- New `internal/uotkey` is the **only** place that knows the field name.
  `Canonical = "uot_port"` (what the hub emits), `Legacy = "server_port_uot"`
  (accepted so a future rename is survivable in the other direction).
- The three structs' `UnmarshalJSON` delegate to it, so they cannot drift apart.
- Precedence resolves canonically from the raw map rather than by struct tag, so
  a payload carrying both keys is deterministic instead of dependent on Go's map
  iteration order (an earlier draft of this fix got that wrong; the
  `TestCanonicalBeatsLegacy` case fails on it).
- Both hooks now carry a FROZEN CONTRACT comment stating why the key cannot be
  renamed.

**Verified.** `internal/uotkey` carries 11 tests, including
`internal/uotkey/contract_test.go`, which unmarshals a verbatim live hub payload
into the real `heartbeat.Response`, `activation.ActivateResponse` and
`storage.ServerConfig` — and asserts the whole gate, not just the port, since
UoT also needs `udp_relay`. The tests were run against the pre-fix code first:
they fail with the exact production symptom (`ServerPortUOT = 0, want 8446`).
That is the assertion whose absence let both `78d193c` and the earlier 2.1.0
verification pass while the feature was dead.

**Still not validated:** a real game session on a school network. The client
path is now genuinely wired and a synthetic client dials 8446 successfully, but
`GAMING-UDP.md`'s acceptance gate remains open.

### 30. `locus.exe` reported version 2.0.0 and the product name "MyVPN"

The committed `rsrc_windows_{amd64,arm64}.syso` files — the compiled
`VS_VERSION_INFO` block that populates the Windows Properties tab — were stamped:

```
FileVersion   2.0.0
ProductVersion 2.0.0
ProductName   MyVPN
FileDescription MyVPN secure school VPN
```

while `v5/VERSION` said `2.1.0` and the product had been renamed to Locus. A
student right-clicking the executable saw a version two releases old under the
old name.

**Why nothing caught it.** `version_consistency_test.go`'s
`TestGeneratedWindowsResourceVersionMatchesRepoVersion` asserted on `main.go`'s
`go:generate` **directive** — and the directive was correct. Nothing regenerates
the `.syso` automatically (`go generate` is manual), so the artifacts had drifted
from the directive and no check looked at the output. A second, independent guard
in CI grepped the same directive text and passed for the same reason. Two guards,
one blind spot, because both checked the instruction rather than the result.

**Fix — read the bytes.** New `internal/winres` parses `VS_VERSION_INFO` out of
the compiled resource (UTF-16LE, both byte alignments, since the block's position
depends on everything preceding it) and
`TestCommittedSysoMatchesRepoVersion` asserts file version, product version,
product name and description prefix against `v5/VERSION`. It runs natively on any
platform because it only reads bytes — no Windows toolchain, no PE parser.

It also fails loudly (`ok=false`) if it cannot find the version block at all, so
a format change surfaces as "this guard is now blind, fix the parser" rather than
a silent pass.

The artifacts were regenerated with the repo's own directive
(`cd v5/client && go generate -tags windows`), and the guard was run against the
stale copies first — it fails with all four fields itemised. CI now invokes it
explicitly inside the version-consistency step so a recurrence is attributed to
versioning rather than looking like a generic test failure.

### Also in this pass

- **`v5/client/Makefile` fell back to `VERSION := 2.0.0`** when `v5/VERSION` was
  missing or unreadable. That is a version which no longer exists in the tree, so
  the build would report a number contradicting both `v5/VERSION` and the shipped
  resource — the same drift as above, arriving through a different door. It now
  fails loudly with instructions (pass `VERSION=` for a scratch build). The
  consistency test cannot see the Makefile, so the guard lives in the Makefile.
- **`docs/FIXES.md` restored.** Commit `15e7630` truncated it from 1769 lines to
  69, deleting entries 1-26, S1-S9 and Follow-ups 1-10 — the entire sing-box,
  WFP and DNS debugging history, and the only written record of why several
  guards in the client exist. The commit message described only *adding* entries
  27-28, so the loss was unintended. Recovered from `2a57d12` and re-merged, with
  the numbering scheme preserved (the file mixes `### N.` entries with unnumbered
  `### Follow-up N` and `### SN` blocks, so the sections are ordered
  newest-first rather than concatenated).
- The paragraph that previously ended entry 28 — claiming UoT "has never run on
  the current host `170.64.196.179`" — was already false when written and is now
  explicitly marked as superseded, so it does not send a future reader down the
  wrong path the way it nearly did.

---

## TWO PUBLISHERS DESCRIBED update_config DIFFERENTLY (2026-09-19, later pass)

> **CORRECTION (2026-09-23).** The account below is **wrong about the cause** and
> was corrected after a direct check. `publish-update.sh` does **not** contain
> `update_linux`/`update_windows` — zero occurrences, confirmed by grep and by
> `git log -S'update_linux' --all`, which returns nothing for that file. Both
> scripts write `download_*`.
>
> The real defects in `publish-update.sh` are (1) it emits **no
> `sha256_<platform>` columns at all**, so no platform could verify a download,
> and (2) its macOS URLs use `locus-macos-*` where CI produces `locus-darwin-*`,
> so they 404. The script has been retired to
> `legacy/publish-update.sh.broken` with this correction in its header.
>
> The lesson that survives is the real one: there was no single definition of the
> record, so nothing could notice the two scripts described it differently. That
> is what `internal/updatecfg` fixes. The entry is kept for that reasoning, not
> for its stated cause.

### 31. `publish-update.sh` wrote field names the hub does not read

`update_config` has three layers of field names, and nothing defined them in one
place:

| Layer | Producer | Consumer | Per-platform URL field |
|---|---|---|---|
| record | publish-release.sh / publish-update.sh | heartbeat.pb.js | `download_<platform>` |
| response | heartbeat.pb.js | the client | `update_<platform>` |
| struct | — | heartbeat.Response | `update_<platform>` |

`publish-release.sh` builds `body["download_" + key]` and is correct.
`publish-update.sh` emitted `"update_linux": ...` in a heredoc — the RESPONSE
name, written into the RECORD. The hub reads `download_*`, found nothing, and
omitted every per-platform URL and hash from the heartbeat.

The client then does what it is designed to do when a per-platform field is
absent: `PlatformDownloadURL()` falls through to the legacy single `update_url`.
Both scripts point that at the **linux** binary. So a release published through
`publish-update.sh` would have offered Windows and macOS clients a Linux
executable, which they would download in full and then reject on the checksum —
with no error surfaced anywhere, because "no per-platform URL" is
indistinguishable from "this release is linux-only".

**Fix.** New `internal/updatecfg` defines the three layers explicitly, records
why the rename between layers 1 and 2 exists, and warns against "simplifying" the
hook by making it emit `download_*` directly (deployed clients read `update_*`).
Its tests read the hook and BOTH scripts as text and assert they agree across all
four platforms — the cross-language check whose absence let two writers describe
the same record incompatibly while each looked right in isolation. Verified
against the pre-fix spelling: the guard fails with a message naming the wrong
layer.

### 32. `tiers.update` returned the tier password in cleartext

`return ok({tier: tierName, config: cfgT})` echoed the whole config object, which
includes the shadowsocks PSK every client on that tier shares. `tiers.list`
deliberately omits it — so the inconsistency was inside a single file.

Nothing consumed the field (the console re-renders from `tiers.list` and never
read `.config`), so it never appeared in the UI. It leaked only to anyone using
curl directly, and landed in terminal scrollback, browser devtools and any
body-capturing reverse proxy. The response now echoes just the editable fields:
`server`, `server_port`, `method`, `uot_port`, `udp_relay`, `active`.

### 33. Expiry and suspension were skipped on re-activation

`activation.pb.js` checked `expires_at` and `suspended` only on the
first-activation path, which sits BELOW the `if (boundFp)` branch that returns
`200 "Already activated"`. So a device that had already bound a code kept
re-activating successfully after that code expired — the check was unreachable
for precisely the machines it was written to protect. Only a fresh device was
ever refused.

Order is now **expiry → suspension → binding**, with two deliberate details:

- The 410 for expiry is returned before the fingerprint comparison, which is
  safe: an expired code tells a probing device nothing it could not have learned
  from the code itself.
- Suspension is checked after the fingerprint comparison on the bound path, so a
  device with a mismatched fingerprint still gets "bound to another device" and
  cannot use the endpoint to probe whether a code has been suspended.

### Also in this pass

- **`templates/Caddyfile` had the handler order `modules/05-caddy.sh` documents
  as a bug.** `/api/*` appeared before `/api/admin/upload`, so release uploads
  would have been proxied to PocketBase — which caps request bodies at a few MB
  — and counted against the general 100-requests/10s limit. The generated
  Caddyfile is correct, so production was never affected; the template is what a
  manual `sed`-based deploy uses, and its own header invites exactly that. The
  template now matches the generated config on the load-bearing ordering, and
  adopted the `route /admin { redir }` form (an exact-path matcher inside
  `handle` loses to the `handle_path` file_server beneath it, so the redirect
  never fired and `/admin` 404'd for a typed URL or bookmark).
- **`/api/code-lookup` and `/api/activate` shared a rate-limit bucket.** Both
  counted `rate_key = <fingerprint>`, so lookups and activation attempts drew
  from the same 5-per-10-minutes allowance. The lookup endpoint exists so a
  student can confirm a code is recognised *before* committing to an activation
  — but mistyping on the activation screen consumed the lookup budget, locking
  the student out of the affordance that would have explained the mistake.
  Confirmed live. Keys are now namespaced (`lookup_` / `activate_`).
- The API reference gained the expiry/suspension error rows and the two-bucket
  rate-limit note; it had been describing behaviour the hooks did not have.

---

### 34. A LIVE tier password was committed, because gitignore has no inline comments

`clash-verge-stealth.yaml` and `clash-verge-stealth-notun.yaml` were both tracked
in git, and both contain the stealth tier's shadowsocks PSK in cleartext:

```
clash-verge-stealth.yaml:53    password: "95fee4978796490984a9b9e6835a9473"
```

That value matched the live hub exactly (confirmed against `tier_configs` on
`170.64.196.179`). The files were on `origin/main` since commit `77619a2`
(2026-08-14) — a public repository — so simply deleting them now would not have
removed the secret from history.

**Root cause.** `.gitignore` contained:

```
clash-verge-stealth.yaml  # contains tier password — never commit
```

Gitignore has **no inline comment syntax**. A `#` only starts a comment at the
beginning of a line; anywhere else it is part of the pattern. So the rule being
matched was the literal string
`clash-verge-stealth.yaml  # contains tier password — never commit`, which
matches no file. `git check-ignore clash-verge-stealth.yaml` returned nothing —
the ignore never applied. The files were committed by a later `git add -A`.

The intent was correct and the comment documented it clearly. The mechanism did
nothing, and nothing verified the mechanism.

**Fix, in three parts.**

1. **Rotated the credential** — the only real remediation, since the value is in
   public history and cannot be un-published. New stealth PSK
   (`60f3159f…`, generated with `openssl rand -hex 16`), applied to both
   `/etc/shadowsocks/stealth.json` and the `tier_configs` record, listener
   restarted. Verified: identical in both places afterward, eco and strike
   untouched, all four listeners plus PocketBase and Caddy still active.

   Cost was zero by luck: the stealth tier had **no codes at all** — `0` issued,
   `0` bound — so no client was holding the old secret. Had any been active, the
   rotation would have needed a coordinated client refresh, because the client
   caches the PSK in `storage.json` and only picks up a new one from the next
   heartbeat.

2. **Removed the files from tracking** (`git rm --cached`) and corrected the
   ignore rules to patterns that actually match, with a comment explaining why
   they carry no trailing comment.

3. **Made the failure visible**: `git check-ignore` now resolves both files and
   `.gotool/`, which was previously untracked and unignored (one `git add -A`
   away from committing a Go toolchain).

**Residual risk, stated plainly:** the old PSK remains readable in the GitHub
history of this public repository. Rotation makes it useless against the live
host, which is what matters, but anyone who cloned before this commit has it.
History rewriting was not attempted — it does not remove it from clones or
forks, and it would invalidate every existing clone.

### 35. Legacy `myvpn` names: a rule, not a cleanup

The rebrand to Locus left `myvpn` strings throughout the server layer.
Blanket-replacing them would have broken the running system: several are paths
and unit names that exist on the deployed host right now — `/usr/local/bin/myvpn-backup.sh`,
`myvpn-tc-apply.sh`, `/etc/myvpn/tc`, `/var/log/myvpn-*.log`, and the client's
`.myvpn-backups/` rollback directory. Renaming those in the repo desynchronises
the definition from the machine, so a fresh `setup.sh` would install a file the
existing systemd units never call.

Split by consequence:

- **Fixed** — every user-visible or human-facing string: the Windows
  executable's product name and description (entry 30), the shadowsocks link tag
  in `hiddify.pb.js` (the tag is the display name a student sees in their client
  — the only one of these that actually reached a user), hook header comments,
  setup/restore banners, and code-card/PDF headings.
- **Left alone, documented** — live server paths, unit names, log filenames and
  the client backup directory. `v5/CONTEXT.md` now carries a naming rule at the
  top explaining which is which and that the second class must only be renamed
  as part of a deliberate redeploy.

Without that rule written down, the next person to run a global rename has no
way to tell the two classes apart, and the failure is silent until a fresh
deploy.

---

### 36. Two seed scripts described the same schema; one is now retired

`seed-pb.py` (398 lines) and `seed-live.py` (291 lines) had the same
`COLLECTIONS` list, the same tier-seeding loop, the same `update_config` and
`tier_configs` upserts, and the same `uot_port` handling. Only one of them is
wired into the deploy path — `06-pocketbase.sh` calls `seed-pb.py` — so the other
could drift indefinitely without anyone noticing, and "which do I run?" had no
answer in the repo.

Checked against the live host before deciding which to keep:

- `seed-pb.py` knows that PB 0.22.x has **no public API to create the first
  admin** (`POST /api/collections/_superusers/records` returns 404) and
  bootstraps via the CLI, then authenticates against `/api/admins`. Verified on
  `170.64.196.179` (PB 0.22.21): `/api/admins/auth-with-password` answers 400
  for bad credentials, while `/api/collections/_superusers/auth-with-password`
  answers 404 — the endpoint does not exist.
- The claim that `seed-live.py` was "broken on 0.22" was **wrong**, and worth
  recording as such: it does handle `_superusers`. It was simply the unmainted
  duplicate.

Both were schema-compatible with the live database (all four collections' columns
matched the running SQLite schema exactly).

**`seed-live.py`'s one unique behaviour was a throwaway end-to-end activation
test** — create a temporary eco code, activate it through the public
`/api/activate` with a synthetic fingerprint, print the returned `server_config`,
delete the code. That is the only thing in the deploy path that proves the hub
*serves* rather than merely *seeds*, so it was ported into `seed-pb.py` behind
`VERIFY=1` (opt-in: the default deploy should not write to `codes`).

The generator was cross-checked against the Go implementation the client and
hooks use — 20 generated codes, 20 accepted by `internal/activation`'s
`luhnModNCheck`. A generator that produced invalid codes would make the
verification fail on "Invalid code format" and look like a hub fault.

Exercised on the live host: activation returned
`networkingguides.duckdns.org:8443 aes-256-gcm`, and the code count was 11 before
and 11 after, so cleanup is reliable.

`seed-live.py` now exits immediately with a pointer to the replacement. It is not
deleted, so an existing runbook or a shell-history recall lands on an explanation
instead of "no such file".

### 37. Dead exports removed from the client's bridge

`getHubURL`, `getCodeCharset` and `getCodePrefix` were exported from
`frontend/src/lib/bridge.ts` and imported by nothing. The corresponding Go
methods stay — they are bound to the frontend by Wails and callable from a
devtools console — but the wrappers were surface a reader would assume was live.

Added a check that `AppBindings` in `types/index.ts` matches the exported `App`
methods in `app.go`: all 14 public methods are declared, and no declared method
is missing from Go. (The rest of `app.go`'s methods are Wails lifecycle hooks or
private helpers, which correctly have no frontend binding.)

---

### 38. `codes.expire` had no UI

The admin hook implemented `codes.expire` (set or clear a code's `expires_at`,
with an audit-log entry), but nothing in the console called it — an operator
could only change an expiry by editing PocketBase directly.

That was tolerable while expiry was effectively advisory, but entry 33 made an
expired code refused on **re-activation** as well as first activation. So the
one action that can remedy a lapsed code was the one with no affordance, and the
operator's only recourse was the raw admin UI.

The Codes page now exposes it in two places: an "Expiry" button on each row, and
an expiry field in the code detail modal. Both prompt for a `YYYY-MM-DD` date and
accept blank to clear it — with the prompt stating plainly that a past date
blocks both activation and re-activation, because that is the consequence the
operator is about to apply.

---

### 39. No code had ever expired: `new Date()` cannot parse PocketBase dates in goja

Found while trying to verify entry 33's fix on the live hub. The fix was deployed
and correct — and the expiry check still did nothing, on either path.

Five places computed an expiry with the same expression:

```js
var ed = new Date(exp).getTime();
if (!isNaN(ed) && ed < Date.now()) { ... }
```

Measured directly on the live host with a temporary probe hook:

```
typeof_get      = object
string_get      = 2020-01-01 00:00:00.000Z
new_Date_raw    = NaN        <- new Date(raw).getTime()
Date_parse_raw  = NaN        <- Date.parse(raw)
getDateTime     = NaN        <- record.getDateTime("expires_at")
T_replaced      = 1577836800000   <- raw.replace(" ", "T")
```

**goja cannot parse PocketBase's date format.** PocketBase returns
`"2027-09-19 00:00:00.000Z"`; the ECMAScript Date Time String Format requires a
`T` between the date and time parts, and goja enforces that strictly. Replacing
the space with `T` parses correctly.

**Why this was invisible.** The `isNaN(ed)` guard was written to skip empty or
unset values — a reasonable intent. But because the parse *always* produced NaN,
the guard was *always* taken, so the comparison never ran. The guard converted a
parse failure into **"still valid"**, which is the most dangerous possible
default for an expiry check. `expires_at` was decorative: every code was
permanently valid regardless of its date.

It also survived review because the line looks correct, and it survives testing
in Node/V8 — `new Date("2020-01-01 00:00:00.000Z")` parses fine there. The bug
only exists in goja, the one runtime these hooks actually run in. That is worth
recording: a JS-level unit test in Node would have passed.

The bug also predates this pass. Entry 33 fixed the check being *unreachable* on
the re-activation path; this is the separate reason it was *ineffective* even
where it did run.

**Fix.** A single `parsePBDate()` helper, defined **inside each `routerAdd`
callback**. It tries the value as-is, then with the space replaced by `T`, then
as a numeric epoch, and distinguishes the three cases that matter: `0` for
empty/unset, a number for a real date, `NaN` for a non-empty unparseable value —
so a future parse failure does not silently mean "no expiry". All five call sites
use it (`activation`, `code_lookup`, `hiddify`, and two in `admin_console`).

The placement is deliberate and was learned the hard way on this very deploy: a
file-level helper is invisible to the callback, because goja does not hoist
function declarations across scopes. `activation.pb.js` has carried a comment
saying exactly that since the beginning. Deploying the helper at file level
produced `parsePBDate is not defined` on every request.

**Verified live**, against PocketBase directly rather than through Caddy:

```
PASS  expired, unbound         want 410  got 410  Code expired
PASS  valid, first use         want 200  got 200  Activation successful
PASS  expired, re-activation   want 410  got 410  Code expired
PASS  no expiry, first use     want 200  got 200  Activation successful
```

The last case is the negative control: a code with no `expires_at` must stay
usable, or the fix would have broken every code issued without one.

**Incidental finding — Caddy rate-limits tests, not just abusers.**
`/api/activate` is capped at 5 requests per 10 minutes keyed on `{remote_host}`.
Iterating a verification script from one IP trips it, and the limiter returns an
empty-bodied 429, which reads exactly like a hook crash. The first three test
runs of this entry were invalidated by that before it was identified. The
verification script now targets `127.0.0.1:8090` and bypasses Caddy deliberately,
since the limiter is a deployment concern and not what is under test.

---

## CONSOLE RELEASES — AUTOMATIC ARTIFACT VERIFICATION (2026-09-19)

### 26. The console could silently publish the wrong binary

The console's Releases page accepted any file whose *name* matched the slot. It
never looked at the file's contents, so two failures passed undetected:

1. **Cross-slot mixup.** Dropping the Windows binary into the Linux slot
   published a `locus-linux-amd64` URL that serves a Windows PE file. Linux
   clients download it, fail the SHA-256 check, and never update — with no error
   anywhere. The hash matched, because the artifact is internally consistent;
   only the *platform* was wrong.
2. **Wrong release.** Uploading 2.0.0's binaries while typing 2.1.0 passes every
   existing check, and the fleet ends up on the wrong build.

`publish-release.sh` caught (1) and (2) via the `manifest.json` cross-check, but
the console had no equivalent — so the two publishing paths had materially
different safety guarantees with nothing in the UI to say so.

**Also confirmed:** the uploader's allowlist already accepted `manifest.json`,
but the console had no slot for it, so there was no way to supply one through
the web UI at all.

**Fix — automatic, no manifest needed.** The artifacts are self-describing, so
the console verifies each file directly (`v5/console/src/lib/artifact.ts`):

- **Format vs slot**, from the magic number: `MZ` = Windows PE, `\x7fELF` =
  Linux, `\xFE\xED\xFA\xCF` / `\xCF\xFA\xED\xFE` (thin) and `\xCA\xFE\xBA\xBE`
  (universal) = macOS, with the CPU type at offset 4 separating Intel
  (`0x01000007`) from Apple Silicon (`0x0100000C`). A mismatch is refused before
  anything is uploaded, naming what the file actually is.
- **Version vs typed version**, by scanning the binary for the version string
  (bounded to 4MB). A miss is treated as failure — the likely cause is a binary
  from a different release — while an unscannable large file degrades to a note
  rather than a block.
- Failed rows cannot be uploaded, and "Upload all" is disabled while any row has
  failed or is still being checked.
- The detected format is shown in the row, so the operator never has to take the
  filename on trust.

Re-picking a file, or changing the version, re-runs the check (only for files
not already uploaded or in flight).

**Verified:** the detection logic was exercised against real magic bytes for all
ten cases — PE, ELF, Mach-O arm64/x86_64 in both byte orders, universal, plus
zip and text as negative controls. This caught a genuine bug in the first
revision, which accepted `0xCAFEBABE` in either byte order and therefore
classified Java `.class` files as macOS binaries; universal Mach-O is accepted
big-endian only. Also verified: console builds, `/admin` base guard passes, and
`artifact.ts` typechecks.

**Result:** the two publishing paths now have equivalent integrity guarantees,
and the console needs no manifest upload.

---

## RELEASE PIPELINE — `manifest.json` WAS NEVER PRODUCED (2026-09-19)

Found while trying to publish the first real release through the updater.

### 25. `download-artifact` does not flatten, and the release job assumed it did

The build job uploads two things per platform:

```
dist/*.zip      → the bundles      (human download)
dist/raw/*      → the binaries     (the auto-updater's artifacts)
```

`actions/download-artifact` with `merge-multiple: true` **merges artifact names,
not directory paths** — the uploaded layout is preserved, so on the release
runner the bundles land at the top level and the binaries land under `raw/`.

Three release steps assumed one flat directory:

| Step | Assumption | Result |
|---|---|---|
| Generate release checksums | `sha256sum *.zip` | worked (zips are top-level) |
| **Generate updater manifest** | `os.listdir(".")` | **found zero artifacts → `sys.exit(1)` → the release job failed, so no `manifest.json` ever attached** |
| Create GitHub Release | `files: *.zip, *.exe` | would have attached no binaries either |

The manifest step's own "fail loudly on a missing platform" guard is what
turned a silent wrong-hash into a hard failure — but it was failing the whole
release, which is why the tag produced zips and nothing else.

**Fix:**
- New **Normalise artifact layout** step flattens `raw/` into the top level
  (names are already unique per platform, so nothing collides), giving every
  subsequent step the flat directory they were written for.
- The manifest generator now **walks the tree** (`os.walk`) instead of listing
  one directory, so a future layout change degrades to "still finds them"
  rather than "fails the release". It still fails loudly, naming the missing
  platforms, when an artifact really is absent.
- Release assets now explicitly include the four raw binaries
  (`locus-linux-amd64`, `locus-windows-amd64.exe`, `locus-darwin-amd64`,
  `locus-darwin-arm64`) plus `*.sha256` — previously only `*.exe` was globbed,
  which would have skipped the three extension-less Unix binaries.
- Checksums step asserts it found bundles and only covers `.zip` (the raw
  binaries are covered by `manifest.json`; mixing both made the file
  ambiguous).

### Verified

Replayed the exact steps locally against a directory reproducing the
`download-artifact` layout (zips top-level, binaries under `raw/`):

- normalise → all 8 files flat, no collisions
- checksums → 4 zip entries
- manifest → all four platforms with correct filenames and hashes
- manifest hashes agree with CI's own `.sha256` files
- `publish-release.sh RELEASE_DIR=… DRY_RUN=1 2.1.0` → finds all four, manifest
  cross-check passes on every platform
- deliberately removing two platforms → manifest step exits 1 naming
  `macos_arm, macos_intel` (still fails loudly)

**What to upload:** the four **raw binaries** (from `dist/raw/`, i.e. inside
each platform's inner zip) plus `manifest.json`. **Not** the outer `.zip`
bundles — the updater replaces the executable in place and cannot unpack an
archive.

---

The client had drifted into being a black box: it could not reliably say which
build it was, "no update available" had five indistinguishable causes, and the
Linux TUN gap was disguised as a mysterious engine failure. This pass fixes the
class of problem rather than the symptoms.

### 17. `isElevated()` returned `true` unconditionally on Unix

`elevate_unix.go` claimed non-Windows platforms "have no separate elevation
model", so `Connect()`'s privilege gate was skipped entirely on Linux. TUN needs
root or `CAP_NET_ADMIN`; the privileged helper is no longer shipped and direct
mode is forced, so on any non-root Linux session the tunnel was **guaranteed**
to fail — and it surfaced as a raw sing-box error deep in a log rather than
"you need root".

**Fix:** `isElevated()` now performs a real check (`os.Geteuid() == 0`) on Unix,
and `elevationUnsupportedReason()` supplies an actionable message. `Connect()`
fails immediately and explains the requirement instead of starting an engine
that cannot work. Windows behaviour is unchanged (real token check, UAC
handoff).

*Track A (shipping a real Linux elevation mechanism — pkexec/polkit or a
revived helper) is deliberately NOT in this change; the gap is now declared
honestly instead of hidden. See `ENGINE-SWAP-ANALYSIS.md`.*

### 18. Permission failures were reported as opaque engine errors

`startDirect` translated only Windows' "Access is denied". A Linux
"operation not permitted" (the exact output of the elevation gap above) fell
through to `"sing-box exited immediately: <raw text>"`, which tells a student
nothing. A shared `looksLikePermissionError` classifier now maps both dialects
to an actionable message.

### 19. Which build is this? The client could not answer

The version existed only as an ldflags string with no runtime introspection. A
dev build (`go build`, no flags) reported the `main.go` fallback literal —
which can drift from the source tree — and nothing anywhere distinguished it
from a release. That made update decisions unexplainable.

**Fix:** new `internal/buildinfo` package records the version, whether it was
injected by the pipeline, the commit/commit time, whether the tree was dirty,
and the toolchain. `main.go` writes it as the **first line of `locus.log`** so
every support log identifies its build; diagnostics and the update flow consume
it. An uninstrumented build is now flagged (`UNINSTRUMENTED`) rather than
silently trusted.

### 20. The version was duplicated in five places with nothing keeping them in sync

`v5/VERSION`, `main.go`'s fallback, `internal/buildinfo`'s fallback,
`wails.json` and `frontend/package.json`. Drift produces a binary whose
diagnostics, installer metadata and update comparisons all disagree — and it
surfaces weeks later as inexplicable client behaviour.

**Fix:** `version_consistency_test.go` fails the build when they disagree, and a
CI step does the same (including checking that a `v*` tag matches `v5/VERSION`,
so a release can never ship a binary reporting a different version than its
tag).

### 21. "No update available" collapsed five distinct causes into one boolean

`CheckForUpdate` returned `{available: false}` for: nothing published, rollout
has not bucketed this device, the advertised version is not newer, the network
is blocked, and every platform asset is missing. The UI rendered **nothing at
all** in every case — clicking "check for updates" appeared to do nothing.

**Fix:** the result now carries a machine-readable `status`
(`available`, `up_to_date`, `no_release`, `unreachable`,
`uninstrumented_build`, `not_activated`, `no_asset`), a human `reason`,
the client's own `currentVersion`/`currentInstrumented`/`platform`, and the
`advertisedVersion`. The UI renders the outcome, styles it (amber when the
check could not complete, so it never reads as a reassuring success), and shows
the platform in the footer. `ApplyUpdate` gained the same treatment: it refuses
an update with no asset/checksum for this platform *before* starting a doomed
download, and every `update:status` event now carries `from`/`to` versions so
progress reads "2.0.0 → 2.1.0" instead of a bare phase.

### 22. Update binary-swap failures could destroy the installation

Windows: if `os.Rename` of the new binary failed **and** the restore rename also
failed, the error was discarded and the machine was left with no runnable
binary (the old one stranded as `.old`). Linux/darwin: the `chmod` ran *after*
the rename, so a chmod failure reported a failed update for a swap that was
already committed — leaving the two-phase sentinel inconsistent with the
binary on disk.

**Fix:** Windows reports the salvage path explicitly ("the previous build is
preserved at `<path>`…"); Linux/darwin chmod the downloaded binary **before**
the rename, making the rename the single commit point.

### 23. Diagnostics could not answer the common support questions

`GetDiagnostics` omitted the resolved sing-box path (and whether it still
exists), the generated config path, the log file location, build provenance, and
the last update outcome. It also had broken indentation in the template.

**Fix:** the report now includes all of the above. A support report is
self-contained.

### 24. Windows-only code was never linted

CI lints on Ubuntu, so `//go:build windows` files were invisible to
golangci-lint. Running it against a Windows target surfaced pre-existing
findings: an unused `kernel32` var, three unchecked `syscall.CloseHandle`/
`messageBox.Call` error returns, and three uses of the deprecated
`syscall.StringToUTF16Ptr`. The unchecked `MessageBoxW` call was in the
**last-resort startup-error box** — a failure there shows no dialog at all,
which is precisely the "app silently does nothing" symptom that file exists to
prevent.

**Fix:** all fixed; `golangci-lint` is now clean for both Windows and Linux
targets. `internal/tray/tray.go` also passed `gofmt` again (the documented
pre-existing formatting failure is resolved).

### Not fixed (still open, by decision)

1. **Linux TUN elevation mechanism** — the gap is declared, not closed.
2. macOS builds unsigned (Gatekeeper) — unchanged.
3. sing-box is not updated by the release pipeline — unchanged.
4. `/update.json` remains a stale placeholder; the updater reads `update_config`.

---

Running the hub required SSH, Python and sqlite. That is fine for an engineer
and hopeless day-to-day, so the operations an operator actually performs now
have a web UI at **`/admin/`**.

### What was built

- **`pb_hooks/admin_console.pb.js`** — one authenticated route
  (`POST /api/admin/console`) with an `action` discriminator covering dashboard,
  code generation/listing/suspend/unbind/expiry/editing/history, middleman
  listing, tier read/write and release read/write. One auth check, one audit
  point, no scattered endpoints.
- **`v5/console/`** — Vue 3 + Vite SPA (Dashboard, Codes & Clients, Releases,
  Tiers). The admin token lives in sessionStorage only and is never in the
  built bundle; Caddy serves the static files with `noindex`.
- **`scripts/release_upload.py`** + `templates/locus-upload.service` — a
  dedicated uploader for release binaries.
- **`scripts/deploy-console.sh`** — build, package, upload, verify.
- New `code_events` collection, plus `unbound_at`, `unbind_reason`, `label`,
  `notes` on `codes`.

### 14. `admin_unbind.pb.js` wrote fields that did not exist

It called `record.set("unbound_at", …)` and `record.set("unbind_reason", …)`,
but neither column was ever in the schema. PocketBase silently discards unknown
fields, so the audit trail looked implemented and recorded **nothing**. Both
columns now exist (via the column-reconciliation added earlier), and an
`code_events` collection records admin actions properly.

### 15. Neither PocketBase nor Caddy can receive a release upload

Two dead ends found by testing rather than assuming:

- **PocketBase caps request bodies.** Raw bodies are accepted at 1 MB and
  rejected at 5 MB (`HTTP 400 "Something went wrong while processing your
  request."`). A Wails bundle is ~15–30 MB. `$apis.requestInfo(e).files` also
  does not exist in this build, so multipart is not available to hooks either.
- **This Caddy build has no upload handler** (`caddy list-modules` shows only
  `request_body`, no `upload`/`webdav`).

**Fix:** a small purpose-built service on `127.0.0.1:8091`, reachable only
through Caddy at `/api/admin/upload`. It authorises *before reading a byte*,
streams to a temp file hashing as it goes, caps the size on both the declared
length and the stream, validates the version and filename against a strict
allowlist (the filename becomes a path), and publishes atomically via
`os.replace` so a client can never fetch a half-written binary.

### 16. Caddy `handle` blocks are evaluated in order — `/api/*` swallowed uploads

The new `/api/admin/upload` block was placed *after* the general `/api/*`
proxy, so uploads were proxied to PocketBase (and hit its size cap) instead of
reaching the uploader. Caddy matches `handle` blocks top-down, so the specific
path must come first. Also verified: each `handle` block ends in its own
`file_server`/`reverse_proxy` (a missing terminal handler inside `handle_path`
is why an earlier revision 404'd).

### Live verification (2026-09-19)

| Check | Result |
|---|---|
| `/admin` (no slash) | 301 → `/admin/` → 200 |
| `/admin/` + hashed asset | 200, correct `/admin/assets/…` base |
| Admin API rejects a bad token | 403 `{"ok":false}` |
| Dashboard | counts + per-tier + activity feed |
| Generate codes (console) | created 4 codes |
| **Generated code validates client-side** | Luhn checksum correct |
| **Generated code activates a real client** | HTTP 200 + correct tier config |
| Subscribe/lookup a generated code | `ready to activate` |
| Suspend → heartbeat | 403 "Account suspended" |
| Reactivate → heartbeat | 200 |
| Unbind → status | returns to `available` |
| Event history | full trail with reasons and timestamps |
| Middleman grouping | `Sarah: 3, Tom: 1` |
| Upload: no token | 403 **before** reading the body |
| Upload: path traversal filename | refused |
| Upload: malformed version | refused |
| Upload: checksum mismatch | refused, with expected/actual |
| Upload: valid file | published; **served hash matches exactly** |
| Tier read/write, release read/write | working |

Test artifacts, test codes and the rollout setting were returned to a clean
state afterwards.

---

## CLIENT UPDATE SYSTEM — BUILT END-TO-END + PB HIDDEN BREAKAGE (2026-09-19)

The update pipeline was implemented in code but had **never worked in
production**: the gate was a silent no-op and there was no way to publish an
artifact. Both are now fixed and verified against the live hub.

### 10. `findRecordsByFilter` is broken — the update gate never fired

The single most consequential finding. In PocketBase 0.22.21's JS hook runtime,
**`$app.dao().findRecordsByFilter()` always returns an empty array**, for every
collection and every filter — including `1=1` and `id!=''` on a populated
table. It raises **no error**, so the surrounding `try/catch` never fires and
the caller simply sees "no records".

Proven with a temporary probe hook (since removed):

| Call | Result |
|---|---|
| `findRecordsByFilter("update_config", "active=true", "", 0, 1)` | **0 rows** |
| `findRecordsByFilter("update_config", "1=1", "-created", 0, 5)` | **0 rows** |
| `findRecordsByFilter("tier_configs", "tier='eco'", "-created", 1, 0)` | **0 rows** |
| `findFirstRecordByFilter("update_config", "active = true")` | **1 row** ✅ |
| `findRecordsByExpr("update_config", $dbx.exp("version = {:v}", …))` | **1 row** ✅ |
| `findFirstRecordByData("tier_configs", "tier", "eco")` | record ✅ |

Impact — silently broken, all in production:

- **Update gate ignored rollout entirely** → no client was ever offered an
  update no matter what `rollout_percent` said. This is why "we never saw an
  update arrive" was not a rollout-tuning problem.
- **Activation rate limiting (5/10 min) never enforced** — an unbounded
  enumeration oracle.
- **`/api/code-lookup` rate limiting (10/10 min) never enforced.**
- **`server_config` lookups** could return nothing, leaving a client
  "successfully activated" with no tunnel settings.

Also found in the same sweep: **top-level function declarations are not visible
inside `routerAdd` handlers** (`helperFn is not defined`). `hiddify.pb.js`
defined `sanitizeFilter()` at file scope, so the entire `/api/hiddify` endpoint
returned **HTTP 500**; it also called `$app.findRecordsByFilter`, which does not
exist on the `$app` object at all.

**Fix applied to all five hooks:** use `findFirstRecordByFilter` for single-row
lookups and `findRecordsByExpr` for counts; inline helper functions inside the
handler; verify each endpoint live.

### 11. Per-platform checksums were missing (updates could never verify)

`update_config` carried a single `update_sha256` but a release publishes four
different binaries, and `PerformUpdate` **refuses to apply an update whose
SHA256 is empty**. A single hash can only ever match one platform, so every
other platform would download successfully and then fail verification.

**Fix:** added `sha256_{linux,windows,macos_intel,macos_arm}` columns,
`UpdateInfo.PlatformSHA256()` (falling back to the legacy single hash), and
per-platform `update_sha256_*` fields in the heartbeat response. `PerformUpdate`
now resolves the hash for the artifact it is actually about to fetch.

### 12. The version gate could downgrade clients

`ApplyUpdate` compared with string equality (`Version == a.version`), so a
stale/rolled-back `update_config` row advertising an *older* release would
downgrade every client — and there is **no server-driven downgrade** in this
system, so recovery would mean shipping a new version. Also, `"1.9.0" >
"1.10.0"` under string comparison.

**Fix:** new `internal/updater/version.go` with `CompareVersions`/`IsNewer`
(numeric segments, zero-padding, pre-release ordering, build metadata ignored).
`recordUpdateSignal` now drops any signal that is not strictly newer (so the UI
never offers a downgrade) and `ApplyUpdate` refuses one regardless.

### 13. No way to publish an artifact (and `/updates/` was not served)

CI published only `.zip` bundles, which the updater cannot consume — writing
zip bytes over the running binary would brick the install. `update_config` was
all defaults with an empty `update_url`, and Caddy served no `/updates/` path
(404).

**Fix, in four parts:**
1. **CI** stages raw per-platform binaries (`locus-linux-amd64`,
   `locus-windows-amd64.exe`, `locus-darwin-amd64`, `locus-darwin-arm64`) plus
   per-file `.sha256`, and emits a `manifest.json`. The manifest generator
   **fails the build** if any platform artifact is missing, rather than
   publishing a partially-updatable release.
2. **Caddy** gained `handle_path /updates/*` → `file_server` on
   `/var/www/updates`, with directory listings disabled and a short cache TTL.
3. **`publish-release.sh`** uploads artifacts (atomic temp-name move so a
   client never fetches a half-written binary), re-downloads each one to verify
   the served hash matches, then updates `update_config` with per-platform URLs
   and hashes. It refuses to publish sub-1MB files or artifacts whose hashes
   disagree with `manifest.json`.
4. **Schema migration:** `seed-pb.py` now reconciles collections, *adding*
   columns that a pre-existing deployment lacks (this is how the missing macOS
   and `sha256_*` columns reach an already-provisioned hub). It never removes or
   retypes, so it cannot destroy data.

### Live verification (2026-09-19)

| Step | Result |
|---|---|
| Publish 4 artifacts via `publish-release.sh` | uploaded, and **each re-downloaded over HTTPS with a matching SHA256** |
| `update_config` after publish | version, rollout, 4 download URLs + 4 per-platform hashes |
| Heartbeat `rollout_percent=0` | no update fields ✅ |
| Heartbeat `rollout_percent=100` | `update_available` + all 4 URL/hash pairs ✅ |
| `active=0` | no update fields ✅ |
| **Client simulation**: download every advertised URL, hash it | **all 4 platforms match the advertised hash** ✅ |
| Directory listing `/updates/<v>/` | 404 (no listing) |
| Missing artifact | 404 |
| Guard rail: tampered artifact vs manifest | publish **refused** |
| Guard rail: truncated (<1MB) artifact | publish **refused** |
| `go test ./internal/updater/...` | 7 test funcs pass |
| Rollout restored to 0; test artifacts + test codes removed | ✅ hub left clean |

**Route note:** the hook registers `GET /api/hiddify` (not POST); testing it the
wrong way yields a 404 that looks like a missing route.

---

## SECRETS WIRING & BACKUP VERIFICATION — TWO MORE BUGS (2026-09-19, later pass)

Found while answering "are all secrets inserted, and are backups working?" on the
live hub. Both are silent-failure class: the system reported success while
doing the wrong thing.

### 8. `seed-pb.py` — creds file could disagree with the live admin password

`/root/.pb_admin_creds` recorded a password that **did not authenticate**
(`HTTP 400 Failed to authenticate`), so the admin UI could not be logged into
with the credential the deploy documented.

Two compounding causes:

1. **`os.environ` clobbering.** Step 1 copied *every* key from
   `/root/.pb_admin_creds` straight into `os.environ`. That silently overwrote
   `PB_ADMIN_PASS` (sourced from `secrets.env.age` by `setup.sh`) with the
   file's value. Any "is the env authoritative?" check therefore always saw the
   file's value — so a wrong recorded password could never be repaired.
2. **Random-password fallback.** When `PB_ADMIN_PASS` was absent, the script
   generated a random password and wrote *that* to the creds file, while the
   admin record had been created earlier with the secrets password.

Net effect: the recorded credential was wrong, and re-running could not fix it
(the CLI `admin create` only creates; it never updates an existing password).

**Fix:** read only `PB_TOKEN` from the creds file (into a local, not
`os.environ`); resolve the password env-first, then creds, then random; and
when the resolved password is the *environment* value and login still fails,
force a reset via `pocketbase admin update` so the recorded credential becomes
truthful. Verified by deliberately breaking the password and the creds file,
then confirming one run converges both back to the secrets value
(`creds == secrets`, login `HTTP 200`).

### 9. `07-backups.sh` — "✓ Backup verified" did not verify the backup

The verify step relied on `b2 download-file-by-name`, which was **removed in b2
CLI v5** and fails with `ERROR: File not present`. Worse, the checksum command
that followed was:

```bash
sha256sum -c "${TMP_DIR}/${COMPRESSED}.sha256"   # hashes the LOCAL source file
```

so it re-hashed the file that had just been uploaded — not the downloaded copy.
Result: `✓ Backup verified` printed even when nothing was retrieved. A backup
whose integrity check cannot fail is not an integrity check.

**Fix:** download with `b2 file download "b2://…" <local>` and compare the
SHA256 of the **downloaded bytes** against the local sum; set `EXIT_CODE=1` and
warn on mismatch or failed download. Verified live: remote and local hashes now
match (`4be5f822…`), and B2 reports `Checksum matches`.

### Round-trip restore proven (2026-09-19)

Separately confirmed that a backup is genuinely restorable, not merely
uploadable:

| Step | Result |
|---|---|
| Download newest `backups/*.db.gz` from B2 | OK (8809 bytes) |
| SHA256 vs the stored `.sha256` object | **exact match** |
| `gunzip` | OK (143360 bytes) |
| `PRAGMA integrity_check` | **ok** |
| Tables present | all 9 (`codes`, `tier_configs`, `activation_attempts`, `update_config`, `users`, `_admins`, …) |
| `tier_configs` rows survive | 3, with intact server/port/password JSON |
| Lifecycle rule | hide after 5 days, delete after 7, prefix `backups/` |

> Note: that snapshot contained `update_config` = **2 rows**, which is the exact
> duplication bug fixed as item 6 — independent confirmation that the fix was
> needed.

**Secrets audit (all green):** all 10 keys present in `secrets.env.age`;
`/root/.tier_passwords`, `/root/.pb_admin_creds`, `/root/.b2-creds`,
`/root/.admin_api_token` all mode `600`; the three tier passwords match
`/etc/shadowsocks/*.json` exactly; `ADMIN_API_TOKEN` matches both the file and
PocketBase's process environment and is accepted by `/api/admin/unbind-code`.

---

## BLANK-VPS DEPLOY — SEVEN BUGS FIXED (2026-09-19)

Validated `v5/server/setup.sh` end-to-end against a **fresh** DigitalOcean
Ubuntu 22.04 droplet (`170.64.196.179`, 1 vCPU / 512MB / Sydney). This box is
now the live hub for `networkingguides.duckdns.org`. Previously the scripts had
only ever been exercised against an already-provisioned host, so several bugs
that only bite on a *blank* box went unnoticed. Every one below was reproduced,
fixed, re-run, and verified.

Summary: (1) `00-env.sh` memory guard rejected 512MB droplets; (2)
`seed-pb.py` used a PocketBase admin API that does not exist in 0.22, so **no
schema was ever created**; (3) `06-pocketbase.sh` downgraded that failure to a
warning; (4) `08-firewall.sh` used a 6-conns/30s `ufw limit` that locked out
deployment automation; (5) `smoke-test.sh` reported false tc failures via
`grep -q` SIGPIPE; (6) `update_config` duplicated on every deploy; (7) unknown
codes returned HTTP 500 instead of 404/not-found in all four hooks.

### 1. `00-env.sh` — 512MB droplets could never pass the memory guard

The guard compared `MemTotal` against `524288` KB (512 MiB) and hard-failed:
```
[00-env][FAIL] Less than 512MB RAM. Minimum 512MB required.
```
A "512MB" droplet reports **464972 KB (454MB)** because the kernel reserves a
slice — so the check was impossible to satisfy on the exact hardware it was
written for. Deployments aborted before module 01.
**Fix:** compare against `MIN_MEM_KB=380000` (~371MB), which still catches
256MB-class boxes. Also warn (not fail) on absent/small swap, with the exact
`fallocate`/`mkswap`/`fstab` recipe, since no module creates swap and a
transient spike can OOM a 512MB host.

### 2. `seed-pb.py` — wrong PocketBase admin API; nothing was ever seeded

```
Superuser creation returned: The requested resource wasn't found.
Could not obtain admin token. Trying existing creds...
```
The script POSTed to `/api/collections/_superusers/records` to create the first
admin. **That endpoint does not exist in PocketBase 0.22.x.** 0.22 uses the
legacy `_admins` table and `/api/admins/auth-with-password`, and offers **no
public API at all** for creating the first admin — only the CLI
(`pocketbase admin create <email> <pass>`). Consequence: no admin ⇒ no token ⇒
**zero collections created**, while `06-pocketbase.sh` downgraded the failure to
a warning and still printed "✓ Setup complete". The hub served 500s to every
client and looked successfully deployed.
**Fix:** create the first admin via the CLI; authenticate against
`/api/admins/auth-with-password` with a `_superusers` fallback so the same
script works on 0.22.x and 0.23+. Added a `SCHEMA_ERRORS` counter that exits
non-zero if collections or tiers fail to seed.

### 3. `06-pocketbase.sh` — bootstrap failure was only a warning

`PocketBase bootstrap encountered errors — check output above` was followed by
`✓ PocketBase setup complete`. A hub with no schema is not a successful deploy.
**Fix:** treat a failed bootstrap as fatal (`fail`), and replace the fixed
`sleep 3` after start with a 30-second health-poll (the fixed sleep was racy on
512MB hosts where first-start migration is slow).

### 4. `08-firewall.sh` — SSH rate limit locked out legitimate automation

`ufw limit 22/tcp` hardcodes **6 new connections / 30s per IP** (see
`backend_iptables.py`: `--seconds 30 --hitcount 6` — there is no config knob).
Back-to-back deploy commands tripped it, producing repeated
`ssh: connect to host ... port 22: Connection refused` mid-deployment.
**Fix:** drop the limiter entirely and use **fail2ban** (`/etc/fail2ban/jail.d/
locus-sshd.local`, 6 failures/10m → 1h ban, `banaction=ufw`), keeping UFW as
plain allow/deny. UFW's own limiter cannot be re-tuned — patching
`user.rules` is futile because `ufw reload` re-renders it from the hardcoded
value.

> **Warning — do not attempt to re-add a custom limiter chain.** An earlier
> attempt injected `recent`-based rules into `/etc/ufw/before.rules`. This
> locked SSH out **completely** (port 22 timed out while 80/443 kept serving)
> and required a provider-console recovery. `after.rules` is worse: referencing
> `ufw-user-input` there fails with `Problem running '/etc/ufw/after.rules'`
> because UFW creates that chain *after* processing the file. Both approaches
> are recorded here so they are not retried.

Also normalised `/etc/ufw/ufw.conf` `ENABLED=no` → `yes`: some images ship the
flag off while rules are already loaded, which makes `ufw status` report
"active" while `ufw reload` silently does nothing ("Firewall not enabled
(skipping reload)").

### 5. `smoke-test.sh` — `grep -q` SIGPIPE caused false tc failures

```
⚠️  WARN: tc Stealth class (1:20) not found
```
on a host where `tc class show` clearly listed it. `tc ... | grep -q` makes
`grep` exit at the first match, SIGPIPE-ing `tc`; under `set -o pipefail` the
pipeline then reports **141** and the `if` takes the failure branch. The same
latent pattern existed in the UFW and `ss` checks.
**Fix:** capture command output once (`TC_NOW=$(...)`, `UFW_NOW=$(...)`,
`SS_NOW=$(...)`) and match against the captured text.

### 6. `update_config` duplicate rows on every deploy

`seed-pb.py` POSTed a new `update_config` record unconditionally, so a second
`setup.sh` run left **2 rows** (client reads the first, so mostly harmless, but
rollout state becomes ambiguous and rows grow per deploy).
**Fix:** same idempotent upsert used for `tier_configs` — delete older
duplicates, PATCH the newest, create only if none exists.

### 7. `findFirstRecordByData` throws — unknown codes returned HTTP 500

Affected **`code_lookup.pb.js`** (new), `activation.pb.js`, `heartbeat.pb.js`,
`admin_unbind.pb.js`. Every file guarded the lookup with `if (!rec)` as if the
DAO returned null. It does not — it **throws** `sql: no rows in result set`, so
the guard was unreachable and a well-formed but unknown code escaped to the
catch-all:
```
POST /api/code-lookup  →  500 {"status":"unknown","message":"sql: no rows in result set"}
```
For a student who mistypes a code this is the worst possible signal: the client
treats 5xx as "cannot tell" and retries, instead of saying "not recognised".
**Fix:** wrap each lookup in `try/catch` and treat a throw as not-found
(lookup → `200 not_found`; activate/heartbeat/unbind → `404`).

### Verification performed on the live hub

| Check | Result |
|---|---|
| `setup.sh` full run, then **complete re-run** | exit 0, all 8 modules ✓ (idempotent) |
| `smoke-test.sh` | **23 passed / 0 failed / 0 warnings** |
| TLS (Let's Encrypt, Caddy) | valid, 2026-09-19 → 2026-12-18 |
| `GET /api/health`, `/update.json` over HTTPS | 200 |
| `POST /api/activate` (all 3 tiers) | 200 + correct tier password/port, `udp_relay:false` |
| `POST /api/code-lookup` unbound / bound-same / bound-other / bad-checksum / unknown | `unbound` / `bound_this_device` / `bound_other` / `Invalid code format` / `not_found` |
| `POST /api/heartbeat` (valid + unknown) | 200 `status:ok` / 404 |
| **Real HTTP through each tier** (`sslocal` → `curl https://example.com`) | **200 + real content on 8443, 8444, 8445** |
| Wrong tier password | correctly fails (no data) |
| BBR + tc caps | BBR default; classes `1:10` 5Mbit, `1:20` 100Mbit, `1:30` 200Mbit |
| Reboot persistence | caddy, pocketbase, 3× ss, 3× tc, ufw, fail2ban, backup timer all enabled |
| `tier_configs` / `update_config` row counts after re-runs | 1 per tier / 1 total |

**Deploy notes:** the hub must be seeded with codes (`codes.json` holds the
`RQ-` set) — a fresh DB has an empty `codes` collection. `SKIP_DNS_CHECK=1` is
required until DNS points at the new host; Caddy only obtains a certificate
once the A record resolves here.

**Deploy-flow trap found during final verification:** `setup.sh` installs hooks
from the **staging copy at `/root/server/pb_hooks/`**, not from the repo. After
fixing a hook in the repo, re-running `setup.sh` with a stale staging dir
silently reverts the live fix (the `code-lookup` 500 reappeared this way after a
clean re-run). Always update `/root/server/pb_hooks/` (or re-`scp` the tree)
before re-running setup, then `systemctl restart pocketbase`.

---

## CODE FORMAT MIGRATION — MYVPN- → RQ- (2026-08-17)

**All activation codes now use `RQ-XXXX-XXXX-XXXX-C` (15 chars) instead of
`MYVPN-XXXX-XXXX-XXXX-C` (18 chars).** The Luhn-mod-N checksum covers the whole
body INCLUDING the prefix, so every code's check digit changed with the prefix.

Changed (live code + docs):
- `v5/client/internal/activation/luhn.go`: `CodePrefix = "RQ"`; `CodeTotalLen`
  is derived (now 15). Client-side validation rejects old `MYVPN-` codes with
  the prefix error.
- `ActivationScreen.vue`: placeholder `RQ-XXXX-XXXX-XXXX-C`, auto-format
  segments `2-4-4-4-1`, full-body length check 15.
- `v5/server/pb_hooks/activation.pb.js` + `heartbeat.pb.js` + `hiddify.pb.js`:
  canonical-lookup normalization now formats `2-4-4-4-1` (15 chars).
- `v5/server/scripts/seed-live.py` `make_test_code()`: `RQ` body + `2-4-4-4-1`.
- `v5/server/scripts/codes.json`: all 15 codes migrated — random segments
  preserved, prefix + checksum recomputed (e.g. `MYVPN-8FAU-DJBA-DBA7-H` →
  `RQ-8FAU-DJBA-DBA7-N`). Same bodies as the previously seeded cards.
- `scripts/generate_codes.sh` + `scripts/README.md`: `PREFIX="RQ"` (generator
  is prefix-length agnostic otherwise).
- Docs: README.md, API.md, ARCHITECTURE.md, BACKEND-API.md, CLIENT-GUIDE.md,
  IMPLEMENT.md, OPS.md, POCKETBASE-SETUP.md, UI-AESTHETICS.md,
  WAILS-MIGRATION.md — code examples/format updated.
- New `luhn_test.go` locks in the RQ format, validates the migrated codes.json
  codes + the doc example, and rejects legacy MYVPN codes.

NOT changed (intentional): `v4/` and `v5/docs/history/` are historical records
and keep the old format; `LOCUS_DEBUG`/`LOCUS_LOG_LEVEL`/`MYVPN_AGE_KEY` are
app-infra env/secret names, not code prefixes; app name remains "Locus".

**Deploy (REQUIRED, breaks old codes):**
1. The live PocketBase `codes` collection still holds the MYVPN codes — update
   each record to its RQ form (same random segments, new checksum) or delete +
   reseed from the new `codes.json` (`seed-live.py` skips existing codes, so
   plain re-run ADDS duplicates — it does NOT replace).
2. Existing bound activations keep working server-side (binding is by
   fingerprint; the heartbeat/activation hooks now normalize to the RQ form),
   BUT already-issued MYVPN cards can no longer be activated: new client
   validation rejects the prefix. Reissue/print cards with the RQ codes.
3. Deploy the three pb_hooks edits (activation/heartbeat/hiddify) with the DB
   update — the hooks are forward/backward tolerant, but the DB must match.

---

## CLIENT APP PASS — UPDATE FLOW WIRED, CODE NORMALIZATION, STORAGE RECOVERY (2026-08-17)

Client-side fix pass (no server re-deploy needed for the client fixes; the two
hook edits are hardening and deploy with the next hook sync):

**1. Update flow was a dead end — now wired end-to-end.**
The `updater` package (crash-safe two-phase swap) was fully implemented but
NOTHING called `PerformUpdate`: the UI's "Update vX available" button only
re-ran `CheckForUpdate` and never downloaded anything. Students would see the
button forever with no update ever applying.

- `app.go`: heartbeat + manual check now record the update signal
  (`recordUpdateSignal` → `a.lastUpdate`), and a new frontend-bound
  `App.ApplyUpdate()` runs `PerformUpdate` in the background (5 min timeout,
  serialized by `upMu`), emitting `update:status` events
  (`downloading → verifying → applying → applied | failed`). On success the
  app quits ~1.5s later so the forked new binary takes over. Re-applying the
  already-running version (staged rollouts re-advertise every heartbeat) is
  rejected with "Already running the latest version".
- Frontend: `bridge.applyUpdate()`, `update:status` listener, store
  `updatePhase`/`updateMessage` state, and the MainScreen button now applies
  the update with a live spinner + "Downloading…/Restarting…" label.
- Note: after an update the app does NOT auto-reconnect the tunnel (the new
  binary starts disconnected; student clicks Connect). Follow-up candidate.

**2. Activation-code normalization (heartbeat 404 risk).**
The server looks up codes formatting-sensitively (`findFirstRecordByData` on
the seeded hyphenated form), but the client stored whatever the user typed.
The frontend auto-formats with hyphens so the happy path worked, but any
unformatted/lowercase stored code (old clients, direct pastes) 404'd every
heartbeat — killing suspension checks and update signals.

- `activation.NormalizeCode()` added; `App.Activate` stores + heartbeats the
  canonical `MYVPN-XXXX-XXXX-XXXX-C` form; `Startup` self-heals legacy stored
  codes by rewriting storage.
- Hardening: both `activation.pb.js` and `heartbeat.pb.js` now normalize the
  lookup code to the canonical form before `findFirstRecordByData`.

**3. Storage: corrupt file now recovers from backups instead of deactivating.**
`storage.New` previously moved a corrupt `storage.json` aside and started
fresh — silently deactivating the device (and the code is then "bound to
another device" server-side, a support nightmare). The `.bak.0..2` rotation
existed but was never used for recovery.

- `New()` now tries `tryRestoreBackups()` (newest valid `.bak.N` first, JSON +
  consistency checked) before the fresh-start fallback; restored via the same
  atomic save path.
- `RestoreFromBackup()` simplified to reuse the same logic.
- New tests: `storage_test.go` (corrupt→restore keeps activation, no-backup→
  fresh start moves file aside, invalid backups skipped, rotation pruning).

**4. Frontend fixes.**
- `MainScreen.vue`: removed dead `await emit('connect')` error handling —
  Vue 3 `emit()` returns void, so the "failed connect" branch could never
  fire. Connect failures surface through the global error toast (store path),
  which already worked. Also removed the unused `check-update` emit/handler.
- `app.go`: bound methods (`Activate`, `Connect`, `GetStatus`, `IsActivated`,
  `GetDiagnostics`, `disconnect`, `startHeartbeatLoop`) now nil-guard via
  `notReady()` when Startup fails part-way — a broken startup shows a clear
  message instead of panicking inside the Wails binding.

**Verified:** `go build ./...` + `go vet` + `go test ./...` (incl. new storage
tests) green on linux, windows-amd64 and darwin-arm64 cross-compile;
`vue-tsc --noEmit` + `vite build` clean; frontend/dist rebuilt and committed.

**Deploy:** client fixes ship with the next client build. The two `pb_hooks`
edits are safe to deploy anytime (normalization is a no-op for canonical
codes); restart pocketbase or rely on hook hot-reload.

---

## GAMING UDP — P0 CODE LANDED (UNTESTED) (2026-08-14)

The P0 transport change from `v5/docs/GAMING-UDP.md` is implemented but
**untested** (no test environment). What landed:

- `v5/server/modules/02-shadowsocks.sh`: gated `ENABLE_UOT=1` section —
  installs sing-box server (v1.12.1) with a Strike-creds shadowsocks inbound
  on `UOT_PORT` (default 8446), `udp_over_tcp: true`, systemd unit
  `sing-box-uot.service`. Additive; 8443/8444/8445 and TCP path untouched.
- `v5/server/scripts/seed-live.py` + `seed-pb.py`: with `ENABLE_UOT=1`,
  strike tier_configs gains `"uot_port"` + `udp_relay=true`; otherwise
  identical to before (raw UDP).
- Client (`process.go`, `app.go`, `heartbeat.go`, `activation.go`,
  `storage.go`): when the tier advertises `uot_port` (UDPRelay &&
  ServerPortUOT > 0), the generated config adds a `proxy-uot` outbound
  (`udp_over_tcp: true`) and a `network: udp` route rule; TCP stays on the
  standard outbound. No advertised port → config byte-identical to before.
- Verified: `go build`/`go vet` green on linux + windows cross-compile.
  Shell modules NOT executed (no VPS).

**Deploy (when testing resumes):** `ENABLE_UOT=1` on setup.sh re-run +
`ENABLE_UOT=1` on seed-live.py. Disable: stop unit + drop uot_port from
tier_configs. Full validation checklist in `v5/docs/GAMING-UDP.md` §P1.

---

## GAMING UDP — CONSOLIDATED CHANGE PLAN (2026-08-14)

The Strike-tier gaming-UDP failure chain (raw SS UDP → hostile school-network
UDP policy; UoT removed because shadowsocks-rust RSTs SagerNet UoT) is
consolidated into an implementation plan at `v5/docs/GAMING-UDP.md`.

**Core change (P0, not yet implemented):** carry game UDP inside the TCP
tunnel via SagerNet UDP-over-TCP, with a **server that implements it**
(additive sing-box server instance on a new port is the leading option —
shadowsocks-rust can't do UoT and RSTs it). The server choice is an open
decision with a tradeoff matrix in the plan (§5a: sing-box server vs Xray
SS+uot vs shadowsocks-rust+udp2raw vs raw-UDP status quo); the deciding
interop test can't run until testing resumes. P1 validation experiments
(VPS-direct byte-exact LiteNetLib probe, packet-rate ladder, server
aliveness) are deferred until testing is possible. See the plan doc for the
full file-change map and sequencing.

---

## MACOS RE-ENABLED — UNSIGNED BUILD (2026-08-14)

Round-2 culling dropped macOS (unsigned builds were deemed unusable).
Revisited when it became clear macOS is ~50% of the target market; decision:
ship the **unsigned** build with a documented Gatekeeper workaround (right-click
→ Open, or `xattr -cr /Applications/MyVPN.app`). Full end-user instructions
will live on the separate download site.

What was restored (all previously removed in the culling commits):
- `tunnel.go`: `darwinTUN`, `killSwitchDarwin` (pfctl), `setDNSDarwin`
  (networksetup), darwin switch cases + interface assertion
- `activation/fingerprint_darwin.go`: self-contained darwin fingerprint
  (networksetup / ioreg), mirroring the linux/windows layout
- `updater/update_darwin.go`: darwin binary swap + fork (`swapDarwin`/`forkDarwin`)
- `darwin_link.go`: `UTType` cgo shim (Wails + Xcode 26 SDK linker fix)
- `manager/process.go`: elevation case back to `linux, darwin`
- `updater.go` + `heartbeat.go`: `DownloadURLMacOSIntel/ARM`,
  `UpdateMacOSIntel/ARM` fields + darwin case in `PlatformDownloadURL`
- `server/pb_hooks/heartbeat.pb.js`: `update_macos_intel`/`arm` field emission
- `.github/workflows/build.yml`: macOS-amd64 + macOS-arm64 matrix entries,
  sing-box darwin download case, release-table macOS rows + unsigned note
- `v5/client/Makefile`: `build-macos-intel` / `build-macos-arm` + build-all

**Verified:** `go build ./...` and `go vet` pass for darwin arm64 + amd64
(cross-compile), plus linux + windows. The full `wails build` (frontend
embed) must be validated on a macOS runner (CI) before shipping a release.

---

## FRESH VPS DEPLOY 2026-08-14 — BUGS FOUND & FIXED (134.199.155.166)

First deploy to the new VPS (Ubuntu 22.04, 1vCPU/1GB, DigitalOcean syd1).
DNS for networkingguides.duckdns.org now points at the new IP. The deploy
surfaced a stack of real bugs — all fixed in this round:

| # | Area | Bug | Fix |
|:-:|------|-----|-----|
| D1 | `00-env.sh` | Hard RAM gate (<1GB FAIL) rejected a 957MB-class VPS that runs the whole stack fine | Fail only <512MB; <1GB is a warning |
| D2 | `05-caddy.sh` | `list-modules | grep -q` under `set -euo pipefail`: grep -q closes the pipe → caddy SIGPIPE → pipefail turns the check into a FALSE NEGATIVE (a good binary was rejected) | Use `grep -c` (consumes all input); fixed in 3 places |
| D3 | `05-caddy.sh` | caddyserver.com download API ignores the version pin (asked 2.8.4, got 2.11.4) and occasionally returns a build WITHOUT the plugin | Added deterministic xcaddy fallback build; keeps API fast-path |
| D4 | `07-backups.sh` | `ensure_sqlite3` called before its definition → "command not found", setup died | Function moved above the Main section |
| D5 | `setup.sh` | Post-module service-start list only started shadowsocks-* — tc-*-cap services (and sing-box-uot) were never started on fresh deploys → caps silently unenforced | Start list now includes tc-*-cap + sing-box-uot (when ENABLE_UOT=1) |
| D6 | `02-shadowsocks.sh` UoT | sing-box REJECTS `"network": "tcp_and_udp"` (shadowsocks-rust syntax) and rejects `"udp_over_tcp"` on the INBOUND (unknown field) | Both omitted — sing-box defaults to tcp+udp and handles UoT magic-domain connections automatically |
| D7 | `seed-pb.py` | `api()` never sent the Authorization header → admin token "verification" always failed, then old `/api/admins` endpoint (removed in PB 0.22) → bootstrap never created collections | Global TOKEN threaded through api(); PB 0.22 `_superusers` endpoints; saved-password re-auth fallback |
| D8 | `seed-live.py` | Same PB 0.22 admin migration needed (`/api/admins` → `_superusers`) | Same fix |
| D9 | seed schemas | `update_config` still declared `download_macos_*` fields | Removed (macOS dropped) |
| D10 | `scripts/generate_codes.sh` | `double=!$double` in bash assigns the literal string `!false` → EVERY generated code had a wrong Luhn checksum → all codes rejected as "Invalid code format" by the client AND the activation hook | Proper toggle `if $double; then double=false; else double=true; fi` |
| D11 | code generator docs | Instructions passed `/root/.admin_api_token` (app-level token) — PocketBase 0.22 rejects it (401). The generator needs the PB admin JWT | Docs + setup.sh hint now use `grep PB_TOKEN /root/.pb_admin_creds \| cut -d= -f2` |

**Deployment notes (streamlined process):**
- Update DNS BEFORE running setup.sh — `00-env.sh` hard-fails if the domain doesn't resolve.
- `scp -r v5/server age-key.txt root@VPS:/root/server/` — age-key.txt must sit alongside setup.sh.
- `ENABLE_UOT=1 DOMAIN=… ./setup.sh` installs the sing-box UoT endpoint (port 8446) + seeds `uot_port` via seed-pb.py.
- Caddy per-IP rate limits are tight for school NAT (activate 5/10min/IP, heartbeat 1/10s/IP, api 100/10s/IP) — fine for the current client cadence; revisit if multiple students share one egress IP.
- The old VPS's PocketBase DB (activation codes) remains in B2 (`vpsvpnbackup`, backups up to 2026-08-10 02:00 UTC) — decision 2026-08-14: fresh start, do NOT restore.

**Verified live:** 23/23 smoke checks; activation 200 with `uot_port:8446`; heartbeat 200 with config refresh; TLS 200; all 10 services active; backup timer hourly.

---

## STRIKE GAMING LATENCY — BUFFERBLOAT CONTROL (2026-08-14)

Gaming speed = latency + jitter, not Mbps (school RTT 51ms is already good).
The real server-side win was bufferbloat: the HTB tier classes used plain
FIFO queues, so when a tier ran at full bandwidth (e.g. someone downloading
at 200 Mbps), queuing added latency spikes to game traffic on the same tier.

Changes (applied live + in modules):

- `04-tc.sh` helper: **fq_codel leaf qdisc under every HTB class**
  (parent 1:10/1:20/1:30, handles 10:/20:/30:) — per-flow fair queuing +
  CoDel AQM keeps latency flat under load (the standard BBR pairing).
  UoT traffic (port 8446, default class 1:30) benefits too.
- `01-bbr.sh` 91-tcp-tune.conf: `tcp_slow_start_after_idle = 0` (games that
  alternate quiet/burst don't re-slow-start every round) +
  `tcp_mtu_probing = 1`.
- `04-tc.sh` module bugs fixed while doing this:
  - helper script is now ALWAYS rewritten (was skip-if-exists → a stale
    helper persisted on deployed VPSs and helper updates never applied)
  - filter flush moved to the module main (once, before applying all three
    tiers) — the per-call flush design would have left only the LAST tier's
    filter; repeated adds without any flush duplicate filters (both hit
    during the 2026-08-14 apply)

Verified live: 3 filters, fq_codel under all 3 classes, sysctls applied.

Expected effect: latency stays flat on Strike when the tier is saturated;
game RTT (already ~51ms school → Sydney) unchanged at low load.

---

## NEW VPS — VOYAGER-PROBLEM REGRESSION TEST (2026-08-14)

Tested 134.199.155.166 against every failure class that plagued the old
Voyager VPS. Results:

| Old-Voyager problem | Test | Result |
|---------------------|------|:------:|
| IP drift (domain pointed at dead IP, cached clients broke) | duckdns resolves to 134.199.155.166 from VPS + public resolvers | ✅ |
| tc caps evaporate on kernel update / reboot | **Full reboot** → all 10 services active, exactly 3 tc filters attached post-boot | ✅ |
| "Can't validate the SS leg on the VPS" (TUN captured own egress) | sing-box client on the VPS → :8445 → curl example.com → **HTTP 200 in 89ms** | ✅ |
| UoT RST'd by shadowsocks-rust (~300ms) | sing-box client `udp_over_tcp` → :8446 → DNS query to 1.1.1.1:53 → **reply returned (2 answers)** — first working UoT round-trip on this stack | ✅ |
| Backups silently stopped (old box's last backup 2026-08-10) | B2 shows 24 fresh `.db.gz` backups from Aug 13-14; timer active; checksum verified | ✅ |
| `source <(age -d …)` secrets silently failing | setup.sh decrypts via age-key.txt temp-file path; tier passwords match configs + tier_configs | ✅ |
| PB hooks broken → generic 400 on activation | Activation returns proper "Missing code" 400; heartbeat 200; health 200; TLS 200 | ✅ |
| "Empty tier_configs" red herring | tier_configs populated (eco/stealth/strike, strike has uot_port) | ✅ |
| B2 CLI syntax drift / restore-path bugs | b2 CLI 4.7.1 installed; backup upload + checksum verification working | ✅ |
| Admin-password drift after restore | PB admin JWT in /root/.pb_admin_creds valid (seeds + API calls succeed) | ✅ |

**Remaining (needs the user's environment):** real SCP:SL game session on
the school network (P1 in GAMING-UDP.md). Also note: school network shapes
downloads per-flow (~2.5 Mbps/flow; 25 Mbps aggregate with parallel flows,
113 Mbps up) — a last-mile policy, not a VPS defect.

---

## HISTORY ARCHIVE — CURATED ORIGINALS RESTORED (2026-08-14)

After the culling rounds 1–2 deleted `originals/` wholesale, review flagged
that the business model and N4L threat research were unique knowledge with no
living-doc equivalent. The still-relevant docs were restored from git history
(`72eec8a^:originals/`) into a clearly-labelled archive:

- `v5/docs/history/Business-Plan.md` — middleman distribution, market vs xVPN
- `v5/docs/history/attacker-perspective.md` — insider-threat / covert-telemetry analysis
- `v5/docs/history/defender-perspective.md` — defense-in-depth research
- `v5/docs/history/comprehensive-vpn-blocklist.md` — N4L/Palo Alto blocklist research
- `v5/docs/history/school-vpn-blocking-implementation.md` — N4L network-level blocking
- `v5/docs/history/Architectural-Plan.md` — original v3 Hysteria 2 plan (context)

**Not restored** (stays in git history only): `Actionable-Plan.md` (4,771-line
superseded action plan), `V2-ACTION-PLAN.md`, `V2-BOTTLENECK-REFERENCE.md`,
and the entire `v3/` + `simplified/` alternative doc sets.

`v5/docs/history/README.md` indexes everything and gives the `git show`
commands for anything still only in history.

---

## REPO CULLING ROUND 2 — STREAMLINED LAYOUT (2026-08-13)

Continuation of the 2026-08 cleanup (round 1 removed `v3/`, `simplified/`,
`originals/`, `v5/legacy/`). Round 2 consolidates duplicates and drops dead
platform support. All removed files are recoverable from git history.

| # | Change | Rationale |
|:-:|--------|-----------|
| C1 | `modular-vps/` deleted | Stale duplicate of `v5/server/` (untouched by commits since 2026-07-27). `v5/server/` is the one and only server deployment directory. Its `hiddify.pb.js` hook was moved into `v5/server/pb_hooks/`. |
| C2 | Root `CONTEXT.md` deleted | Superseded by `v5/CONTEXT.md` — one context document. |
| C3 | `v5/scripts/` deleted | Duplicate of root `scripts/` (which holds the newer hardened `generate_codes.sh`). Root `scripts/` is canonical; all docs updated to `./scripts/…`. |
| C4 | macOS (darwin) support dropped from client | Unsigned builds are unusable on macOS (no Apple signing/notarization). **REVERSED 2026-08-14** — macOS was ~50% of market, so the unsigned build is shipped with a documented Gatekeeper workaround (right-click → Open / `xattr -cr`). See "MACOS RE-ENABLED" below. |
| C5 | Fingerprint files made self-contained | `fingerprint.go` (shared) + `fingerprint_darwin.go` deleted; shared logic (hashSources, UUID fallback, ValidateFingerprint, caching) folded into `fingerprint_linux.go` and `fingerprint_windows.go`. `update_unix.go` → `update_linux.go` (linux-only, macOS gone). |
| C6 | `POCKETBASE-SETUP.md` leaked admin token | Five hardcoded `ADMIN_API_TOKEN` values replaced with `YOUR_ADMIN_TOKEN` placeholder. |

**Docs updated:** root `README.md`, `v5/README.md`, `v5/CONTEXT.md`,
`v5/docs/ARCHITECTURE.md`, `BACKEND-API.md`, `CLIENT-GUIDE.md`, `CI-CD.md`,
`API.md`, `OPS.md`, `DEPLOY.md`, `POCKETBASE-SETUP.md`, `IMPLEMENT.md`,
`WAILS-MIGRATION.md`, `.github/workflows/build.yml`, `v5/client/Makefile`.

---

## DEPLOY/RESTORE — PLUG-N-PLAY HARDENING (2026-08-01)

Validated a full blank-VPS deploy + B2 restore live (114.23.136.59). The deploy
path was already one-command; the restore path had gaps, all fixed:

| # | Area | Issue | Fix |
|:-:|------|-------|-----|
| R1 | `restore.sh` | Latest-backup detection used `b2 ls --long "$BUCKET" backups/` — plain bucket names fail on the current CLI ("Invalid B2 URI") | `b2 ls --recursive "b2://$BUCKET/backups/"` filtered to `.db.gz`, sorted |
| R2 | `restore.sh` | Download/checksum used deprecated `b2 download-file-by-name` / `b2 file-info` (exit 1 + error output on success paths) | Current syntax: `b2 file download b2://…` / `b2 file info b2://…` |
| R3 | `restore.sh` | Restored `data.db` over a fresh-seeded DB without removing `-wal`/`-shm` — stale WAL can be replayed against the restored file | `rm -f data.db-wal data.db-shm` before chown |
| R4 | `restore.sh` | Restored DB's admin password may predate the secrets file (old auto-generated era) → login fails with documented creds | Auto-run `./pocketbase admin update "$PB_ADMIN_EMAIL" "$PB_ADMIN_PASS"` (while PB stopped) |
| R5 | `restore.sh` | `pocketbase-backup.timer` has `Requires=pocketbase.service` — stopping PB (part of restore) **stops the timer too** (Requires propagates stops, not starts); it stayed inactive after restore | Explicit `systemctl restart pocketbase-backup.timer` + `is-active` verification after start |
| R6 | `restore.sh` | Codes-count check used the API with a nonexistent token file → always 0/unknown (collections are admin-only) | Direct `sqlite3 … "SELECT COUNT(*) FROM codes"` |
| R7 | `restore.sh` | No hook-load verification after restore | POST `/api/activate {}` must return 400 "Missing code" (generic 400 = hook load error) |
| R8 | `07-backups.sh` | `sqlite3` not installed → backup script silently used the direct-copy fallback (inconsistent backups under write load) | Module now installs `sqlite3` so `.backup` (safe) is always used |
| R9 | `07-backups.sh` | Timer enable/restart swallowed errors with `|| true` | Loud enable/restart + `is-active` verification with manual-fix instructions |

**Docs updated:** `DEPLOY.md` (post-deploy checklist now includes backup timer +
backup log + update.json + the Requires= gotcha; disaster-recovery section
describes the full one-command flow), `OPS.md` (restore runbook rewritten with
the verified manual steps + current b2 syntax + admin password alignment +
timer restart), `v5/CONTEXT.md` (agent rules 7–9: timer gotcha, admin password
alignment, b2 URI syntax).

**Verdict:** a blank Ubuntu 22.04 VPS + repo + `age-key.txt` is fully plug-n-play:
`setup.sh` for fresh deploy (auto-runs the smoke test), `restore.sh` for
migration/disaster recovery — both validated live 2026-08-01.

---

## STEALTH TIER — TCP BRUTAL REMOVED, BACK TO BBR (2026-08-01)

**Decision:** Stealth no longer uses the `tcp-brutal` kernel module + LD_PRELOAD
wrapper. The aggressive rate-based CC filled buffers and caused bufferbloat/jitter
on the school network when it throttled, and the Linux `tcp-brutal` module is not
as robust or powerful as Hysteria 2's built-in Brutal CC. Stealth now runs plain
**BBR** like the other tiers, with a **tc HTB cap of 100 Mbps** (class `1:20`,
port 8444) replacing the old 48 Mbps Brutal target rate.

**Changes:**
- `v5/server/modules/03-brutal.sh` — **deleted** (kernel module build/load,
  DKMS registration, `brutal-wrap.so` LD_PRELOAD wrapper, 48 Mbps target).
- `v5/server/modules/02-shadowsocks.sh` — removed `write_brutal_dropin()`
  (the `LD_PRELOAD=/usr/local/lib/brutal-wrap.so` drop-in for the Stealth
  systemd service); Stealth service description is now "BBR, 100 Mbps tc cap".
- `v5/server/modules/04-tc.sh` — Stealth now gets a tc class `1:20` @ `100mbit`
  and a `tc-stealth-cap.service` oneshot (alongside Eco `1:10` and Strike `1:30`).
- `v5/server/setup.sh` — `03-brutal.sh` removed from the module chain; summary
  line updated to "Stealth port: 8444 (BBR, 100 Mbps tc)".
- `v5/server/scripts/smoke-test.sh` — Brutal module check replaced with a
  Stealth tc class (`1:20`) check; tc verification now covers 8443/8444/8445.
- Docs updated: root `README.md`, `v5/CONTEXT.md`, `v5/docs/ARCHITECTURE.md`,
  `v5/docs/DEPLOY.md`, `v5/docs/OPS.md` (tier tables, diagrams, deploy
  checklist, ops checks, kernel-update guidance).
- Historical references in `v3/`, `v4/`, `simplified/`, `originals/`,
  `modular-vps/` and root `CONTEXT.md` are left untouched (reference-only).

**Ops note:** no kernel modules are maintained anymore. After a kernel update,
re-apply the caps with `systemctl restart tc-eco-cap tc-stealth-cap tc-strike-cap`.
The old Brutal entries below this one are historical records of the earlier design.

---

## CI LINT — ERRCHECK FIXES (Wails Migration)

Found when the Wails CI pipeline first ran `golangci-lint` on the client module.
All fixes are behavior-neutral (`_ =` acknowledges best-effort/cleanup errors).

| # | File | Line | Fix |
|:-:|------|:----:|-----|
| L1 | `v5/client/internal/storage/storage.go` | 159 | `f.Sync()` → `_ = f.Sync()` (best-effort fsync) |
| L2 | `v5/client/internal/updater/updater.go` | 165 | `u.restoreBackup(...)` → `_ = ...` (cleanup after swap failure; original error already returned) |
| L3 | `v5/client/internal/updater/updater.go` | 364 | `os.Chmod(...)` → `_ = ...` (post-revert permission set) |
| L4 | `v5/client/internal/manager/process.go` | 261 | `m.cmd.Wait()` → `_ = ...` (graceful shutdown goroutine) |
| L5 | `v5/client/internal/manager/process.go` | 409 | `cmd.Wait()` → `_ = ...` (startup probe after immediate exit) |
| L6 | `v5/client/internal/tunnel/tunnel.go` | 140 | `iptables -F OUTPUT` → `_ = ...Run()` (best-effort rule removal) |
| L7 | `v5/client/internal/tunnel/tunnel.go` | 163 | `pfctl -F all` → `_ = ...Run()` (best-effort) |
| L8 | `v5/client/internal/tunnel/tunnel.go` | 164 | `pfctl -d` → `_ = ...Run()` (best-effort) |
| L9 | `v5/client/internal/tunnel/tunnel.go` | 257 | `t.Stop()` → `_ = t.Stop()` (rollback on TUN setup failure) |
| L10 | `v5/client/internal/tunnel/tunnel.go` | 294 | `t.Stop()` → `_ = t.Stop()` (rollback on TUN setup failure) |

Also fixed siblings not flagged by the linter: `f.Close()` in the same storage block,
and the `linuxTUN.Stop()` best-effort cleanup loop.

---

## CI LINT — REMAINING ERRCHECK + UNUSED FIXES (2026-07-31)

`golangci-lint run ./...` (v2.12.2, default linters — the CI `lint` job) still
failed after the L1–L10 batch. 26 more issues fixed, all behavior-neutral
(`_ =` acknowledgements; two genuinely dead functions removed):

| # | File | Fix |
|:-:|------|-----|
| L11 | `v5/client/app.go` | `a.mgr.Stop()` → `_ = ...` (disconnect) |
| L12 | `v5/client/app.go` | `a.store.SetHeartbeat(...)` → `_ = ...` (heartbeat callback) |
| L13 | `v5/client/app.go` | `a.store.SetHeartbeatFailure(...)` → `_ = ...` (heartbeat callback) |
| L14 | `v5/client/internal/activation/activation.go` | `defer resp.Body.Close()` → `defer func() { _ = ... }()` |
| L15 | `v5/client/internal/heartbeat/heartbeat.go` | `defer resp.Body.Close()` → closure |
| L16 | `v5/client/internal/heartbeat/heartbeat.go` | **Removed dead `getInterval()`** (unused) |
| L17 | `v5/client/internal/manager/process.go` | `defer conn.Close()` → closure (helper IPC client) |
| L18 | `v5/client/internal/manager/process.go` | `os.Remove(configPath)` → `_ = ...` (stop cleanup) |
| L19 | `v5/client/internal/storage/storage.go` | `os.Remove(tmpPath)` → `_ = ...` (save failure cleanup) |
| L20 | `v5/client/internal/storage/storage.go` | `os.Remove(oldest)` → `_ = ...` (backup rotation) |
| L21 | `v5/client/internal/tunnel/tunnel.go` | `netsh ... delete rule` → `_ = ...Run()` (Windows kill switch off) |
| L22 | `v5/client/internal/tunnel/tunnel.go` | `networksetup ... Ethernet` → `_ = ...Run()` (macOS DNS fallback) |
| L23 | `v5/client/internal/tunnel/tunnel.go` | `ifconfig down` → `_ = ...Run()` (darwinTUN.Stop) |
| L24 | `v5/client/internal/updater/updater.go` | 5× `defer {Body,f}.Close()` → closures (download/checksum/copy) |
| L25 | `v5/client/internal/updater/updater.go` | 13× `os.Remove(...)` → `_ = ...` (cleanup paths in PerformUpdate/downloadBinary/CheckOnStartup) |
| L26 | `v5/client/internal/updater/updater.go` | **Removed dead `cleanupSentinelFiles()`** (unused) |
| L27 | `v5/client/internal/updater/recover.go` | 6× `os.Remove(...)` → `_ = ...` (sentinel/marker cleanup) |
| L28 | `v5/client/internal/updater/update_windows.go` | 2× `os.Remove(oldPath)` → `_ = ...` (.old cleanup in swapWindows) |

Also ran `gofmt -w` across the module (pre-existing alignment drift in const/var/struct
blocks — cosmetic only) and synced `frontend/package.json` to the committed
`package-lock.json` (`vite ^6.4.3` → `^5.4.21`) so CI `npm install` is
deterministic. Verified green locally: `golangci-lint run ./...` (0 issues),
`go vet ./...`, `go build -tags frontend` (linux/windows/darwin amd64+arm64),
`go test ./...`, and a clean `npm ci && npm run build`.

---

## CI/RUNTIME — MISSING WAILS BUILD TAGS + BROKEN JS BRIDGE (2026-07-31)

**Symptom:** Windows binary built by CI showed the Wails error dialog
*"Wails applications will not build without the correct build tags"* and
opened https://wails.io/docs/guides/manual-builds/.

**Root cause 1 — build tags:** Wails v2 selects its app implementation with
build tags (`internal/app/app_default_*.go` is `//go:build !dev && !production
&& !bindings`). The CI workflow compiled with only `-tags frontend` (which just
selects the embedded asset FS), so the *stub* implementation shipped: a binary
that shows the error dialog. `wails build` adds `desktop,production` itself;
raw `go build` must pass them explicitly.

**Fix:** `.github/workflows/build.yml` now uses `-tags "frontend desktop production"`
in the lint job (`go vet`, compile check) and the 4-platform build matrix.

**Root cause 2 — broken frontend bridge:** `frontend/src/lib/bridge.ts` called
`window.runtime.Call('GetVersion', …)`, but Wails v2.9's runtime does NOT expose
`Call` on `window.runtime` (only Log/Window/Events/etc.), and method names must
be qualified (`main.App.GetVersion`) per the binding DB. Every UI action would
have thrown after the tags were fixed.

**Fix:** `bridge.ts` now calls `window.go.main.App.<Method>(…)` (the bindings map
the backend injects at startup — the generated `wailsjs/` files are only optional
IDE helpers) and keeps `window.runtime.EventsOn/EventsOff` for events.

**Verified:** Windows binary built with the real tags embeds
`-tags=frontend,desktop,production` (per `go version -m`) and no longer contains
the stub error string. Frontend rebuilds cleanly. Linux desktop build requires
WebKitGTK headers locally (CI installs `libgtk-3-dev libwebkit2gtk-4.1-dev` —
includes `pkg-config`); macOS builds require CGO on a macOS runner (matrix
already sets `cgo: "1"` for macOS).

### Follow-up (2026-07-31) — Linux lint failure: missing `webkit2_41` tag

The first green push still failed the `Lint & Vet` job on ubuntu-latest. Cause:
Wails' Linux desktop cgo code selects the WebKitGTK version via a build tag —

```c
#cgo !webkit2_41 pkg-config: webkit2gtk-4.0
#cgo webkit2_41 pkg-config: webkit2gtk-4.1
```

ubuntu-latest (24.04) ships only WebKitGTK **4.1** (`libwebkit2gtk-4.1-dev`),
so the tag-less build looked for `webkit2gtk-4.0` and failed at the pkg-config
step. The `wails` CLI does not auto-add this tag in v2.9.1 — it must be passed
manually. **Fix:** `.github/workflows/build.yml` adds `webkit2_41` to the lint
job (`go vet` / compile check) and the Linux matrix entry; macOS/Windows keep
`frontend desktop production` (the tag is linux-only). Docs and `main.go`
comments updated to mention `-tags "frontend desktop production webkit2_41"`
for Ubuntu 24.04+ builds.

---

## WINDOWS RUNTIME — BLANK POWERSHELL FLASH + INVISIBLE APP (2026-07-31)

**Symptom:** launching the Windows exe popped a blank PowerShell window, then
"nothing happened".

**Root cause 1 — PowerShell console flash:** the device fingerprint collector
(`internal/activation/fingerprint_windows.go`) spawns `powershell.exe` 3×
(Get-NetAdapter, Win32_DiskDrive, Win32_ComputerSystemProduct). A GUI-subsystem
parent spawning console-subsystem children gets a **visible console window per
child** on Windows.

**Fix:** all PowerShell spawns now run via `runHidden()` with
`syscall.SysProcAttr{HideWindow: true}`.

**Root cause 2 — invisible app:** `main.go` set `StartHidden: true`, but Wails
v2.9.1 has **no system tray API** (verified: no `SystemTray` in `pkg/runtime` /
`pkg/options`) and this app never creates a tray icon — the `tray:show` /
`tray:quit` listeners in `setupSystemTray` are dormant hooks nothing emits.
Result: the app ran completely invisible ("nothing happened"). Wails v2.9 also
has no close-to-hide interception, so closing the window quits.

**Fix:** removed `StartHidden` (window is shown on launch); documented the
dormant tray hooks and close-quits behaviour in code comments and docs.

**Also fixed:** `manager/process_windows.go` `newProcAttr()` now sets
`HideWindow: true` so sing-box (console subsystem) doesn't flash a console when
the user connects. Docs (`ARCHITECTURE.md`, `CLIENT-GUIDE.md`,
`UI-AESTHETICS.md`) updated to match.

---

## MACOS BUILD — UNDEFINED `_OBJC_CLASS_$_UTType` (2026-07-31)

**Symptom:** both macOS matrix jobs (`Build macOS-amd64`, `Build macOS-arm64`)
failed at the "Build client app" step with a final-link error:

```
Undefined symbols for architecture arm64: "_OBJC_CLASS_$_UTType"
```

**Root cause:** Wails v2.9.1's darwin frontend uses `UTType` for file dialogs
(`WailsContext.m:575,659`, `UTType typeWithFilenameExtension:`) but its cgo
LDFLAGS only link `Foundation`, `Cocoa`, `WebKit`. On older SDKs the
`UniformTypeIdentifiers` framework was re-exported transitively; the
Xcode 26 / macOS 26 SDK on `macos-latest` removed that implicit linkage.

**Fix:** `v5/client/darwin_link.go` (`//go:build darwin`) adds the missing
framework to the final link:

```go
#cgo LDFLAGS: -framework UniformTypeIdentifiers
```

cgo flags from the main package are included in the final link step, so this
covers both `wails build` and manual `go build` on amd64/arm64. Non-darwin
builds are unaffected (build tag). Upstream Wails v2.10+ fixes this properly
in the darwin package itself.

---

## WINDOWS RUNTIME — BLACK WINDOW FLASHES THEN APP DIES (2026-07-31)

**Symptom:** the window (black) flashes for ~1 second then closes; no process
remains in Task Manager. Wails runs `OnStartup` in a goroutine with no
recovery, and `wailsruntime.LogFatal` calls `os.Exit(1)` — for a GUI build
(no console) any startup failure or panic dies **completely silently**.

**Likely trigger:** a corrupt `storage.json` (e.g. left by the earlier
invisible/crashed sessions) → `storage.New` failed → `LogFatal` → `os.Exit(1)`
about one second after launch (the visible gap = the hidden PowerShell
fingerprint calls). The black window is the WebView mid-load when the process
dies.

**Fixes (make failures impossible to hide):**
1. `internal/storage/storage.go` — `New()` is now **self-healing**: an
   unreadable/corrupt `storage.json` is moved aside
   (`storage.json.corrupt-<unix-ts>`) and a fresh store is created; config-dir
   failures fall back to the OS temp dir. A bad JSON file can no longer brick
   startup.
2. `main.go` — `os.Stderr` and the standard logger are redirected to
   `%APPDATA%\locus\myvpn.log` (rotated at 1MB). Panics, `log.Fatal` and
   `log.Printf` output are now captured on GUI builds with no console.
3. `app.go` — `Startup` wraps its body in `recover()` (logs the panic to
   `myvpn.log` and keeps the window alive) and the storage failure path uses
   `LogError` instead of `LogFatal` (no more `os.Exit(1)`).
4. `main.go` — `wails.Run` errors still exit, but the message lands in
   `myvpn.log` instead of a null console.

**Diagnosis path for future Windows issues:** run the exe, then read
`%APPDATA%\locus\myvpn.log` — any panic stack or startup error will be there.

### Follow-up (2026-07-31) — exit-path instrumentation

With the log in place, a fresh CI build (run 42 — all jobs green incl. macOS)
still flashed and died with ONLY `MyVPN starting (version main)` in the log —
no panic, no `wails.Run` error. Since go-webview2 `log.Fatalf`s on WebView2
env/controller failure (which would have been logged), WebView2 init succeeded;
the death is either a native crash, a browser-process failure, or an external
kill (school-managed machines: AV/AppLocker). Added stage markers to the log:

- `DOM ready — webview loaded the UI` (new `OnDomReady` hook in `main.go`)
- `Startup complete (activated=...)` (end of `App.Startup`)
- `Shutting down MyVPN...` (`App.Shutdown` — present iff the app exited via
  the normal window-close path)

Combined with the Windows Event Viewer (Application log → "Application Error"
for `myvpn.exe`, showing the faulting module), the next run identifies the
exact dying stage.

### Follow-up 2 (2026-07-31) — WAILS v2.9.1 → v2.12.0 (go-webview2 crash fixes)

The instrumented log showed `MyVPN starting` → `DOM ready — webview loaded the
UI` and then **silent death**, with `Startup complete` and `Shutting down`
never logged — i.e. the process died right when the Vue app started calling
bound Go methods over the WebView2 JS↔Go IPC. Wails v2.9.1 bundles
`go-webview2 v1.0.10` (2023-10), which predates the upstream crash fixes:

| go-webview2 | Fix |
|-------------|-----|
| v1.0.12 | infinite recursion fix |
| v1.0.13 | overlapped I/O error on long JS scripts |
| v1.0.16 | **panic when sending long data from JS to Go** |
| v1.0.19 | COM error handling |
| v1.0.20/21 | **random crashes** |

**Fix:** upgraded `github.com/wailsapp/wails/v2` **v2.9.1 → v2.12.0** (needs
only Go 1.22, so CI is unchanged) which bundles **go-webview2 v1.0.22** with
all of the above. Verified: Windows build (real tags), golangci-lint 0 issues,
`go vet`, `go test` all green. The darwin `UTType` shim and the linux
`webkit2_41` tag remain required in v2.12.0 (confirmed in its source).

---

## SERVER-SIDE — STALE/EMPTY tier_configs REJECTS CLIENTS (2026-07-31)

**Symptom:** with the app running (as admin), Connect worked but the tunnel
died instantly: `inbound/tun[tun-in]: ... wsasend: An existing connection was
forcibly closed by the remote host` (both upload and download) to
`114.23.136.59:8445`. TCP connects fine (all ss ports open) — the ssserver
**rejects the Shadowsocks handshake**: wrong password/method.

**Investigation (verified from outside):**
- DNS: `networkingguides.duckdns.org` → **114.23.136.59** (older docs say
  .47 — the VPS IP changed; docs updated).
- All three tier passwords from `secrets.env.age` were tested against the live
  ss servers with a real sing-box client — **all three authenticate**
  (HTTP 200 through the tunnel).
- **Correction (with SSH access, the story simplified):** the live
  `tier_configs` collection was **correct all along** — all three records
  match `/etc/shadowsocks/*.json` exactly, one record per tier. The earlier
  "empty collection" conclusion was wrong: the collection's API rules are
  admin-only (`@request.auth.admin = true`), so unauthenticated list/create
  requests returned misleading empty/generic-400 responses.
- **The real problem is the CLIENT's stale stored config**: the device was
  activated on the PREVIOUS PocketBase instance (before the Jul 28 re-setup
  replaced the data dir and rotated the ss passwords). The current client
  never refreshes stored connection parameters:
  - the heartbeat hook returns `server_config`, but the client ignored it,
  - re-activation short-circuits client-side ("Already activated" returns
    before any server call),
  - so the app kept connecting with an old password → ssserver RST.

**Fixes (repo + deployed to VPS):**
- `v5/server/pb_hooks/activation.pb.js` + `heartbeat.pb.js` — always pick the
  **newest** `tier_configs` record; the "Already activated" response now
  includes `server_config`. NOTE: `{:param}` binding is unreliable in
  `findRecordsByFilter` on PB 0.22 — filters use sanitized inline values.
- `v5/server/scripts/seed-pb.py` — tier seeding is an **idempotent upsert**
  (delete older duplicates, PATCH existing or POST new).
- `v5/server/scripts/fix-tier-configs.py` — repair script for the VPS
  (ground-truth passwords from `/etc/shadowsocks/*.json`).
- Client (`v5/client/app.go`): heartbeat responses now **apply
  `server_config`** (self-heal within one heartbeat once a client with this
  fix runs).
- Client (`v5/client/internal/manager/process.go`): sing-box stderr captured
  and surfaced in Connect errors ("TUN interface creation was denied — run
  Locus as administrator: ... Access is denied.").

**Live-VPS actions performed (2026-07-31, authorized):** deployed both hooks
to `/opt/pocketbase/pb_hooks/` (+ `/root/server/pb_hooks/`), restarted
PocketBase, verified the heartbeat route serves the correct `server_config`
(end-to-end test with a real code: eco → `networkingguides.duckdns.org:8443`
+ matching password). Client unblock: run the new build (heartbeat refresh)
or delete `%APPDATA%\locus\storage.json` and re-enter the activation code.

---

## WINDOWS RUNTIME — CONNECT SPINS FOREVER (Process.Signal(0) BROKEN) (2026-08-01)

**Symptom:** after the server fix, Connect starts sing-box (correct password,
TUN created) but the button spins for minutes with no new log lines — while
the tunnel actually runs in the background.

**Root cause:** the manager checked process liveness with
`cmd.Process.Signal(syscall.Signal(0))` everywhere (startup probe, IsRunning,
State, healthLoop, already-running check). On **Windows**, `Process.Signal`
only supports `Kill` (TerminateProcess) — any other signal returns
`syscall.EWINDOWS` ("not supported by windows") — verified in Go's
`src/os/exec_windows.go`. So every check reported "dead":
- the 500 ms startup probe concluded sing-box exited, called `cmd.Wait()`,
  which **blocks while sing-box is alive** → `Connect()` never returns → the
  UI spinner runs forever;
- closing the app then spawned a second `cmd.Wait()` in `stopLocked`, which
  errors immediately ("Wait was already called"), skipping the kill → an
  **orphaned sing-box.exe** kept running.

**Fix (`v5/client/internal/manager/process.go`):** replaced ALL signal-based
liveness checks with a cross-platform **exited channel**: a goroutine owns
`cmd.Wait()` and `close(exited)` when the process exits; `processAlive()`
selects on that channel. Applied to the startup probe (select vs 500 ms
timer), IsRunning, State, healthLoop (incl. the restart path — each restarted
process gets a fresh channel), stopLocked (waits on the existing channel; no
second Wait), and Start's already-running check.

**Tests added (`internal/manager/process_test.go`):** `TestLifecycle` (start →
running → auto-exit detected → crashed → stop → stopped) and
`TestImmediateExit` (probe surfaces sing-box stderr, e.g. "Access is denied").
Both pass. Windows build, golangci-lint, vet all green.

**Note for users of the previous build:** after closing the hung app, kill any
leftover `sing-box.exe` in Task Manager before running the new build.

---

## WINDOWS RUNTIME — CONNECTS BUT NO TRAFFIC (dial i/o timeout) (2026-08-01)

**Symptom:** with the fixed build, the UI reports Connected, but
whatismyip.com still shows the real IP and sing-box logs repeated
`inbound/tun[tun-in]: dial tcp 114.23.136.59:8445: i/o timeout` (5 s dial
timeouts every ~60-130 s). The heartbeat (direct, not via TUN) still works.

**Server verified healthy from an outside host:** all three ss ports open;
strike password authenticates (HTTP 200 through the tunnel in ~60 ms).
So the server is NOT the problem.

**Most likely cause — stale host state, not config:** the same sing-box config
reached the server in an earlier session (RST after connect = connection
established). The sessions in between (old builds with the broken shutdown)
**orphaned sing-box.exe processes and left the `locus0` TUN adapter with
stale routes/WFP filters**; a fresh sing-box then routes its own dial into the
dead TUN state → timeout. (The config already sets
`route.auto_detect_interface: true`, which binds the outbound to the physical
interface — so a clean host should not loop.)

**Diagnostics added (`app.go`):** the report now includes
`Server: <addr> reachable|UNREACHABLE (...)` — a 3 s TCP dial to the configured
server from the app (before/without the tunnel). This distinguishes:
- `UNREACHABLE` ⇒ the network blocks the ss port (e.g. different WiFi);
- `reachable` while the tunnel still times out ⇒ stale TUN/routes on the host
  (cleanup below).

**User-side cleanup (one-time, after the old builds):**
1. Close Locus; in Task Manager end ALL `sing-box.exe` processes.
2. As admin: `netsh interface show interface` → find `locus0` →
   `netsh interface delete interface locus0` (if present).
3. Reboot (clears routes + WFP filters), then Connect again.
4. Check Diagnostics: `Server: … reachable` + `Engine: running`.

### Follow-up — strict_route disabled (2026-08-01)

New diagnostics (VPN ON) showed the app's OWN dial failing at
`lookup networkingguides.duckdns.org: i/o timeout` — i.e. with the TUN up,
**DNS and everything else through the tunnel dies**, while direct Shadowsocks
(Hiddify, no TUN) works and the server is verified healthy. The prime suspect
was `strict_route: true` in the generated sing-box config: on Windows it
installs WFP filters that "strictly block all connections not from the TUN" —
a misfiring filter also blocks sing-box's own outbound and DNS, which matches
"connects but cuts out the internet" exactly.

**Fix:** `generateConfig` now sets `strict_route: false` (omitted from JSON;
`auto_route` + `auto_detect_interface` remain — still a full tunnel). If the
tunnel still fails after the cleanup+reboot, set `LOCUS_DEBUG=1` before
launching (switches sing-box to debug logging) and send the log — it shows the
dial's interface binding and route decisions.

### Follow-up 2 — DNS loop: final must be dns-tunnel (2026-08-01)

After disabling strict_route the tunnel egressed (ping 1.1.1.1 worked) but
**domains never resolved**. Root cause: `generateConfig` set
`dns.final = "dns-direct"`, whose server had `detour: "direct"` — the direct
outbound's DoH traffic is routed back into the TUN by auto_route → **DNS loop**
("dial tcp: lookup …: i/o timeout" while the tunnel itself was fine). This
also explains "the app has never worked": the proxy outbound was fine all
along; DNS was always looping.

**Fix:** `dns.final` is now `"dns-tunnel"` (DoH via the default proxy outbound
→ through the tunnel); `dns-direct` is kept but unused. Verified: Windows
build, lint, vet, manager tests green; docs (ARCHITECTURE.md, CLIENT-GUIDE.md)
updated to match.

### Follow-up 3 — DNS query loopback: explicit detour required (2026-08-01)

With `final: dns-tunnel` (no detour), sing-box flooded
`DNS query loopback in transport[dns-tunnel]` for every query. Verified in
`sing-box v1.10.0 route/router.go`: a DNS server with an **empty detour**
dials via `dialer.NewRouter(router)` — the transport's own connection is
re-routed by the route rules back into the DNS handler, and sing-dns's
context-based loop detection fires (`sing-dns client.go`).

**Fix:** `dns-tunnel` now has an explicit **`detour: "proxy"`** — the DoH dial
goes straight through the Shadowsocks outbound (`dialer.NewDetour`), bypassing
the router entirely. `TestGeneratedConfig` asserts the detour; all tests,
Windows build, lint, vet green.

### Follow-up 4 — THE missing piece: `sniff: true` on the TUN inbound (2026-08-01)

Verified empirically on the VPS (root, real TUN): without sniffing, DNS
queries were routed to the **shadowsocks outbound** (`outbound connection to
10.0.0.2:53` via proxy) — the `{"protocol": "dns", "outbound": "dns-out"}`
rule NEVER matched. Source: `route/router.go:856` gates ALL sniffing (which
sets `metadata.Protocol` for rule matching) behind
`metadata.InboundOptions.SniffEnabled` — i.e. the TUN inbound's `"sniff": true`
option. Every official sing-box TUN example includes it; our config never did
— this single omission explains every DNS failure variant (direct detour loop,
transport loopback, and the plain "no DNS at all" behavior).

With `"sniff": true` the VPS test confirmed the full interception pipeline:
`sniﬀed packet protocol: dns` → `match protocol=dns => dns-out` →
`dns: exchange example.com`. (The ss leg could not be validated on the VPS
itself — a TUN client on the ss server host captures the server's own egress —
but the tunnel was already proven working from remote hosts.)

**Fix:** TUN inbound now sets `"sniff": true`. `TestGeneratedConfig` asserts
it. Windows build, lint, vet, tests green; docs updated.

### Follow-up 5 — server-domain resolution loop (sing-box issue #2207) (2026-08-01)

Even with sniff + detour, every query failed instantly with
`DNS query loopback in transport[dns-tunnel]`. Root cause (confirmed via
sing-box issue #2207, closed as fixed upstream but still present in v1.10):
the Shadowsocks outbound's server is the **domain**
`networkingguides.duckdns.org` — when the DNS transport dials the DoH via the
proxy, the proxy must resolve that domain, and the resolution re-enters the
DNS system → the transport's own context (tagged `dns-tunnel`) hits the loop
detection in sing-dns.

**Fix (the reporter-confirmed workaround):** a DNS rule sending ALL
sing-box-initiated (outbound) resolution DIRECT:
`{"outbound": ["any"], "server": "dns-direct"}`. Plus a route rule excluding
the resolved VPN-server IP from the tunnel (`ip_cidr: <server>/32` → direct;
resolved at config generation, before the TUN exists) so a captured ss
connection egresses physically instead of looping.

**VPS validation (root, real TUN, exact config):** `sniffed protocol: dns` →
`match => dns-out` → exchange; DoH routed via the ss outbound; the server
domain resolved DIRECT (`lookup succeed: 114.23.136.59`, NOERROR) — **zero
loopback errors**. (The ss leg itself can't be validated on the VPS — the TUN
on the ss-server host captures the server's own egress — but the tunnel is
proven working from remote hosts.)

### Follow-up 6 — bind_interface: force the ss dial onto the physical NIC (2026-08-01)

With the DNS pipeline fixed, every exchange still timed out at 10 s — the DoH
through the tunnel never completes on the user's Windows machine, while the
same path works from remote hosts in ~350 ms (verified: DoH query through the
tunnel returns HTTP 200 + a valid DNS response) and Hiddify (no TUN) works on
the same machine. Conclusion: with the TUN up, sing-box's OWN Shadowsocks
dial is still being captured by auto_route on Windows — `auto_detect_interface`
is not sufficient there.

**Fix:** the proxy outbound now gets an explicit **`bind_interface`** — the
physical NIC is detected at Connect time, BEFORE the TUN exists
(`interface_windows.go`: hidden PowerShell `Get-NetRoute` for the 0.0.0.0/0
alias; `interface_unix.go`: `ip route show default`). A socket bound to the
physical interface cannot be captured by the TUN, so the ss connection always
egresses directly. `app.go` Connect passes the detected interface into
`manager.Config.BindInterface`; `generateConfig` emits
`"bind_interface"` on the shadowsocks outbound.

### Follow-up 7 — sing-box 1.10.0 → 1.12.1: THE fix (2026-08-01)

Despite every config safeguard (sniff, DNS detour, outbound:any rule,
server-IP rule, bind_interface), the ss connection STILL timed out with the
TUN up — on both the user's Windows machine AND the Linux VPS. Meanwhile
**Hiddify (also sing-box + TUN + Shadowsocks) worked on the same machine** —
its core runs a much newer sing-box.

**Validation on the VPS with sing-box 1.12.1** (root, real TUN, the 1.12
config format): `curl https://example.com` through the tunnel returned
**HTTP 200 in 84 ms** — the first end-to-end success ever, even in the
self-host scenario.

**Changes:**
- Engine bumped **1.10.0 → 1.12.1** (`.github/workflows/build.yml`,
  `engines/README.md`). 1.11 added "Improve tun compatibility" (3 fixes);
  1.12 refactored the DNS servers and route rules.
- Config rewritten for the 1.12 format: DNS servers use
  `{"type": "https", "server": "1.1.1.1", "server_port": 443, "detour": ...}`
  (no more `"address"`), the `dns-out` special outbound is gone (replaced by
  the route rule action `{"protocol": "dns", "action": {"type": "dns"}}`), and
  the server-IP exclusion uses `{"action": {"type": "route", "outbound":
  "direct"}}`.
- `TestGeneratedConfig` updated and passing; Windows build, lint, vet green.

### Follow-up 8 — REMOVED bind_interface: align with Hiddify exactly (2026-08-01)

The 1.12 config parsed and ran, but the ss dial STILL timed out with
`bind_interface: "Wi-Fi"`. Evidence review: the ONLY session where the ss dial
reached the server (23:35) had NO bind; Hiddify (works on the same machine)
uses NO bind — only `auto_detect_interface`; and sing-box's Windows interface
name resolution for `bind_interface` is unverified (an unresolvable name
silently breaks every dial — the exact observed symptom).

**Fix:** `bind_interface` is REMOVED from both outbounds. The direct outbound
is kept non-empty via `"connect_timeout": "10s"` (1.12 rejects DNS detours to
empty direct outbounds). The generated config now matches the Hiddify pattern:
no binds, `auto_detect_interface`, `default_domain_resolver`, `hijack-dns`
action, sniff, DoH through the tunnel. Verified: parses and runs on the VPS
(process alive, TUN up, zero FATAL), tests pass, Windows build + lint green.

### Follow-up (2026-07-31) — macOS link failure: missing `UniformTypeIdentifiers` framework

Linux/Windows builds then passed, but both macOS matrix jobs failed at the
final link with:

```
Undefined symbols for architecture arm64:
  "_OBJC_CLASS_$_UTType"
ld: symbol(s) not found for architecture arm64
```

Wails v2.9.1's darwin code (`WailsContext.m`) uses `UTType` but only links
`-framework Foundation -framework Cocoa -framework WebKit`. On older SDKs,
UniformTypeIdentifiers was re-exported transitively and the symbol resolved
implicitly; **Xcode 26 / macOS 26 SDK removed that implicit linkage**, so the
link broke. (Checked: v2.13.0 does not add the framework either — and it
requires Go 1.25, so bumping Wails was not an option on Go 1.22.)

**Fix:** added `v5/client/darwin_link.go` — a `//go:build darwin` cgo shim in
the main package with `#cgo LDFLAGS: -framework UniformTypeIdentifiers`. cgo
flags from the main package are passed to the final link, so this fixes both
macOS architectures and both `wails build` and manual `go build` paths. No
workflow change needed.

---

### Follow-up 9 — REMOVED udp_over_tcp: sing-box's UDP-over-TCP is proprietary (2026-08-01)

**Symptom:** client says Connected and DNS resolves, but pages hang ("nothing
loads properly"). The log shows successful DNS exchanges and DoH through the
tunnel (`outbound/shadowsocks[proxy]: outbound connection to 1.1.1.1:443`),
but every UDP/QUIC flow dies:

```
outbound/shadowsocks[proxy]: outbound UoT packet connection to 142.251.12.84:443
connection: packet download closed: read tcp ...->114.23.136.59:8445:
  wsarecv: An existing connection was forcibly closed by the remote host.
```

(Repeated ~300ms apart — Chrome/Edge QUIC retry storm. Hiddify mobile VPN
mode and Hiddify desktop System Proxy work with the same server; Hiddify
desktop VPN mode fails the same way.)

**Root cause:** `generateConfig` emitted
`"udp_over_tcp": { "enabled": true, "version": 2 }` for tiers with
`udp_relay` (Strike). But sing-box's UDP-over-TCP is a **proprietary SagerNet
protocol** — magic domains `sp.udp-over-tcp.arpa` (v1) / `sp.v2.udp-over-tcp.arpa`
(v2) — **not** the Shadowsocks SIP003 UDP-over-TCP. The deployed server is
shadowsocks-rust, which does not implement it and RSTs every UoT connection.
The TCP data path was fine all along; the failure was dead UDP plus browser
QUIC retry loops. (This was also observed earlier the same day in
`seed-live.py`'s notes: "every UoT conn RST after ~300ms".)

**Fix:**
- `v5/client/internal/manager/process.go` — `udp_over_tcp` is no longer
  emitted for any tier. UDP goes **raw** (standard ss UDP; Strike server runs
  `tcp_and_udp`), works wherever the network allows UDP, and browsers fall
  back to TCP where it doesn't (N4L school WiFi). `UDPOverTCPConfig` type and
  `Outbound.UDPOverTCP` field removed; `Config.UDPRelay` is now informational
  only.
- `v5/server/scripts/seed-pb.py`, `fix-tier-configs.py` — strike seeds
  `udp_relay: false` (it historically flipped on client UoT). `seed-live.py`
  already had this.
- **Live DB (pending):** tier_configs → strike must have `udp_relay: false`.
  Clients self-heal via the heartbeat server_config refresh within one beat
  (no rebuild needed for existing installs).
- Revisit UoT only if a **sing-box server** is deployed for a tier.

---

### Follow-up 10 — Pre-flight guard: refuse a second sing-box on the same TUN (2026-08-01)

**Symptom:** after the UoT fix, the client still misbehaved ("a variety of
issues"). The log showed TWO engine startups in one session:

```
23:55:42 sing-box log level: debug          ← leftover sing-box.exe (orphaned
23:55:45 sing-box log level: debug          ← fresh Connect spawn
         inbound/tun[tun-in]: started at locus0   ← ×2 — two instances, one TUN
```

**Root cause:** orphaned `sing-box.exe` processes from earlier broken builds
(and/or other VPN apps like Hiddify) survive on Windows. A fresh Connect then
spawns a second engine; both instances share the `locus0` wintun adapter —
packets are delivered to both, routes fight, and failures look random.

**Fix (`v5/client/internal/manager/`):**
- New `foreignSingBoxRunning()` (Windows: `tasklist /FI "IMAGENAME eq sing-box.exe"`;
  Unix: `pgrep -x sing-box`) — true when an untracked sing-box process exists.
- `Start()` now refuses with a clear message ("another sing-box process is
  already running — close it (Task Manager) and retry") instead of stacking a
  second engine. Only runs on user-initiated Connect (never in the health-loop
  restart path, and never when our own process is tracked and alive).
- Note: this also blocks Connect while Hiddify's sing-box core is running —
  intentional (two TUN VPNs at once is invalid), the message says so.

**One-time user cleanup (still required for machines with orphaned engines):**
1. Task Manager → end ALL `sing-box.exe`.
2. `netsh interface show interface` → if `locus0` exists:
   `netsh interface delete interface locus0` (admin).
3. Reboot (clears stale routes/WFP filters).

---

## VPS TESTING — ISSUES FOUND & FIXED (2026-07-26)

These were discovered during real deployment to a Voyager VPS (Ubuntu 22.04)
and are now fixed in `v5/server/`.

### S1. Caddy Systemd Service Missing

| Severity | 🔴 Caddy wouldn't start |
|----------|------------------------|
| **File:** | `v5/server/modules/05-caddy.sh` |
| **Issue:** | The module downloaded a custom Caddy binary with rate_limit support, but never created a systemd service file. `systemctl restart caddy` failed with "Unit not found." |
| **Fix:** | Added `create_systemd_service()` that: creates `caddy` user if missing, creates `/var/log/caddy` and `/var/www/html`, writes a proper systemd unit file, and grants `cap_net_bind_service+ep` to the binary so it can bind :80/:443 as non-root. |

### S2. Caddy TLS Certificate Provisioning Failed

| Severity | 🔴 HTTPS broken |
|----------|-----------------|
| **File:** | `v5/server/modules/05-caddy.sh` |
| **Issue:** | Caddy ran as user `caddy` which had no home directory. Caddy tried to write TLS storage to `/home/caddy/.config/caddy/` but got "permission denied." |
| **Fix:** | Removed `ProtectSystem=full` from the service file. Created `/var/lib/caddy` for Caddy's runtime data. Added `Environment=XDG_CONFIG_HOME=/var/lib/caddy` and `Environment=XDG_DATA_HOME=/var/lib/caddy` so Caddy stores TLS certs in a writable location. |

### S3. PocketBase Service Failed with NAMESPACE Error

| Severity | 🔴 PocketBase wouldn't start |
|----------|------------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The service had `ProtectHome=true`, `ProtectSystem=full`, and `PrivateTmp=true`. On this VPS kernel (5.15.0-161-generic), these caused `status=226/NAMESPACE` errors — the kernel rejected namespace operations. |
| **Fix:** | Removed all systemd security hardening directives (`ProtectHome`, `ProtectSystem`, `ReadWritePaths`). The service now uses only `NoNewPrivileges=true` and `PrivateTmp=true`. |

### S4. Admin Token Extraction Failed (Shell Escaping)

| Severity | 🟡 Admin created but collections not seeded |
|----------|---------------------------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The admin creation API response was piped through `echo "$RESP" | python3 -c "..."` which broke when the JWT contained special characters (dots, dashes). The token came back empty, so the script thought "Admin created but could not extract token." |
| **Fix:** | Wrote the API response to a temp file before parsing. Also added a fallback auth flow (`/api/admins/auth-with-password`) if token extraction fails. Then refactored the entire bootstrap into `scripts/seed-pb.py` which uses Python's `json.dump()` to write temp files for all API calls, avoiding shell escaping entirely. |

### S5. Collection Creation Failed with Bash Heredocs

| Severity | 🟡 Collections not created |
|----------|---------------------------|
| **File:** | `v5/server/modules/06-pocketbase.sh` |
| **Issue:** | The shell heredocs (`cat > file << 'JSON'`) for the collection schema JSON were inconsistently parsed when passed through SSH. The JSON contained nested quotes and special characters that bash mangled. |
| **Fix:** | Moved all collection creation logic into `scripts/seed-pb.py`. Python writes the JSON payload to a temp file using `json.dump()`, then passes `@file` to curl. This is bulletproof — no shell escaping issues. |

### S6. `generate_codes.sh` Bash Bug (!$double)

| Severity | 🔴 Code generation produced errors |
|----------|-----------------------------------|
| **File:** | `v5/scripts/generate_codes.sh` |
| **Issue:** | The Luhn-mod-N checksum used `double=!$double` to toggle a boolean. In bash, `!$double` triggers history expansion (or fails with `!false: command not found` when history expansion is off). |
| **Fix:** | Replaced with `if $double; then double=false; else double=true; fi`. |

### S7. PocketBase 0.22 JS Hook Compatibility

| Severity | 🔴 Activation endpoint returns 400 |
|----------|-----------------------------------|
| **File:** | `v5/server/pb_hooks/activation.pb.js`, `heartbeat.pb.js`, `admin_unbind.pb.js` |
| **Issue:** | PocketBase 0.22.21 changed the JavaScript VM API. The hooks were originally written for the 0.21 API (`$app.dao()`, `$apis.requestInfo()`). On 0.22.21, `$app.dao()` still works via compatibility shim, but `$app.dao().db().exec()` and `$app.dao().findRecordsByFilter()` with `{:param}` syntax had degraded behavior. |
| **Fix:** | **✅ Fixed 2026-07-26.** Hooks updated to use working API patterns for PocketBase 0.22.21: |
| **Changes made:** | • `$apis.requestInfo(c).data` → `$apis.requestInfo(e).data` (compat shim still works)<br>• `$app.dao().db().exec()` → `$app.dao().db().newQuery().execute()`<br>• `$app.dao().findRecordsByFilter()` with `{:param}` → `$app.dao().findFirstRecordByData()` for single-record lookups<br>• `$app.dao().findCollectionByNameOrId()` + `new Record()` + `$app.dao().saveRecord()` for creating records<br>• `e.remoteIP` → `e.request().remoteAddr.split(":")[0]` (remoteIP not exposed in 0.22.21 router events)<br>• `const`/`let` → `var` (goja compatibility)<br>• Helper functions must be defined inside the `routerAdd` callback (goja doesn't hoist declarations into callback scope)<br>• Rate limiting filter: inline string with sanitization instead of `{:param}` syntax (filter parser changed)<br>• `sqlite_pragmas.pb.js` removed — `on()` hook API removed in 0.22, WAL mode set automatically by PocketBase |

### S8. Smoke Test IP Detection Off by One

| Severity | 🟡 False positive warning |
|----------|---------------------------|
| **File:** | `v5/server/scripts/smoke-test.sh` |
| **Issue:** | The smoke test detects the VPS IP using `ip route show default | awk '{print $3}'` which returns the **gateway** IP (e.g., `114.23.136.1`), not the interface IP (`114.23.136.47`). The DNS match check then fails even when DNS is correct. |
| **Fix:** | Changed to `ip -4 addr show | grep -oP 'inet \K[0-9.]+' | grep -v '^127\\.' | head -1` to get the actual interface IP. |

---

### S9. Secrets Decryption via Process Substitution Fails Silently

| Severity | 🔴 All SS passwords auto-generated instead of using stable secrets |
|----------|-------------------------------------------------------------------|
| **File:** | `v5/server/setup.sh`, `v5/server/restore.sh` |
| **Issue:** | Both `setup.sh` and `restore.sh` used `source <(age -d ...)` (bash process substitution) to load decrypted secrets. In non-interactive SSH sessions — specifically when running via `ssh root@host "/root/server/setup.sh"` — the process substitution would appear to succeed (exit 0, "✓ Secrets decrypted" printed) but **the variables would not actually be loaded into the current shell**. This caused all downstream modules to fall through to auto-generated passwords instead of using the stable credentials from `secrets.env.age`. The symptom: `DOMAIN` was empty despite "Secrets decrypted" having printed, causing a hard fail at the DOMAIN check. |
| **Root cause:** | `source <(command)` is a bash extension that works unreliably in certain SSH environments. The process substitution forks a subprocess, and `source` may read from an already-closed pipe under some shell configurations. Ubuntu's `/bin/sh` is dash, which does not support process substitution at all — but even under bash the behavior was inconsistent. |
| **Fix:** | Replaced `source <(age -d ...)` in both files with a two-step temp-file approach: `age -d ... > /tmp/.secrets-$$.env` followed by `source /tmp/.secrets-$$.env`. This is POSIX-compatible, works in all shell environments, and is easy to debug (the temp file can be inspected). Also added an `age` binary check + auto-install via `apt-get` before decryption, so a fresh VPS without `age` pre-installed will still work. |
