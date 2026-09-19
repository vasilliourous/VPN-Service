<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
import { call, uploadArtifact, sha256OfFile } from '../api'
import { toast } from '../toast'
import { verifyArtifact, type ArtifactCheck, type PlatformKey } from '../lib/artifact'

interface Platform {
  url: string
  sha256: string
}
interface Release {
  version: string
  rollout_percent: number
  active: boolean
  platforms: Record<string, Platform>
}

// The filenames the client's updater looks for. If these do not match, updates
// silently never apply — so the UI names them explicitly rather than letting a
// wrong file be picked.
const EXPECTED: { key: PlatformKey; filename: string; label: string }[] = [
  { key: 'linux', filename: 'locus-linux-amd64', label: 'Linux (amd64)' },
  { key: 'windows', filename: 'locus-windows-amd64.exe', label: 'Windows (amd64)' },
  { key: 'macos_intel', filename: 'locus-darwin-amd64', label: 'macOS (Intel)' },
  { key: 'macos_arm', filename: 'locus-darwin-arm64', label: 'macOS (Apple Silicon)' },
]

const release = ref<Release | null>(null)
const version = ref('')
const rollout = ref(5)
const loading = ref(true)
const savingRollout = ref(false)

// Upload state, keyed by filename
interface Slot {
  file: File | null
  sha256: string
  percent: number
  status: 'empty' | 'ready' | 'checking' | 'uploading' | 'done' | 'error'
  message: string
  // Verification result, surfaced in the row so the operator can see what the
  // file actually is rather than trusting the filename.
  detected: string
  notes: string[]
  problem: string
}
const slots = ref<Record<string, Slot>>({})

function blankSlot(): Slot {
  return { file: null, sha256: '', percent: 0, status: 'empty', message: '', detected: '', notes: [], problem: '' }
}

// Re-verify already-picked files when the version changes.
//
// The embedded-version check needs the version string, and the operator may
// reasonably type it before or after choosing files. Only files that are not
// already uploaded are re-checked, and the in-flight upload state is never
// disturbed.
watch(version, (v) => {
  if (!v.trim()) return
  for (const e of EXPECTED) {
    const slot = slots.value[e.filename]
    if (slot?.file && slot.status !== 'done' && slot.status !== 'uploading') {
      void verifySlot(e, slot.file)
    }
  }
})

function ensureSlots() {
  for (const e of EXPECTED) {
    if (!slots.value[e.filename]) slots.value[e.filename] = blankSlot()
  }
}

async function loadRelease() {
  loading.value = true
  const res = await call<{ release: Release }>('releases.get')
  if (res.ok && res.data) {
    release.value = res.data.release
    version.value = res.data.release.version
    rollout.value = res.data.release.rollout_percent
  } else {
    toast.err(res.message || res.transportError || 'Could not load release state')
  }
  loading.value = false
}

function pickFile(entry: { filename: string; key: PlatformKey }, fileList: FileList | null) {
  const file = fileList && fileList[0]
  if (!file) return
  // Guard the obvious mistake: picking the .zip bundle instead of the raw
  // binary. The updater cannot consume a zip, and a wrong name is rejected by
  // the server anyway — better to say so immediately.
  if (file.name !== entry.filename) {
    toast.err(`Expected a file named "${entry.filename}" but got "${file.name}". Pick the raw binary, not the .zip bundle.`)
    slots.value[entry.filename] = { ...blankSlot(), status: 'error', message: `Wrong file: ${file.name}` }
    return
  }
  slots.value[entry.filename] = {
    file,
    sha256: '',
    percent: 0,
    status: 'ready',
    message: `${(file.size / 1024 / 1024).toFixed(1)} MB ready`,
    detected: '',
    notes: [],
    problem: '',
  }

  // Verify the artifact against the slot it was dropped into. This is what
  // replaces the manual manifest.json cross-check: the executables are
  // self-describing, so a cross-slot mixup (Windows binary in the Linux slot)
  // or a wrong-release binary is caught here rather than silently published.
  // The check is asynchronous; the slot is marked 'checking' until it settles.
  if (!version.value.trim()) {
    slots.value[entry.filename].status = 'ready'
    slots.value[entry.filename].message =
      `${(file.size / 1024 / 1024).toFixed(1)} MB ready — enter the version to verify it`
    return
  }
  void verifySlot(entry, file)
}

