import { Box, Button, CircularProgress, Paper, Typography, alpha, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { TierBadge } from '@/components/connection/tier-badge'
import { TrafficGraph } from '@/components/connection/traffic-graph'
import { useConnection } from '@/components/connection/use-connection'
import { useTrafficSummary } from '@/components/connection/use-traffic-summary'
import { accentCardSx, cardSx } from '@/pages/_surfaces'

/**
 * The Connection screen — the product's primary surface.
 *
 * One decision, made obvious: the tunnel is on or off. A vendor VPN app shows a
 * single control and the evidence it is working; everything a student should not
 * have to reason about (which core, which mode, which node, which port) is
 * decided for them by Locus and the hub.
 *
 * What replaced what:
 *   - the raw "system proxy / TUN" switches  → the one Connect control
 *   - the proxy page's node picker           → nothing; one server per tier
 *   - the proxy page's core-unavailable text → the backend's own coded error
 */
const ConnectionPage = () => {
  const { t } = useTranslation()
  const theme = useTheme()
  const { phase, error, status, toggle } = useConnection()
  const summary = useTrafficSummary()

  const busy = phase === 'connecting' || phase === 'disconnecting'
  const connected = phase === 'connected'

  const label =
    phase === 'unknown'
      ? t('home.components.connection.checking')
      : phase === 'connecting'
        ? t('home.components.connection.connecting')
        : phase === 'disconnecting'
          ? t('home.components.connection.disconnecting')
          : connected
            ? t('home.components.connection.disconnect')
            : t('home.components.connection.connect')

  // The status line answers "is it working?" before the student asks.
  //
  // The tier is deliberately NOT concatenated into this string any more: it is
  // its own badge below. "Connected · strike" read as one run-on label, and the
  // spec gives the tier its own colour so it is scannable rather than parsed.
  const statusText = connected
    ? t('home.components.connection.connected')
    : t('home.components.connection.notConnected')

  // Spec §5: connected green, connecting amber, disconnected grey. The dot is
  // paired with the word everywhere it appears, so colour is never the only cue.
  const accent =
    phase === 'connecting' || phase === 'disconnecting'
      ? theme.palette.warning.main
      : connected
        ? theme.palette.success.main
        : theme.palette.text.disabled

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <Paper
        elevation={0}
        sx={{
          p: 3,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: 2,
          ...accentCardSx(theme, accent),
          transition: 'border-color 0.2s, background-color 0.2s',
        }}
      >
        {/* The state indicator is a coloured dot plus a word, not a colour alone:
            colour-only state is invisible to a colour-blind student. The dot
            pulses while connecting so "it is doing something" is visible without
            reading the button. */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.25 }}>
          <Box
            sx={{
              width: 12,
              height: 12,
              borderRadius: '50%',
              bgcolor: accent,
              boxShadow: connected
                ? `0 0 0 4px ${alpha(accent, 0.18)}`
                : busy
                  ? `0 0 0 4px ${alpha(accent, 0.14)}`
                  : 'none',
              transition: 'background-color 0.2s, box-shadow 0.2s',
              ...(busy && {
                animation: 'locus-pulse 1.4s ease-in-out infinite',
                '@keyframes locus-pulse': {
                  '0%, 100%': { opacity: 1 },
                  '50%': { opacity: 0.45 },
                },
              }),
            }}
          />
          <Typography variant="h6" sx={{ fontWeight: 500 }}>
            {statusText}
          </Typography>
        </Box>

        {/* The tier, given its own identity rather than appended to the status.
            Only shown once the user has a tier to show — an unactivated device
            has none, and an empty badge would read as a rendering fault. */}
        {status?.tier && <TierBadge tier={status.tier} />}

        <Button
          size="large"
          variant={connected ? 'outlined' : 'contained'}
          color={connected ? 'inherit' : 'primary'}
          disabled={busy || phase === 'unknown'}
          onClick={() => void toggle()}
          sx={{ minWidth: 220, py: 1.6, fontSize: 16 }}
        >
          {busy ? <CircularProgress size={22} color="inherit" /> : label}
        </Button>

        {/* The backend's sentence, shown as-is.
            `LOCUS_TUN_NOT_AVAILABLE` and `SERVICE_ELEVATION_FAILED` land here;
            both are written to be actionable, and paraphrasing them would throw
            away the part that says what to do. */}
        {error && (
          <Typography
            variant="body2"
            sx={{ color: 'error.main', textAlign: 'center', maxWidth: 420, lineHeight: 1.5 }}
          >
            {error}
          </Typography>
        )}

        {phase === 'unknown' && !error && (
          <Typography variant="caption" color="text.secondary" sx={{ textAlign: 'center' }}>
            {t('home.components.connection.needsActivation')}
          </Typography>
        )}
      </Paper>

      {/* Live speed. Hidden while disconnected rather than showing a flat zero,
          because a zero that means "off" reads the same as a zero that means
          "broken" — and the graph below is the honest answer when connected. */}
      {connected && (
        <Paper elevation={0} sx={{ p: 1.5, ...cardSx(theme) }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-around', mb: 1.5 }}>
            <SpeedReadout
              label={t('home.components.traffic.metrics.downloadSpeed')}
              value={summary.downSpeed}
              unit={`${summary.downSpeedUnit}/s`}
              color={theme.palette.primary.main}
            />
            <SpeedReadout
              label={t('home.components.traffic.metrics.uploadSpeed')}
              value={summary.upSpeed}
              unit={`${summary.upSpeedUnit}/s`}
              color={theme.palette.secondary.main}
            />
          </Box>
          <TrafficGraph />
        </Paper>
      )}
    </Box>
  )
}

const SpeedReadout = ({
  label,
  value,
  unit,
  color,
}: {
  label: string
  value: string
  unit: string
  color: string
}) => (
  <Box sx={{ textAlign: 'center' }}>
    <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
      {label}
    </Typography>
    <Typography
      variant="h6"
      sx={{ color, fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}
    >
      {value}
      <Typography component="span" variant="caption" sx={{ ml: 0.5, color: 'text.secondary' }}>
        {unit}
      </Typography>
    </Typography>
  </Box>
)

export default ConnectionPage
