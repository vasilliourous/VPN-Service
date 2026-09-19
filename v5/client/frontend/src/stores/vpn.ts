// Locus reactive state store (composable).
// Manages all shared state between Vue components.

import { reactive, readonly } from 'vue'
import * as bridge from '@/lib/bridge'
import type { StatusResult, UpdateCheckResult, UpdatePhase, UpdateStatus, FailureKind } from '@/types'

// offlineMessage mirrors the constant in lib/bridge (re-exported there for the
// components); kept as a single source via import so wording never drifts.
import { classifyFailure, offlineMessage } from '@/lib/bridge'

interface State {
  // Connection
  connected: boolean
  tier: string
  state: string
  failures: number
  graceDays: number
  tunnelOk: boolean // watchdog tunnel health
  repairStage: string // watchdog recovery stage: '' | 'restart' | 'full-reset' | 'degraded'

  // Activation
  activated: boolean
  activationError: string

  // UI
  loading: boolean
  connecting: boolean
  // reconnectRequested is true while we are waiting for the backend to bring the
  // tunnel up after the watchdog gave up (or after a manual Retry). It keeps the
  // adaptive button reading "Reconnecting…" instead of looking idle.
  reconnectRequested: boolean
  // connectStage drives staged progress text while connecting:
  // 'idle' | 'starting' (engine launching) | 'waiting' (engine up, awaiting the
  // watchdog's first traffic probe).
  connectStage: string
  error: string
  // lastErrorKind classifies the most recent failure so the UI can show the
  // student an actionable step (e.g. "check your wi-fi") rather than a
  // transient toast that has already dismissed itself.
  lastErrorKind: FailureKind
  // lastActionable is the persistent, non-dismissed explanation of the last
  // failure. Unlike `error` (auto-dismissed toast) it survives until the next
  // successful action, so a student who looked away still sees why it failed.
  lastActionable: string
  diagnostics: string
  version: string

  // Update
  updateAvailable: boolean
  updateVersion: string
  updateUrl: string
  updateSha256: string
  // Background apply flow (ApplyUpdate): 'idle' until a download starts.
  updatePhase: UpdatePhase
  updateMessage: string
  // updateStatus/updateReason explain the most recent check. "No update
  // available" used to be the only information the UI had, which made an
  // unconfigured server, an offline laptop and a fully-patched client look
  // identical. The reason string is shown verbatim.
  updateStatus: UpdateStatus
  updateReason: string
  // updateFrom/updateTo name the versions in play so the banner can read
  // "2.0.0 → 2.1.0" during an apply.
  updateFrom: string
  updateTo: string
  // platform is the artifact this client would fetch (e.g. "windows/amd64").
  // Shown in the footer so support can tell which build a student is running
  // without asking them to open Diagnostics.
  platform: string
}

const state = reactive<State>({
  connected: false,
  tier: '',
  state: 'stopped',
  failures: 0,
  graceDays: 7,
  tunnelOk: false,
  repairStage: '',
  activated: false,
  activationError: '',
  loading: false,
  connecting: false,
  reconnectRequested: false,
  connectStage: 'idle',
  error: '',
  lastErrorKind: 'unknown',
  lastActionable: '',
  diagnostics: '',
  version: '',
  updateAvailable: false,
  updateVersion: '',
  updateUrl: '',
  updateSha256: '',
  updatePhase: 'idle',
  updateMessage: '',
  updateStatus: 'no_release',
  updateReason: '',
  updateFrom: '',
  updateTo: '',
  platform: '',
})

// setFailure records a failure in both places the UI needs it: the transient
// toast (`error`, auto-dismissed by App.vue) and the persistent explanation
// (`lastActionable`) that stays put until the next successful action. Offline
// failures get the student-facing wording instead of the raw dial error.
function setFailure(msg: string): void {
  const kind = classifyFailure(msg)
  state.lastErrorKind = kind
  const friendly = kind === 'offline' ? offlineMessage : msg
  state.error = friendly
  state.lastActionable = friendly
}

// clearFailure is called when an action succeeds — it retires the persistent
// explanation so a stale "check your wi-fi" banner cannot linger after a later
// successful connect.
function clearFailure(): void {
  state.lastActionable = ''
  state.lastErrorKind = 'unknown'
}

function clearError(): void {
  state.error = ''
}

// refreshSeq guards against a slow in-flight refreshStatus() resolving AFTER a
// newer event-driven status update and clobbering it with stale data. Each call
// takes a ticket; only the newest ticket is allowed to commit.
let refreshSeq = 0

async function refreshStatus(): Promise<void> {
  const ticket = ++refreshSeq
  try {
    const s: StatusResult = await bridge.getStatus()
    if (ticket !== refreshSeq) return // a newer refresh already won — drop stale
    applyStatus(s)
  } catch (err: any) {
    if (ticket !== refreshSeq) return
    setFailure(err?.message || 'Failed to get status')
  }
}

