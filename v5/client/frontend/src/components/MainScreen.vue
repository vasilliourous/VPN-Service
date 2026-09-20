<template>
  <div class="main">
    <!-- Header bar -->
    <div class="header">
      <div class="header-left">
        <StatusIndicator :state="statusState" />
        <span class="status-label">{{ statusLabel }}</span>
      </div>
      <TierBadge :tier="tier" />
    </div>

    <!-- Connection card -->
    <div class="card connection-card">
      <!-- Status circle -->
      <div :class="['status-circle', statusState]">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path v-if="!connected" d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
          <path v-else d="M22 12h-4l-3 9L9 3l-3 9H2"/>
        </svg>
      </div>

      <div class="status-text" aria-live="polite">{{ statusLabel }}</div>
      <div class="tier-label">{{ tierDisplay }}</div>

      <!-- Staged connect progress: tells the student what is happening during
           the (up to ~15s) engine startup instead of a bare spinner. -->
      <div v-if="connectNote" class="connect-note" aria-live="polite">{{ connectNote }}</div>

      <!-- Tunnel degraded warning (watchdog flagged no traffic passing) -->
      <div v-if="connected && !tunnelOk" class="degraded-warning">
        ⚠ {{ repairNote }}
        <span v-if="repairStage === 'degraded'">Open Diagnostics and copy the report if you need help.</span>
      </div>

      <!-- Grace period warning -->
      <div v-if="graceDays <= 3 && connected" class="grace-warning">
        ⚠ {{ graceDays }} day{{ graceDays === 1 ? '' : 's' }} remaining
      </div>

      <!-- Connect / Disconnect button. Adaptive: its label + colour + action
           change with state, so a tap is never a silent no-op. -->
      <button
        :class="['btn', primaryActionClass, 'btn-large']"
        :disabled="primaryDisabled"
        @click="toggleConnection"
      >
        <span v-if="loading || reconnectRequested" class="spinner"></span>
        <span v-else>{{ primaryLabel }}</span>
      </button>

      <!-- Persistent, actionable failure explanation. Unlike the auto-dismiss
           toast this remains until the next successful action, so a student
           who looked away still sees WHY it failed and what to do. -->
      <div v-if="lastActionable" class="actionable-error" role="alert">
        <span class="actionable-icon" aria-hidden="true">⚠</span>
        <div class="actionable-body">
          <span>{{ lastActionable }}</span>
          <span v-if="failureHint" class="actionable-hint">{{ failureHint }}</span>
        </div>
      </div>

    </div>

    <!-- Stats card -->
    <div class="card stats-card">
      <div class="stat-row">
        <span class="stat-label">State</span>
        <span :class="['stat-value', stateColor]">{{ stateLabel }}</span>
      </div>
      <div class="stat-row">
        <span class="stat-label">Heartbeat</span>
        <span :class="['stat-value', failures > 0 ? 'stat-warn' : 'stat-ok']">
          {{ failures > 0 ? failures + ' failures' : 'OK' }}
        </span>
      </div>
      <div class="stat-row">
        <span class="stat-label">Grace Period</span>
        <span class="stat-value">{{ graceDays }} days</span>
      </div>
    </div>

    <!-- Actions -->
    <div class="actions">
      <button class="btn btn-secondary" @click="handleDiagnostics">
        Diagnostics
      </button>
      <!-- Check for updates.
           A negative result must be *visible*: the previous UI only rendered
           anything when an update existed, so "check for updates" appeared to
           do nothing at all. The result line below states the outcome (and the
           reason) in plain language. -->
      <button
        class="btn btn-secondary"
        :disabled="checkingUpdate || updateActive"
        @click="handleCheckUpdate"
      >
        <span v-if="checkingUpdate" class="spinner"></span>
        <span v-else>Check for updates</span>
      </button>
      <!-- Update button: applies the staged update when one is available. While
           the background apply flow runs it shows progress; the backend quits
           the app after applying so the forked new binary takes over. -->
      <button
        v-if="updateAvailable || updatePhase !== 'idle'"
        class="btn btn-accent"
        :disabled="updateActive"
        @click="handleApplyUpdate"
      >
        <span v-if="updateActive" class="spinner"></span>
        <span v-else>Update {{ updateVersion }} available</span>
        <span v-if="updateActive">
          {{ updatePhase === 'applied' ? 'Restarting…' : (updateMessage || updateLabel) }}
        </span>
      </button>
    </div>

    <!-- Update check result. Rendered only after a check has run, so it never
         adds noise to the normal connect flow. -->
    <p v-if="updateNotice" class="update-notice" :class="updateNoticeClass">
      {{ updateNotice }}
    </p>

    <!-- Version footer (helps with support diagnostics in the field) -->
    <div class="version-footer" aria-label="App version">
      Locus{{ version ? ' v' + version : '' }}<span v-if="platform"> · {{ platform }}</span>
    </div>

    <!-- Diagnostics modal -->
    <div v-if="showDiagnostics" class="modal-overlay" @click.self="showDiagnostics = false">
      <div class="modal">
        <h3>Diagnostics</h3>
        <pre class="diagnostics-text">{{ diagnosticsText }}</pre>
        <div class="modal-actions">
          <button class="btn btn-secondary" @click="copyDiagnostics">
            {{ copied ? 'Copied!' : 'Copy' }}
          </button>
          <button class="btn btn-primary" @click="showDiagnostics = false">Close</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import StatusIndicator from './StatusIndicator.vue'
