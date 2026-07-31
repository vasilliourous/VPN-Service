// Bridge — typed wrapper around Wails runtime bindings.
//
// Wails v2 exposes bound Go methods on `window.go.main.App.<Method>(...)`
// (the backend injects this map at startup — the generated `wailsjs/`
// files are only optional IDE helpers) and runtime helpers on
// `window.runtime`. This module provides typed wrappers so the Vue
// components don't touch the window objects directly.
//
// See: https://wails.io/docs/howdoesitwork

import type {
  ValidateResult,
  ActivateResult,
  StatusResult,
  OpResult,
  UpdateCheckResult,
} from '@/types'

// Get the Go binding namespace — works in both dev and production
const go = (): any => {
  const ns = (window as any).go
  if (!ns || !ns.main || !ns.main.App) {
    throw new Error('Wails Go bindings not found — build with the wails build tags')
  }
  return ns.main.App
}

// Get the Wails runtime namespace (events, window controls, …)
const runtime = (): any => {
  const rt = (window as any).runtime
  if (!rt) {
    throw new Error('Wails runtime not found')
  }
  return rt
}

export async function getVersion(): Promise<string> {
  return go().GetVersion()
}

export async function getHubURL(): Promise<string> {
  return go().GetHubURL()
}

export async function getCodeCharset(): Promise<string> {
  return go().GetCodeCharset()
}

export async function getCodePrefix(): Promise<string> {
  return go().GetCodePrefix()
}

export async function validateCode(code: string): Promise<ValidateResult> {
  return go().ValidateCode(code)
}

export async function activate(code: string): Promise<ActivateResult> {
  return go().Activate(code)
}

export async function isActivated(): Promise<boolean> {
  return go().IsActivated()
}

export async function connect(): Promise<OpResult> {
  return go().Connect()
}

export async function disconnect(): Promise<OpResult> {
  return go().Disconnect()
}

export async function getStatus(): Promise<StatusResult> {
  return go().GetStatus()
}

export async function checkForUpdate(): Promise<UpdateCheckResult> {
  return go().CheckForUpdate()
}

export async function getDiagnostics(): Promise<string> {
  return go().GetDiagnostics()
}

// ── Event listeners (Go → frontend notifications) ──

export function onStatusChanged(callback: (status: StatusResult) => void): void {
  runtime().EventsOn('status:changed', (data: any) => {
    callback(data as StatusResult)
  })
}

export function onUpdateAvailable(callback: (event: { version: string; url: string; sha256: string }) => void): void {
  runtime().EventsOn('update:available', (data: any) => {
    callback(data)
  })
}

export function removeAllListeners(event: string): void {
  runtime().EventsOff(event)
}
