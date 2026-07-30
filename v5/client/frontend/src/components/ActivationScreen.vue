<template>
  <div class="activation">
    <!-- Logo / Brand -->
    <div class="brand">
      <div class="brand-icon">
        <svg width="48" height="48" viewBox="0 0 64 64" fill="none">
          <path d="M32 4L8 16v16c0 14.3 9.6 27.7 24 32 14.4-4.3 24-17.7 24-32V16L32 4z"
                fill="#A855F7" opacity="0.9"/>
          <path d="M24 28l6 6 10-10"
                stroke="white" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </div>
      <h1 class="brand-title">MyVPN</h1>
      <p class="brand-subtitle">Secure School VPN</p>
    </div>

    <!-- Activation Code Input -->
    <div class="card">
      <h2 class="card-title">Activate</h2>
      <p class="card-desc">Enter the code from your activation card</p>

      <div class="input-group">
        <input
          ref="codeInput"
          v-model="code"
          type="text"
          class="code-input"
          placeholder="MYVPN-XXXX-XXXX-XXXX-C"
          maxlength="23"
          :disabled="loading"
          @input="onCodeInput"
          @keyup.enter="submitActivation"
        />
        <div v-if="validationMessage" :class="['validation-hint', validationClass]">
          {{ validationMessage }}
        </div>
      </div>

      <button
        class="btn btn-primary"
        :disabled="!canActivate || loading"
        @click="submitActivation"
      >
        <span v-if="loading" class="spinner"></span>
        <span v-else>Activate</span>
      </button>

      <div v-if="error" class="error-message">
        {{ error }}
      </div>
    </div>

    <!-- Tier info -->
    <div class="tier-info">
      <div class="tier-row">
        <span class="tier-dot eco"></span>
        <span>Eco — $2/mo — Browsing</span>
      </div>
      <div class="tier-row">
        <span class="tier-dot stealth"></span>
        <span>Stealth — $4/mo — Streaming</span>
      </div>
      <div class="tier-row">
        <span class="tier-dot strike"></span>
        <span>Strike — $8/mo — Gaming</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import * as bridge from '@/lib/bridge'

const emit = defineEmits<{
  activate: [code: string]
}>()

const props = defineProps<{
  error: string
  loading: boolean
}>()

const code = ref('')
const validationMessage = ref('')
const validationClass = ref('')
const codeInput = ref<HTMLInputElement>()

const canActivate = computed(() => {
  return code.value.replace(/-/g, '').length >= 16 && !props.loading
})

async function onCodeInput(): Promise<void> {
  // Auto-format: insert hyphens as user types
  let raw = code.value.replace(/-/g, '').toUpperCase()
  const parts: string[] = []

  if (raw.length > 0) parts.push(raw.substring(0, 5))   // MYVPN
  if (raw.length > 5) parts.push(raw.substring(5, 9))   // XXXX
  if (raw.length > 9) parts.push(raw.substring(9, 13))  // XXXX
  if (raw.length > 13) parts.push(raw.substring(13, 17)) // XXXX
  if (raw.length > 17) parts.push(raw.substring(17, 18)) // C

  code.value = parts.join('-')

  // Validate client-side when full
  if (raw.length === 18) {
    const result = await bridge.validateCode(raw)
    validationMessage.value = result.valid ? '✓ Valid code' : result.message || 'Invalid code'
    validationClass.value = result.valid ? 'valid' : 'invalid'
  } else {
    validationMessage.value = ''
  }
}

async function submitActivation(): Promise<void> {
  if (!canActivate.value) return
  emit('activate', code.value)
}
</script>

<style scoped>
.activation {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  flex: 1;
  gap: 24px;
}

.brand {
  text-align: center;
  margin-bottom: 8px;
}

.brand-icon {
  margin-bottom: 12px;
}

.brand-title {
  font-size: 28px;
  font-weight: 700;
  color: #F5F5F7;
  letter-spacing: -0.5px;
}

.brand-subtitle {
  font-size: 14px;
  color: #8E8E96;
  margin-top: 4px;
}

.card {
  background: #1A1A1E;
  border: 1px solid #2E2E35;
  border-radius: 12px;
  padding: 24px;
  width: 100%;
  max-width: 380px;
}

.card-title {
  font-size: 18px;
  font-weight: 600;
  margin-bottom: 4px;
}

.card-desc {
  font-size: 13px;
  color: #8E8E96;
  margin-bottom: 16px;
}

.input-group {
  margin-bottom: 16px;
}

.code-input {
  width: 100%;
  padding: 12px 16px;
  background: #0D0D0F;
  border: 1px solid #2E2E35;
  border-radius: 8px;
  color: #F5F5F7;
  font-size: 16px;
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  letter-spacing: 1px;
  outline: none;
  transition: border-color 0.2s;
}

.code-input:focus {
  border-color: #A855F7;
}

.code-input::placeholder {
  color: #4A4A52;
  font-size: 13px;
  letter-spacing: 0.5px;
}

.validation-hint {
  font-size: 12px;
  margin-top: 6px;
  padding-left: 4px;
}

.validation-hint.valid {
  color: #22C55E;
}

.validation-hint.invalid {
  color: #EF4444;
}

.btn {
  width: 100%;
  padding: 12px;
  border: none;
  border-radius: 8px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, opacity 0.2s;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.btn-primary {
  background: #A855F7;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background: #C084FC;
}

.btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.error-message {
  margin-top: 12px;
  padding: 10px 14px;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: 8px;
  color: #EF4444;
  font-size: 13px;
}

.tier-info {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  background: #1A1A1E;
  border: 1px solid #2E2E35;
  border-radius: 12px;
  width: 100%;
  max-width: 380px;
  font-size: 13px;
  color: #8E8E96;
}

.tier-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tier-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.tier-dot.eco    { background: #6B7280; }
.tier-dot.stealth { background: #A855F7; }
.tier-dot.strike  { background: #EAB308; }

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
