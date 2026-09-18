// Bridge — typed wrapper around Wails runtime bindings.
//
// Wails v2 exposes bound Go methods on `window.go.main.App.<Method>(...)`
// and runtime helpers on `window.runtime`. This module provides typed,
// timeout-aware wrappers so the Vue components never touch globals and any
// call that hangs (Wails falls back to a 30s default) or rejects is surfaced
// as a plain, predictable failure instead of an unhandled rejection.

import type {
  ValidateResult,
  ActivateResult,
  CodeCheckResult,
  StatusResult,
  OpResult,
  UpdateCheckResult,
  UpdateResult,
  UpdateStatusEvent,
  AppBindings,
  FailureKind,
} from '@/types'

// Go bindings are injected by Wails at `window.go.main.App`. The precise
// signatures are declared in env.d.ts; we narrow them here to our public types.
function go() {
  const ns = (window as any).go
  if (!ns || !ns.main || !ns.main.App) {
    throw new Error('Wails Go bindings not found — build with the wails build tags')
  }
  return ns.main.App as AppBindings
}

const runtime = () => {
  const rt = (window as any).runtime
  if (!rt) {
    throw new Error('Wails runtime not found')
  }
  return rt
}

// RPC timeout floors, per call shape.
//
// These MUST exceed the backend's own context deadline for the matching
// operation, otherwise the frontend rejects a call that is still legitimately
// running and reports a false "timed out" — which is exactly what happened
// when a single 10s limit sat under Activate's 30s server round-trip.
//   quick    — local reads (status, version, diagnostics): fail fast.
//   standard — Connect / Disconnect (backend startup deadline is 15s; stop has
//              a 2s graceful + 2s wait grace, so 20s is ample headroom).
//   long     — Activate (30s server round-trip plus retry/backoff) and the
//              code lookup (10s) which also retries.
const RPC_TIMEOUT_QUICK_MS = 8_000
const RPC_TIMEOUT_STANDARD_MS = 20_000
const RPC_TIMEOUT_LONG_MS = 40_000

// wrap guards a single Wails call: rejects if it exceeds the supplied timeout
// and coerces any rejection to a readable Error.
async function wrap<T>(run: () => Promise<T>, timeoutMs: number = RPC_TIMEOUT_QUICK_MS): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(
        () => reject(new Error('Request timed out — the backend did not respond in time')),
        timeoutMs,
      )
    })
    return await Promise.race([run(), timeout])
  } catch (e) {
    throw e instanceof Error ? e : new Error(String(e))
  } finally {
    if (timer) clearTimeout(timer)
  }
}

export async function getVersion(): Promise<string> {
  return wrap(() => go().GetVersion())
}

export async function getHubURL(): Promise<string> {
  return wrap(() => go().GetHubURL())
}

export async function getCodeCharset(): Promise<string> {
  return wrap(() => go().GetCodeCharset())
}

export async function getCodePrefix(): Promise<string> {
  return wrap(() => go().GetCodePrefix())
}

export async function validateCode(code: string): Promise<ValidateResult> {
  // Local-only Luhn checksum — fast.
  return wrap(() => go().ValidateCode(code), RPC_TIMEOUT_QUICK_MS)
}

// checkCode asks the hub whether the code actually exists (read-only).
export async function checkCode(code: string): Promise<CodeCheckResult> {
  // Server round-trip with retry/backoff on the Go side.
  return wrap(() => go().CheckCode(code), RPC_TIMEOUT_LONG_MS)
}

export async function activate(code: string): Promise<ActivateResult> {
  // Activate's backend context deadline is 30s plus retry backoff.
  return wrap(() => go().Activate(code), RPC_TIMEOUT_LONG_MS)
}

export async function isActivated(): Promise<boolean> {
  return wrap(() => go().IsActivated())
}

export async function connect(): Promise<OpResult> {
  // Backend startup deadline is 15s.
  return wrap(() => go().Connect(), RPC_TIMEOUT_STANDARD_MS)
}

export async function disconnect(): Promise<OpResult> {
  // Stop is bounded by a 2s graceful window + 2s wait grace.
  return wrap(() => go().Disconnect(), RPC_TIMEOUT_STANDARD_MS)
}

export async function getStatus(): Promise<StatusResult> {
  return wrap(() => go().GetStatus())
}

export async function checkForUpdate(): Promise<UpdateCheckResult> {
  return wrap(() => go().CheckForUpdate())
}

export async function applyUpdate(): Promise<UpdateResult> {
  return wrap(() => go().ApplyUpdate())
}

export async function getDiagnostics(): Promise<string> {
  return wrap(() => go().GetDiagnostics())
}

// ── Failure classification ──

