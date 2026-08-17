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
  StatusResult,
  OpResult,
  UpdateCheckResult,
  UpdateResult,
  UpdateStatusEvent,
  AppBindings,
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

// Wails's own RPC timeout (see wails_options_other). We use a shorter one so
// the UI can react promptly instead of waiting for the 30s default before
// showing an error.
const RPC_TIMEOUT_MS = 10_000

// wrap guards a single Wails call: rejects if it exceeds RPC_TIMEOUT_MS and
// coerces any rejection to a readable Error.
async function wrap<T>(run: () => Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(
        () => reject(new Error('Request timed out — the backend did not respond in time')),
        RPC_TIMEOUT_MS,
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
  return wrap(() => go().ValidateCode(code))
}

export async function activate(code: string): Promise<ActivateResult> {
  return wrap(() => go().Activate(code))
}

export async function isActivated(): Promise<boolean> {
  return wrap(() => go().IsActivated())
}

export async function connect(): Promise<OpResult> {
  return wrap(() => go().Connect())
}

export async function disconnect(): Promise<OpResult> {
  return wrap(() => go().Disconnect())
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
