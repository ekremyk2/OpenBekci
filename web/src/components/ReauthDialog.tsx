import { useCallback, useRef, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Client } from 'openapi-fetch'
import { api } from '@/api/client'
import type { paths } from '@/api/draft'
import { AppError } from '@/api/errors'
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

/**
 * Re-authentication for sensitive actions (web plan §7): the API answers `403 reauth_required`,
 * this dialog collects the password, calls `POST /v1/auth/reauth` (valid for 10 minutes) and then
 * the caller replays the original request with the SAME Idempotency-Key.
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
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (password === '') {
      setError(t`Enter your password.`)
      return
    }
    setPending(true)
    setError(null)
    try {
      await authApi.POST('/v1/auth/reauth', { body: { method: 'password', password } })
      setPassword('')
      onSuccess() // before closing: the guard hands the pending action over first
      onOpenChange(false)
    } catch (err) {
      setError(
        err instanceof AppError && err.status === 429
          ? t`Too many attempts. Please wait and try again.`
          : t`That password is not correct.`,
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
              <Trans>This action needs a recent sign-in. Enter your password to continue.</Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="reauth-password">
              <Trans>Password</Trans>
            </Label>
            <Input
              id="reauth-password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-invalid={error ? true : undefined}
            />
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
 *
 *   const { run, dialog } = useReauthGuard()
 *   await run(() => doTheRequest())   // rejects with the original error unless reauth was needed
 *   return <>{dialog}</>
 */
export function useReauthGuard() {
  const [open, setOpen] = useState(false)
  const pending = useRef<{
    fn: () => Promise<unknown>
    resolve: (v: unknown) => void
    reject: (e: unknown) => void
  } | null>(null)

  const run = useCallback(<T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    return fn().catch((e: unknown) => {
      if (e instanceof AppError && e.isReauthRequired) {
        return new Promise<T | undefined>((resolve, reject) => {
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
          p.resolve(undefined)
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
