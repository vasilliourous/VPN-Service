<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { call } from '../api'
import { toast } from '../toast'

interface TierRow {
  id: string
  tier: string
  active: boolean
  udp_relay: boolean
  server: string
  server_port: number
  method: string
}

const tiers = ref<TierRow[]>([])
const loading = ref(true)
const saving = ref(false)

async function load() {
  loading.value = true
  const res = await call<{ tiers: TierRow[] }>('tiers.list')
  if (res.ok && res.data) tiers.value = res.data.tiers
  else toast.err(res.message || res.transportError || 'Could not load tiers')
  loading.value = false
}

async function save(row: TierRow) {
  saving.value = true
  try {
    const res = await call('tiers.update', {
      tier: row.tier,
      server: row.server,
      server_port: row.server_port,
      method: row.method,
      active: row.active,
      udp_relay: row.udp_relay,
    })
    if (res.ok) {
      toast.ok(`${row.tier} updated — clients pick this up on their next heartbeat`)
      await load()
    } else {
      toast.err(res.message || res.transportError || 'Save failed')
    }
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <h1 class="page-title">Tiers</h1>
  <p class="page-sub">Connection settings clients receive when they activate.</p>

  <div class="msg warn">
    <strong>The password is deliberately not editable here.</strong>
    Changing it instantly breaks every client already using that tier, and
    existing installs have no way to recover on their own. That stays a
    deliberate, documented operation — see OPS.md.
  </div>

  <div v-if="loading" class="muted">Loading…</div>

  <div v-else class="panel" v-for="row in tiers" :key="row.id">
    <h2 style="text-transform: capitalize">{{ row.tier }}</h2>
    <div class="row">
      <label class="field">
        <span>Server hostname</span>
        <input v-model="row.server" />
      </label>
      <label class="field">
        <span>Port</span>
        <input v-model.number="row.server_port" type="number" min="1" max="65535" />
      </label>
      <label class="field">
        <span>Encryption method</span>
        <input v-model="row.method" />
      </label>
    </div>
    <div class="row">
      <label class="field shrink">
        <span>Active</span>
        <input v-model="row.active" type="checkbox" />
      </label>
      <label class="field shrink">
        <span>UDP relay</span>
        <input v-model="row.udp_relay" type="checkbox" />
      </label>
      <div class="shrink">
        <button class="primary" :disabled="saving" @click="save(row)">Save {{ row.tier }}</button>
      </div>
    </div>
    <p class="muted" style="margin: 0; font-size: 12px">
      <strong>UDP relay</strong> must stay off for this server: shadowsocks-rust does
      not implement sing-box's UDP-over-TCP, and leaving it on makes UDP traffic fail.
    </p>
  </div>
</template>
