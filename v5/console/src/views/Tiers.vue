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
  // The UDP-over-TCP endpoint port, or 0 when the tier has none. Stored inside
  // the tier's config JSON and surfaced so the udp_relay checkbox has a visible
  // partner — it is the other half of the switch.
  uot_port: number
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
      uot_port: row.uot_port,
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
      <label class="field">
        <span>UoT port (0 = none)</span>
        <input v-model.number="row.uot_port" type="number" min="0" max="65535" />
      </label>
      <div class="shrink">
        <button class="primary" :disabled="saving" @click="save(row)">Save {{ row.tier }}</button>
      </div>
    </div>

    <!-- Warn when the two halves of the switch disagree. This is the state that
         looks configured but does nothing, or worse, points UDP at a port that
         does not speak the protocol. -->
    <p v-if="row.udp_relay && !row.uot_port" class="msg warn" style="font-size: 12px">
      <strong>UDP relay is on but no UoT port is set.</strong> This is a no-op —
      the client only builds a UDP-over-TCP outbound when both are present, so
      UDP stays raw. Harmless, but the tier does not do what the checkbox implies.
    </p>
    <p v-else-if="!row.udp_relay && row.uot_port" class="msg warn" style="font-size: 12px">
      <strong>A UoT port is set but UDP relay is off.</strong> The endpoint exists
      but no client will be told to use it.
    </p>
    <p class="muted" style="margin: 0; font-size: 12px">
      <strong>UoT port</strong> is the UDP-over-TCP endpoint the client will tunnel
      UDP through. Both switches are required: the client only creates the UoT
      outbound when <strong>UDP relay</strong> is on <em>and</em> a
      <code>uot_port</code> is set.
      <br /><br />
      Only use this for <strong>Strike</strong>, and only when a sing-box UoT
      listener is running on this server (normally 8446) — install it with
      <code>enable-uot.sh</code>; see <code>docs/GAMING-UDP.md</code>. Do not point
      it at the ordinary shadowsocks-rust ports (8443/8444/8445): those do not
      implement sing-box's UDP-over-TCP and will refuse the connections.
      <br /><br />
      Confirm the listener is actually up before setting the port:
      <code>systemctl status sing-box-uot</code> and
      <code>ss -lntup | grep 8446</code> (it must listen on <em>UDP</em> as well as
      TCP). A port advertised with nothing listening breaks UDP — game and voice
      traffic — while TCP and DNS keep working, so the failure looks like "some
      things work, some don't" rather than an obvious outage.
    </p>
  </div>
</template>