// verifySlot runs the artifact checks and updates the slot state. Kept separate
// from pickFile so the version field changing can re-run it (see watch below).
async function verifySlot(entry: { filename: string; key: PlatformKey }, file: File, showToast = false) {
  const slot = slots.value[entry.filename]
  if (!slot || slot.file !== file) return
  slot.status = 'checking'
  slot.problem = ''
  slot.message = 'Checking file…'

  let check: ArtifactCheck
  try {
    check = await verifyArtifact(file, entry.key, version.value.trim())
  } catch (err) {
    // A verification failure must never block a legitimate publish — fall
    // through to the size-only state and say the check could not run.
    slot.status = 'ready'
    slot.message = `${(file.size / 1024 / 1024).toFixed(1)} MB ready (could not verify: ${(err as Error).message})`
    return
  }

  slot.detected = check.detected
  slot.notes = check.notes
  if (!check.ok) {
    slot.status = 'error'
    slot.problem = check.problem || 'Verification failed'
    slot.message = slot.problem
    if (showToast) toast.err(`${entry.label}: file failed verification`)
    return
  }
  slot.status = 'ready'
  const size = `${(file.size / 1024 / 1024).toFixed(1)} MB`
  slot.message = `${size} ready · ${check.detected}` + (check.notes.length ? ` · ${check.notes.join(', ')}` : '')
}

async function uploadSlot(entry: { filename: string }) {
  const slot = slots.value[entry.filename]
  if (!slot || !slot.file) return
  if (!version.value.trim()) {
    toast.err('Enter the version number first (e.g. 1.1.0)')
    return
  }
  slot.status = 'uploading'
  slot.percent = 0
  slot.message = 'Hashing…'

  try {
    // Hash locally so the server can detect a truncated transfer.
    if (!slot.sha256) slot.sha256 = await sha256OfFile(slot.file)

    slot.message = 'Uploading…'
    const res = await uploadArtifact(version.value.trim(), slot.file, slot.sha256, (p) => {
      slot.percent = p.percent
    })

    if (res.ok && res.data) {
      slot.status = 'done'
      slot.percent = 100
      slot.message = `Uploaded (${(res.data.bytes / 1024 / 1024).toFixed(1)} MB)`
      toast.ok(`${entry.filename} uploaded`)
    } else {
      slot.status = 'error'
      slot.message = res.message || res.transportError || 'Upload failed'
      toast.err(`${entry.filename}: ${slot.message}`)
    }
  } catch (err) {
    slot.status = 'error'
    slot.message = (err as Error).message || 'Upload failed'
    toast.err(`${entry.filename}: ${slot.message}`)
  }
}

async function uploadAll() {
  for (const e of EXPECTED) {
    const slot = slots.value[e.filename]
    if (slot && slot.file && slot.status !== 'done') {
      await uploadSlot(e)
    }
  }
}