// applyStatus copies a StatusResult into reactive state. Single place so the
// polling path and the event path can never drift (previously the event handler
// forgot repairStage, freezing the "Repairing…" label).
function applyStatus(s: StatusResult): void {
  state.connected = s.connected
  state.tier = s.tier
  state.state = s.state
  state.failures = s.failures
  state.graceDays = s.graceDays
  state.tunnelOk = s.tunnelOk
  state.repairStage = s.repairStage || ''
  // Once the backend reports a healthy tunnel, any pending reconnect request
  // has been fulfilled — stop showing "Reconnecting…".
  if (state.connected && state.tunnelOk) {
    state.reconnectRequested = false
    state.connectStage = 'idle'
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
  state.connectStage = 'starting'
  try {
    const result = await bridge.connect()
    if (result.success) {
      state.connected = true
      // A fresh connect invalidates any previous degraded/repair state — clear
      // it so a stale "Repairing…" banner cannot survive a user-initiated
      // reconnect.
      state.repairStage = ''
      state.tunnelOk = false
      // NOTE: reconnectRequested is deliberately NOT cleared here. connect()
      // returning success only means the engine started — the tunnel is not
      // proven healthy until the watchdog's first probe lands (which arrives as
      // a status:changed event and clears the flag in applyStatus). Leaving it
      // set keeps the button on "Reconnecting…" until we actually are connected,
      // instead of briefly claiming success and then falling back to Repairing.
      state.connectStage = 'waiting'
      clearFailure()
      await refreshStatus()
      return null
    }
    setFailure(result.message)
    return result.message
  } catch (err: any) {
    const msg = err?.message || 'Connection failed'
    setFailure(msg)
    return msg
  } finally {
    state.connecting = false
    state.loading = false
    state.connectStage = 'idle'
  }
}

// retryConnect is the adaptive-button path used once the watchdog has given up
// (degraded) or a previous attempt failed. It forces a clean reconnect and
// flags reconnectRequested so the button reads "Reconnecting…" until the
// backend reports a healthy tunnel.
async function retryConnect(): Promise<string | null> {
  if (state.connecting) return null
  // If the backend still believes it is connected (e.g. wedged), tear it down
  // first so Connect() starts from a clean engine rather than no-op'ing on
  // "Already connected".
  if (state.connected) {
    try {
      await bridge.disconnect()
    } catch {
      // best effort — fall through to connect, which auto-cleans leftovers
    }
    state.connected = false
  }
  state.reconnectRequested = true
  const err = await connect()
  if (err) {
    // connect() already recorded the failure; make sure we are not stuck
    // showing "Reconnecting…" after a hard failure.
    state.reconnectRequested = false
  }
  return err
}

async function disconnect(): Promise<void> {
  if (!state.connected && !state.loading) return
  state.loading = true
  try {
    await bridge.disconnect()
    state.connected = false
    state.tunnelOk = false
    // A deliberate disconnect ends any recovery-in-progress or pending retry.
    state.repairStage = ''
    state.reconnectRequested = false
    clearFailure()
    await refreshStatus()
  } catch (err: any) {
    setFailure(err?.message || 'Disconnect failed')
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
    // Record why, so the UI can explain a negative result instead of silently
    // doing nothing (the previous behaviour, which made every "no update"
    // cause look identical).
    state.updateStatus = result.status || (result.available ? 'available' : 'no_release')
    state.updateReason = result.reason || ''
    if (result.platform) {
      state.platform = result.platform
    }
    if (result.currentVersion) {
      // Trust the backend's own report of the running version over the
      // one loaded at startup — they should agree, and this keeps the
      // footer honest if a check races a rebuild.
      state.version = result.currentVersion
    }
    if (result.available) {
      state.updateVersion = result.version || ''
      state.updateUrl = result.url || ''
      state.updateSha256 = result.sha256 || ''
    }
  } catch (err: any) {
    // Updates are non-critical — never block the UI over a failed check, but
    // do record that the check itself failed so the UI does not imply
    // "up to date".
    state.updateStatus = 'unreachable'
    state.updateReason = err?.message || 'Could not check for updates.'
  }
}

async function applyUpdate(): Promise<string | null> {
  if (state.updatePhase === 'downloading' || state.updatePhase === 'verifying' || state.updatePhase === 'applying') {
    return 'An update is already being applied'
  }
  try {
    const result = await bridge.applyUpdate()
    if (!result.success) {
      setFailure(result.message)
      return result.message
    }
    // The backend drives progress via update:status from here on.
    return null
  } catch (err: any) {
    const msg = err?.message || 'Update failed to start'
    setFailure(msg)
    return msg
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
    // A pushed status is newer than any in-flight poll — bump the sequence so a
    // slow refreshStatus() resolving later cannot roll us back to stale data.
    refreshSeq++
    applyStatus(status)
  })

  bridge.onUpdateAvailable((event) => {
    state.updateAvailable = true
    state.updateStatus = 'available'
    state.updateVersion = event.version
    state.updateUrl = event.url
    state.updateSha256 = event.sha256
    state.updateFrom = state.version
    state.updateTo = event.version
    // A heartbeat-delivered signal is a positive result; clear any stale
    // negative reason from an earlier check.
    state.updateReason = ''
  })

  bridge.onUpdateStatus((event) => {
    state.updatePhase = event.phase
    state.updateMessage = event.message || ''
    // Carry the version pair through the whole apply flow so the banner can
    // read "2.0.0 → 2.1.0" instead of a bare phase.
    if (event.from) state.updateFrom = event.from
    if (event.to) {
      state.updateTo = event.to
      state.updateVersion = event.to
    }
    if (event.phase === 'applied') {
      // The backend quits ~1.5s after emitting this — the forked new binary
      // takes over. Clear the stale availability flag so a re-render during
      // the restart window doesn't offer the update again.
      state.updateAvailable = false
    } else if (event.phase === 'failed') {
      setFailure(event.message || 'Update failed')
    }
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
    retryConnect,
    disconnect,
    activate,
    checkUpdate,
    applyUpdate,
    loadDiagnostics,
    setupEventListeners,
    tearDownEventListeners,
    clearError,
  }
}
