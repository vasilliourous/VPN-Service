// MyVPN reactive state store (composable).
// Manages all shared state between Vue components.

import { reactive, readonly } from 'vue'
import * as bridge from '@/lib/bridge'
import type { StatusResult, UpdateCheckResult } from '@/types'

interface State {
  // Connection
  connected: boolean
  tier: string
  state: string
  failures: number
  graceDays: number
  tunnelOk: boolean // watchdog tunnel health

  // Activation
  activated: boolean
  activationError: string

  // UI
  loading: boolean
  connecting: boolean
  error: string
  diagnostics: string
  version: string

  // Update
  updateAvailable: boolean
  updateVersion: string
  updateUrl: string
  updateSha256: string
}

const state = reactive<State>({
  connected: false,
  tier: '',
  state: 'stopped',
  failures: 0,
  graceDays: 7,
  tunnelOk: false,
  activated: false,
  activationError: '',
  loading: false,
  connecting: false,
  error: '',
  diagnostics: '',
  version: '',
  updateAvailable: false,
  updateVersion: '',
  updateUrl: '',
  updateSha256: '',
})

function setError(msg: string): void {
  state.error = msg
}

function clearError(): void {
  state.error = ''
}

async function refreshStatus(): Promise<void> {
  try {
    const s: StatusResult = await bridge.getStatus()
    state.connected = s.connected
    state.tier = s.tier
    state.state = s.state
    state.failures = s.failures
    state.graceDays = s.graceDays
    state.tunnelOk = s.tunnelOk
  } catch (err: any) {
    setError(err?.message || 'Failed to get status')
  }
}

async function checkActivated(): Promise<void> {
  try {
    state.activated = await bridge.isActivated()
    if (state.activated) {
      await refreshStatus()
    }
  } catch (err: any) {
    state.error = err?.message || 'Failed to check activation'
  }
}

async function connect(): Promise<string | null> {
  if (state.connecting) return null // debounce double-taps / double clicks
  if (state.connected) return null
  state.connecting = true
  state.loading = true
  state.error = ''
  try {
    const result = await bridge.connect()
    if (result.success) {
      state.connected = true
      await refreshStatus()
      return null
    }
    setError(result.message)
    return result.message
  } catch (err: any) {
    const msg = err?.message || 'Connection failed'
    setError(msg)
    return msg
  } finally {
    state.connecting = false
    state.loading = false
  }
}

async function disconnect(): Promise<void> {
  if (!state.connected && !state.loading) return
  state.loading = true
  try {
    await bridge.disconnect()
    state.connected = false
    state.tunnelOk = false
    await refreshStatus()
  } catch (err: any) {
    setError(err?.message || 'Disconnect failed')
  } finally {
    state.loading = false
  }
}

async function activate(code: string): Promise<string | null> {
  if (state.loading) return null // debounce
  state.loading = true
  state.activationError = ''
  try {
    const result = await bridge.activate(code)
    if (result.success) {
      state.activated = true
      state.tier = result.tier || ''
      await refreshStatus()
      // Auto-connect immediately so the student is protected the moment they
      // activate — surface the reason if it fails rather than being silent.
      const connectErr = await connect()
      if (connectErr) {
        state.activationError = `Activated, but could not connect: ${connectErr}`
        return `Activated. Connect error: ${connectErr}`
      }
      return null
    }
    state.activationError = result.message
    return result.message
  } catch (err: any) {
    state.activationError = err?.message || 'Activation failed'
    return err?.message || 'Activation failed'
  } finally {
    state.loading = false
  }
}

async function checkUpdate(): Promise<void> {
  try {
    const result: UpdateCheckResult = await bridge.checkForUpdate()
    state.updateAvailable = result.available
    if (result.available) {
      state.updateVersion = result.version || ''
      state.updateUrl = result.url || ''
      state.updateSha256 = result.sha256 || ''
    }
  } catch {
    // Updates are non-critical — never block the UI over a failed check.
  }
}

async function loadDiagnostics(): Promise<void> {
  try {
    state.diagnostics = await bridge.getDiagnostics()
  } catch (err: any) {
    state.diagnostics = 'Failed to load diagnostics: ' + (err?.message || 'unknown error')
  }
}

async function loadVersion(): Promise<void> {
  try {
    state.version = await bridge.getVersion()
  } catch {
    // best effort
  }
}

// ── Setup / teardown ──

export function setupEventListeners(): void {
  bridge.onStatusChanged((status: StatusResult) => {
    state.connected = status.connected
    state.tier = status.tier
    state.state = status.state
    state.failures = status.failures
    state.graceDays = status.graceDays
    state.tunnelOk = status.tunnelOk
  })

  bridge.onUpdateAvailable((event) => {
    state.updateAvailable = true
    state.updateVersion = event.version
    state.updateUrl = event.url
    state.updateSha256 = event.sha256
  })

  // Proactively refresh status when the window regains focus — the tunnel may
  // have changed (reconnected, degraded, recovered) while unfocused.
  window.addEventListener('focus', refreshStatus)

  // Initial data load.
  void loadVersion()
}

export function tearDownEventListeners(): void {
  window.removeEventListener('focus', refreshStatus)
  bridge.removeAllListeners()
}

// refreshNow is a manual, user-initiated status refresh (e.g. from a pull).
export async function refreshNow(): Promise<void> {
  await refreshStatus()
}

// ── Export ──

export function useVPN() {
  return {
    state: readonly(state),
    refreshStatus,
    refreshNow,
    checkActivated,
    connect,
    disconnect,
    activate,
    checkUpdate,
    loadDiagnostics,
    setupEventListeners,
    tearDownEventListeners,
    clearError,
  }
}
