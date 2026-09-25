import { Box, Button, CircularProgress, Link, Typography } from '@mui/material'
import { useCallback, useEffect, useRef, useState } from 'react'

import { HUB_URL } from '@/services/hub'
import {
  locusActivate,
  locusCheckCode,
  locusValidateCode,
  type ActivationResult,
  type ValidateCodeResult,
} from '@/services/locus'

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
      setError(String(err))
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
        bgcolor: 'background.default',
      }}
    >
      <Box sx={{ width: 420, maxWidth: 'calc(100vw - 48px)', textAlign: 'center' }}>
        <Typography variant="h4" sx={{ fontWeight: 600, mb: 1 }}>
          Locus
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 4 }}>
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
            border: `1px solid ${shownError ? '#d32f2f' : localValid ? '#2e7d32' : 'rgba(0,0,0,0.23)'}`,
            outline: 'none',
            background: 'transparent',
            color: 'inherit',
          }}
        />

        {/* Reserved space, so the button does not jump as messages appear. */}
        <Box sx={{ minHeight: 44, mt: 1.5, display: 'flex', alignItems: 'flex-start', justifyContent: 'center' }}>
          {shownError && (
            <Typography variant="body2" sx={{ color: 'error.main', lineHeight: 1.4 }}>
              {shownError}
            </Typography>
          )}
          {!shownError && notice && (
            <Typography variant="body2" sx={{ color: 'success.main', lineHeight: 1.4 }}>
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
          sx={{ mt: 1, py: 1.4 }}
        >
          {busy ? <CircularProgress size={22} color="inherit" /> : 'Activate'}
        </Button>

        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 3 }}>
          Nothing on your card? The code is 15 characters and never contains{' '}
          <strong>0</strong>, <strong>O</strong>, <strong>1</strong> or <strong>I</strong>.
        </Typography>

        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>
          <Link href={HUB_URL} target="_blank" rel="noreferrer" underline="hover">
            {HUB_URL.replace(/^https?:\/\//, '')}
          </Link>
        </Typography>
      </Box>
    </Box>
  )
}

export default ActivationScreen