import TierBadge from './TierBadge.vue'
import * as bridge from '@/lib/bridge'
const emit = defineEmits<{
  connect: []
  disconnect: []
  'retry-connect': []
  'show-diagnostics': []
  'apply-update': []
  'check-update': []
}>()

const props = defineProps<{
  connected: boolean
  tier: string
  state: string
  graceDays: number
  failures: number
  tunnelOk: boolean
  repairStage: string
  connecting: boolean
  loading: boolean
  version: string
  updateAvailable: boolean
  updateVersion: string
  updatePhase: string
  updateMessage: string
  // updateStatus/updateReason explain the last update check; platform is the
  // artifact this client would fetch. Together they let a negative check say
  // *why* instead of rendering nothing.
  updateStatus: string
  updateReason: string
  platform: string
  // reconnectRequested / lastActionable come from the store so the button and
  // the persistent error banner stay in sync with live engine state.
  reconnectRequested: boolean
  // connectStage drives staged progress text while connecting.
  connectStage: string
  lastActionable: string
  // lastErrorKind is the classified failure kind from the store; drives the
  // actionable hint under the error text.
  lastErrorKind: string
}>()

const showDiagnostics = ref(false)
const diagnosticsText = ref('')
const copied = ref(false)
// checkingUpdate distinguishes "the check is in flight" from "the check
// returned nothing", so the button can show a spinner and the notice only
// appears once there is a real result.
const checkingUpdate = ref(false)
const lastCheckAt = ref('')

// updateNotice turns the backend's machine-readable update outcome into one
// plain sentence for the student. It stays empty until a check has actually
// run, so it never adds noise to the normal flow.
const updateNotice = computed(() => {
  if (checkingUpdate.value) return 'Checking for updates…'
  if (props.updatePhase === 'downloading' || props.updatePhase === 'verifying' || props.updatePhase === 'applying') {
    return props.updateMessage || 'Installing update…'
  }
  if (props.updatePhase === 'applied') return props.updateMessage || 'Update installed — restarting…'
  // A failed apply is reported through the error banner, not here.
  if (!lastCheckAt.value) return ''
  if (props.updateAvailable) {
    return `Update ${props.updateVersion} is available.`
  }
  switch (props.updateStatus) {
    case 'up_to_date':
      return props.updateReason || 'You are running the newest version.'
    case 'no_release':
      return props.updateReason || 'You are running the newest published version.'
    case 'unreachable':
      return 'Could not reach the update server — check your connection and try again.'
    case 'uninstrumented_build':
      return 'This is a development build, so update checks are informational only.'
    case 'no_asset':
      return props.updateReason || 'An update exists but has no build for this device.'
    case 'not_activated':
      return 'Activate Locus first — updates are delivered with your activation.'
    default:
      return props.updateReason || 'No update available.'
  }
})

// updateNoticeClass lets a "could not check" result read as a warning rather
// than a reassuring success.
const updateNoticeClass = computed(() => {
  if (props.updateAvailable) return 'update-notice--action'
  if (props.updateStatus === 'unreachable' || props.updateStatus === 'no_asset') return 'update-notice--warn'
  return ''
})

