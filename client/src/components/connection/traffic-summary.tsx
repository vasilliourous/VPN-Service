import { CloudDownloadRounded, CloudUploadRounded } from '@mui/icons-material'
import { Box, Typography, alpha, useTheme } from '@mui/material'
import { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { useTrafficSummary } from './use-traffic-summary'

interface TotalRowProps {
  icon: ReactNode
  label: string
  value: string
  unit: string
  color: string
}

const TotalRow = ({ icon, label, value, unit, color }: TotalRowProps) => (
  <Box
    sx={{
      display: 'flex',
      alignItems: 'center',
      gap: 1.5,
      py: 0.75,
    }}
  >
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: 32,
        height: 32,
        borderRadius: 1.5,
        flexShrink: 0,
        bgcolor: alpha(color, 0.1),
        color,
      }}
    >
      {icon}
    </Box>
    <Typography variant="body2" color="text.secondary" sx={{ flexGrow: 1 }}>
      {label}
    </Typography>
    <Typography variant="body2" sx={{ fontWeight: 'medium', fontVariantNumeric: 'tabular-nums' }}>
      {value} {unit}
    </Typography>
  </Box>
)

/**
 * Session traffic totals for the Account screen.
 *
 * **"This session", not "this account".** mihomo's counters are per-process, so a
 * disconnect, a reconnect or any core restart zeroes them. Presenting them as a
 * lifetime figure would be a lie the student can catch — they would upload
 * something, restart the app, and watch the number reset. This is also why the
 * product shows no quota or usage allowance: the client genuinely does not know
 * one, and inventing it is worse than omitting it.
 *
 * The live *speed* readouts are deliberately NOT here — they belong on the
 * Connection screen next to the tunnel state, where "is it moving?" is the
 * question being asked.
 */
export const TrafficSummaryCard = () => {
  const theme = useTheme()
  const { t } = useTranslation()
  const summary = useTrafficSummary()

  return (
    <Box>
      <TotalRow
        icon={<CloudUploadRounded fontSize="small" />}
        label={t('home.components.connection.sessionUpload')}
        value={summary.uploaded}
        unit={summary.uploadedUnit}
        color={theme.palette.secondary.main}
      />
      <TotalRow
        icon={<CloudDownloadRounded fontSize="small" />}
        label={t('home.components.connection.sessionDownload')}
        value={summary.downloaded}
        unit={summary.downloadedUnit}
        color={theme.palette.primary.main}
      />
    </Box>
  )
}
