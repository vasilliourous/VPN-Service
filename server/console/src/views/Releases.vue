<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { call, fetchRelease, type FetchArtifact } from '../api'
import { toast } from '../toast'

// ── Releases page: publish by pulling a GitHub Release ──
//
// This page used to upload four ~15-30 MB binaries through the browser. That
// made publishing depend on the operator's laptop, its upload bandwidth and its
// browser, and it needed a dedicated upload service on the hub because
// PocketBase rejects bodies above a few MB.
//
// Now the hub fetches the artifacts itself, straight from the GitHub Release
// for a version. Nothing large is sent TO the hub at all, so the upload path —
// and its whole class of transfer problems — is gone.
//
// The flow is deliberately three explicit steps, in this order:
//
//   1. Publish  — hub pulls from GitHub, verifies, writes update_config
//   2. Offer    — choose what proportion of clients is told about it
//
// They are separate because they fail differently and at different times: a
// GitHub fetch can fail for reasons that have nothing to do with rollout, and
// raising the rollout over an un-fetched version sends the fleet to a 404.
// Doing the fetch first makes that mistake impossible to reach.

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
// silently never apply, so the page names them explicitly.
const EXPECTED: { key: string; filename: string; label: string }[] = [
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
const fetching = ref(false)

// Result of the last fetch, so the operator can see exactly what was verified
// (filename, size, hash, and the format actually detected) rather than a
// success toast they have to take on faith.
const fetched = ref<Record<string, FetchArtifact> | null>(null)
const fetchError = ref('')
const fetchElapsed = ref<number | null>(null)

// One-shot trigger link state.
const link = ref('')
const linkBusy = ref(false)

function fmtBytes(n: number): string {
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

async function loadRelease() {
  loading.value = true
  const res = await call<{ release: Release }>('releases.get')
  if (res.ok && res.data) {
    release.value = res.data.release
    // Do not clobber a version the operator has already typed: on first load
    // the current version is a useful default, but after they enter a new one
    // (e.g. 2.2.1 while the hub is on 2.2.0) reloading must not reset the field.
    if (!version.value.trim()) version.value = res.data.release.version
    rollout.value = res.data.release.rollout_percent
  } else {
    toast.err(res.message || res.transportError || 'Could not load release state')
  }
  loading.value = false
}

// ── Step 1: pull the release from GitHub ──
//
// Calls the fetch service THROUGH Caddy, which resolves every asset by name
// from the GitHub release manifest, streams each one to disk, verifies the
// format, and reports the hashes it computed. A release missing a platform is
// refused there — no partial publish is possible from this button.
async function publishFromGithub() {
  const v = version.value.trim().replace(/^v/, '')
  if (!v) {
    toast.err('Enter the version to publish (e.g. 2.2.1)')
    return
  }
  if (!/^[0-9]+\.[0-9]+\.[0-9]+/.test(v)) {
    toast.err('Version must look like 1.2.3')
    return
  }
  fetching.value = true
  fetched.value = null
  fetchError.value = ''
  fetchElapsed.value = null

  try {
    const res = await fetchRelease(v)
    if (!res.ok || !res.data) {
      fetchError.value = res.message || res.transportError || 'Fetch failed'
      toast.err(fetchError.value)
      return
    }

    fetched.value = res.data.artifacts
    fetchElapsed.value = res.data.elapsed ?? null

    // ── Step 2 (DB half): write update_config from what was verified ──
    //
    // The hashes come from the files the hub actually hashed on disk, not from
    // anything the browser claims. Rollout is NOT changed here — publishing and
    // offering are separate decisions (releases.set owns the rollout).
    const pub = await call('releases.publish', {
      version: v,
      artifacts: res.data.artifacts,
      base_url: window.location.origin,
    })
    if (!pub.ok) {
      fetchError.value = pub.message || pub.transportError || 'Could not record the release'
      toast.err(fetchError.value)
      return
    }

    toast.ok(`Release ${v} published — all four platforms verified`)
    await loadRelease()
  } finally {
    fetching.value = false
  }
}

// ── One-shot trigger link ──
//
// The point of the link is that a fetch can be triggered from somewhere that
// does not hold the admin token — a phone, a CI job. Every link is fresh,
// expires, and can be used exactly once; the hub refuses a replay. That is what
// makes it safe to paste into a chat or a bookmark.
async function makeLink() {
  const v = version.value.trim().replace(/^v/, '')
  if (!v) {
    toast.err('Enter a version first — the link is bound to one version')
    return
  }
  linkBusy.value = true
  try {
    const res = await call<{ link: string; expires_in: number }>('releases.fetchLink', { version: v })
    if (res.ok && res.data?.link) {
      link.value = res.data.link
      toast.ok('New one-shot link minted')
    } else {
      toast.err(res.message || res.transportError || 'Could not mint a link')
    }
  } finally {
    linkBusy.value = false
  }
}

async function copyLink() {
  try {
    await navigator.clipboard.writeText(link.value)
    toast.ok('Link copied — single use, and it expires')
  } catch {
    toast.err('Could not copy — select the text manually')
  }
}

// ── Rollout ──
async function activate(percentOverride?: number) {
  const v = version.value.trim().replace(/^v/, '')
  if (!v) {
    toast.err('Enter a version number')
    return
  }
  const pct = percentOverride !== undefined ? percentOverride : rollout.value

  // Never advertise a version we cannot serve.
  //
  // The heartbeat sends whatever URLs sit in update_config, independently of
  // what is on disk. Raising the rollout while those URLs are empty or point at
  // a different version tells every eligible client to download a 404 — and a
  // client that fetches nothing cannot update, so the release silently fails
  // for the whole fleet with no error anywhere but the client log. The hook
  // enforces this too; checking here gives a better message than a 400.
  if (pct > 0) {
    const missing = EXPECTED.filter((e) => !release.value?.platforms[e.key]?.url)
      .map((e) => e.label)
    if (missing.length) {
      toast.err(
        `Refusing to offer ${v} at ${pct}% — nothing published for: ${missing.join(', ')}. ` +
        `Publish the release first, or clients will be sent to a 404.`
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

// What the hub is currently advertising, for the "on the hub" summary.
const publishedCount = computed(
  () => EXPECTED.filter((e) => release.value?.platforms[e.key]?.url).length,
)
const allPublished = computed(() => publishedCount.value === EXPECTED.length)

onMounted(() => {
  loadRelease()
})
</script>

<template>
  <h1 class="page-title">Releases</h1>
  <p class="page-sub">
    Publish a client update by pulling its build straight from GitHub. Nothing is
    uploaded from this browser.
  </p>

  <!-- ── What the hub currently advertises ── -->
  <div class="card">
    <h2>On the hub now</h2>
    <p v-if="loading" class="muted">Loading…</p>
    <template v-else-if="release">
      <div class="kv">
        <div><span class="muted">Version</span><strong>{{ release.version || '—' }}</strong></div>
        <div><span class="muted">Rollout</span><strong>{{ release.rollout_percent }}%</strong></div>
        <div><span class="muted">Active</span><strong>{{ release.active ? 'yes' : 'no' }}</strong></div>
      </div>

      <table class="grid">
        <thead>
          <tr><th>Platform</th><th>Artifact</th><th>SHA-256</th></tr>
        </thead>
        <tbody>
          <tr v-for="e in EXPECTED" :key="e.key">
            <td>{{ e.label }}</td>
            <td class="mono">{{ e.filename }}</td>
            <td class="mono">
              {{ release.platforms[e.key]?.sha256 ? release.platforms[e.key].sha256.slice(0, 16) + '…' : '— not published —' }}
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="!allPublished" class="msg warn">
        The version above is advertised with {{ publishedCount }} of 4 platforms.
        Clients on a missing platform will be offered the update and have nothing
        to download. Publish the release to fill them in.
      </div>
    </template>
  </div>

  <!-- ── Step 1: publish from GitHub ── -->
  <div class="card">
    <h2>Publish from GitHub</h2>
    <p class="muted">
      The hub downloads the four raw binaries and <code>manifest.json</code> from
      the GitHub Release tagged <code>v&lt;version&gt;</code>, verifies every file,
      and points the hub at them. CI must have finished and the tag must be pushed.
      Do <strong>not</strong> use the <code>.zip</code> bundles — the updater
      replaces the app binary directly and cannot unpack a zip.
    </p>

    <div class="row">
      <label class="field">
        <span>Version</span>
        <input v-model="version" type="text" placeholder="2.2.1" />
      </label>
      <div class="shrink">
        <button class="primary" :disabled="fetching" @click="publishFromGithub">
          {{ fetching ? 'Fetching from GitHub…' : 'Fetch & publish' }}
        </button>
      </div>
    </div>

    <p v-if="fetching" class="muted">
      Downloading and verifying every artifact. This takes a few seconds per
      platform; do not close the page.
    </p>

    <div v-if="fetchError" class="msg err">{{ fetchError }}</div>

    <table v-if="fetched" class="grid">
      <thead>
        <tr><th>Platform</th><th>File</th><th>Size</th><th>Detected</th><th>SHA-256</th></tr>
      </thead>
      <tbody>
        <tr v-for="e in EXPECTED" :key="e.key">
          <td>{{ e.label }}</td>
          <td class="mono">{{ fetched[e.key]?.filename || '—' }}</td>
          <td>{{ fetched[e.key] ? fmtBytes(fetched[e.key].bytes) : '—' }}</td>
          <td>{{ fetched[e.key]?.format || '—' }}</td>
          <td class="mono">{{ fetched[e.key]?.sha256.slice(0, 16) }}…</td>
        </tr>
      </tbody>
    </table>
    <p v-if="fetched && fetchElapsed !== null" class="muted">
      Verified and recorded in {{ fetchElapsed }}s. Rollout is unchanged — choose
      it below.
    </p>
  </div>

  <!-- ── One-shot trigger links ── -->
  <div class="card">
    <h2>One-shot trigger link</h2>
    <p class="muted">
      A link that fetches this same version without the admin token — useful from
      a phone or a CI job. Each link is <strong>single-use</strong> and expires;
      a used or expired link is refused. Mint a new one every time.
    </p>
    <div class="row">
      <div class="shrink">
        <button :disabled="linkBusy" @click="makeLink">
          {{ linkBusy ? 'Minting…' : 'New link' }}
        </button>
      </div>
      <div v-if="link" class="shrink">
        <button @click="copyLink">Copy</button>
      </div>
    </div>
    <p v-if="link" class="mono breakall">{{ link }}</p>
  </div>

  <!-- ── Step 2: rollout ── -->
  <div class="card">
    <h2>Rollout</h2>
    <p class="muted">
      Decides what proportion of clients is told the update exists. Clients are
      selected by a stable hash of their device, so the same device always sees
      the same decision. Start low, widen as confidence grows.
    </p>

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
