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
      :loading="vpn.state.loading"
      :update-available="vpn.state.updateAvailable"
      :update-version="vpn.state.updateVersion"
      @connect="handleConnect"
      @disconnect="handleDisconnect"
      @show-diagnostics="handleDiagnostics"
      @check-update="handleCheckUpdate"
    />

    <!-- Error toast -->
    <div v-if="vpn.state.error" class="toast toast-error" @click="clearError">
      {{ vpn.state.error }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import ActivationScreen from './components/ActivationScreen.vue'
import MainScreen from './components/MainScreen.vue'
import { useVPN } from './stores/vpn'

const vpn = useVPN()

onMounted(async () => {
  vpn.setupEventListeners()
  await vpn.checkActivated()
})

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

async function handleCheckUpdate(): Promise<void> {
  await vpn.checkUpdate()
}

function clearError(): void {
  // Cleared on next status change
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
  user-select: none;
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
  padding: 12px 24px;
  border-radius: 8px;
  font-size: 14px;
  cursor: pointer;
  z-index: 100;
  animation: slideUp 0.3s ease;
}

.toast-error {
  background: #EF4444;
  color: white;
}

@keyframes slideUp {
  from { transform: translateX(-50%) translateY(20px); opacity: 0; }
  to   { transform: translateX(-50%) translateY(0); opacity: 1; }
}
</style>
