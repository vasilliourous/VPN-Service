<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { call } from '../api'
import { toast } from '../toast'

interface CodeRow {
  id: string
  code: string
  tier: string
  status: 'available' | 'bound' | 'suspended' | 'expired'
  bound: boolean
  fingerprint: string
  suspended: boolean
  expires_at: string
  activated_at: string
  middleman: string
  label: string
  notes: string
}

const codes = ref<CodeRow[]>([])
const loading = ref(false)

// Filters
const query = ref('')
const tierFilter = ref('')
const statusFilter = ref('')
const middlemanFilter = ref('')

// Generation form
const genTier = ref('eco')
const genCount = ref(10)
const genExpires = ref('')
const genMiddleman = ref('')
const genLabel = ref('')
const genNotes = ref('')
const generating = ref(false)
const generated = ref<string[]>([])

// Per-code detail
const detail = ref<CodeRow | null>(null)
const history = ref<{ event: string; detail: string; created: string }[]>([])
const historyLoading = ref(false)

const tiers = ref<string[]>(['eco', 'stealth', 'strike'])
const middlemen = ref<{ name: string; codes: number }[]>([])

async function load() {
  loading.value = true
  const res = await call<{ codes: CodeRow[]; total: number }>('codes.list', {
    query: query.value,
    tier: tierFilter.value,
    status: statusFilter.value,
    middleman: middlemanFilter.value,
  })
  if (res.ok && res.data) {
    codes.value = res.data.codes
  } else {
    toast.err(res.message || res.transportError || 'Could not load codes')
  }
  loading.value = false
}

async function loadMeta() {
  const t = await call<{ tiers: { tier: string }[] }>('tiers.list')
  if (t.ok && t.data) tiers.value = t.data.tiers.map((x) => x.tier)

  const m = await call<{ middlemen: { name: string; codes: number }[] }>('middlemen.list')
  if (m.ok && m.data) middlemen.value = m.data.middlemen
}

function defaultExpiry(): string {
  // One year out — the previous codes all expired ~12 months after issue.
  const d = new Date()
  d.setFullYear(d.getFullYear() + 1)
  return d.toISOString().slice(0, 10)
}

async function generate() {
  if (!genTier.value) {
    toast.err('Choose a tier first')
    return
  }
  const count = Number(genCount.value)
  if (!Number.isFinite(count) || count < 1 || count > 500) {
    toast.err('Number of codes must be between 1 and 500')
    return
  }
  generating.value = true
  generated.value = []
  try {
    const res = await call<{ created: string[]; skipped: number }>('codes.generate', {
      tier: genTier.value,
      count,
      expires_at: genExpires.value,
      middleman: genMiddleman.value,
      label: genLabel.value,
      notes: genNotes.value,
    })
    if (res.ok && res.data) {
      generated.value = res.data.created
      toast.ok(`Created ${res.data.created.length} code(s)` + (res.data.skipped ? `, ${res.data.skipped} skipped` : ''))
      await load()
      await loadMeta()
    } else {
      toast.err(res.message || res.transportError || 'Could not create codes')
    }
  } finally {
    generating.value = false
  }
}

async function setSuspended(row: CodeRow, suspend: boolean) {
  const res = await call('codes.' + (suspend ? 'suspend' : 'unsuspend'), { code: row.code })
  if (res.ok) {
    toast.ok(`${suspend ? 'Suspended' : 'Reactivated'} ${row.code}`)
    await load()
  } else {
    toast.err(res.message || res.transportError || 'Action failed')
  }
}

async function unbind(row: CodeRow) {
  const reason = window.prompt(
    `Unbind ${row.code} from this device?\n\nThe student can activate again on the same or a new device.`,
    'Student changed device',
  )
  if (reason === null) return
  const res = await call('codes.unbind', { code: row.code, reason })
  if (res.ok) {
    toast.ok(`Unbound ${row.code} — it can be activated again`)
    await load()
    if (detail.value?.code === row.code) await openDetail(row)
  } else {
    toast.err(res.message || res.transportError || 'Unbind failed')
  }
}

