# `_tools/` — local site editor (not published)

These files are the in-page editor that was previously loose in the site root,
where it sat alongside the files that get deployed. They are kept because the
backup-and-undo design is genuinely useful, but they must never be served as
part of the published site.

## What each file does

| File | Role |
|---|---|
| `edit.js` | Local HTTP server. Serves the site on `:4321` with the editor overlay injected, and accepts writes back to the real `.html` / `.css` files on disk. |
| `editor.js` | The overlay itself — click-to-edit text, shared-values panel, undo, save. |
| `editor.css` | Overlay styling only. Never part of the published site. |

## Running it

From the **site root** (one level up), not from this directory:

```sh
node _tools/edit.js                 # http://localhost:4321
node _tools/edit.js --port 5000
```

`edit.js` resolves paths relative to its own location, so it finds the site
files next to `_tools/` without extra configuration.

## Safety behaviour worth preserving

- Every write backs the original up into `.edits/backups/` before touching it.
- The whole session's changes can be undone from the editor's History panel.
- Shared values (prices, caps, counts) are applied across every page from a
  single source.

## Important

- **Do not move these back into the site root.** `edit.js` serves raw file
  contents when it sees an `x-raw: 1` header and writes to disk on request — it
  is a development tool, not part of the product.
- The site no longer needs the editor to change shared values. Prices, caps,
  counts and policy figures now live in `content.json` and are substituted at
  load time by `site.js`, so they can be edited directly in that one file.
- If you use the editor, delete the `.edits/` directory it creates before
  committing. That directory is intentionally not tracked.
