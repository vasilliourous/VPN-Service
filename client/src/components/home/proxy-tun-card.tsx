import { Box, Button, CircularProgress, Typography, alpha, useTheme } from '@mui/material'
import { useCallback, useEffect, useState } from 'react'

import { EnhancedCard } from '@/components/home/enhanced-card'
import { locusConnect, locusDisconnect, locusStatus } from '@/services/locus'
import { showNotice } from '@/services/notice-service'

/**
 * The Locus connection control.
 *
 * Replaces the two raw Clash switches (system proxy / TUN) with one action,
 * because a Locus student is not choosing a proxy mode — they are turning the
 * VPN on. Those switches still exist underneath; they are simply not a decision
 * the student should have to make, and exposing both invited them to turn on the
 * one that does not work at school.
 *
 * The state machine is deliberately small:
 *
 *   unknown -> disconnected -> connecting -> connected
 *                          \-> error (with the reason, never a bare "failed")
 *
 * `unknown` is not `disconnected`. Treating "we have not asked yet" as "off"
 * would flash a Connect button on every launch and invite a double-tap.
 */

type Phase = 'unknown' | 'disconnected' | 'connecting' | 'disconnecting' | 'connected'

const ProxyTunCard = () => {
  const theme = useTheme()
  const [phase, setPhase] = useState<Phase>('unknown')
  const [error, setError] = useState<string | null>(null)
  const [tier, setTier] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const status = await locusStatus()
      setTier(status.tier)
      setPhase((current) =>
        // Never stomp an in-flight transition: a poll landing mid-connect would
        // otherwise snap the button back and let the student double-fire it.
        current === 'connecting' || current === 'disconnecting'
          ? current
          : status.activated
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
    const timer = window.setInterval(() => void refresh(), 15000)
    return () => window.clearInterval(timer)
  }, [refresh])

  const toggle = useCallback(async () => {
    setError(null)
    const connecting = phase !== 'connected'
    setPhase(connecting ? 'connecting' : 'disconnecting')

    try {
      const result = connecting ? await locusConnect() : await locusDisconnect()
      setPhase(result.connected ? 'connected' : 'disconnected')
    } catch (err) {
      // Show the backend's reason verbatim. It already distinguishes "your code
      // is bound elsewhere" from "the config was refused" from "the core would
      // not start", and rewording it here would lose that.
      const message = String(err)
      setError(message)
      setPhase(connecting ? 'disconnected' : 'connected')
      showNotice.error(message)
    }
  }, [phase])

  const busy = phase === 'connecting' || phase === 'disconnecting'
  const connected = phase === 'connected'

  const label =
    phase === 'unknown'
      ? 'Checking…'
      : phase === 'connecting'
        ? 'Connecting…'
        : phase === 'disconnecting'
          ? 'Disconnecting…'
          : connected
            ? 'Disconnect'
            : 'Connect'

  const statusText = connected
    ? tier
      ? `Connected · ${tier} tier`
      : 'Connected'
    : error
      ? error
      : 'Not connected'

  return (
    <EnhancedCard title="Locus" icon={undefined}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Box
            sx={{
              width: 10,
              height: 10,
              borderRadius: '50%',
              flexShrink: 0,
              bgcolor: connected
                ? theme.palette.success.main
                : error
                  ? theme.palette.error.main
                  : theme.palette.text.disabled,
              boxShadow: connected
                ? `0 0 0 4px ${alpha(theme.palette.success.main, 0.18)}`
                : 'none',
              transition: 'background-color 0.2s, box-shadow 0.2s',
            }}
          />
          <Typography
            variant="body2"
            sx={{
              color: error ? 'error.main' : 'text.secondary',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
            title={statusText}
          >
            {statusText}
          </Typography>
        </Box>

        <Button
          fullWidth
          size="large"
          variant={connected ? 'outlined' : 'contained'}
          color={connected ? 'inherit' : 'primary'}
          disabled={busy || phase === 'unknown'}
          onClick={() => void toggle()}
          sx={{ py: 1.3 }}
        >
          {busy ? <CircularProgress size={20} color="inherit" /> : label}
        </Button>

        {phase === 'unknown' && (
          <Typography variant="caption" color="text.secondary" sx={{ textAlign: 'center' }}>
            This device needs an activation code before it can connect.
          </Typography>
        )}
      </Box>
    </EnhancedCard>
  )
}

export default ProxyTunCard
