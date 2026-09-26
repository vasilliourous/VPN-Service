import getSystem from '@/utils/get-system'

const OS = getSystem()

/**
 * Locus's colour system.
 *
 * These are the **defaults**, not a hard override: `use-custom-theme` reads each
 * field as `setting.X || dt.X`, so a student who has customised a colour keeps
 * it. Changing this file changes what a fresh install — and anyone who never
 * opened the old colour picker — sees.
 *
 * # Where these values come from
 *
 * `docs/archive/UI-AESTHETICS.md` defines the brand: a dark green-black theme
 * with Locus green as the single accent. That spec was written for the retired
 * client and never ported, which is why this fork still shipped Clash Verge Rev's
 * iOS blue (`#007AFF`) and grey (`#2E303D`) — an identity belonging to a
 * different product, on every screen a student sees.
 *
 * # The one departure from the spec
 *
 * The spec defines **only** the dark theme; it was written for a client that
 * shipped dark-only. This fork has a light mode, so the light palette below is
 * derived rather than quoted: the same green accent and the same structural
 * relationships, inverted for a light surface. Documented as an addition so
 * nobody later mistakes it for part of the original spec.
 *
 * # Why the greens are dark enough to matter
 *
 * `#2EA86A` on white is roughly 3.0:1 — fine for large text and UI shapes,
 * short of the 4.5:1 needed for body copy. Light mode therefore uses a darker
 * accent (`#1E7A4A`, ~4.6:1 on white) for anything textual, while keeping the
 * brand green for fills and borders where contrast rules are looser. Using one
 * green for both would have meant either failing contrast in light mode or
 * dulling the brand in dark, which is the mode that matters most.
 */

/** Locus green, and the surfaces it sits on. Single source for the brand. */
export const LOCUS_COLORS = {
  /** The window. Green-black, deliberately almost black so the accent carries. */
  background: '#06130C',
  /** Cards and panels: one step up from the window. */
  surface: '#0C1711',
  /** Hover state for interactive surfaces. */
  surfaceHover: '#13241A',
  /** Dividers, input borders. */
  border: '#1F3629',
  /** Body text on the dark surface. */
  textPrimary: '#EAF2EC',
  /** Labels and hints. */
  textSecondary: '#8CA596',
  /** The brand accent. Buttons, active indicators. */
  accent: '#2EA86A',
  /** Accent hover. */
  accentHover: '#46C186',
  /** Connected. */
  success: '#22C55E',
  /** Disconnected, failures. */
  error: '#EF4444',
  /** Connecting, degraded, renewal warnings. */
  warning: '#F59E0B',
} as const

/**
 * Light-mode surfaces.
 *
 * Derived, not quoted — see the note above. The greens keep their relationship
 * to the surfaces (surface lightest, border a visible step down) so the layout
 * reads identically in both modes.
 */
export const LOCUS_LIGHT = {
  background: '#F4F8F5',
  surface: '#FFFFFF',
  surfaceHover: '#E8F0EA',
  border: '#CBDDD2',
  accent: '#1E7A4A',
  accentHover: '#166139',
} as const

/** The font stack. System fonts: they load instantly and look native. */
const fontFamily = `-apple-system, BlinkMacSystemFont,"Microsoft YaHei UI", "Microsoft YaHei", Roboto, "Helvetica Neue", Arial, sans-serif, "Apple Color Emoji"${
  OS === 'windows' ? ', twemoji mozilla' : ''
}`

export const defaultTheme = {
  // Locus light. The accent is the darker green, because this value is used for
  // text-bearing controls (buttons, links) where `#2EA86A` on white is too thin.
  primary_color: LOCUS_LIGHT.accent,
  secondary_color: '#5B8C6F',
  primary_text: '#0B1F14',
  secondary_text: '#4A6356',
  info_color: '#2563EB',
  error_color: '#C2362B',
  warning_color: '#B45309',
  success_color: '#1E7A4A',
  background_color: LOCUS_LIGHT.background,
  font_family: fontFamily,
}

export const defaultDarkTheme = {
  primary_color: LOCUS_COLORS.accent,
  secondary_color: '#5BBF8E',
  primary_text: LOCUS_COLORS.textPrimary,
  secondary_text: LOCUS_COLORS.textSecondary,
  info_color: '#5AA9E6',
  error_color: LOCUS_COLORS.error,
  warning_color: LOCUS_COLORS.warning,
  success_color: LOCUS_COLORS.success,
  background_color: LOCUS_COLORS.background,
  font_family: fontFamily,
}

/**
 * Tier identity, from the spec's badge table.
 *
 * Colours are per-tier so the tier "sells itself" without extra UI. Kept beside
 * the palette because they are brand colours with the same rules, and because a
 * tier added on the hub must be given a colour in exactly one place.
 */
export const TIER_COLORS: Record<string, { color: string; background: string }> = {
  strike: { color: '#EAB308', background: 'rgba(234, 179, 8, 0.20)' },
  stealth: { color: '#46C186', background: 'rgba(70, 193, 134, 0.18)' },
  eco: { color: '#7FB48F', background: 'rgba(127, 180, 143, 0.18)' },
}

/** The fallback for a tier this build does not know, so an unknown tier renders. */
export const TIER_FALLBACK = { color: '#8CA596', background: 'rgba(140, 165, 150, 0.18)' }
