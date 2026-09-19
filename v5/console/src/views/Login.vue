<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { setToken, verifyToken } from '../api'

const emit = defineEmits<{ (e: 'signed-in'): void }>()

const token = ref('')
const busy = ref(false)
const error = ref('')

// If the URL carries ?token=..., prefill it. This makes the first sign-in a
// single click from a bookmark, without ever putting the token in the built
// bundle. We clear it from the address bar immediately afterwards.
onMounted(() => {
  const url = new URL(window.location.href)
  const t = url.searchParams.get('token')
  if (t) {
    token.value = t
    url.searchParams.delete('token')
    window.history.replaceState({}, '', url.pathname + url.hash)
  }
})

async function submit() {
  const value = token.value.trim()
  if (!value) {
    error.value = 'Enter the admin token.'
    return
  }
  busy.value = true
  error.value = ''
  try {
    const ok = await verifyToken(value)
    if (!ok) {
      error.value = 'That token was not accepted. Check it matches ADMIN_API_TOKEN on the server.'
      return
    }
    setToken(value)
    emit('signed-in')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-box">
      <div class="panel">
        <h2 style="margin-bottom: 4px">Locus Console</h2>
        <p class="muted" style="margin-top: 0; font-size: 13px">
          Sign in with the admin token to manage codes, releases and tiers.
        </p>

        <div v-if="error" class="msg err">{{ error }}</div>

        <label class="field">
          <span>Admin token</span>
          <input
            v-model="token"
            type="password"
            autocomplete="off"
            spellcheck="false"
            placeholder="paste the admin token"
            @keyup.enter="submit"
          />
        </label>

        <button class="primary" style="width: 100%" :disabled="busy" @click="submit">
          {{ busy ? 'Checking…' : 'Sign in' }}
        </button>

        <p class="muted" style="font-size: 12px; margin-bottom: 0; margin-top: 14px">
          Tip: you can bookmark
          <code>/admin/?token=…</code> to sign in with one click.
          The token is stored only for this browser session.
        </p>
      </div>
    </div>
  </div>
</template>
