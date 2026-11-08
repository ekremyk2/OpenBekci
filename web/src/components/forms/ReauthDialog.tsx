import { useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { AppError } from '@/api/errors'
import { settleReauth, useReauthOpen } from '@/app/reauth'
import { useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Field, FormError, SubmitButton } from './FormBits'

type Method = 'password' | 'sms_otp'

/** Seconds before another SMS code can be requested. */
export const RESEND_COOLDOWN_S = 30

/**
 * Re-authentication modal (web plan §7): password or a code sent to the account's own phone, then
 * `POST /v1/auth/reauth`. Closing it cancels; a successful confirmation lets `runWithReauth`
 * replay the pending request. Mounted once (ErrorEffects).
 */
export function ReauthDialog() {
  const open = useReauthOpen()
  const { me } = useSession()
  const hasPhone = !!me?.user.phone
  return (
    <Dialog open={open} onOpenChange={(next) => !next && settleReauth(false)}>
      <DialogContent>
        {open ? <ReauthForm hasPhone={hasPhone} phone={me?.user.phone ?? null} /> : null}
      </DialogContent>
    </Dialog>
  )
}

function ReauthForm({ hasPhone, phone }: { hasPhone: boolean; phone: string | null }) {
  const { t } = useLingui()
  const [method, setMethod] = useState<Method>('password')
  const [secret, setSecret] = useState('')
  const [sent, setSent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | undefined>()
  const [cooldown, setCooldown] = useState(0)

  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setTimeout(() => setCooldown((c) => c - 1), 1000)
    return () => clearTimeout(timer)
  }, [cooldown])

  async function sendCode() {
    if (!phone || cooldown > 0) return
    setError(undefined)
    setCooldown(RESEND_COOLDOWN_S)
    try {
      await draftApi.POST('/v1/auth/otp', { body: { phone } })
      setSent(true)
    } catch {
      setCooldown(0)
      setError(t`We could not send the code. Please try again.`)
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!secret) {
      setError(method === 'password' ? t`Enter your password` : t`Enter the code from the SMS`)
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      await draftApi.POST('/v1/auth/reauth', {
        body:
          method === 'password' ? { method, password: secret } : { method, code: secret.trim() },
      })
      settleReauth(true)
    } catch (err) {
      setError(
        err instanceof AppError && err.isRateLimited
          ? t`Too many attempts. Please wait and try again.`
          : t`That did not work. Check it and try again.`,
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="grid gap-4">
      <DialogHeader>
        <DialogTitle>
          <Trans>Confirm it is you</Trans>
        </DialogTitle>
        <DialogDescription>
          <Trans>This action is sensitive. Enter your password or a code to continue.</Trans>
        </DialogDescription>
      </DialogHeader>
      <Field
        id="reauth-secret"
        label={method === 'password' ? <Trans>Password</Trans> : <Trans>Code from the SMS</Trans>}
      >
        <Input
          id="reauth-secret"
          autoComplete={method === 'password' ? 'current-password' : 'one-time-code'}
          type={method === 'password' ? 'password' : 'text'}
          inputMode={method === 'password' ? undefined : 'numeric'}
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
        />
      </Field>
      {hasPhone ? (
        <div className="flex flex-wrap gap-2 text-sm">
          {method === 'password' ? (
            <Button
              type="button"
              variant="link"
              className="h-auto p-0"
              onClick={() => {
                setMethod('sms_otp')
                setSecret('')
                void sendCode()
              }}
            >
              <Trans>Send me a code instead</Trans>
            </Button>
          ) : (
            <>
              <Button
                type="button"
                variant="link"
                className="h-auto p-0"
                onClick={() => {
                  setMethod('password')
                  setSecret('')
                }}
              >
                <Trans>Use my password</Trans>
              </Button>
              {sent ? (
                <span className="text-muted-foreground">
                  <Trans>Code sent.</Trans>
                </span>
              ) : null}
              <Button
                type="button"
                variant="link"
                className="h-auto p-0"
                disabled={cooldown > 0}
                onClick={() => void sendCode()}
              >
                {cooldown > 0 ? (
                  <Trans>Send the code again in {cooldown} s</Trans>
                ) : (
                  <Trans>Send the code again</Trans>
                )}
              </Button>
            </>
          )}
        </div>
      ) : null}
      <FormError message={error} />
      <DialogFooter>
        <Button type="button" variant="outline" onClick={() => settleReauth(false)}>
          <Trans>Cancel</Trans>
        </Button>
        <SubmitButton busy={busy}>
          <Trans>Confirm</Trans>
        </SubmitButton>
      </DialogFooter>
    </form>
  )
}