// A tunnel that is connected but not passing traffic (watchdog flagged it) is
// shown as degraded (amber) rather than a healthy green "Connected".
const statusState = computed(() => {
  if (props.connected) {
    return props.tunnelOk ? 'connected' : 'degraded'
  }
  if (props.state === 'crashed') return 'error'
  return 'disconnected'
})

const statusLabel = computed(() => {
  if (props.connected) {
    return props.tunnelOk ? 'Connected' : 'Repairing…'
  }
  if (props.connecting || props.reconnectRequested) return 'Connecting…'
  if (props.state === 'crashed') return 'Engine Error'
  return 'Disconnected'
})

// primaryLabel/primaryActionClass/primaryDisabled drive the single adaptive
// button. States, in priority order:
//   1. mid-flight connect (connecting/reconnectRequested) → disabled "Connecting…"
//   2. connected & healthy → "Disconnect" (danger)
//   3. connected but degraded, watchdog has given up → "Retry" (accent), forces
//      a clean reconnect rather than a silent no-op
//   4. connected but degraded, still recovering → "Stop repairing" (danger),
//      which disconnects. NOT disabled: see the note on primaryDisabled.
//   5. a previous attempt failed (lastActionable set) → "Retry"
//   6. idle → "Connect"
const recovering = computed(() => props.connected && !props.tunnelOk)

// connectNote explains which phase of connecting we are in, so the slow part
// (engine startup, then the first traffic probe) is visible rather than silent.
const connectNote = computed(() => {
  if (props.connecting || props.reconnectRequested) {
    switch (props.connectStage) {
      case 'starting':
        return 'Starting the secure tunnel…'
      case 'waiting':
        return 'Verifying the tunnel is passing traffic…'
      default:
        return 'Connecting…'
    }
  }
  if (recovering.value && props.repairStage !== 'degraded') {
    return 'Re-establishing the tunnel…'
  }
  return ''
})

const primaryLabel = computed(() => {
  if (props.connecting || props.reconnectRequested) return 'Reconnecting…'
  if (props.connected) {
    if (!props.tunnelOk) {
      // While the watchdog is mid-repair the button is a real, working CANCEL,
      // so it says so. It used to read "Repairing…" while being disabled, which
      // looked like a status readout but was actually a dead control: clicking
      // it did nothing, and the disabled styling made the app look as though it
      // had disconnected. The watchdog is also the only owner of the engine at
      // that point, so the user needs an explicit way to say "stop".
      return props.repairStage === 'degraded' ? 'Retry' : 'Stop repairing'
    }
    return 'Disconnect'
  }
  return props.lastActionable ? 'Retry' : 'Connect'
})

const primaryActionClass = computed(() => {
  if (props.connecting || props.reconnectRequested) return 'btn-secondary'
  if (props.connected) {
    if (!props.tunnelOk) {
      // Degraded: the watchdog has given up → Retry (accent). Still repairing →
      // a usable danger-styled Stop, since pressing it disconnects the tunnel.
      return props.repairStage === 'degraded' ? 'btn-accent' : 'btn-danger'
    }
    return 'btn-danger'
  }
  return 'btn-primary'
})

const primaryDisabled = computed(() => {
  // Block double-taps while a connect is in flight. Everything else is an
  // actionable state: in particular "Repairing…" is NOT disabled any more, so a
  // student who wants the app to stop churning the engine can say so instead of
  // being stuck watching it, or force-quitting the app.
  if (props.connecting || props.reconnectRequested) return true
  return false
})

const stateColor = computed(() => {
  if (props.state === 'running') return 'stat-ok'
  if (props.state === 'crashed') return 'stat-error'
  return ''
})

// Human-readable label for the raw engine state string.
const stateLabel = computed(() => {
  if (props.state === 'running') return 'Running'
  if (props.state === 'crashed') return 'Crashed'
  if (props.state === 'stopped') return 'Stopped'
  return props.state
})

const tierDisplay = computed(() => {
  if (!props.tier) return ''
  return props.tier.charAt(0).toUpperCase() + props.tier.slice(1)
})

