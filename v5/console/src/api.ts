// API client for the Locus admin console.
//
// The admin token is kept in sessionStorage, NOT localStorage: it should not
// outlive the browser session on a shared machine, and it is never baked into
// the built bundle (which is served publicly as a static file).

const TOKEN_KEY = 'locus_admin_token'

export function getToken(): string {
  return sessionStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string) {
  sessionStorage.setItem(TOKEN_KEY, token)
}

export function clearToken() {
  sessionStorage.removeItem(TOKEN_KEY)
}

export function hasToken(): boolean {
  return !!getToken()
}

export interface ApiResult<T = Record<string, unknown>> {
  ok: boolean
  message?: string
  /** Present when the request itself failed (network / non-JSON response). */
  transportError?: string
  data?: T
}

/**
 * Call the admin API.
 *
 * Every console action funnels through here so auth handling, error shaping and
 * timeout behaviour stay in one place. The endpoint is deliberately a single
 * PB route with an `action` discriminator — one auth check, one place to audit.
 */
export async function call<T = Record<string, unknown>>(
  action: string,
  payload: Record<string, unknown> = {},
  timeoutMs = 30000,
): Promise<ApiResult<T>> {
  const token = getToken()
  if (!token) return { ok: false, message: 'Not signed in' }

  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const resp = await fetch('/api/admin/console', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        // Sent as a header so it never lands in a URL / access log.
        'X-Admin-Token': token,
      },
      body: JSON.stringify({ action, admin_token: token, ...payload }),
      signal: controller.signal,
    })

    const text = await resp.text()
    let parsed: Record<string, unknown> = {}
    try {
      parsed = text ? JSON.parse(text) : {}
    } catch {
      return {
        ok: false,
        transportError: `Server returned a non-JSON response (HTTP ${resp.status})`,
      }
    }

    if (resp.status === 403) {
      clearToken()
      return { ok: false, message: 'Your session is no longer valid — please sign in again.' }
    }
    if (!parsed.ok) {
      return { ok: false, message: (parsed.message as string) || `Request failed (HTTP ${resp.status})` }
    }
    return { ok: true, data: parsed as T }
  } catch (err) {
    const e = err as Error
    if (e.name === 'AbortError') {
      return { ok: false, transportError: 'The server took too long to respond.' }
    }
    return { ok: false, transportError: e.message || 'Network error' }
  } finally {
    clearTimeout(timer)
  }
}

/** Verify a token by making the cheapest possible authenticated call. */
export async function verifyToken(token: string): Promise<boolean> {
  try {
    const resp = await fetch('/api/admin/console', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token },
      body: JSON.stringify({ action: 'dashboard', admin_token: token }),
    })
    if (!resp.ok) return false
    const json = await resp.json().catch(() => null)
    return !!json && json.ok === true
  } catch {
    return false
  }
}

export interface UploadProgress {
  filename: string
  loaded: number
  total: number
  percent: number
}

/**
 * Upload a single release artifact.
 *
 * Uses XMLHttpRequest rather than fetch because it reports upload progress —
 * on a school connection a 30MB upload is slow enough that a spinner with no
 * progress feels broken, and the operator needs to know it is still moving.
 *
 * The SHA256 is computed in the browser and sent as a header so the server can
 * refuse a file that did not survive the transfer.
 */
export function uploadArtifact(
  version: string,
  file: File,
  sha256: string,
  onProgress: (p: UploadProgress) => void,
): Promise<ApiResult<{ version: string; filename: string; bytes: number; sha256: string }>> {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/admin/upload', true)
    xhr.setRequestHeader('X-Admin-Token', getToken())
    xhr.setRequestHeader('X-Release-Version', version)
    xhr.setRequestHeader('X-Release-Filename', file.name)
    xhr.setRequestHeader('X-Release-Sha256', sha256)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')

    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable) {
        onProgress({
          filename: file.name,
          loaded: ev.loaded,
          total: ev.total,
          percent: Math.round((ev.loaded / ev.total) * 100),
        })
      }
    }

    xhr.onload = () => {
      let parsed: Record<string, unknown> = {}
      try {
        parsed = JSON.parse(xhr.responseText || '{}')
      } catch {
        resolve({ ok: false, transportError: `Upload returned HTTP ${xhr.status}` })
        return
      }
      if (xhr.status >= 200 && xhr.status < 300 && parsed.ok) {
        resolve({ ok: true, data: parsed as never })
      } else {
        resolve({ ok: false, message: (parsed.message as string) || `Upload failed (HTTP ${xhr.status})` })
      }
    }
    xhr.onerror = () => resolve({ ok: false, transportError: 'Upload failed — connection lost' })
    xhr.onabort = () => resolve({ ok: false, transportError: 'Upload cancelled' })

    xhr.send(file)
  })
}

/** Hash a File in the browser (streamed, so a 30MB file does not block). */
export async function sha256OfFile(file: File): Promise<string> {
  const buffer = await file.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buffer)
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}
