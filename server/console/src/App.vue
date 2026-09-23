<script setup lang="ts">
import { ref, computed } from 'vue'
import { hasToken, clearToken } from './api'
import Login from './views/Login.vue'
import Dashboard from './views/Dashboard.vue'
import Codes from './views/Codes.vue'
import Releases from './views/Releases.vue'
import Tiers from './views/Tiers.vue'
import Toast from './components/ToastView.vue'

type Page = 'dashboard' | 'codes' | 'releases' | 'tiers'

const signedIn = ref(hasToken())
// Simple hash-less view switching — this is a four-page internal tool, a router
// would be more moving parts than value. Refresh keeps you signed in (token is
// in sessionStorage) and returns to the dashboard.
const page = ref<Page>('dashboard')

const pages: { id: Page; label: string }[] = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'codes', label: 'Codes & Clients' },
  { id: 'releases', label: 'Releases' },
  { id: 'tiers', label: 'Tiers' },
]

const currentLabel = computed(() => pages.find((p) => p.id === page.value)?.label || '')

function onSignedIn() {
  signedIn.value = true
  page.value = 'dashboard'
}

function signOut() {
  clearToken()
  signedIn.value = false
}
</script>

<template>
  <Login v-if="!signedIn" @signed-in="onSignedIn" />

  <div v-else class="shell">
    <nav class="sidebar">
      <div class="brand">
        Locus Console
        <small>networkingguides.duckdns.org</small>
      </div>
      <button
        v-for="p in pages"
        :key="p.id"
        class="nav-item"
        :class="{ active: page === p.id }"
        @click="page = p.id"
      >
        {{ p.label }}
      </button>
      <div class="spacer" />
      <button class="nav-item" @click="signOut">Sign out</button>
    </nav>

    <main class="main">
      <Dashboard v-if="page === 'dashboard'" />
      <Codes v-else-if="page === 'codes'" />
      <Releases v-else-if="page === 'releases'" />
      <Tiers v-else-if="page === 'tiers'" :key="currentLabel" />
    </main>
  </div>

  <Toast />
</template>
