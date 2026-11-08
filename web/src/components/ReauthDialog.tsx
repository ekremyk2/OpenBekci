import { useCallback, useEffect, useRef, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Client } from 'openapi-fetch'
import { api } from '@/api/client'
import type { paths } from '@/api/draft'
import { AppError } from '@/api/errors'
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
import { Label } from '@/components/ui/label'

const authApi = api as unknown as Client<paths>

/** Thrown by `useReauthGuard().run` when the person dismissed the dialog: the action did not happen. */
export class ReauthCancelled extends Error {
  constructor() {
    super('reauth_cancelled')
    this.name = 'ReauthCancelled'
  }
}
export const isReauthCancelled = (e: unknown): e is ReauthCancelled => e instanceof ReauthCancelled

// While a guard is mounted the dialog itself answers `reauth_required`; the global toast stays quiet.
let mountedGuards = 0
export const hasReauthGuard = () => mountedGuards > 0

type Method = 'password' | 'sms_otp'

/**
 * Re-authentication for sensitive actions (web plan §7): the API answers `403 reauth_required`,
 * this dialog collects the password OR an SMS code sent to the account's own phone, calls
 * `POST /v1/auth/reauth` (valid for 10 minutes) and then the caller replays the original request
 * with the SAME Idempotency-Key.
 */
export function ReauthDialog({
  open,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useLingui()
  const { me } = useSession()
  const phone = me?.user.phone ?? null
  const [method, setMethod] = useState<Method>('password')
  const [secret, setSecret] = useState('')
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  function choose(next: Method) {
    setMethod(next)
    setSecret('')
    setError(null)
  }

  async function sendCode() {
    if (!phone) return
    setPending(true)
    setError(null)
    try {
      await authApi.POST('/v1/auth/otp', { body: { phone } })
      setSent(true)
    } catch (err) {
      setError(
        err instanceof AppError && err.status === 429
          ? t`Too many attempts. Please wait and try again.`
          : t`The code could not be sent.`,
      )
    } finally {
      setPending(false)
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (secret === '') {
      setError(method === 'password' ? t`Enter your password.` : t`Enter the code.`)
      return
    }
    setPending(true)
    setError(null)
    try {
      await authApi.POST('/v1/auth/reauth', {
        body:
          method === 'password'
            ? { method: 'password', password: secret }
            : { method: 'sms_otp', code: secret },
      })
      setSecret('')
      onSuccess() // before closing: the guard hands the pending action over first
      onOpenChange(false)
    } catch (err) {
      setError(
        err instanceof AppError && err.status === 429
          ? t`Too many attempts. Please wait and try again.`
          : method === 'password'
            ? t`That password is not correct.`
            : t`That code is not correct.`,
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>
              <Trans>Confirm it is you</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>This action needs a recent sign-in.</Trans>
            </DialogDescription>
          </DialogHeader>
          {phone ? (
            <div role="group" aria-label={t`How to confirm`} className="flex gap-2">
              <Button
                type="button"
                size="sm"
                variant={method === 'password' ? 'default' : 'outline'}
                aria-pressed={method === 'password'}
                onClick={() => choose('password')}
              >
                <Trans>Password</Trans>
              </Button>
              <Button
                type="button"
                size="sm"
                variant={method === 'sms_otp' ? 'default' : 'outline'}
                aria-pressed={method === 'sms_otp'}
                onClick={() => choose('sms_otp')}
              >
                <Trans>SMS code</Trans>
              </Button>
            </div>
          ) : null}
          <div className="flex flex-col gap-1.5">
            {method === 'password' ? (
              <>
                <Label htmlFor="reauth-password">
                  <Trans>Password</Trans>
                </Label>
                <Input
                  id="reauth-password"
                  type="password"
                  autoComplete="current-password"
                  value={secret}
                  onChange={(e) => setSecret(e.target.value)}
                  aria-invalid={error ? true : undefined}
                />
              </>
            ) : (
              <>
                <div className="flex items-center justify-between gap-2">
                  <Label htmlFor="reauth-code">
                    <Trans>Code from the SMS</Trans>
                  </Label>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={pending}
                    onClick={() => void sendCode()}
                  >
                    {sent ? <Trans>Send again</Trans> : <Trans>Send code</Trans>}
                  </Button>
                </div>
                <Input
                  id="reauth-code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={secret}
                  onChange={(e) => setSecret(e.target.value.replace(/\D/g, ''))}
                  aria-invalid={error ? true : undefined}
                />
              </>
            )}
            {error ? (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={pending}>
              <Trans>Confirm</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Wraps a submit action: when the API demands re-authentication the dialog opens, and after a
 * successful challenge the same action is replayed (the caller's idempotency key is untouched).
 * Dismissing the dialog rejects with `ReauthCancelled`, which callers ignore (no false error).
 *
 *   const { run, dialog } = useReauthGuard()
 *   await run(() => doTheRequest())
 *   return <>{dialog}</>
 */
export function useReauthGuard() {
  const [open, setOpen] = useState(false)
  const pending = useRef<{
    fn: () => Promise<unknown>
    resolve: (v: unknown) => void
    reject: (e: unknown) => void
  } | null>(null)

  useEffect(() => {
    mountedGuards += 1
    return () => {
      mountedGuards -= 1
    }
  }, [])

  const run = useCallback(<T,>(fn: () => Promise<T>): Promise<T> => {
    return fn().catch((e: unknown) => {
      if (e instanceof AppError && e.isReauthRequired) {
        return new Promise<T>((resolve, reject) => {
          pending.current = { fn, resolve: resolve as (v: unknown) => void, reject }
          setOpen(true)
        })
      }
      throw e
    })
  }, [])

  const dialog = (
    <ReauthDialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o && pending.current) {
          // dismissed without authenticating: the action simply did not happen
          const p = pending.current
          pending.current = null
          p.reject(new ReauthCancelled())
        }
      }}
      onSuccess={() => {
        const p = pending.current
        pending.current = null
        if (p) p.fn().then(p.resolve, p.reject)
      }}
    />
  )
  return { run, dialog }
}
