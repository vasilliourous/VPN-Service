# Locus VPN website — rework notes

**Date:** 2026-09-18
**Scope:** `locus-vpn-website/` (the static marketing site)

This file records what the rework changed, what was removed, and where the
removed files can be recovered. It exists so a future agent can reconstruct
the reasoning without reading a diff.

---

## Why the rework happened

The site had drifted away from the product it describes:

1. **Broken copy.** A scripted find-and-replace pass (recorded in
   `.edits/history.json`) had half-replaced several sentences in
   `index.html`, leaving fragments like `"The name"`, `"The condition
   being that…"`, `"Most VPN sites promise a lot…"` and the heading
   `"Promises That Deliever"` visible in the rendered page.
2. **A local write server in the publishing surface.** `edit.js` served the
   site on `:4321`, honoured an `x-raw: 1` header to return unstripped file
   contents, and wrote changes back to disk. It was never part of the
   published site, but it sat in the same directory.
3. **A stray nested repository.** `locus-vpn-website/VPN-Service/` was a
   full second checkout of this repository, one commit behind.
4. **No shared design system.** Styling was near-monochrome and unrelated to
   the shipped client's theme, and the pages carried inline `style=`
   attributes that fought the stylesheet.

---

## What changed

### New shared design system

`styles.css` was rewritten around the **same token set as the shipped client**
(`v5/docs/UI-AESTHETICS.md` §2), so the site and the app now read as one
product:

| Token | Value | Role |
|---|---|---|
| `--bg` | `#06130C` | Page background |
| `--surface` | `#0C1711` | Cards, panels |
| `--surface-hover` | `#13241A` | Hover states |
| `--border` | `#1F3629` | Dividers |
| `--text` / `--text-secondary` | `#EAF2EC` / `#8CA596` | Body / secondary text |
| `--accent` / `--accent-hover` | `#2EA86A` / `#46C186` | Locus green |
| `--tier-basic` | `#7FB48F` | Basic tier |
| `--tier-stream` | `#46C186` | Stream tier |
| `--tier-gaming` | `#EAB308` | Gaming tier (gold) |

Added: a spacing/type/radius scale, a sticky header with scroll state, a
mobile drawer with focus handling and Escape-to-close, a signal-path hero
diagram, tier cards with per-tier accents, a client screen viewer, sticky
table-of-contents sidebars for long documents, a responsive comparison table,
and reveal-on-scroll — all disabled under `prefers-reduced-motion`.

### Single-sourced content

`content.json` is the single source of truth for tier prices, caps, device
counts, network figures, policy windows and brand details. Pages reference
these as `{{tokens}}`.

**Tokens are substituted at build time by `build-content.js`.** That script
writes two things into every page:

1. The tokens are replaced in the markup itself, so the copy is correct with
   **no JavaScript at all**.
2. The full `content.json` object is inlined as `window.LOCUS_CONTENT`, so the
   interactive code (billing toggle, tier switching, signup summary) has the
   structured data it needs without a request.

`site.js` prefers `window.LOCUS_CONTENT` and only falls back to fetching
`content.json` if the inline copy is absent (a page edited but not yet
rebuilt).

#### After editing content.json

```sh
node build-content.js          # bake into all pages
node build-content.js --check  # verify pages are up to date
```

**Why this is not done at runtime with fetch().** An earlier version fetched
`content.json` in the browser. That works over HTTP but fails on `file://`,
where the browser blocks the request — leaving literal `{{tokens}}` visible in
the copy. Baking at build time makes the pages correct standalone, which is
also what lets them be opened directly from disk or dropped on any static host
with no server-side step.

### Pages rebuilt

All 11 HTML pages were rewritten: `index`, `features`, `client`,
`architecture`, `pricing`, `faq`, `signup`, `privacy`, `terms`, `refunds`,
`transparency`.

Notable per-page work:

- **`index.html`** — corrected the four corrupted passages; new hero with an
  animated signal-path diagram; tier band driven by `content.json`.
- **`features.html`** — the ten capabilities are now alternating full-width
  bands with an index and an explicit trade-off note each, instead of ten
  identical cards.
