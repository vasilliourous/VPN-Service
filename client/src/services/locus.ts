import { invoke } from '@tauri-apps/api/core'

/**
 * The Locus product surface.
 *
 * Every call returns a TYPED result rather than a string, because the situations
 * a student can hit are genuinely different and only some of them are their
 * fault: a code bound to their old laptop is not the same as a hub they cannot
 * reach. Collapsing those into "activation failed" is what made the retired
 * client's support conversations start from nothing.
 */

export interface LocusStatus {
  activated: boolean
  tier: string | null
  /** Truncated fingerprint — enough for support to correlate, not a shareable id. */
  deviceId: string
  platform: string | null
  version: string
}

export interface ValidateCodeResult {
  valid: boolean
  /** The canonical hyphenated form, so the UI can show what will be sent. */
  canonical: string | null
  message: string | null
}

export interface CodeCheck {
  ready: boolean
  tier: string | null
  expiresAt: string | null
  message: string
}

export interface ActivationResult {
  code: string
  tier: string
  udpRelay: boolean
  /** False while the config-apply path is still being wired. */
  configApplied: boolean
  message: string
}

export const locusStatus = () => invoke<LocusStatus>('locus_status')

/** Offline checksum check. No network — safe to call on every keystroke. */
export const locusValidateCode = (code: string) =>
  invoke<ValidateCodeResult>('locus_validate_code', { code })

/** Read-only hub pre-check: is this code real, and is it usable here? */
export const locusCheckCode = (code: string) =>
  invoke<CodeCheck>('locus_check_code', { code })

export const locusActivate = (code: string) =>
  invoke<ActivationResult>('locus_activate', { code })

export const locusHubUrl = () => invoke<string>('locus_hub_url')

export const locusUpdateStagingDir = () =>
  invoke<string>('locus_update_staging_dir')
