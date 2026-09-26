import {
  Box,
  Button,
  CircularProgress,
  Typography,
  alpha,
} from '@mui/material'
import { useCallback, useEffect, useRef, useState } from 'react'

import { LOCUS_COLORS } from '@/pages/_theme'
import {
  locusActivate,
  locusCheckCode,
  locusValidateCode,
  type ActivationResult,
  type ValidateCodeResult,
} from '@/services/locus'
import { errorDetail } from '@/services/notice-service'

/**
 * First-run activation gate.
 *
 * The app renders this and NOTHING else until a code is accepted. That is the
 * whole product decision: a Locus student does not configure Clash, choose a
 * profile or pick a node — they enter the code on the card they were sold.
 *
 * Behaviour deliberately preserved from the retired client, because it was
 * learned from real support conversations:
 *
 *  * The checksum is validated LOCALLY as you type. `/api/activate` allows 5
 *    attempts per 10 minutes per address, so four typos would lock a student out
 *    of the one screen that could explain their mistake. A local check costs
 *    nothing and never counts against them.
 *
 *  * Formatting is normalised before anything is sent. A code is stored and
 *    looked up hyphenated, so a paste without hyphens would 404 and read as
 *    "your code is not recognised" — which is the single most annoying failure
 *    this screen can produce.
 *
 *  * "Already used on THIS device" is a success, not an error. Students
 *    reinstall, and re-pasting their own code must not tell them it is dead.
 */

type Phase = 'idle' | 'checking' | 'activating' | 'done'

const CODE_LENGTH = 15 // RQ + 3x4 + checksum, hyphens excluded

/** Strips formatting and uppercases, mirroring the backend's normalisation. */
const clean = (input: string) => input.replace(/[^a-z0-9]/gi, '').toUpperCase()

/** Groups a bare code into the hyphenated form as the student types. */
const present = (input: string) => {
  const raw = clean(input)
  const parts = [raw.slice(0, 2), raw.slice(2, 6), raw.slice(6, 10), raw.slice(10, 14), raw.slice(14, 15)]
  return parts.filter(Boolean).join('-')
}

interface Props {
  onActivated: (result: ActivationResult) => void
}

