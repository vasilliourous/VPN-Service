/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'

import { describe, expect, it } from 'vitest'

import { LOCUS_COLORS, LOCUS_LIGHT } from '../src/pages/_theme'

/**
 * The three layers that paint before the app is themed must agree.
 *
 * There are three, and each exists for a different moment:
 *
 *   1. the NATIVE window (`src-tauri/.../window.rs`) — painted by the OS before
 *      any web content exists;
 *   2. the DOCUMENT (`src/index.html`) — parsed before any bundle runs, so it
 *      cannot import the theme module;
 *   3. the APP (`_theme.tsx` via `use-custom-theme`) — the themed surface.
 *
 * A mismatch between any two of them is a visible flash of the wrong colour
 * during startup, which is exactly how Clash Verge Rev's greys lingered in this
 * fork after everything else had been rebranded. The copies cannot be removed —
 * layer 2 cannot import layer 3 — so this test is what keeps them honest.
 *
 * The Rust side is pinned separately, in `window.rs`'s own test module, because
 * a JS test cannot read a Rust `const`. Keeping the *pinned literal* here equal
 * to the one there is a human step; the comment in each says so.
 */
const read = (relative: string) =>
  readFileSync(path.resolve(__dirname, relative), 'utf8')

/**
 * File contents with comments removed, lowercased.
 *
 * Every file checked here explains WHICH upstream colours it replaced, so the
 * raw text legitimately contains the hex codes in prose. Without stripping,
 * these tests fail on a correct file and the tempting fix is to delete the
 * explanation — which is the documentation that stops the colours coming back.
 */
const readCode = (relative: string) =>
  read(relative)
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/.*$/gm, '')
    .toLowerCase()

describe('locus palette consistency across paint layers', () => {
  it('the document background matches the theme background', () => {
    const html = read('../src/index.html')

    // Dark: the primary experience, and the one the spec defines.
    expect(html).toContain(`--bg-color: ${LOCUS_COLORS.background.toLowerCase()}`)
    // Light: derived, but must match its counterpart too.
    expect(html).toContain(`--bg-color: ${LOCUS_LIGHT.background.toLowerCase()}`)
  })

  it('the document text colour matches the theme text colour', () => {
    const html = read('../src/index.html')
    expect(html).toContain(`--text-color: ${LOCUS_COLORS.textPrimary.toLowerCase()}`)
  })

  it('no Clash Verge grey survives in the document', () => {
    const html = read('../src/index.html').toLowerCase()
    // The two literals this fork inherited from upstream. Either reappearing
    // means the pre-bundle frame flashes another product's colours.
    expect(html).not.toContain('#2e303d')
    expect(html).not.toContain('#f5f5f5')
    expect(html).not.toContain('#181a1b')
  })

  it('the theme carries no Clash Verge blue or grey', () => {
    // Comments are stripped first, deliberately.
    //
    // The file explains WHICH upstream colours it replaced, so the raw text does
    // contain the hex codes — in prose. Scanning prose would fail on a file that
    // is correct, and the tempting fix would be to delete the explanation. Strip
    // comments and check the code instead, so the doc comment can stay specific.
    const theme = readCode('../src/pages/_theme.tsx')
    for (const verge of ['#007aff', '#0a84ff', '#2e303d', '#fc9b76', '#ff9f0a']) {
      expect(theme).not.toContain(verge)
    }
  })

  it('the comment-stripping did not defeat the check', () => {
    // Guard on the guard: if the strip became too eager it would remove real
    // code too, and the test above would pass vacuously. A colour that IS in the
    // code must still be found.
    const theme = readCode('../src/pages/_theme.tsx')
    expect(theme).toContain('#2ea86a')
  })

  it('the stylesheet fallback is Locus, not Verge', () => {
    // The fourth place the palette is written, and the easiest to forget: SCSS
    // `:root` variables are overwritten by the theme hook at runtime, so a wrong
    // value here is only visible for a moment on each launch — which is exactly
    // how it survives review. It previously held Verge's purple accent
    // (`#5b5c9d`), visible as a purple flash before the green theme applied.
    const scss = readCode('../src/assets/styles/index.scss')
    expect(scss).toContain('--primary-main: #2ea86a')
    expect(scss).toContain('--background-color: #06130c')
    expect(scss).not.toContain('#5b5c9d')
    expect(scss).not.toContain('#f5f5f5')
  })

  it('no component hardcodes an upstream surface colour', () => {
    // Component-level colours are the easiest to miss: they sit outside the
    // theme, so they do not follow light/dark and do not appear in any palette
    // review. Two were found this way — `base-page.tsx` painting the page
    // background `#1e1f27` (Verge's dark surface) on EVERY screen, and the
    // traffic graph falling back to Verge's purple when the palette was unset.
    //
    // Scans `src/components` and `src/pages` for the specific upstream literals.
    // Not a general "no hex outside the theme" rule: legitimate one-off colours
    // exist (verdict reds/greens on the activation field), and a blanket ban
    // would push people to obfuscate them rather than to the palette.
    const offenders: string[] = []
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name)
        if (entry.isDirectory()) {
          walk(full)
          continue
        }
        if (!entry.name.endsWith('.tsx') && !entry.name.endsWith('.ts')) continue
        if (entry.name === '_theme.tsx') continue
        const code = readFileSync(full, 'utf8')
          .replace(/\/\*[\s\S]*?\*\//g, '')
          .replace(/\/\/.*$/gm, '')
          .toLowerCase()
        for (const upstream of ['#1e1f27', '#39393d', '#5b5c9d', '#9c27b0', '#33cf4d', '#bbbbbb']) {
          if (code.includes(upstream)) offenders.push(`${full} -> ${upstream}`)
        }
      }
    }
    walk(path.resolve(__dirname, '../src/components'))
    walk(path.resolve(__dirname, '../src/pages'))
    expect(offenders).toEqual([])
  })

  it('the tier colours are the spec values', () => {
    // Lowercased helper, lowercased needles — the two must agree or the
    // assertion can never fire.
    const theme = readCode('../src/pages/_theme.tsx')
    // From docs/archive/UI-AESTHETICS.md §7. Pinned because these are the
    // "tier sells itself" cues and a wrong gold/green would be a brand error
    // nobody notices in code review.
    expect(theme).toContain('#eab308') // strike, gold
    expect(theme).toContain('#46c186') // stealth, green
    expect(theme).toContain('#7fb48f') // eco, muted green
  })

  it('the accent is Locus green and the surfaces are the spec values', () => {
    expect(LOCUS_COLORS.accent).toBe('#2EA86A')
    expect(LOCUS_COLORS.background).toBe('#06130C')
    expect(LOCUS_COLORS.surface).toBe('#0C1711')
    expect(LOCUS_COLORS.border).toBe('#1F3629')
    expect(LOCUS_COLORS.textPrimary).toBe('#EAF2EC')
    expect(LOCUS_COLORS.textSecondary).toBe('#8CA596')
  })

  it('light-mode text is dark and dark-mode text is light', () => {
    // The activation screen shipped invisible once because a light palette was
    // paired with light text. Cheap invariant, catches the same class of error.
    const luminance = (hex: string) => {
      const r = parseInt(hex.slice(1, 3), 16)
      const g = parseInt(hex.slice(3, 5), 16)
      const b = parseInt(hex.slice(5, 7), 16)
      return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
    expect(luminance(LOCUS_COLORS.textPrimary)).toBeGreaterThan(
      luminance(LOCUS_COLORS.background),
    )
    expect(luminance('#0B1F14')).toBeLessThan(luminance(LOCUS_LIGHT.background))
  })
})
