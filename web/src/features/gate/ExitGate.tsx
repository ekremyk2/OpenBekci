import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ArrowLeftIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
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
import { useIdempotencyKey } from '@/lib/idempotency'
import { unwrap, type SiteCtx } from '@/features/meetings/kit/api'
import { guardApi, setGuardSession, type createGuardApi, type GuardSessionState } from './guardApi'

/**
 * Leaving the guard console takes the guard's PIN: the booth is signed in as the booth user, and
 * the app shell behind the console must not be one tap away for anyone standing at the booth. The
 * PIN is checked with the same call as the quick switch. On success the guard session is dropped
 * (the contract has no end-session call yet) and the booth user is taken to the site page.
 */
export function ExitGate({
  ctx,
  guard,
  api = guardApi,
}: {
  ctx: SiteCtx
  guard: GuardSessionState
  api?: ReturnType<typeof createGuardApi>
}) {
  const { t } = useLingui()
  const navigate = useNavigate()
  const idem = useIdempotencyKey()
  const [open, setOpen] = useState(false)
  const [pin, setPin] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (pin.length < 4 || busy) return
    setBusy(true)
    setError(null)
    try {
      await unwrap(
        api.POST('/v1/orgs/{org_id}/gate/sessions', {
          params: { path: { org_id: ctx.orgId }, header: { 'Idempotency-Key': idem.key } },
          body: { site_id: ctx.siteId, person_id: guard.personId, pin },
        }),
      )
      idem.renew()
      setPin('')
      setGuardSession(null)
      void navigate({ to: '/orgs/$orgId', params: { orgId: ctx.orgId } })
    } catch (err) {
      idem.renew()
      setPin('')
      if (err instanceof AppError && err.isRateLimited)
        setError(t`Too many wrong PINs. Try again later.`)
      else if (err instanceof AppError && err.code === 'invalid_grant')
        setError(t`Wrong PIN. Try again.`)
      else setError(t`The PIN could not be checked. Check the connection and try again.`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button
        variant="ghost"
        size="xl"
        aria-label={t`Leave the guard console`}
        onClick={() => setOpen(true)}
      >
        <ArrowLeftIcon className="rtl-flip" />
        <Trans>Exit</Trans>
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) {
            setPin('')
            setError(null)
          }
        }}
      >
        <DialogContent>
          <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>
                <Trans>Leave the guard console</Trans>
              </DialogTitle>
              <DialogDescription>
                <Trans>Enter your PIN to leave. {guard.displayName} is on the gate.</Trans>
              </DialogDescription>
            </DialogHeader>
            <Input
              type="password"
              inputMode="numeric"
              autoComplete="off"
              aria-label={t`PIN`}
              maxLength={8}
              value={pin}
              onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))}
              aria-invalid={error ? true : undefined}
              className="h-14 text-center text-2xl tracking-[0.5em]"
            />
            {error ? (
              <p role="alert" className="text-destructive">
                {error}
              </p>
            ) : null}
            <DialogFooter>
              <Button type="button" variant="outline" size="xl" onClick={() => setOpen(false)}>
                <Trans>Cancel</Trans>
              </Button>
              <Button type="submit" size="xl" disabled={pin.length < 4 || busy}>
                <Trans>Leave</Trans>
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