// repairNote explains the specific recovery action the watchdog is taking so
// the degraded banner is not a one-size-fits-all "being repaired". Maps the
// backend repairStage to a short human phrase.
const repairNote = computed(() => {
  switch (props.repairStage) {
    case 'restart': return 'Tunnel stopped passing traffic — restarting the engine…'
    case 'full-reset': return 'Tunnel still down — performing a full reset…'
    case 'degraded': return 'Tunnel could not be recovered automatically. Use Retry, or check your internet connection.'
    default: return 'Tunnel stopped passing traffic; recovering automatically.'
  }
})

// failureHint turns the classified failure kind into a concrete next step. The
// banner already carries the raw explanation (lastActionable); this adds the
// "so what do I do" half. `unknown` deliberately shows nothing rather than
// guessing at a cause.
const failureHint = computed(() => {
  switch (props.lastErrorKind) {
    case 'offline':
      return 'Tip: open Diagnostics to confirm Locus can reach the network, then try again.'
    case 'elevation':
      return 'Tip: relaunch Locus and allow the administrator prompt.'
    case 'engine':
      return 'Tip: open Diagnostics and copy the report — the tunnel engine failed to start.'
    case 'server':
      return 'Tip: if this keeps happening, open Diagnostics and copy the report for support.'
    default:
      return ''
  }
}) 

// NOTE: Vue 3 emits are fire-and-forget — emit() returns void, it does NOT
// resolve with the parent handler's return value. Connect failures are
// surfaced by the parent store (persistent banner + toast), so there is no
// local error path here.
async function toggleConnection(): Promise<void> {
  // A degraded tunnel whose watchdog has given up (or a previous failure) is a
  // RETRY, not a disconnect: force a clean reconnect instead of tearing down.
  if (props.connected && props.tunnelOk) {
    emit('disconnect')
    return
  }
  if (props.connected && props.repairStage === 'degraded') {
    emit('retry-connect')
    return
  }
  if (props.connected) {
    // Still repairing. This is an explicit CANCEL: the student is telling us to
    // stop the watchdog churning the engine. It used to fall through with a
    // "button is disabled, but guard anyway" comment and do nothing at all,
    // leaving no way out of a repair the network would never satisfy (Norton
    // tearing the tunnel down, for example) short of killing the app.
    emit('disconnect')
    return
  }
  if (props.lastActionable) {
    emit('retry-connect')
    return
  }
  emit('connect')
}

// updateActive is true while the backend apply flow runs (downloading →
// verifying → applying → applied). The button is disabled and shows progress.
const updateActive = computed(() => props.updatePhase === 'downloading' || props.updatePhase === 'verifying' || props.updatePhase === 'applying' || props.updatePhase === 'applied')

const updateLabel = computed(() => {
  switch (props.updatePhase) {
    case 'downloading': return 'Downloading…'
    case 'verifying': return 'Verifying…'
    case 'applying': return 'Applying…'
    default: return 'Updating…'
  }
})

async function handleApplyUpdate(): Promise<void> {
  emit('apply-update')
}

// handleCheckUpdate runs an on-demand update check and records that one ran, so
// the notice below the buttons can report the outcome. Without the timestamp,
// a negative result is indistinguishable from "never checked".
//
// The parent's 'check-update' handler is asynchronous; Vue's emit does not
// return a promise, so this stays a fire-and-forget that only owns the spinner
// and the "a check happened" flag. The notice reads the store's own state,
// which the parent updates.
function handleCheckUpdate(): void {
  if (checkingUpdate.value) return
  checkingUpdate.value = true
  lastCheckAt.value = new Date().toISOString()
  emit('check-update')
  // The store's check resolves quickly (one heartbeat round-trip); release the
  // spinner on the next tick boundary that lets the result paint.
  window.setTimeout(() => { checkingUpdate.value = false }, 1500)
}

async function handleDiagnostics(): Promise<void> {
  const text = await bridge.getDiagnostics()
  diagnosticsText.value = text
  showDiagnostics.value = true
}

