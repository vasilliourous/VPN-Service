import {
  Brightness4Outlined,
  CheckCircleOutlined,
  ErrorOutlineRounded,
  LanguageOutlined,
  MemoryOutlined,
  RefreshOutlined,
  ScheduleOutlined,
} from '@mui/icons-material'
import {
  Box,
  Button,
  Divider,
  MenuItem,
  Paper,
  Select,
  Tooltip,
  Typography,
  alpha,
  useTheme,
} from '@mui/material'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { TierBadge } from '@/components/connection/tier-badge'
import { TrafficSummaryCard } from '@/components/connection/traffic-summary'
import { useConnection } from '@/components/connection/use-connection'
import { useI18n } from '@/hooks/use-i18n'
import { useVerge } from '@/hooks/use-verge'
import { cardSx } from '@/pages/_surfaces'
import { supportedLanguages } from '@/services/i18n'
import { showNotice } from '@/services/notice-service'

/**
 * The Account screen — everything a student owns, and nothing about the engine.
 *
 * Settings live here rather than behind a separate tab because there are only
 * four a student should touch, and a whole destination for four rows is worse
 * than folding them in. The distinction that matters: language, theme, start-at-
 * login and update-checking are *preferences*; core choice, TUN, system proxy,
 * ports and DNS are *internals* Locus decides, and have no UI at all.
 */
const AccountPage = () => {
  const { t } = useTranslation()
  const theme = useTheme()
  const { status, refresh } = useConnection()
  const { verge, patchVerge } = useVerge()
  const { switchLanguage } = useI18n()

  const onError = useCallback((err: unknown) => showNotice.error(err), [])

  const subscription = status?.subscription ?? { state: 'unknown' as const }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {/* ── Subscription ─────────────────────────────────────────────────── */}
      <Paper elevation={0} sx={{ p: 2, ...cardSx(theme) }}>
        <SectionTitle icon={<ScheduleOutlined fontSize="small" />} title={t('home.components.connection.account.subscription')} />

        <Row
          label={t('home.components.connection.account.plan')}
          valueNode={
            status?.tier ? (
              <TierBadge tier={status.tier} />
            ) : (
              <Typography variant="body2" color="text.secondary">
                —
              </Typography>
            )
          }
        />

        {/* Expiry renders ONLY when the hub has actually told us a date.
            An unknown subscription shows the honest sentence instead of a blank
            row, so the student is not left wondering whether it failed to load. */}
        {subscription.state === 'active' && (
          <Row
            label={t('home.components.connection.account.expires')}
            value={
              subscription.daysRemaining <= 0
                ? t('home.components.connection.account.today')
                : subscription.daysRemaining === 1
                  ? t('home.components.connection.account.oneDayLeft')
                  : t('home.components.connection.account.daysLeft', { count: subscription.daysRemaining })
            }
            valueColor={subscription.urgent ? theme.palette.warning.main : undefined}
            tooltip={subscription.expiresAt}
          />
        )}
        {subscription.state === 'lapsed' && (
          <Row
            label={t('home.components.connection.account.expires')}
            value={t('home.components.connection.account.expired')}
            valueColor={theme.palette.error.main}
            icon={<ErrorOutlineRounded fontSize="small" sx={{ color: theme.palette.error.main }} />}
          />
        )}
        {subscription.state === 'unknown' && (
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
            {status?.activated
              ? t('home.components.connection.account.noExpiryYet')
              : t('home.components.connection.account.activateForExpiry')}
          </Typography>
        )}

        {subscription.state === 'active' && subscription.urgent && (
          <Typography
            variant="caption"
            sx={{ display: 'block', mt: 1, color: 'warning.main', lineHeight: 1.5 }}
          >
            {t('home.components.connection.account.expiresSoon')}
          </Typography>
        )}

        <Divider sx={{ my: 1.5 }} />

        {/* Session totals, not lifetime totals — mihomo's counters reset with the
            core, so the label says so rather than implying a billing figure. */}
        <TrafficSummaryCard />
      </Paper>

      {/* ── This device ──────────────────────────────────────────────────── */}
      <Paper elevation={0} sx={{ p: 2, ...cardSx(theme) }}>
        <SectionTitle icon={<MemoryOutlined fontSize="small" />} title={t('home.components.connection.account.thisDevice')} />
        <Row label={t('home.components.connection.account.deviceId')} value={status?.deviceId ?? '—'} mono />
        <Row label={t('home.components.connection.account.platform')} value={status?.platform ?? '—'} />
        <Row label={t('home.components.connection.account.appVersion')} value={status ? `v${status.version}` : '—'} />
        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>
          {t('home.components.connection.account.deviceIdHint')}
        </Typography>
      </Paper>

      {/* ── Preferences ──────────────────────────────────────────────────── */}
      <Paper elevation={0} sx={{ p: 2, ...cardSx(theme) }}>
        <SectionTitle icon={<Brightness4Outlined fontSize="small" />} title={t('home.components.connection.account.preferences')} />

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 1 }}>
          <LanguageOutlined fontSize="small" sx={{ color: 'text.secondary' }} />
          <Typography variant="body2" sx={{ flexGrow: 1 }}>
            {t('home.components.connection.account.language')}
          </Typography>
          <Select
            size="small"
            value={verge?.language ?? 'en'}
            onChange={(e) => void switchLanguage(e.target.value).catch(onError)}
            sx={{ minWidth: 140 }}
          >
            {supportedLanguages.map((lang) => (
              <MenuItem key={lang} value={lang}>
                {lang}
              </MenuItem>
            ))}
          </Select>
        </Box>

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 1 }}>
          <Brightness4Outlined fontSize="small" sx={{ color: 'text.secondary' }} />
          <Typography variant="body2" sx={{ flexGrow: 1 }}>
            {t('home.components.connection.account.appearance')}
          </Typography>
          {/* Reads and writes `verge.theme_mode`, which is the stored preference
              and supports "system". `useThemeMode()` is the RESOLVED value
              (light or dark only), so using it here would silently drop the
              "follow the system" choice and rewrite it on the next edit. */}
          <Select
            size="small"
            value={verge?.theme_mode ?? 'system'}
            onChange={(e) =>
              void patchVerge({ theme_mode: e.target.value }).catch(onError)
            }
            sx={{ minWidth: 140 }}
          >
            <MenuItem value="system">
              {t('home.components.connection.account.themeSystem')}
            </MenuItem>
            <MenuItem value="light">
              {t('home.components.connection.account.themeLight')}
            </MenuItem>
            <MenuItem value="dark">
              {t('home.components.connection.account.themeDark')}
            </MenuItem>
          </Select>
        </Box>

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 1 }}>
          <RefreshOutlined fontSize="small" sx={{ color: 'text.secondary' }} />
          <Typography variant="body2" sx={{ flexGrow: 1 }}>
            {t('home.components.connection.account.autoUpdate')}
          </Typography>
          <Button
            size="small"
            variant="text"
            onClick={() => {
              void patchVerge({ enable_auto_launch: !verge?.enable_auto_launch }).catch(onError)
            }}
          >
            {verge?.enable_auto_launch
              ? t('home.components.connection.account.on')
              : t('home.components.connection.account.off')}
          </Button>
        </Box>

        <Divider sx={{ my: 1.5 }} />

        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button size="small" startIcon={<RefreshOutlined />} onClick={() => void refresh()}>
            {t('home.components.connection.account.refresh')}
          </Button>
        </Box>
      </Paper>

      {/* Activation state, when there is something to say about it. */}
      {status && !status.activated && (
        <Paper
          elevation={0}
          sx={{
            p: 2,
            borderRadius: 2,
            border: `1px solid ${alpha(theme.palette.warning.main, 0.3)}`,
            bgcolor: alpha(theme.palette.warning.main, 0.05),
          }}
        >
          <Box sx={{ display: 'flex', gap: 1.25, alignItems: 'flex-start' }}>
            <CheckCircleOutlined
              fontSize="small"
              sx={{ color: theme.palette.warning.main, mt: 0.25 }}
            />
            <Typography variant="body2" sx={{ lineHeight: 1.5 }}>
              {t('home.components.connection.account.notActivated')}
            </Typography>
          </Box>
        </Paper>
      )}
    </Box>
  )
}

