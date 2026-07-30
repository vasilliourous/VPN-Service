// MyVPN TypeScript types — mirrors the Go backend API
// See docs/BACKEND-API.md for the complete Go type definitions.

// ── Validation (client-side, no server call) ──

export interface ValidateResult {
  valid: boolean
  message?: string
}

// ── Activation ──

export interface ActivateResult {
  success: boolean
  message: string
  tier?: string
}

// ── Status ──

export interface StatusResult {
  connected: boolean
  tier: string
  state: string     // "running" | "stopped" | "crashed"
  failures: number
  graceDays: number
}

// ── Connection Operations ──

export interface OpResult {
  success: boolean
  message: string
}

// ── Updates ──

export interface UpdateCheckResult {
  available: boolean
  version?: string
  url?: string
  sha256?: string
}

// ── Events (emitted by Go backend) ──

export interface UpdateAvailableEvent {
  version: string
  url: string
  sha256: string
}