const ActivationScreen = ({ onActivated }: Props) => {
  const [input, setInput] = useState('')
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  // `null` means "not checked yet". Derived state holds the answer rather than
  // being pushed into another state variable on every keystroke.
  const [check, setCheck] = useState<ValidateCodeResult | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  // Local checksum check, run only once the code is the right length.
  //
  // Set from inside the async callback rather than synchronously in the effect
  // body: writing state during an effect causes an extra render on every
  // keystroke, and the incomplete-code case needs no state at all — it is
  // simply "no check has run yet", which the derived values below already
  // express.
  useEffect(() => {
    const raw = clean(input)
    if (raw.length !== CODE_LENGTH) return

    let cancelled = false
    locusValidateCode(raw)
      .then((result) => {
        if (!cancelled) setCheck(result)
      })
      .catch(() => {
        if (!cancelled) setCheck(null)
      })

    return () => {
      cancelled = true
    }
  }, [input])

  const raw = clean(input)
  const complete = raw.length === CODE_LENGTH

  // The check belongs to THIS input only. While the student is still typing, a
  // verdict about a previous, longer code must not colour the field.
  const verdict = complete && check?.canonical === present(raw) ? check : null
  const localValid = verdict?.valid === true

  // A malformed-code complaint is shown only once the code is complete, so the
  // student is not scolded mid-typing. Anything raised by activation itself
  // (`error`) takes precedence and is shown regardless.
  const shownError = error ?? (complete ? (verdict?.message ?? null) : null)

  const canSubmit = localValid && phase === 'idle'

  const submit = useCallback(async () => {
    const raw = clean(input)
    if (raw.length !== CODE_LENGTH) return

    setError(null)
    setNotice(null)

    // Ask the hub whether the code is usable here BEFORE binding. This is what
    // turns "activation failed" into "this code is in use on another device",
    // and it costs one read-only request instead of a failed binding attempt
    // that counts against the rate limit.
    setPhase('checking')
    try {
      const check = await locusCheckCode(raw)
      if (!check.ready) {
        setError(check.message)
        setPhase('idle')
        return
      }
      if (check.tier) setNotice(`Ready — this code gives you the ${check.tier} tier`)
    } catch {
      // The lookup is advisory. If it cannot be reached we still try to
      // activate, because the hub will make the same call anyway and refusing
      // here would block a student on a flaky connection for no reason.
      setNotice(null)
    }

    setPhase('activating')
    try {
      const result = await locusActivate(raw)
      setPhase('done')
      onActivated(result)
    } catch (err) {
      // `errorDetail`, not `String`: a Rust failure arrives as
      // `CommandFailure { code, detail }`, so `String(err)` renders as
      // "[object Object]" and throws away the only useful sentence — "this code
      // is already in use on another device", "the code has expired", and so on.
      setError(errorDetail(err))
      setPhase('idle')
    }
  }, [input, onActivated])

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter' && canSubmit) void submit()
  }

  const busy = phase === 'checking' || phase === 'activating'

  return (
    <Box
      sx={{
        width: '100vw',
        height: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        // ── The brand, stated explicitly ──────────────────────────────────
        //
        // This screen renders OUTSIDE `ThemeModeProvider` (see `main.tsx`): it is
        // the pre-activation gate, so it cannot read the app theme — it gets a
        // bare `createTheme()`. Everything here is therefore written from the
        // Locus constants directly.
        //
        // That is not only a styling choice. The document sets
        // `body { color: var(--text-color) }`, which flips to `#ffffff` under a
        // dark system scheme, and it once paired that with MUI's white default
        // background: white text on white, so every label was invisible while
        // present and correct in the DOM. Stating both colours makes the screen
        // self-consistent by construction.
        //
        // It is also the highest-value surface in the product: it is the first
        // thing a student sees, and the only screen a person who has not paid
        // yet will ever look at.
        color: LOCUS_COLORS.textPrimary,
        bgcolor: LOCUS_COLORS.background,
        // A single soft green radial, per the spec's "green-black" identity.
        // Subtle on purpose: a strong gradient behind a text input hurts
        // legibility, and this is a screen about typing accurately.
        backgroundImage: `radial-gradient(ellipse 80% 60% at 50% 0%, ${alpha(LOCUS_COLORS.accent, 0.10)}, transparent 70%)`,
      }}
    >
      <Box sx={{ width: 420, maxWidth: 'calc(100vw - 48px)', textAlign: 'center' }}>
        {/* Brand mark. The spec calls for a 48×48 shield in Locus green beside
            the wordmark; there is no Locus asset in the repo yet, so the mark is
            the wordmark alone for now — deliberately, rather than substituting
            another product's logo. */}
        <Typography
          variant="h4"
          sx={{
            fontWeight: 700,
            letterSpacing: '-0.01em',
            mb: 0.5,
            color: LOCUS_COLORS.accent,
          }}
        >
          Locus
        </Typography>
        <Typography
          variant="body2"
          sx={{ mb: 4, color: LOCUS_COLORS.textSecondary, letterSpacing: '0.01em' }}
        >
          Secure school VPN
        </Typography>

        <Typography
          variant="body2"
          sx={{ mb: 2.5, color: LOCUS_COLORS.textPrimary, fontWeight: 500 }}
        >
          Enter the activation code from your card.
        </Typography>

        <input
          ref={inputRef}
          value={present(input)}
          onChange={(event) => {
            // An event handler is the right place for this: the previous
            // verdict no longer describes what is in the field.
            setCheck(null)
            setError(null)
            setInput(event.target.value)
          }}
          onKeyDown={onKeyDown}
          disabled={busy}
          spellCheck={false}
          autoComplete="off"
          autoCapitalize="characters"
          placeholder="RQ-XXXX-XXXX-XXXX-X"
          aria-label="Activation code"
          style={{
            width: '100%',
            boxSizing: 'border-box',
            padding: '16px 18px',
            fontSize: 20,
            letterSpacing: '0.08em',
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            textAlign: 'center',
            textTransform: 'uppercase',
            borderRadius: 8,
            // The verdict colours are Locus's, not MUI's defaults: this screen
            // is outside the provider, so `theme.palette.divider` would be the
            // LIGHT theme's divider — nearly invisible on this green-black page.
            border: `1px solid ${
              shownError
                ? LOCUS_COLORS.error
                : localValid
                  ? LOCUS_COLORS.success
                  : LOCUS_COLORS.border
            }`,
            outline: 'none',
            // A filled field rather than a transparent one: on the dark surface
            // an transparent input reads as a bare underline and looks broken.
            background: LOCUS_COLORS.surface,
            color: LOCUS_COLORS.textPrimary,
            transition: 'border-color 0.15s ease-out',
          }}
        />

        {/* Reserved space, so the button does not jump as messages appear. */}
        <Box sx={{ minHeight: 44, mt: 1.5, display: 'flex', alignItems: 'flex-start', justifyContent: 'center' }}>
          {shownError && (
            <Typography variant="body2" sx={{ color: LOCUS_COLORS.error, lineHeight: 1.4 }}>
              {shownError}
            </Typography>
          )}
          {!shownError && notice && (
            <Typography variant="body2" sx={{ color: LOCUS_COLORS.success, lineHeight: 1.4 }}>
              {notice}
            </Typography>
          )}
        </Box>

        <Button
          fullWidth
          size="large"
          variant="contained"
          disabled={!canSubmit}
          onClick={() => void submit()}
          sx={{
            mt: 1,
            py: 1.4,
            // The primary action, in Locus green. Stated explicitly because
            // this screen cannot read the app theme; without it the button
            // would render in MUI's default blue on every install.
            bgcolor: LOCUS_COLORS.accent,
            color: LOCUS_COLORS.background,
            fontWeight: 600,
            textTransform: 'none',
            fontSize: 16,
            borderRadius: 2,
            boxShadow: 'none',
            transition: 'background-color 0.15s ease-out',
            '&:hover': { bgcolor: LOCUS_COLORS.accentHover },
            '&.Mui-disabled': {
              bgcolor: LOCUS_COLORS.accent,
              opacity: 0.35,
            },
          }}
        >
          {busy ? <CircularProgress size={22} color="inherit" /> : 'Activate'}
        </Button>

        <Typography
          variant="caption"
          sx={{ display: 'block', mt: 3, color: LOCUS_COLORS.textSecondary }}
        >
          Nothing on your card? The code is 15 characters and never contains{' '}
          <strong>0</strong>, <strong>O</strong>, <strong>1</strong> or <strong>I</strong>.
        </Typography>

        {/* No hub URL here.
            //
            // This used to render `HUB_URL` as a visible link. The hub host is
            // infrastructure — `networkingguides.duckdns.org` is the address the
            // client *calls*, not a place a student should be pointed at. Showing
            // it on the one screen a student meets before they have any context
            // invites them to treat a backend hostname as product surface, and a
            // hostname is not a support route: it cannot answer "my code says it
            // is already used".
            //
            // The card is the support route: the code is printed on it, beside
            // whatever contact details the reseller puts there. There is
            // deliberately nothing here in its place — inventing a placeholder
            // address would only move the same problem. */}
      </Box>
    </Box>
  )
}

export default ActivationScreen
