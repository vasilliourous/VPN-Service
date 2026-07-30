// MyVPN reactive state store (composable)
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

  // Activation
  activated: boolean
  activationError: string

  // UI
  loading: boolean
  error: string
  diagnostics: string

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
  activated: false,
  activationError: '',
  loading: false,
  error: '',
  diagnostics: '',
  updateAvailable: false,
  updateVersion: '',
  updateUrl: '',
  updateSha256: '',
})

// ── Actions ──

async function refreshStatus(): Promise<void> {
  try {
    const s: StatusResult = await bridge.getStatus()
    state.connected = s.connected
    state.tier = s.tier
    state.state = s.state
    state.failures = s.failures
    state.graceDays = s.graceDays
  } catch (err: any) {
    state.error = err.message || 'Failed to get status'
  }
}

async function checkActivated(): Promise<void> {
  try {
    state.activated = await bridge.isActivated()
    if (state.activated) {
      await refreshStatus()
    }
  } catch (err: any) {
    state.error = err.message || 'Failed to check activation'
  }
}

async function connect(): Promise<string | null> {
  state.loading = true
  try {
    const result = await bridge.connect()
    if (result.success) {
      state.connected = true
      state.error = ''
      await refreshStatus()
      return null
    }
    return result.message
  } catch (err: any) {
    return err.message || 'Connection failed'
  } finally {
    state.loading = false
  }
}

async function disconnect(): Promise<void> {
  state.loading = true
  try {
    await bridge.disconnect()
    state.connected = false
    await refreshStatus()
  } catch (err: any) {
    state.error = err.message || 'Disconnect failed'
  } finally {
    state.loading = false
  }
}

async function activate(code: string): Promise<string | null> {
  state.loading = true
  state.activationError = ''
  try {
    const result = await bridge.activate(code)
    if (result.success) {
      state.activated = true
      state.tier = result.tier || ''
      await refreshStatus()
      return null
    }
    state.activationError = result.message
    return result.message
  } catch (err: any) {
    state.activationError = err.message || 'Activation failed'
    return err.message || 'Activation failed'
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
    // Silently fail — updates are non-critical
  }
}

async function loadDiagnostics(): Promise<void> {
  try {
    state.diagnostics = await bridge.getDiagnostics()
  } catch (err: any) {
    state.diagnostics = 'Failed to load diagnostics: ' + (err.message || 'unknown error')
  }
}

// ── Setup event listeners ──

function setupEventListeners(): void {
  bridge.onStatusChanged((status: StatusResult) => {
    state.connected = status.connected
    state.tier = status.tier
    state.state = status.state
    state.failures = status.failures
    state.graceDays = status.graceDays
  })

  bridge.onUpdateAvailable((event) => {
    state.updateAvailable = true
    state.updateVersion = event.version
    state.updateUrl = event.url
    state.updateSha256 = event.sha256
  })
}

// ── Export ──

export function useVPN() {
  return {
    state: readonly(state),
    refreshStatus,
    checkActivated,
    connect,
    disconnect,
    activate,
    checkUpdate,
    loadDiagnostics,
    setupEventListeners,
  }
}
