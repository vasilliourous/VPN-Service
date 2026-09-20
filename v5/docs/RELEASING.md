# Cutting a Release with `release-cut.sh`

> One script, one environment variable, two arguments. It wraps `./bump.sh`
> (which deliberately does not touch git) and adds the commit, tag and push.

```bash
export GH_TOKEN=ghp_xxxxxxxx
./scripts/release-cut.sh patch
```

That is the entire interface. Everything below is detail.

---

## Why this exists

`bump.sh` rewrites the version in six files and regenerates the Windows
resources, then stops and prints the git commands for you to run. That was
deliberate — a bump that half-applies is easier to recover from if nothing has
been committed.

The downside is four commands to copy by hand every release, with a `git push
origin main` and a `git push origin v2.2.7` that look identical but are not.
`release-cut.sh` runs those four commands for you.

---

## Setup: the token

There is no credential helper on this machine and the remote is HTTPS, so git
cannot prompt its way in. Every push needs a token.

1. Go to **github.com/settings/tokens** and create a classic PAT with the
   **`repo`** scope.
2. Export it:

```bash
export GH_TOKEN=ghp_xxxxxxxxxxxxxxxx
```

Put that line in your shell profile if you cut releases often, or prefix the
command with it for a one-off:

```bash
GH_TOKEN=ghp_xxx ./scripts/release-cut.sh patch
```

The script passes the token in the push URL
(`https://x-access-token:$GH_TOKEN@github.com/...`). It is never written to a
file and never printed.

> **Do not paste the token into the script.** A PAT is a repo-write credential;
> a literal in a tracked file ends up in every future `git diff` and in shell
> history. This repo has already lost a live tier password exactly that way —
> see the header of `.gitignore`.

If `GH_TOKEN` is not set the script stops immediately with a reminder.

---

## Usage

```bash
./scripts/release-cut.sh patch            # 2.2.6 -> 2.2.7, commit, tag, push
./scripts/release-cut.sh minor            # 2.2.6 -> 2.3.0
./scripts/release-cut.sh major            # 2.2.6 -> 3.0.0
./scripts/release-cut.sh 2.5.3            # explicit
./scripts/release-cut.sh patch --no-tag   # TEST BUILD — no tag, no release
```

`patch` is the default if you pass nothing, but pass it anyway — being explicit
is cheaper than being surprised.

### `--no-tag` — the test-build path

Use this when you want CI to build a version but you are **not** ready to
release it.

```bash
./scripts/release-cut.sh patch --no-tag
```

It bumps, commits and pushes `main`, and creates no tag. CI runs, builds all
four platforms, and **skips the `Create Release` job** — so you get a real
build you can inspect, with nothing published.

This exists because a pushed tag is hard to take back. If CI fails after you
have tagged, your options are to force-push the tag (which forks anyone's fetch
and can leave CI pointing at the old commit) or to bump again and dodge the
collision. Pushing the branch alone avoids the choice entirely.

**The flag must be the second argument.** `patch --no-tag` works. `--no-tag
patch` would treat `--no-tag` as the version word.

---

## What it does, in order

1. **`./bump.sh <arg> --no-verify`** — rewrites the six version-bearing files
   and regenerates `rsrc_windows_{amd64,arm64}.syso`.
2. **`git checkout -- v5/client/go.mod v5/client/go.sum`** — `bump.sh`'s
   `go:generate` drags winres dependencies into the module files. This drops
   that noise so the release commit contains only version changes.
3. **`git add -A && git commit -m "chore(release): <version>"`**
4. **`git tag -a v<version>`** — annotated. Immediately deleted again if you
   passed `--no-tag`.
5. **`git push <url> main`**, then **`git push <url> v<version>`** — the token
   is in `<url>`. The tag is skipped under `--no-tag`.

On success it prints the version, whether a tag was pushed, the `gh run watch`
command, and the `publish-release.sh` line for the next step.

---

## The `--no-verify` flag, and why it is unconditional

Line 20 passes `--no-verify` to `bump.sh`, which skips the version-consistency
test gate. On this machine that gate **always fails**, and it is not a real
failure:

```
github.com/getlantern/systray: exec: "pkg-config": executable file not found in $PATH
FAIL	locus [build failed]
```

The Go client needs `libwebkit2gtk` and `pkg-config` to build, and neither is
installed here. The check that actually matters — `./internal/winres`, which
reads the version back **out of the compiled `.syso`** — passes:

```bash
cd v5/client && go test ./internal/winres/... -count=1   # ok
```

So the flag is baked in rather than being something you have to remember. To
turn the gate back on, edit `--no-verify` out of line 20 and run from a host
with `pkg-config` installed. CI does this properly on every release.

The `did not find icon winres/icon.png` warning from the `.syso` regeneration
is pre-existing and harmless.

---

## After the push

The script stops at the push. What happens next:

**CI** (only if you tagged) builds four platforms and creates the GitHub
Release. Watch it with `gh run watch` or the Actions tab.

**Publishing to the live hub** is a separate, deliberate step:

```bash
# Hold the rollout at 0 — upload only, offer it to nobody
ROLLOUT_PERCENT=0 v5/server/scripts/publish-release.sh 2.2.7 --from-github

# Or start the rollout (default 5%)
v5/server/scripts/publish-release.sh 2.2.7 --from-github
```

**Confirm the hub is serving it:**

```bash
curl -s https://networkingguides.duckdns.org/api/release
```

### ⚠️ Hold the rollout at 0 unless the change was verified on real hardware

A clean compile is not proof that a runtime fix works. For any release
containing a client behaviour change, publish with `ROLLOUT_PERCENT=0`, install
it on a real Windows machine, confirm the fix, and only then raise the rollout.

---

## If the tag does not reach GitHub

This is the failure mode worth knowing, because **it looks like success.**

The `Create Release` job is gated on `refs/tags/v*`:

```yaml
release:
  if: startsWith(github.ref, 'refs/tags/v')
```

On a branch-only push, `github.ref` is `refs/heads/main`, so the job is
**skipped** — and GitHub renders a skipped job as a green check. Seven green
jobs, no release, nothing obviously wrong.

**Do not re-run that workflow.** It replays against `refs/heads/main` and skips
identically. Push the tag instead:

```bash
git push origin v2.2.7
```

This is why `--no-tag` is safe to use deliberately but dangerous to end up in
by accident: a branch-only push is indistinguishable from a release push in the
Actions UI.

Verify the tag actually landed before trusting the green check:

```bash
git ls-remote --tags origin | grep v2.2.7
```

---

## Troubleshooting

| Symptom | Cause |
|---|---|
| `set GH_TOKEN first` | `GH_TOKEN` not exported in this shell |
| `working tree is dirty` | `bump.sh` refuses a dirty tree. Commit or stash first. |
| `bump.sh` consistency gate fails | `pkg-config` missing. Expected here — see above. |
| Push hangs, then times out | `GH_TOKEN` unset, or the token lacks `repo` scope |
| CI green but no release | The tag never reached origin. See above. |
| `tag v2.2.7 already exists` | You already cut this version. Bump again, or delete the local tag. |
| Wrong version committed | `git reset --hard HEAD~1 && git tag -d v<version>`, then re-run |

---

## Related

- `v5/docs/CI-CD.md` — the full pipeline: what CI does with the tag, the
  release asset list, and which consumer needs which artefact
- `v5/docs/SECRETS-MANAGEMENT.md` — how server credentials are stored and
  rotated
- `bump.sh` / `v5/server/scripts/bump-version.sh` — the version rewrite itself
  and the consistency checks it runs
- `v5/server/scripts/publish-release.sh` — pushing a built release to the hub
