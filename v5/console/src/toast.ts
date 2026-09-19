// Minimal toast store + component.
//
// Operators act in short bursts (generate codes, unbind a device, publish a
// release) and need unambiguous confirmation that it worked — especially for
// actions that are hard to undo, like flipping a rollout to 100%.
import { reactive } from 'vue'

export type ToastKind = 'ok' | 'err' | 'warn'

interface ToastItem {
  id: number
  kind: ToastKind
  text: string
}

const state = reactive<{ items: ToastItem[] }>({ items: [] })
let nextId = 1

function push(kind: ToastKind, text: string, ms = 5000) {
  const id = nextId++
  state.items.push({ id, kind, text })
  // Errors linger — they usually need reading and acting on.
  const ttl = kind === 'err' ? Math.max(ms, 9000) : ms
  setTimeout(() => {
    const i = state.items.findIndex((t) => t.id === id)
    if (i >= 0) state.items.splice(i, 1)
  }, ttl)
}

export const toast = {
  ok: (t: string) => push('ok', t),
  err: (t: string) => push('err', t),
  warn: (t: string) => push('warn', t),
  state,
  dismiss(id: number) {
    const i = state.items.findIndex((t) => t.id === id)
    if (i >= 0) state.items.splice(i, 1)
  },
}