async function activate(percentOverride?: number) {
  const v = version.value.trim()
  if (!v) {
    toast.err('Enter a version number')
    return
  }
  const pct = percentOverride !== undefined ? percentOverride : rollout.value

  // Guard: never advertise a version we cannot actually serve.
  //
  // `update_config` stores the download URLs AND their hashes in its own columns,
  // independently of what is on disk. A rollout percentage raised while those
  // URLs point at artifacts that were never uploaded (or were cleaned up) tells
  // every eligible client to download a 404 — and a client that fetches nothing
  // cannot update, so the release silently fails for the whole fleet.
  //
  // Checked against the *saved* release state, not the unsaved upload slots:
  // what matters is what the hub is actually advertising right now.
  if (pct > 0) {
    const missing = EXPECTED.filter((e) => !release.value?.platforms[e.key]?.url)
      .map((e) => e.key)
    if (missing.length) {
      toast.err(
        `Refusing to offer ${v} at ${pct}% — no uploaded artifact for: ${missing.join(', ')}. ` +
        `Upload all four raw binaries first, or clients will be sent to a 404.`
      )
      return
    }
  }

  savingRollout.value = true
  try {
    const res = await call('releases.set', { version: v, rollout_percent: pct, active: true })
    if (res.ok) {
      toast.ok(pct === 0 ? `Release ${v} staged (offered to nobody yet)` : `Release ${v} live at ${pct}%`)
      await loadRelease()
    } else {
      toast.err(res.message || res.transportError || 'Could not update the release')
    }
  } finally {
    savingRollout.value = false
  }
}

async function stopOffering() {
  if (!window.confirm('Stop offering this update? Clients that already updated stay updated.')) return
  await activate(0)
}

function uploadedCount(): number {
  return EXPECTED.filter((e) => slots.value[e.filename]?.status === 'done').length
}

const allUploaded = computed(() => uploadedCount() === EXPECTED.length)

// A file that failed verification must not be uploadable, and "Upload all"
// must not silently skip it. These drive the buttons' disabled state.
function slotFailed(name: string): boolean {
  return slots.value[name]?.status === 'error'
}
function anyFailed(): boolean {
  return EXPECTED.some((e) => slotFailed(e.filename))
}
function anyChecking(): boolean {
  return EXPECTED.some((e) => slots.value[e.filename]?.status === 'checking')
}

onMounted(() => {
  ensureSlots()
  loadRelease()
})
</script>