- **`client.html`** — the three app screens are a `role="tablist"` viewer so
  the reader can step through activation, connected and diagnostics states,
  with arrow/Home/End key support.
- **`pricing.html`** — the billing toggle now drives real price, total,
  savings and CTA-parameter state rather than duplicating a hard-coded array;
  comparison table moved into a shared primitive.
- **`signup.html`** — plan selection, billing period, order summary, and a
  card checkout form with Luhn validation, expiry checking, input formatting
  (card grouping, `MM / YY`) and per-field error messaging.
- **`architecture.html`, `privacy.html`, `terms.html`, `refunds.html`,
  `transparency.html`** — converted to the sticky-TOC document layout.

### Accessibility work

- Skip link on every page, pointing at `<main>`.
- One `<h1>` per page; no skipped heading levels in the new sections.
- Proper `aria-expanded` / `aria-controls` on nav toggle and accordions;
  `role="tablist"` with roving `tabindex` on the client viewer.
- Visible focus rings on all interactive elements.
- `no-js` fallback: FAQ answers and all three client screens are visible
  without JavaScript rather than being permanently collapsed.

---

## What was removed

| Path | Reason | Recovery |
|---|---|---|
| `.edits/` | Stale edit history and `.bak` files from the find-and-replace pass that corrupted `index.html`. | Recoverable from the pre-rework tarball (see below). |
| `VPN-Service/` | Stray nested checkout of this repository, one commit behind. Would have published a second, outdated copy of the source alongside the site. | `git clone` the repository, or check out `eb59e06`. **Not deleted — see the note below.** |
| `.qashots/*.png` | Stale screenshots of the pre-rework design. | Regenerated from the new design. |

### Preserved, not deleted

The editor tooling was **moved**, not removed, because its backup/undo design
is worth keeping:

- `edit.js` → `_tools/`
- `editor.js` → `_tools/`
- `editor.css` → `_tools/`

`_tools/README.md` explains how to run it. It is deliberately outside the
publishing surface, so it cannot be served accidentally with the site.

---

## Pre-rework snapshot

A tarball of the site as it was before this rework was written to:

```
/tmp/locus-website-prerework-20260918-135122.tar.gz
```

It excludes the nested `VPN-Service/` checkout. If it is gone, the pre-rework
files are still recoverable from git history once this work is committed.

---

## Verification performed

Re-run any of these after further changes:

- **Build** — `node build-content.js --check` confirms every page is in sync
  with `content.json`.
- **No tokens in markup** — `grep -o '{{[a-zA-Z0-9._]*}}' *.html` returns
  nothing, and the same holds when the pages are parsed with scripting
  disabled entirely.
- **Works from file://** — pages were rendered and screenshotted directly from
  `file://` with `fetch` unavailable, confirming the copy and the interactive
  controls (billing toggle, signup summary) work without a server.
- **Structure** — all pages parse, tag balance matches, exactly one `<h1>` per
  page, `<title>` and meta descriptions fully substituted, nav `aria-current`
  correct on every page.
- **Links** — 108 internal links and anchors resolve across all 11 pages.
- **Accessibility** — `lang`, labels on every input, accessible names on every
  button and link, no `img` without `alt`, tab/dialog ARIA wiring intact.
- **Behaviour** — tab switching with arrow/Home/End keys, FAQ accordion toggle
  and deep links (`faq.html#udp`), mobile drawer open/Escape/focus return,
  pricing billing toggle updating price/note/CTA, signup validation rejecting
  Luhn-invalid and expired cards.
- **Rendering** — screenshots captured for all 11 pages into `.qashots/`, with
  the expected `#06130C` background and green token palette confirmed by pixel
  sampling.

---

## Open item

`locus-vpn-website/VPN-Service/` still exists. Removing it requires a recursive
delete, which was blocked by the execution policy in the session that did this
rework. Delete it manually:

```sh
rm -rf locus-vpn-website/VPN-Service
```

It is a nested checkout with its own `.git`; nothing in the site links to it,
and it is redundant with the parent repository.
