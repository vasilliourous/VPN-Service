# handoff/

Record of **this conversation's work** on Locus. Four tasks, 2026-09-19.
Not project documentation — the repo and `docs/` already have that.
(The original text below said `v5/docs/`; that path is the pre-2026-09-23
location, now `docs/`.)

> **Two corrections, applied 2026-09:** the `GOTCHAS-FROM-THIS-WORK.md` this
> index refers to was renamed during the restructure; read it as
> `docs/history/SESSION-GOTCHAS.md`. And `stamp-batch.py` moved from
> `v5/server/scripts/` to `server/scripts/stamp-batch.py`.

| File | Read it when |
|---|---|
| `WHAT-WE-DID.md` | You need to know what changed and why |
| `SESSION-GOTCHAS.md` (was `GOTCHAS-FROM-THIS-WORK.md`) | Something is behaving oddly and you want the traps I already hit |
| `STILL-OPEN.md` | You are picking up where this left off |

---

## In one paragraph

I learned the project, fact-corrected 9 documentation files, ran a full read-only
audit of the live hub (finding one real defect: `update_config` advertised
`/updates/1.0.2/*` artifacts that did not exist), and made `setup.sh` zero-touch
so a fresh deploy needs no follow-up flags. Everything landed in **one squashed
commit `4791381 "Another Version!"`** — which is why these files exist; the commit
message carries none of the reasoning.

## Where the work physically is

| Artifact | Location |
|---|---|
| All my changes | commit `4791381` (21 files, +1208 / −237) |
| `stamp-batch.py` (I created it) | `v5/server/scripts/` |
| The `releases.set` guard | `admin_console.pb.js` ~line 685 |
| Pre-change backups I left on the VPS | `/root/data.db.pre-updateconfig-fix-1789795235`, `/root/admin_console.pb.js.pre-guard-1789795273` |

## Two things that were later superseded

So you do not mistake these for current:

1. **The browser uploader I hardened was retired** (`338c189`) — the hub now pulls
   releases from GitHub instead. `release_upload.py`, `locus-upload.service` and
   `artifact.ts` no longer exist.
2. **`FIXES.md` was truncated to 69 lines** by `15e7630`, then restored in
   `ec3b129`. If you see a large `FIXES.md` diff, check whether entries were
   *dropped* rather than added.

## The rules I would want stated plainly

I verified these the hard way; each cost time.

- **Verify behaviour, not status.** Every serious bug found in this conversation
  was inert configuration that looked healthy: a rate limit that never counted,
  URLs pointing at deleted files.
- **Suspect your test before the system.** Twice I diagnosed a server fault that
  was a bad input on my side (a truncated fingerprint, a Luhn-invalid code).
- **No sandbox.** The single VPS is production. Stage, measure, be reversible.
- **Never bulk-delete `codes` or `code_events`.** Real customer data.
