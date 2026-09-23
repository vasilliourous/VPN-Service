<template>
  <div class="activation">
    <!-- Logo / Brand -->
    <div class="brand">
      <div class="brand-icon">
        <svg width="48" height="48" viewBox="0 0 64 64" fill="none">
          <path d="M32 4L8 16v16c0 14.3 9.6 27.7 24 32 14.4-4.3 24-17.7 24-32V16L32 4z"
                fill="#2EA86A" opacity="0.9"/>
          <path d="M24 28l6 6 10-10"
                stroke="white" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </div>
      <h1 class="brand-title">Locus</h1>
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
          placeholder="RQ-XXXX-XXXX-XXXX-C"
          maxlength="19"
          inputmode="text"
          autocomplete="off"
          autocapitalize="characters"
          autocorrect="off"
          spellcheck="false"
          aria-label="Activation code"
          aria-describedby="code-hint"
          :disabled="loading"
          @input="onCodeInput"
          @paste="onPaste"
          @keyup.enter="submitActivation"
        />
        <!-- Honest status: "format is right" and "the server knows this code"
             are different claims and are shown as such. -->
        <div id="code-hint" v-if="hintText" :class="['validation-hint', hintClass]" aria-live="polite">
          {{ hintText }}
        </div>
      </div>

      <button
        class="btn btn-primary"
        :disabled="!canActivate || loading"
        @click="submitActivation"
      >
        <span v-if="loading" class="spinner"></span>
        <span v-else>{{ primaryLabel }}</span>
      </button>
      <!-- Staged progress so a 30s activation does not look frozen. -->
      <div v-if="loading" class="progress-note" aria-live="polite">{{ progressNote }}</div>

      <div v-if="error" class="error-message">
        {{ error }}
      </div>
    </div>

    <!-- Update escape hatch.
         A build broken badly enough to prevent activation is exactly when a
         fix needs to be installable — but every other update path runs through
         the activation-bound heartbeat, so this device could never be told one
         exists. This uses the credential-free public release list and is the
         only way out of that deadlock. -->
    <div v-if="updateAvailable || updateMessage" class="update-panel">
      <button
        v-if="updateAvailable && !updating"
        class="btn btn-secondary btn-small"
        @click="handleUpdate"
      >
        Install update {{ updateVersion }}
      </button>
      <div v-if="updating" class="progress-note" aria-live="polite">
        {{ updateMessage || 'Downloading update…' }}
      </div>
      <p v-else-if="updateMessage" class="update-note">{{ updateMessage }}</p>
    </div>

    <button class="btn-link" :disabled="checkingUpdate" @click="handleCheckUpdate">
      {{ checkingUpdate ? 'Checking…' : 'Check for a newer version' }}
    </button>

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

// ── Update escape hatch ──
// Kept local to this component: the parent's store is only wired up once the
// app is activated, and that is precisely the state we cannot rely on here.
const checkingUpdate = ref(false)
const updating = ref(false)
const updateAvailable = ref(false)
const updateVersion = ref('')
const updateMessage = ref('')

async function handleCheckUpdate(): Promise<void> {
  if (checkingUpdate.value) return
  checkingUpdate.value = true
  updateMessage.value = ''
  try {
    const res = await bridge.checkForUpdate()
    updateAvailable.value = res.available
    updateVersion.value = res.version || ''
    updateMessage.value = res.available
      ? `Version ${res.version} is available.`
      : (res.reason || 'You are running the newest published version.')
  } catch (err: any) {
    updateMessage.value = err?.message || 'Could not check for updates.'
  } finally {
    checkingUpdate.value = false
  }
}

async function handleUpdate(): Promise<void> {
  if (updating.value) return
  updating.value = true
  updateMessage.value = 'Downloading update…'
  try {
    const res = await bridge.applyUpdate()
    if (!res.success) {
      updating.value = false
      updateMessage.value = res.message
    }
    // On success the backend applies the update and restarts the app, so there
    // is nothing further to do here — keep the progress note up until it goes.
  } catch (err: any) {
    updating.value = false
    updateMessage.value = err?.message || 'Update failed.'
  }
}

const code = ref('')
const validationMessage = ref('')
const validationClass = ref('')
const codeInput = ref<HTMLInputElement>()

// lookupInFlight is true while the hub lookup is running, so the hint can say
// "Checking…" instead of leaving the student staring at nothing.
const lookupInFlight = ref(false)
// activatedTier is set once the hub confirms the code, so we can show which
// tier the student is about to get.
const confirmedTier = ref('')

// validationSeq guards the async code checks: typing fast fires overlapping
// hub lookups, and a slow earlier response must not overwrite the result of a
// newer one (which could leave "code found" shown for a code already edited).
let validationSeq = 0

const canActivate = computed(() => {
  // Code must be the full 15-char body (RQ + 3×4 segments + checksum)
  // before the button enables — submitting a shorter code would only fail
  // client-side Luhn validation and waste a server round-trip.
  return code.value.replace(/-/g, '').length === 15 && !props.loading
})

// primaryLabel reflects what Activate is actually doing, so the button is never
// a blank wait during a 30s server round-trip.
const primaryLabel = computed(() => (props.loading ? 'Activating…' : 'Activate'))

// progressNote gives the student staged feedback while activation runs.
const progressNote = computed(() => {
  if (!props.loading) return ''
  return 'Contacting the Locus server — this can take a few seconds. Please keep this window open.'
})

const hintText = computed(() => {
  if (lookupInFlight.value) return 'Checking this code with the server…'
  return validationMessage.value
})

const hintClass = computed(() => {
  if (lookupInFlight.value) return '' // neutral while checking
  return validationClass.value
})