<template>
  <h1 class="page-title">Releases</h1>
  <p class="page-sub">
    Upload a build and decide what proportion of clients are offered it.
  </p>

  <div v-if="loading" class="muted">Loading…</div>

  <template v-else>
    <!-- ── Current state ── -->
    <div class="panel">
      <h2>Current release</h2>
      <div v-if="release && release.version" class="cards" style="margin-bottom: 0">
        <div class="card">
          <div class="n" style="font-size: 18px">{{ release.version }}</div>
          <div class="l">Advertised version</div>
        </div>
        <div class="card" :class="release.rollout_percent > 0 ? 'good' : ''">
          <div class="n">{{ release.rollout_percent }}%</div>
          <div class="l">Rollout</div>
        </div>
        <div class="card" :class="release.active ? 'good' : 'bad'">
          <div class="n" style="font-size: 18px">{{ release.active ? 'Active' : 'Off' }}</div>
          <div class="l">Advertising</div>
        </div>
      </div>
      <p v-else class="muted" style="margin: 0">No release has been published yet.</p>

      <table v-if="release" style="margin-top: 14px">
        <thead><tr><th>Platform</th><th>Artifact</th><th>Hash recorded</th></tr></thead>
        <tbody>
          <tr v-for="e in EXPECTED" :key="e.key">
            <td>{{ e.label }}</td>
            <td class="mono" style="font-size: 12px">{{ e.filename }}</td>
            <td class="muted mono" style="font-size: 11px">
              {{ release.platforms[e.key]?.sha256 ? release.platforms[e.key].sha256.slice(0, 16) + '…' : '— not uploaded —' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- ── Upload ── -->
    <div class="panel">
      <h2>Upload a build</h2>
      <p class="muted" style="margin-top: 0">
        Pick the four raw binaries from CI. Do <strong>not</strong> upload the
        <code>.zip</code> bundles — the updater replaces the app binary directly and
        cannot unpack a zip.
      </p>
      <p class="muted" style="margin-top: 0">
        Each file is checked automatically against the platform slot it was dropped
        into and against the version you enter below — a Windows binary in the Linux
        slot, or a binary from a different release, is refused before it is uploaded.
        No separate manifest file is needed.
      </p>

      <label class="field">
        <span>Version being published</span>
        <input v-model="version" placeholder="e.g. 1.1.0" />
      </label>

      <table>
        <thead><tr><th>Platform</th><th>File</th><th>State</th><th></th></tr></thead>
        <tbody>
          <tr v-for="e in EXPECTED" :key="e.key">
            <td>{{ e.label }}<div class="muted mono" style="font-size: 11px">{{ e.filename }}</div></td>
            <td>
              <input
                type="file"
                @change="(ev) => pickFile(e, (ev.target as HTMLInputElement).files)"
              />
            </td>
            <td style="min-width: 170px">
              <span
                class="badge"
                :class="{
                  available: slots[e.filename]?.status === 'done',
                  bound: slots[e.filename]?.status === 'uploading' || slots[e.filename]?.status === 'checking',
                  suspended: slots[e.filename]?.status === 'error',
                }"
              >{{ slots[e.filename]?.status || 'empty' }}</span>
              <div class="muted" style="font-size: 11px; margin-top: 3px">{{ slots[e.filename]?.message }}</div>
              <!-- Show what the file actually is. The operator should never
                   have to take the filename on trust. -->
              <div v-if="slots[e.filename]?.detected" class="muted mono" style="font-size: 10px; margin-top: 2px">
                {{ slots[e.filename].detected }}
              </div>
              <div v-if="slots[e.filename]?.status === 'uploading'" class="progress">
                <div :style="{ width: slots[e.filename].percent + '%' }" />
              </div>
            </td>
            <td>
              <button
                class="tiny"
                :disabled="!slots[e.filename]?.file || slots[e.filename]?.status === 'uploading' || slots[e.filename]?.status === 'checking' || slots[e.filename]?.status === 'done' || slots[e.filename]?.status === 'error'"
                @click="uploadSlot(e)"
              >Upload</button>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="anyFailed()" class="msg warn">
        One or more files failed verification and cannot be uploaded. Fix the
        highlighted rows — a wrong file here is published to every client on that
        platform and fails silently on their devices.
      </div>

      <div class="actions">
        <button
          class="primary"
          :disabled="uploadedCount() === 0 || anyFailed() || anyChecking()"
          @click="uploadAll"
        >
          Upload all pending
        </button>
        <span class="muted" style="align-self: center">{{ uploadedCount() }} / 4 uploaded</span>
      </div>
    </div>

    <!-- ── Rollout ── -->
    <div class="panel">
      <h2>Rollout</h2>
      <p class="muted" style="margin-top: 0">
        Clients are selected by a stable hash of their device, so the same device
        always sees the same decision. Start low, widen as confidence grows.
      </p>
      <div v-if="!allUploaded" class="msg warn">
        Not all four platforms are uploaded for this session. Clients on a missing
        platform will be offered the version but have nothing to download — upload
        all four first, or publish while only some are ready and accept that.
      </div>

      <div class="row">
        <label class="field">
          <span>Rollout percentage</span>
          <input v-model.number="rollout" type="number" min="0" max="100" />
        </label>
        <div class="shrink">
          <button class="primary" :disabled="savingRollout" @click="activate()">
            {{ savingRollout ? 'Saving…' : 'Publish at this rollout' }}
          </button>
        </div>
      </div>

      <div class="actions">
        <button @click="activate(5)">Stage at 5%</button>
        <button @click="activate(25)">Widen to 25%</button>
        <button @click="activate(100)">Full rollout (100%)</button>
        <button class="danger" @click="stopOffering">Stop offering (0%)</button>
      </div>

      <p class="muted" style="margin-bottom: 0; margin-top: 12px; font-size: 12px">
        There is no automatic downgrade. If a bad build reaches 100%, the fix is to
        publish a higher version — so watch the first hours at a low percentage.
      </p>
    </div>
  </template>
</template>
