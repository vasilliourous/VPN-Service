import { useCallback, useMemo, useSyncExternalStore } from 'react'
import { MihomoWebSocket, type Message } from 'tauri-plugin-mihomo-api'

/**
 * Live connection-count telemetry from the mihomo core.
 *
 * Locus exposes one number from this — how many connections are active right
 * now, shown on the home page's traffic card. The full connection table, the
 * per-connection metadata normalisation and the closed-connection ring buffer
 * that used to live here served the Connections page, which is not part of this
 * product (there is one server per tier and nothing to choose between), so they
 * were removed with it rather than left as dead weight.
 *
 * The core still reports everything; we just subscribe to the cheap summary
 * socket (`connect_connections_count`) instead of the per-connection feed.
 */

const CONNECTION_RECONNECT_DELAY_MS = 1_000

type ConnectionListener = () => void

interface ConnectionSummaryPayload {
  count?: number
}

interface ConnectionSummaryData {
  activeConnectionCount: number
}

const initConnSummaryData: ConnectionSummaryData = { activeConnectionCount: 0 }

let connectionSummary: ConnectionSummaryData = initConnSummaryData

const summaryListeners = new Set<ConnectionListener>()

const notifySummaryListeners = () => {
  summaryListeners.forEach((listener) => listener())
}

const mergeConnectionSummary = (
  payload: ConnectionSummaryPayload,
): ConnectionSummaryData => ({
  activeConnectionCount: payload.count ?? 0,
})

const handleSummaryMessage = (message: Message) => {
  if (message.type !== 'Text') return

  let payload: ConnectionSummaryPayload
  try {
    payload = JSON.parse(message.data) as ConnectionSummaryPayload
  } catch (err) {
    console.error(
      '[Connections] Failed to parse connections count payload',
      err,
    )
    return
  }

  connectionSummary = mergeConnectionSummary(payload)
  notifySummaryListeners()
}

interface SocketSupervisor {
  start: () => void
  stopIfIdle: () => void
}

/**
 * Owns one websocket for one listener set: connects on first subscriber,
 * disconnects once the last one leaves, reconnects after a failure, and treats
 * an in-band "Websocket error" frame as a reason to reconnect.
 *
 * The reconnect-on-error frame is not theoretical — the core emits it in place
 * of closing the socket, so without this a failed stream would sit silently
 * dead while the UI showed a stale count.
 */
const createSocketSupervisor = (options: {
  listeners: Set<ConnectionListener>
  connectSocket: () => Promise<MihomoWebSocket>
  onMessage: (message: Message) => void
  closeLogLabel: string
}): SocketSupervisor => {
  const { listeners, connectSocket, onMessage, closeLogLabel } = options
  const hasSubscribers = () => listeners.size > 0
  let socket: MihomoWebSocket | null = null
  let connecting = false
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null

  const clearReconnectTimer = () => {
    if (!reconnectTimer) return
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }

  const closeSocket = async () => {
    const current = socket
    socket = null
    if (!current) return

    try {
      await current.close()
    } catch (err) {
      console.warn(`Failed to close ${closeLogLabel} websocket`, err)
    }
  }

  const scheduleReconnect = () => {
    if (!hasSubscribers()) return
    if (reconnectTimer) return
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null
      void connect()
    }, CONNECTION_RECONNECT_DELAY_MS)
  }

  const reconnect = async () => {
    if (!hasSubscribers()) return
    await closeSocket()
    scheduleReconnect()
  }

  const connect = async () => {
    if (socket || connecting) return
    if (!hasSubscribers()) return

    clearReconnectTimer()
    connecting = true

    try {
      const connected = await connectSocket()
      if (!hasSubscribers()) {
        await connected.close()
        return
      }
      socket = connected
      connected.addListener((message) => {
        if (socket !== connected) return
        if (message.type !== 'Text') return
        if (message.data.startsWith('Websocket error')) {
          void reconnect()
          return
        }

        onMessage(message)
      })
    } catch {
      scheduleReconnect()
    } finally {
      connecting = false
    }
  }

  return {
    start: () => {
      void connect()
    },
    stopIfIdle: () => {
      if (hasSubscribers()) return

      clearReconnectTimer()
      void closeSocket()
    },
  }
}

const summarySupervisor = createSocketSupervisor({
  listeners: summaryListeners,
  connectSocket: () => MihomoWebSocket.connect_connections_count(),
  onMessage: handleSummaryMessage,
  closeLogLabel: 'connections count',
})

const getConnectionSummarySnapshot = () => connectionSummary

const subscribeConnectionSummary = (listener: ConnectionListener) => {
  summaryListeners.add(listener)
  summarySupervisor.start()
  return () => {
    summaryListeners.delete(listener)
    summarySupervisor.stopIfIdle()
  }
}

export const useConnectionSummaryData = (options?: { enabled?: boolean }) => {
  const enabled = options?.enabled ?? true
  const subscribe = useCallback(
    (listener: ConnectionListener) =>
      enabled ? subscribeConnectionSummary(listener) : () => {},
    [enabled],
  )
  const data = useSyncExternalStore(
    subscribe,
    getConnectionSummarySnapshot,
    getConnectionSummarySnapshot,
  )
  const response = useMemo(() => ({ data }), [data])

  return {
    response,
  }
}
