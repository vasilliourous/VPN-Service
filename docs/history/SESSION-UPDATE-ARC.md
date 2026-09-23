# This session: the update/installer arc

The session after `4791381`. That one was docs, hub audit and zero-touch deploys;
this one starts from a field bug report and follows it through the release
pipeline. Four tasks, 2026-09-20.

Where the previous agent's files describe the *hub and deploy* side, these are
the *client self-update* side. Read `WHAT-WE-DID.md` and friends for everything
before `6fd6847`.

---

## In one paragraph

A Windows client reported `cannot rename downloaded file … locus.exe.new.tmp`.
I diagnosed it as a directory-watcher problem, shipped an installer into
`Program Files` plus a staging directory, fixed two CI path bugs that were
blocking the installer from building, and released 2.2.5. **The rename kept
failing in `Program Files`** — which is not an observed directory, so the
original diagnosis was wrong. Chasing that led to three real client bugs:
terminal windows flashing, update debris accumulating in `Program Files`, and a
double window during update. The rename itself is still not explained.

## The commits

| Commit | What |
|---|---|
| `6fd6847` | Inno Setup paths absolute (pre-session, in flight) |
| `e4f1e71` | Pass the `.iss` path absolutely too |
| `fca5ccd` | `chore(release): 2.2.5` |
| `af9cf92` | Stop console windows flashing on Windows |
| `cd4dbc0` | `chore(release): 2.2.6` |
| `2aee6ed` | Reclaim update debris; stop the double window |

`origin/main` is at `af9cf92`; `cd4dbc0` and `2aee6ed` are unpushed. `v2.2.6` is
tagged and pushed at `cd4dbc0` — **so the pushed tag predates both client
fixes**, and the release built from it does not contain them.

---

## 1. The rename failure, and a diagnosis I got wrong

Field report, twice:

```
Update to 2.2.3 failed: download failed: cannot rename downloaded file:
rename C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp ...
The process cannot access the file because it is being used by another process.
```

I diagnosed it as a directory-watcher problem and built the fix around that
premise: the updater staged its download in the directory the binary ran from,
and `Downloads` is the most heavily observed directory on Windows (Defender,
SmartScreen, the indexer, sync clients). The fix was to install into Program
Files and stage inside a private subdirectory.

