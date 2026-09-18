<template>
  <div class="app-container">
    <!-- Activation Screen (shown when not activated) -->
    <ActivationScreen
      v-if="!vpn.state.activated"
      @activate="handleActivate"
      :error="vpn.state.activationError"
      :loading="vpn.state.loading"
    />

    <!-- Main Screen (shown when activated) -->
    <MainScreen
      v-else
      :connected="vpn.state.connected"
      :tier="vpn.state.tier"
      :state="vpn.state.state"
      :grace-days="vpn.state.graceDays"
      :failures="vpn.state.failures"
      :tunnel-ok="vpn.state.tunnelOk"
      :repair-stage="vpn.state.repairStage"
      :connecting="vpn.state.connecting"
      :loading="vpn.state.loading"
      :version="vpn.state.version"
      :reconnect-requested="vpn.state.reconnectRequested"
      :connect-stage="vpn.state.connectStage"
      :last-actionable="vpn.state.lastActionable"
      :last-error-kind="vpn.state.lastErrorKind"
      :update-available="vpn.state.updateAvailable"
      :update-version="vpn.state.updateVersion"
      :update-phase="vpn.state.updatePhase"
      :update-message="vpn.state.updateMessage"
      @connect="handleConnect"
      @disconnect="handleDisconnect"
      @retry-connect="handleRetryConnect"
      @show-diagnostics="handleDiagnostics"
      @apply-update="handleApplyUpdate"
    />

    <!-- Error toast (auto-dismisses; click to dismiss immediately) -->
    <Transition name="toast">
      <div v-if="vpn.state.error" class="toast toast-error" @click="clearError" role="alert">
        {{ vpn.state.error }}
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, watch } from 'vue'
import ActivationScreen from './components/ActivationScreen.vue'
import MainScreen from './components/MainScreen.vue'
import { useVPN } from './stores/vpn'

const vpn = useVPN()
let toastTimer: ReturnType<typeof setTimeout> | undefined

onMounted(async () => {
  vpn.setupEventListeners()
  await vpn.checkActivated()
})

onBeforeUnmount(() => {
  vpn.tearDownEventListeners()
  if (toastTimer) clearTimeout(toastTimer)
})

// Auto-dismiss the error toast after a delay so it can't linger forever.
watch(
  () => vpn.state.error,
  (val) => {
    if (toastTimer) clearTimeout(toastTimer)
    if (val) {
      toastTimer = setTimeout(() => vpn.clearError(), 6000)
    }
  },
)

async function handleActivate(code: string): Promise<string | null> {
  return await vpn.activate(code)
}

async function handleConnect(): Promise<string | null> {
  return await vpn.connect()
}

async function handleDisconnect(): Promise<void> {
  await vpn.disconnect()
}

async function handleRetryConnect(): Promise<string | null> {
  return await vpn.retryConnect()
}

async function handleDiagnostics(): Promise<string> {
  await vpn.loadDiagnostics()
  return vpn.state.diagnostics
}

async function handleApplyUpdate(): Promise<void> {
  await vpn.applyUpdate()
}

function clearError(): void {
  vpn.clearError()
}
</script>

<style>
/* ── Global styles ── */
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  background-color: #06130C;
  color: #EAF2EC;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
  -webkit-font-smoothing: antialiased;
}

.app-container {
  display: flex;
  flex-direction: column;
  height: 100vh;
  padding: 24px;
  background: linear-gradient(180deg, #06130C 0%, #0C1A12 100%);
  /* The window can be resized down to 380×500 and laptop display scaling can
     reduce the effective viewport further, so the content column scrolls
     rather than clipping the Connect button off-screen. */
  overflow-y: auto;
  overflow-x: hidden;
}

/* Keep the activation/main content vertically centred when there is room, but
   never let it compress below its natural height (which would clip). */
.app-container > * {
  flex-shrink: 0;
}

.toast {
  position: fixed;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  padding: 12px 20px;
  border-radius: 8px;
  font-size: 13px;
  cursor: pointer;
  z-index: 100;
  max-width: 90%;
  text-align: center;
  /* A long backend error must wrap, not run off the edge of the webview. */
  overflow-wrap: anywhere;
}

.toast-error {
  background: #EF4444;
  color: white;
}

/* toast transition */
.toast-enter-active, .toast-leave-active { transition: opacity 0.3s, transform 0.3s; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translate(-50%, 12px); }
</style>