const SectionTitle = ({ icon, title }: { icon: React.ReactNode; title: string }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
    <Box sx={{ color: 'text.secondary', display: 'flex' }}>{icon}</Box>
    <Typography variant="subtitle2" sx={{ fontWeight: 600 }}>
      {title}
    </Typography>
  </Box>
)

const Row = ({
  label,
  value,
  valueColor,
  tooltip,
  icon,
  mono,
  /** Rendered instead of `value` when the caller has rich content (a badge). */
  valueNode,
}: {
  label: string
  value?: string
  valueColor?: string
  tooltip?: string
  icon?: React.ReactNode
  mono?: boolean
  valueNode?: React.ReactNode
}) => {
  const content = valueNode ?? (
    <Typography
      variant="body2"
      sx={{
        fontWeight: 'medium',
        color: valueColor,
        fontFamily: mono ? 'ui-monospace, SFMono-Regular, Menlo, monospace' : undefined,
      }}
    >
      {value}
    </Typography>
  )

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 0.75 }}>
      <Typography variant="body2" color="text.secondary" sx={{ flexGrow: 1 }}>
        {label}
      </Typography>
      {icon}
      {/* `Tooltip` requires a single element child, and `content` is now
          sometimes a bare string (via `valueNode`), so it is wrapped rather
          than passed through. */}
      {tooltip ? (
        <Tooltip title={tooltip} arrow>
          <Box component="span">{content}</Box>
        </Tooltip>
      ) : (
        content
      )}
    </Box>
  )
}

export default AccountPage
