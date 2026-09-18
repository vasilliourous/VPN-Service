// Locus TypeScript types — mirrors the Go backend API
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

// Result of the read-only hub code lookup (App.CheckCode).
// `known` is false when the hub could not be reached — the UI must NOT treat
// that as an invalid code.
export interface CodeCheckResult {
  recognised: boolean
  status: string
  known: boolean
  message?: string
  tier?: string
  expiresAt?: string
}

// ── Status ──

export interface StatusResult {
  connected: boolean
  tier: string
  state: string     // "running" | "stopped" | "crashed"
  failures: number
  graceDays: number
  tunnelOk: boolean // watchdog: is the tunnel actually passing traffic?
  repairStage?: string // '' | 'restart' | 'full-reset' | 'degraded'
}

// ── Connection Operations ──

export interface OpResult {
  success: boolean
  message: string
}

// ── Failure classification ──
//
// The Go connect path returns a raw OpResult.Message. Some failures are local
// (the laptop has no working network connection — no route/DNS to the VPN
// server), others are server/engine-side. Students can act on the former
// ("check your wi-fi") but can only report the latter, so the UI classifies
// the message to pick the right wording.
export type FailureKind = 'offline' | 'elevation' | 'engine' | 'server' | 'unknown'

// ── Updates ──

export interface UpdateCheckResult {
  available: boolean
  version?: string
  url?: string
  sha256?: string
}

export interface UpdateResult {
  success: boolean
  message: string
}

// Phases of the background apply flow emitted on the update:status event.
export type UpdatePhase = 'idle' | 'downloading' | 'verifying' | 'applying' | 'applied' | 'failed'

export interface UpdateStatusEvent {
  phase: UpdatePhase
  message?: string
}

// ── Go backend binding surface (mirrors the bound App methods) ──
// Keep in sync with the Go `App` struct's frontend-bound methods.

export interface AppBindings {
  GetVersion(): Promise<string>
  GetHubURL(): Promise<string>
  GetCodeCharset(): Promise<string>
  GetCodePrefix(): Promise<string>
  ValidateCode(code: string): Promise<ValidateResult>
  CheckCode(code: string): Promise<CodeCheckResult>
  Activate(code: string): Promise<ActivateResult>
  IsActivated(): Promise<boolean>
  Connect(): Promise<OpResult>
  Disconnect(): Promise<OpResult>
  GetStatus(): Promise<StatusResult>
  CheckForUpdate(): Promise<UpdateCheckResult>
  ApplyUpdate(): Promise<UpdateResult>
  GetDiagnostics(): Promise<string>
}