// formatCode turns an arbitrary typed/pasted string into the canonical
// RQ-XXXX-XXXX-XXXX-C layout. It strips ALL non-alphanumerics (not just
// hyphens) so a paste with spaces, NBSPs, or smart-quotes still formats, and it
// caps the body at 15 chars so an over-long paste cannot overflow maxlength.
function formatCode(input: string): string {
  const raw = (input || '').replace(/[^a-z0-9]/gi, '').toUpperCase().slice(0, 15)
  const parts: string[] = []
  if (raw.length > 0) parts.push(raw.substring(0, 2))
  if (raw.length > 2) parts.push(raw.substring(2, 6))
  if (raw.length > 6) parts.push(raw.substring(6, 10))
  if (raw.length > 10) parts.push(raw.substring(10, 14))
  if (raw.length > 14) parts.push(raw.substring(14, 15))
  return parts.join('-')
}

async function onCodeInput(): Promise<void> {
  code.value = formatCode(code.value)
  await runValidation(code.value.replace(/-/g, ''))
}

// onPaste normalizes whatever lands in the field. The browser applies the paste
// before this runs, so we simply re-format from the resulting value.
function onPaste(): void {
  // Defer so the pasted text has been committed to the input's value first.
  setTimeout(() => {
    code.value = formatCode(code.value)
    void runValidation(code.value.replace(/-/g, ''))
  }, 0)
}

async function runValidation(raw: string): Promise<void> {
  if (raw.length !== 15) {
    validationMessage.value = ''
    validationClass.value = ''
    confirmedTier.value = ''
    return
  }
  const ticket = ++validationSeq
  lookupInFlight.value = true
  try {
    // 1. Local Luhn check first — instant, and avoids spending a server
    //    rate-limit slot on an obviously malformed code.
    const format = await bridge.validateCode(raw)
    if (ticket !== validationSeq) return
    if (!format.valid) {
      validationMessage.value = format.message || 'That code does not look right — check for typos.'
      validationClass.value = 'invalid'
      confirmedTier.value = ''
      return
    }

    // 2. Ask the hub whether the code actually exists. This is the check the
    //    old UI never did: "well-formed" is not "real".
    const check = await bridge.checkCode(raw)
    if (ticket !== validationSeq) return

    if (!check.known) {
      // Hub unreachable or too old to have the endpoint. Be honest: we could
      // not verify, but that does NOT mean the code is wrong.
      validationMessage.value =
        "Code format looks right — we'll confirm it with the server when you activate."
      validationClass.value = 'unverified'
      confirmedTier.value = ''
      return
    }

    if (check.recognised) {
      confirmedTier.value = check.tier || ''
      const tierPart = check.tier ? ` (${check.tier.charAt(0).toUpperCase() + check.tier.slice(1)})` : ''
      validationMessage.value = `✓ Code found${tierPart} — ready to activate`
      validationClass.value = 'valid'
      return
    }

    // Definitive negative from the server — say exactly which.
    confirmedTier.value = ''
    validationMessage.value = check.message || 'This code is not valid. Please check it and try again.'
    validationClass.value = 'invalid'
  } catch {
    if (ticket !== validationSeq) return
    // A failed check must never block activation — the server is the authority
    // at activate time. Never show a false "invalid" here.
    validationMessage.value =
      "Code format looks right — we'll confirm it with the server when you activate."
    validationClass.value = 'unverified'
    confirmedTier.value = ''
  } finally {
    if (ticket === validationSeq) lookupInFlight.value = false
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
  color: #EAF2EC;
  letter-spacing: -0.5px;
}

.brand-subtitle {
  font-size: 14px;
  color: #8CA596;
  margin-top: 4px;
}

.card {
  background: #0C1711;
  border: 1px solid #1F3629;
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
  color: #8CA596;
  margin-bottom: 16px;
}

.input-group {
  margin-bottom: 16px;
}

.code-input {
  width: 100%;
  padding: 12px 16px;
  background: #06130C;
  border: 1px solid #1F3629;
  border-radius: 8px;
  color: #EAF2EC;
  font-size: 16px;
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  letter-spacing: 1px;
  outline: none;
  transition: border-color 0.2s;
}

.code-input:focus {
  border-color: #2EA86A;
}

.code-input::placeholder {
  color: #3A5344;
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

/* Unverified: we could not reach the hub to confirm. Deliberately neutral
   (not red) because the code may well be fine. */
.validation-hint.unverified {
  color: #F59E0B;
}

.progress-note {
  margin-top: 10px;
  font-size: 12px;
  line-height: 1.5;
  color: #8CA596;
  text-align: center;
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
  background: #2EA86A;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background: #46C186;
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

/* Update escape hatch — deliberately low-key: it is a recovery path, not part
   of the normal activation flow. */
.update-panel {
  margin-top: 4px;
  text-align: center;
}

.btn-small {
  padding: 8px 14px;
  font-size: 13px;
}

.update-note {
  margin-top: 8px;
  font-size: 12px;
  line-height: 1.4;
  color: #5C7A66;
  overflow-wrap: anywhere;
}

.btn-link {
  background: none;
  border: none;
  padding: 6px;
  color: #4ADE80;
  font-size: 12px;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 3px;
}

.btn-link:disabled {
  color: #3A5344;
  cursor: default;
  text-decoration: none;
}

.tier-info {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  background: #0C1711;
  border: 1px solid #1F3629;
  border-radius: 12px;
  width: 100%;
  max-width: 380px;
  font-size: 13px;
  color: #8CA596;
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

.tier-dot.eco    { background: #5E6E63; }
.tier-dot.stealth { background: #2EA86A; }
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