// classifyFailure maps a raw backend/RPC error string to a coarse kind so the
// UI can show the student an actionable next step instead of a Go error dump.
//
// The patterns mirror the errors that actually reach the UI from the connect
// path in this repo:
//   offline  — net.Dial failures from ProbeTunnel/Start when the laptop has no
//              usable connection ("no such host", "i/o timeout", unreachable…)
//   elevation— Windows TUN "Access is denied" / admin refusal (app.go Connect)
//   engine   — sing-box exited / could not start / config generation
//   server   — the server answered but rejected us (auth/config mismatch)
// Anything unrecognised falls back to 'unknown' — we never invent a cause.
export function classifyFailure(message: string): FailureKind {
  const m = (message || '').toLowerCase()

  if (
    m.includes('no such host') ||
    m.includes('no route to host') ||
    m.includes('network is unreachable') ||
    m.includes('i/o timeout') ||
    m.includes('connection timed out') ||
    m.includes('temporary failure in name resolution') ||
    m.includes('server misbehaving') ||
    m.includes('dial tcp') && m.includes('timeout')
  ) {
    return 'offline'
  }

  if (
    m.includes('access is denied') ||
    m.includes('administrator') ||
    m.includes('elevation') ||
    m.includes('permission')
  ) {
    return 'elevation'
  }

  if (
    m.includes('sing-box') ||
    m.includes('cannot generate config') ||
    m.includes('tun interface') ||
    m.includes('engine')
  ) {
    return 'engine'
  }

  if (
    m.includes('timed out — the backend') ||
    m.includes('connection refused') ||
    m.includes('rejected') ||
    m.includes('authentication') ||
    m.includes('not activated')
  ) {
    return 'server'
  }

  return 'unknown'
}

// offlineMessage is the student-facing wording for a local connectivity
// failure. Kept here so the phrasing is consistent between the store and any
// component that classifies a failure.
export const offlineMessage =
  "Couldn't reach the secure server — check your internet connection or school wi-fi, then try again."

// ── Event listeners (Go → frontend notifications) ──

// A single bridge-level listener per event is registered with Wails; Vue
// components subscribe through `*Listeners`. This means re-mounts (HMR, nav)
// do NOT stack Go-bound event handlers — they simply add/remove JS callbacks.
let registeredStatus = false
let registeredUpdate = false
let registeredUpdateStatus = false
let statusListeners: Array<(s: StatusResult) => void> = []
let updateListeners: Array<(e: { version: string; url: string; sha256: string }) => void> = []
let updateStatusListeners: Array<(e: UpdateStatusEvent) => void> = []

export function onStatusChanged(callback: (status: StatusResult) => void): void {
  statusListeners.push(callback)
  if (registeredStatus) return
  registeredStatus = true
  runtime().EventsOn('status:changed', (data: any) => {
    const s = data as StatusResult
    statusListeners.forEach((cb) => {
      try {
        cb(s)
      } catch {
        // A misbehaving listener must not break the others or the app.
      }
    })
  })
}

export function onUpdateAvailable(callback: (e: { version: string; url: string; sha256: string }) => void): void {
  updateListeners.push(callback)
  if (registeredUpdate) return
  registeredUpdate = true
  runtime().EventsOn('update:available', (data: any) => {
    const e = data as { version: string; url: string; sha256: string }
    updateListeners.forEach((cb) => {
      try {
        cb(e)
      } catch {
        // ignore per-listener failures
      }
    })
  })
}

// onUpdateStatus subscribes to the background apply-update progress event
// emitted by App.ApplyUpdate (downloading → verifying → applying → applied /
// failed). Same single-Go-listener pattern as the other events.
export function onUpdateStatus(callback: (e: UpdateStatusEvent) => void): void {
  updateStatusListeners.push(callback)
  if (registeredUpdateStatus) return
  registeredUpdateStatus = true
  runtime().EventsOn('update:status', (data: any) => {
    const e = data as UpdateStatusEvent
    updateStatusListeners.forEach((cb) => {
      try {
        cb(e)
      } catch {
        // ignore per-listener failures
      }
    })
  })
}

export function unsubscribeUpdateStatus(cb: (e: UpdateStatusEvent) => void): void {
  updateStatusListeners = updateStatusListeners.filter((c) => c !== cb)
  if (updateStatusListeners.length === 0 && registeredUpdateStatus) {
    registeredUpdateStatus = false
    runtime().EventsOff('update:status')
  }
}

// unsubscribe removes a single listener. The Wails event stays registered as
// long as at least one listener remains, and is fully removed when none do.
export function unsubscribeStatus(cb: (s: StatusResult) => void): void {
  statusListeners = statusListeners.filter((c) => c !== cb)
  if (statusListeners.length === 0 && registeredStatus) {
    registeredStatus = false
    runtime().EventsOff('status:changed')
  }
}

export function unsubscribeUpdate(cb: (e: { version: string; url: string; sha256: string }) => void): void {
  updateListeners = updateListeners.filter((c) => c !== cb)
  if (updateListeners.length === 0 && registeredUpdate) {
    registeredUpdate = false
    runtime().EventsOff('update:available')
  }
}

// removeAllListeners unregisters every bridge listener and the underlying Go
// event handlers. Call this on app teardown / HMR dispose.
export function removeAllListeners(): void {
  statusListeners = []
  updateListeners = []
  updateStatusListeners = []
  if (registeredStatus) {
    registeredStatus = false
    runtime().EventsOff('status:changed')
  }
  if (registeredUpdate) {
    registeredUpdate = false
    runtime().EventsOff('update:available')
  }
  if (registeredUpdateStatus) {
    registeredUpdateStatus = false
    runtime().EventsOff('update:status')
  }
}

// copyToClipboard copies text with a robust fallback for environments where
// navigator.clipboard is unavailable (older webviews, non-secure contexts).
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to Wails runtime
  }
  try {
    await runtime().ClipboardSetText(text)
    return true
  } catch {
    return false
  }
}