async function copyDiagnostics(): Promise<void> {
  const ok = await bridge.copyToClipboard(diagnosticsText.value)
  if (ok) {
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}
</script>

<style scoped>
.main {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 8px;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.status-label {
  font-size: 14px;
  font-weight: 500;
}

.card {
  background: #0C1711;
  border: 1px solid #1F3629;
  border-radius: 12px;
  padding: 24px;
}

.connection-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
}

.status-circle {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.status-circle.connected {
  background: rgba(34, 197, 94, 0.15);
  color: #22C55E;
}

.status-circle.disconnected {
  background: rgba(140, 165, 150, 0.15);
  color: #8CA596;
}

.status-circle.error {
  background: rgba(239, 68, 68, 0.15);
  color: #EF4444;
}

.status-circle.degraded {
  background: rgba(245, 158, 11, 0.15);
  color: #F59E0B;
}

.status-text {
  font-size: 20px;
  font-weight: 600;
}

.tier-label {
  font-size: 13px;
  color: #8CA596;
}

.connect-note {
  font-size: 12px;
  color: #8CA596;
  text-align: center;
  line-height: 1.5;
}

.grace-warning {
  font-size: 12px;
  color: #F59E0B;
  padding: 6px 12px;
  background: rgba(245, 158, 11, 0.1);
  border-radius: 6px;
}

.degraded-warning {
  font-size: 12px;
  color: #F59E0B;
  line-height: 1.5;
  padding: 8px 12px;
  background: rgba(245, 158, 11, 0.12);
  border: 1px solid rgba(245, 158, 11, 0.3);
  border-radius: 8px;
}

/* Persistent actionable failure banner: wrap-friendly so a long backend error
   cannot overflow the fixed-width shell. */
.actionable-error {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
  text-align: left;
  font-size: 12px;
  line-height: 1.5;
  color: #FCA5A5;
  padding: 8px 12px;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: 8px;
  overflow-wrap: anywhere;
}

.actionable-icon {
  flex-shrink: 0;
}

.actionable-body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.actionable-hint {
  color: #8CA596;
}

.btn {
  padding: 10px 24px;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, opacity 0.2s;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.btn-large {
  min-width: 200px;
  padding: 12px 24px;
  font-size: 15px;
}

.btn-primary {
  background: #2EA86A;
  color: white;
}
.btn-primary:hover:not(:disabled) { background: #46C186; }

.btn-danger {
  background: #EF4444;
  color: white;
}
.btn-danger:hover:not(:disabled) { background: #DC2626; }

.btn-secondary {
  background: #1F3629;
  color: #EAF2EC;
}
.btn-secondary:hover { background: #2B4636; }

.btn-accent {
  background: #F59E0B;
  color: #06130C;
}
.btn-accent:hover { background: #D97706; }

.btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.stats-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.stat-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.stat-label {
  font-size: 13px;
  color: #8CA596;
}

.stat-value {
  font-size: 13px;
  font-weight: 500;
}

.stat-ok    { color: #22C55E; }
.stat-warn  { color: #F59E0B; }
.stat-error { color: #EF4444; }

.actions {
  display: flex;
  gap: 8px;
  justify-content: center;
}

.actions .btn {
  flex: 1;
}

.version-footer {
  margin-top: 4px;
  text-align: center;
  font-size: 11px;
  color: #3A5344;
  user-select: text;
}

/* Update check result line. Low-key by default so it reads as information;
   amber when the check could not complete, so "unreachable" never looks like
   the reassuring "you are up to date" case. */
.update-notice {
  margin-top: -6px;
  text-align: center;
  font-size: 11px;
  line-height: 1.4;
  color: #5C7A66;
  overflow-wrap: anywhere;
}

.update-notice--action {
  color: #4ADE80;
}

.update-notice--warn {
  color: #FBBF24;
}

/* ── Diagnostics Modal ── */

.modal-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
}

.modal {
  background: #0C1711;
  border: 1px solid #1F3629;
  border-radius: 12px;
  padding: 24px;
  width: 90%;
  max-width: 500px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  gap: 12px;
  overflow: hidden;
}

.modal h3 {
  font-size: 16px;
  font-weight: 600;
  flex-shrink: 0;
}

.modal-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  flex-shrink: 0;
}

.diagnostics-text {
  background: #06130C;
  border: 1px solid #1F3629;
  border-radius: 8px;
  padding: 12px;
  font-family: 'SF Mono', 'Fira Code', monospace;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow: auto;
  flex: 1;
  min-height: 80px;
  color: #8CA596;
}

.modal-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.modal-actions .btn {
  min-width: 80px;
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
