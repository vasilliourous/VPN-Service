import { useCallback, useEffect, useRef, useState } from 'react'

import {
  locusConnect,
  locusDisconnect,
  locusStatus,
  type LocusStatus,
} from '@/services/locus'
import { errorDetail } from '@/services/notice-service'

/**
 * The tunnel state machine, lifted from the home card that used to own it.
 *
 * Kept as one hook rather than inlined in the screen because the screen will
 * grow and the state rules are the part that must not drift. Every rule below
 * was paid for by a real failure mode in the retired client; the comments say
 * which, so a future edit can tell a rule from a preference.
 */
export type ConnectionPhase =
  | 'unknown'
  | 'disconnected'
  | 'connecting'
  | 'disconnecting'
  | 'connected'

export interface ConnectionState {
  phase: ConnectionPhase
  /** The backend's own sentence, shown verbatim. `null` when there is none. */
  error: string | null
  /** The activation/subscription state, once read. */
  status: LocusStatus | null
  toggle: () => Promise<void>
  /** Force a status re-read, e.g. after an action elsewhere changes it. */
  refresh: () => Promise<void>
}

/** How often to re-read status while the screen is open. */
const POLL_INTERVAL_MS = 15000

export const useConnection = (): ConnectionState => {
  const [phase, setPhase] = useState<ConnectionPhase>('unknown')
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<LocusStatus | null>(null)

  // The phase as the async paths below need to read it. `useState`'s value is
  // captured by the closure at render time, so a `toggle` that started before a
  // poll landed would act on a stale phase — the exact double-fire the poll
  // guard below exists to prevent. A ref always reads the current value.
  const phaseRef = useRef<ConnectionPhase>('unknown')
  phaseRef.current = phase

  const refresh = useCallback(async () => {
    try {
      const next = await locusStatus()
      setStatus(next)
      setPhase((current) =>
        // Never stomp an in-flight transition: a poll landing mid-connect would
        // otherwise snap the button back and let the student double-fire it.
        current === 'connecting' || current === 'disconnecting'
          ? current
          : next.activated
            ? 'disconnected'
            : 'unknown'
      )
    } catch {
      // Leave the phase alone. A status read failing is not evidence about the
      // tunnel, and flapping the UI on a transient error is worse than a stale
      // label a moment longer.
    }
  }, [])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [refresh])

  const toggle = useCallback(async () => {
    // Guard on the ref, not the state: a click that lands while a previous
    // transition is still in flight must be ignored rather than queued.
    const current = phaseRef.current
    if (current === 'connecting' || current === 'disconnecting' || current === 'unknown') {
      return
    }

    setError(null)
    const connecting = current !== 'connected'
    setPhase(connecting ? 'connecting' : 'disconnecting')

    try {
      const result = connecting ? await locusConnect() : await locusDisconnect()
      setPhase(result.connected ? 'connected' : 'disconnected')
      // Re-read so the tier and subscription shown are the ones the hub just
      // confirmed, not what we happened to have cached.
      void refresh()
    } catch (err) {
      // Show the backend's reason verbatim. It already distinguishes "your code
      // is bound elsewhere" from "the config was refused" from "the core would
      // not start", and rewording it here would lose that.
      //
      // `errorDetail`, not `String`: Rust failures cross the IPC boundary as
      // `CommandFailure { code, detail }`, and `String(obj)` is
      // "[object Object]" — which is what the connect button used to report,
      // hiding the real reason the tunnel would not start.
      const message = errorDetail(err)
      setError(message)
      setPhase(connecting ? 'disconnected' : 'connected')
    }
  }, [refresh])

  return { phase, error, status, toggle, refresh }
}
