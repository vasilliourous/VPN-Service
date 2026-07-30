// Bridge — typed wrapper around Wails runtime calls.
//
// Wails automatically binds methods from the Go App struct and exposes them
// via window.runtime. This module provides typed wrappers so the Vue components
// don't call window.runtime directly.
//
// See: https://wails.io/docs/next/howdoesitwork

import type {
  ValidateResult,
  ActivateResult,
  StatusResult,
  OpResult,
  UpdateCheckResult,
} from '@/types'

// Get Go runtime bridge — works in both dev and production
const getBridge = (): any => {
  // Wails v2 injects runtime functions into the window
  return (window as any).runtime
}

export async function getVersion(): Promise<string> {
  return getBridge().Call('GetVersion')
}

export async function getHubURL(): Promise<string> {
  return getBridge().Call('GetHubURL')
}

export async function getCodeCharset(): Promise<string> {
  return getBridge().Call('GetCodeCharset')
}

export async function getCodePrefix(): Promise<string> {
  return getBridge().Call('GetCodePrefix')
}

export async function validateCode(code: string): Promise<ValidateResult> {
  return getBridge().Call('ValidateCode', code)
}

export async function activate(code: string): Promise<ActivateResult> {
  return getBridge().Call('Activate', code)
}

export async function isActivated(): Promise<boolean> {
  return getBridge().Call('IsActivated')
}

export async function connect(): Promise<OpResult> {
  return getBridge().Call('Connect')
}

export async function disconnect(): Promise<OpResult> {
  return getBridge().Call('Disconnect')
}

export async function getStatus(): Promise<StatusResult> {
  return getBridge().Call('GetStatus')
}

export async function checkForUpdate(): Promise<UpdateCheckResult> {
  return getBridge().Call('CheckForUpdate')
}

export async function getDiagnostics(): Promise<string> {
  return getBridge().Call('GetDiagnostics')
}

// ── Event listeners (Go → frontend notifications) ──

export function onStatusChanged(callback: (status: StatusResult) => void): void {
  getBridge().EventsOn('status:changed', (data: any) => {
    callback(data as StatusResult)
  })
}

export function onUpdateAvailable(callback: (event: { version: string; url: string; sha256: string }) => void): void {
  getBridge().EventsOn('update:available', (data: any) => {
    callback(data)
  })
}

export function removeAllListeners(event: string): void {
  getBridge().EventsOff(event)
}
