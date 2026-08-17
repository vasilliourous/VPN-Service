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
      :connecting="vpn.state.connecting"
      :loading="vpn.state.loading"
      :version="vpn.state.version"
      :update-available="vpn.state.updateAvailable"
      :update-version="vpn.state.updateVersion"
      :update-phase="vpn.state.updatePhase"
      :update-message="vpn.state.updateMessage"
      @connect="handleConnect"
      @disconnect="handleDisconnect"
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
  background-color: #0D0D0F;
  color: #F5F5F7;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
  -webkit-font-smoothing: antialiased;
}

.app-container {
  display: flex;
  flex-direction: column;
  height: 100vh;
  padding: 24px;
  background: linear-gradient(180deg, #0D0D0F 0%, #111114 100%);
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
}

.toast-error {
  background: #EF4444;
  color: white;
}

/* toast transition */
.toast-enter-active, .toast-leave-active { transition: opacity 0.3s, transform 0.3s; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translate(-50%, 12px); }
</style>