**Then it failed in `C:\Program Files\Locus\`.** That is the strongest possible
counter-evidence — nothing scans Program Files. A location-independent failure
means the holder was not a third party. Reading the code, the actual cause was
isomorphic and much simpler: the 2.2.3 updater closed the download with
`defer f.Close()`, which runs *after* the rename. **It was racing its own open
file descriptor.** On Windows any open handle blocks a rename, so it failed
regardless of directory.

The location change was never what fixed it; the explicit close was. Same
outcome, wrong reasoning — and the reasoning is what went into the comments.

### Why I could not settle it

The maintainer reported it still failing after installing 2.2.5. I argued from
the path shape that the running binary must be 2.2.3, because 2.2.5 always
includes a `.locus-staging` path segment and the error had none. I verified:

- the hub serves 2.2.5, checksum-matched to the GitHub release
- that binary contains `.locus-staging`, `retrying in %s`, `succeeded on attempt`
- `ensureStagingDir()` falls back to `appDir/.locus-staging` even when
  `SetStagingDir` is never called, so no 2.2.5 path omits the segment

**That reasoning is sound but rests on an assumption I never tested: that the
running binary matches the tag.** I could read code and checksums; I could not
see the machine. I presented the conclusion with more confidence than the
evidence supported, and it may still be wrong.

The two measurements that would settle it, still unasked:

1. What version does the client display?
2. Does `C:\Program Files\Locus\.locus-staging\` exist?

Absent ⇒ old binary. Present ⇒ live bug in shipped code.

---

## 2. CI: the installer was not building

Two distinct path bugs, both from Inno Setup resolving relative paths against
`SourceDir` (the `.iss` script's directory), not the cwd.

- `6fd6847` — `[Files] Source:` and `OutputDir` were relative. CI compiles from
  `v5/client` and reads results from `v5/client/dist`; the compiler looked in
  `build/windows/` and would have written `build/windows/dist`.
- `e4f1e71` — the `.iss` argument itself was `..\..\build\windows\locus.iss`,
  the last cwd-relative path left. Absolute now, guarded by `Test-Path`.

Verified by the release existing: `locus-setup-2.2.5.exe`, 14.9 MB.

Also from this session: `bump.sh` died at "no Go toolchain" **after** bumping
`VERSION` but **before** regenerating `rsrc_windows_*.syso`. I caught the stale
`.syso` (still 2.2.4) only because `version_consistency_test.go` compares it
against `VERSION`. A control run confirmed the test is real:

```
winres_test.go:67: FileVersion = "2.2.4", want "2.2.5" (v5/VERSION).
```

Had that shipped, `Lint & Vet` would have failed on the first step — the same
class of failure that burned the `v2.2.4` tag.

---

## 3. Terminal windows flashing

Real bug, found by reading for it. Locus is a `windowsgui` binary, so a
console-subsystem child has no console to inherit and Windows **allocates a
fresh one** — a terminal that flashes open and vanishes.

`internal/manager` and `internal/activation` already had `HideWindow`. Three
Windows-reachable spawns were missed:

- `internal/tunnel/tunnel.go` — 3 `netsh` calls (DNS + kill-switch firewall),
  run on **every** Connect and Disconnect
- `internal/updater/update_windows.go` — the fork, which flashes at the exact
  moment the UI disappears

Fixed by routing the tunnel's spawns through a `hiddenCommand` constructor
(`exec_windows.go` / `exec_other.go`) and setting `HideWindow` on the fork. The
manager comment already said this bug "kept coming back" because call sites were
fixed one at a time — the constructor is what stops that recurring there.

## 4. Update debris in Program Files

`swapWindows` renames the running `locus.exe` → `locus.exe.old` (Windows cannot
delete a mapped image), then calls `os.Remove(oldPath)` — which **always fails**,
because the process is still running from it. Nothing retried. **Every update
stranded a full ~10 MB copy.** `CleanStaleMarkers` only handles
`.update-start-*`.

Added `updater.Housekeeping(appDir, binaryName)`, called from `app.go` startup:
removes the superseded binary and staged `*.tmp` older than an hour. It
deliberately never touches a live `.new` — deleting that would break an
in-flight update — and a test asserts that property.

## 5. Two windows during update

Two causes, both in the fork:

- `cmd.Stdin/Stdout/Stderr = os.Stdin/Stdout/Stderr`. A GUI process has no
  console, so those handles are invalid and `exec` stalls resolving the
  inheritance — the child painted its window while the parent was on screen.
  Now `nil`.
- The fork runs *inside* `PerformUpdate`, then `app.go` sleeps 1500 ms before
  quitting. The child was already booting during the parent's lifetime.

The fork now appends `updater.HandoffFlag` (`--handoff`), and the successor
waits 1200 ms — just inside the parent's 1500 ms — before showing its window.
The flag is a shared constant so the two processes cannot drift on the spelling;
a mismatch would silently reintroduce the bug.

---

## What I would want a successor to know

- **The rename bug is not proven fixed.** Debris and double-window are fixed;
  neither is shown to be *the* cause of the rename error.
- **`v2.2.6` on origin predates both client fixes.** The release from it ships
  neither. 2.2.6 as tagged is not the 2.2.6 described here.
- **The location theory was wrong**, even though the change it motivated is
  still reasonable. The comments in `internal/install` and `rename.go` still
  tell the Downloads/Defender story; that is not what the evidence supports.
- **2.2.5 shipped no `.dmg` assets** — `make-dmg.sh` succeeded but the zips are
  what reached the release. macOS still works via zip; the release notes
  document `.dmg` as the macOS path, so the two disagree.
- **A leaked PAT was pasted into chat and never confirmed revoked.**