async function openDetail(row: CodeRow) {
  detail.value = row
  historyLoading.value = true
  history.value = []
  const res = await call<{ events: { event: string; detail: string; created: string }[] }>(
    'codes.history',
    { code: row.code },
  )
  if (res.ok && res.data) history.value = res.data.events
  historyLoading.value = false
}

async function saveDetail() {
  if (!detail.value) return
  const res = await call('codes.update', {
    code: detail.value.code,
    middleman: detail.value.middleman,
    label: detail.value.label,
    notes: detail.value.notes,
  })
  if (res.ok) {
    toast.ok('Saved')
    await load()
    await loadMeta()
  } else {
    toast.err(res.message || res.transportError || 'Save failed')
  }
}

async function copyCodes(list: string[]) {
  const text = list.join('\n')
  try {
    await navigator.clipboard.writeText(text)
    toast.ok(`Copied ${list.length} code(s) to the clipboard`)
  } catch {
    toast.warn('Could not copy automatically — select the text below and copy manually')
  }
}

function downloadCsv() {
  const rows = [['code', 'tier', 'status', 'middleman', 'label', 'expires_at', 'activated_at']]
  for (const c of codes.value) {
    rows.push([c.code, c.tier, c.status, c.middleman, c.label, c.expires_at, c.activated_at])
  }
  const csv = rows.map((r) => r.map((v) => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\n')
  const blob = new Blob([csv], { type: 'text/csv' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `locus-codes-${new Date().toISOString().slice(0, 10)}.csv`
  a.click()
  URL.revokeObjectURL(a.href)
}

const shown = computed(() => codes.value.length)

onMounted(async () => {
  genExpires.value = defaultExpiry()
  await loadMeta()
  await load()
})
</script>

<template>
  <h1 class="page-title">Codes &amp; Clients</h1>
  <p class="page-sub">Generate activation codes, see who is using them, and fix problems.</p>

  <!-- ── Generate ── -->
  <div class="panel">
    <h2>Create codes</h2>
    <div class="row">
      <label class="field">
        <span>Tier</span>
        <select v-model="genTier">
          <option v-for="t in tiers" :key="t" :value="t">{{ t }}</option>
        </select>
      </label>
      <label class="field">
        <span>How many</span>
        <input v-model.number="genCount" type="number" min="1" max="500" />
      </label>
      <label class="field">
        <span>Expires</span>
        <input v-model="genExpires" type="date" />
      </label>
      <label class="field">
        <span>For middleman (optional)</span>
        <input v-model="genMiddleman" list="middlemen-list" placeholder="e.g. Sarah" />
        <datalist id="middlemen-list">
          <option v-for="m in middlemen" :key="m.name" :value="m.name" />
        </datalist>
      </label>
    </div>
    <div class="row">
      <label class="field">
        <span>Label (optional)</span>
        <input v-model="genLabel" placeholder="e.g. Macleans Year 13" />
      </label>
      <label class="field">
        <span>Notes (optional)</span>
        <input v-model="genNotes" placeholder="free-form" />
      </label>
      <div class="shrink">
        <button class="primary" :disabled="generating" @click="generate">
          {{ generating ? 'Creating…' : 'Create codes' }}
        </button>
      </div>
    </div>

    <div v-if="generated.length" class="msg ok" style="margin-top: 12px">
      <div><strong>Created {{ generated.length }} code(s):</strong></div>
      <div class="pre-wrap mono" style="margin-top: 8px; max-height: 220px; overflow: auto">{{ generated.join('\n') }}</div>
      <div class="actions">
        <button class="tiny" @click="copyCodes(generated)">Copy all</button>
        <button class="tiny" @click="generated = []">Hide</button>
      </div>
    </div>
  </div>

  <!-- ── Filter / list ── -->
  <div class="panel">
    <h2>Issued codes</h2>
    <div class="row">
      <label class="field">
        <span>Search code</span>
        <input v-model="query" placeholder="type part of a code" @keyup.enter="load" />
      </label>
      <label class="field">
        <span>Tier</span>
        <select v-model="tierFilter" @change="load">
          <option value="">All</option>
          <option v-for="t in tiers" :key="t" :value="t">{{ t }}</option>
        </select>
      </label>
      <label class="field">
        <span>Status</span>
        <select v-model="statusFilter" @change="load">
          <option value="">All</option>
          <option value="available">Available</option>
          <option value="bound">Activated</option>
          <option value="suspended">Suspended</option>
          <option value="expired">Expired</option>
        </select>
      </label>
      <label class="field">
        <span>Middleman</span>
        <input v-model="middlemanFilter" placeholder="name" @keyup.enter="load" />
      </label>
      <div class="shrink">
        <button @click="load">Apply</button>
      </div>
      <div class="shrink">
        <button @click="downloadCsv">Export CSV</button>
      </div>
    </div>

    <div v-if="loading" class="muted" style="margin-top: 10px">Loading…</div>
    <div v-else-if="shown === 0" class="muted" style="margin-top: 10px">
      No codes match. Create some above, or clear the filters.
    </div>
    <table v-else style="margin-top: 10px">
      <thead>
        <tr>
          <th>Code</th><th>Tier</th><th>Status</th><th>Middleman</th>
          <th>Expires</th><th>Activated</th><th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in codes" :key="row.id">
          <td>
            <code>{{ row.code }}</code>
            <div v-if="row.label" class="muted" style="font-size: 11px">{{ row.label }}</div>
          </td>
          <td>{{ row.tier }}</td>
          <td><span class="badge" :class="row.status">{{ row.status }}</span></td>
          <td class="muted">{{ row.middleman || '—' }}</td>
          <td class="muted">{{ row.expires_at ? row.expires_at.slice(0, 10) : '—' }}</td>
          <td class="muted">{{ row.activated_at ? row.activated_at.slice(0, 10) : '—' }}</td>
          <td>
            <div class="actions" style="margin: 0">
              <button class="tiny" @click="openDetail(row)">Details</button>
              <button v-if="row.bound" class="tiny" @click="unbind(row)">Unbind</button>
              <button v-if="!row.suspended" class="tiny danger" @click="setSuspended(row, true)">Suspend</button>
              <button v-else class="tiny" @click="setSuspended(row, false)">Reactivate</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <!-- ── Detail modal ── -->
  <div v-if="detail" class="overlay" @click.self="detail = null">
    <div class="modal">
      <h2 style="margin-top: 0"><code>{{ detail.code }}</code></h2>
      <p class="muted" style="margin-top: 0">
        Tier <strong>{{ detail.tier }}</strong> ·
        status <strong>{{ detail.status }}</strong>
        <span v-if="detail.fingerprint"> · device <code>{{ detail.fingerprint }}</code></span>
      </p>

      <label class="field">
        <span>Middleman</span>
        <input v-model="detail.middleman" list="middlemen-list" />
      </label>
      <label class="field">
        <span>Label</span>
        <input v-model="detail.label" />
      </label>
      <label class="field">
        <span>Notes</span>
        <textarea v-model="detail.notes"></textarea>
      </label>

      <div class="actions">
        <button class="primary" @click="saveDetail">Save details</button>
        <button v-if="detail.bound" @click="unbind(detail)">Unbind device</button>
        <button
          v-if="!detail.suspended"
          class="danger"
          @click="setSuspended(detail, true); detail = null"
        >Suspend</button>
        <button v-else @click="setSuspended(detail, false); detail = null">Reactivate</button>
        <button @click="detail = null">Close</button>
      </div>

      <h3 style="font-size: 13px; margin-bottom: 6px">History</h3>
      <div v-if="historyLoading" class="muted">Loading…</div>
      <div v-else-if="history.length === 0" class="muted">No recorded events for this code.</div>
      <table v-else>
        <tbody>
          <tr v-for="(h, i) in history" :key="i">
            <td class="muted">{{ h.created ? h.created.slice(0, 16).replace('T', ' ') : '' }}</td>
            <td>{{ h.event }}</td>
            <td class="muted">{{ h.detail }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
