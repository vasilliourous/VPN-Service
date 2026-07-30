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

      <div class="status-text">{{ statusLabel }}</div>
      <div class="tier-label">{{ tierDisplay }}</div>

      <!-- Grace period warning -->
      <div v-if="graceDays <= 3 && connected" class="grace-warning">
        ⚠ {{ graceDays }} day{{ graceDays === 1 ? '' : 's' }} remaining
      </div>

      <!-- Connect / Disconnect button -->
      <button
        :class="['btn', connected ? 'btn-danger' : 'btn-primary', 'btn-large']"
        :disabled="loading"
        @click="toggleConnection"
      >
        <span v-if="loading" class="spinner"></span>
        <span v-else>{{ connected ? 'Disconnect' : 'Connect' }}</span>
      </button>

      <!-- Error message -->
      <div v-if="error" class="error-text">{{ error }}</div>
    </div>

    <!-- Stats card -->
    <div class="card stats-card">
      <div class="stat-row">
        <span class="stat-label">State</span>
        <span :class="['stat-value', stateColor]">{{ state }}</span>
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
      <button
        v-if="updateAvailable"
        class="btn btn-accent"
        @click="handleCheckUpdate"
      >
        Update {{ updateVersion }} available
      </button>
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
  'show-diagnostics': []
  'check-update': []
}>()

const props = defineProps<{
  connected: boolean
  tier: string
  state: string
  graceDays: number
  failures: number
  loading: boolean
  updateAvailable: boolean
  updateVersion: string
}>()

const error = ref('')
const showDiagnostics = ref(false)
const diagnosticsText = ref('')
const copied = ref(false)

const statusState = computed(() => {
  if (props.connected) return 'connected'
  if (props.state === 'crashed') return 'error'
  return 'disconnected'
})

const statusLabel = computed(() => {
  if (props.connected) return 'Connected'
  if (props.state === 'crashed') return 'Engine Error'
  return 'Disconnected'
})

const stateColor = computed(() => {
  if (props.state === 'running') return 'stat-ok'
  if (props.state === 'crashed') return 'stat-error'
  return ''
})

const tierDisplay = computed(() => {
  if (!props.tier) return ''
  return props.tier.charAt(0).toUpperCase() + props.tier.slice(1)
})

async function toggleConnection(): Promise<void> {
  error.value = ''
  if (props.connected) {
    emit('disconnect')
  } else {
    const err = await emit('connect')
    // The parent handles the actual connect; we just pass it through
  }
}

async function handleDiagnostics(): Promise<void> {
  const text = await bridge.getDiagnostics()
  diagnosticsText.value = text
  showDiagnostics.value = true
}

async function handleCheckUpdate(): Promise<void> {
  // The update flow would trigger the Go updater
  // For now, just reset the notification
  emit('check-update')
}

async function copyDiagnostics(): Promise<void> {
  try {
    await navigator.clipboard.writeText(diagnosticsText.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // Clipboard not available
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
  background: #1A1A1E;
  border: 1px solid #2E2E35;
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
  background: rgba(142, 142, 150, 0.15);
  color: #8E8E96;
}

.status-circle.error {
  background: rgba(239, 68, 68, 0.15);
  color: #EF4444;
}

.status-text {
  font-size: 20px;
  font-weight: 600;
}

.tier-label {
  font-size: 13px;
  color: #8E8E96;
}

.grace-warning {
  font-size: 12px;
  color: #F59E0B;
  padding: 6px 12px;
  background: rgba(245, 158, 11, 0.1);
  border-radius: 6px;
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
  background: #A855F7;
  color: white;
}
.btn-primary:hover:not(:disabled) { background: #C084FC; }

.btn-danger {
  background: #EF4444;
  color: white;
}
.btn-danger:hover:not(:disabled) { background: #DC2626; }

.btn-secondary {
  background: #2E2E35;
  color: #F5F5F7;
}
.btn-secondary:hover { background: #3E3E45; }

.btn-accent {
  background: #F59E0B;
  color: #0D0D0F;
}
.btn-accent:hover { background: #D97706; }

.btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.error-text {
  font-size: 13px;
  color: #EF4444;
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
  color: #8E8E96;
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
  background: #1A1A1E;
  border: 1px solid #2E2E35;
  border-radius: 12px;
  padding: 24px;
  width: 90%;
  max-width: 500px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.modal h3 {
  font-size: 16px;
  font-weight: 600;
}

.diagnostics-text {
  background: #0D0D0F;
  border: 1px solid #2E2E35;
  border-radius: 8px;
  padding: 12px;
  font-family: 'SF Mono', 'Fira Code', monospace;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow: auto;
  max-height: 400px;
  color: #8E8E96;
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
