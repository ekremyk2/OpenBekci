import { useState } from 'react'
import { DeleteIcon, UserRoundIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { AppError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/data/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { useIdempotencyKey } from '@/lib/idempotency'
import { unwrap, type SiteCtx } from '@/features/meetings/kit/api'
import { guardApi, setGuardSession, type createGuardApi } from './guardApi'

/**
 * Quick PIN switch between guards (C2): pick your name, enter your PIN. A wrong PIN, an unknown
 * guard and a guard without a PIN all look the same on purpose. `api` is injectable for tests.
 */
export function PinSwitch({
  ctx,
  api = guardApi,
}: {
  ctx: SiteCtx
  api?: ReturnType<typeof createGuardApi>
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const guards = useQuery({
    queryKey: ['ob', ctx.orgId, 'gate-guards', ctx.siteId],
    queryFn: () =>
      unwrap(
        api.GET('/v1/orgs/{org_id}/gate/guards', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 50 } },
        }),
      ).then((r) => r.items),
  })
  const [guard, setGuard] = useState<{ person_id: string; display_name: string } | null>(null)
  const [pin, setPin] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(value = pin) {
    if (!guard || value.length < 4 || busy) return
    setBusy(true)
    setError(null)
    try {
      const s = await unwrap(
        api.POST('/v1/orgs/{org_id}/gate/sessions', {
          params: { path: { org_id: ctx.orgId }, header: { 'Idempotency-Key': idem.key } },
          body: { site_id: ctx.siteId, person_id: guard.person_id, pin: value },
        }),
      )
      idem.renew()
      setPin('')
      setGuardSession({ token: s.token, personId: s.person_id, displayName: s.display_name, expiresAt: s.expires_at })
    } catch (e) {
      idem.renew()
      setPin('')
      if (e instanceof AppError && e.isRateLimited) {
        const minutes = Math.ceil((e.retryAfter ?? 900) / 60)
        setError(t`Too many wrong PINs. This guard is locked for about ${minutes} minutes.`)
      } else setError(t`Wrong PIN. Try again.`)
    } finally {
      setBusy(false)
    }
  }

  const press = (d: string) => setPin((p) => (p.length < 8 ? p + d : p))

  if (!guard) {
    return (
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 p-6">
        <h1 className="text-3xl font-semibold">
          <Trans>Who is on the gate?</Trans>
        </h1>
        {guards.isPending ? (
          <div role="status" aria-busy="true" className="grid gap-3 sm:grid-cols-2">
            <Skeleton className="h-20" />
            <Skeleton className="h-20" />
          </div>
        ) : guards.isError ? (
          <ErrorState error={guards.error} onRetry={() => void guards.refetch()} />
        ) : guards.data.length === 0 ? (
          <p className="text-lg text-muted-foreground">
            <Trans>No guard has a PIN yet. A manager sets guard PINs in the people settings.</Trans>
          </p>
        ) : (
          <ul className="grid gap-3 sm:grid-cols-2">
            {guards.data
              .filter((g) => g.has_pin)
              .map((g) => (
                <li key={g.person_id}>
                  <Button
                    variant="outline"
                    className="h-20 w-full justify-start gap-4 px-6 text-xl"
                    onClick={() => {
                      setGuard(g)
                      setError(null)
                    }}
                  >
                    <UserRoundIcon className="size-8" aria-hidden="true" />
                    {g.display_name}
                  </Button>
                </li>
              ))}
          </ul>
        )}
      </div>
    )
  }

  return (
    <form
      className="mx-auto flex w-full max-w-sm flex-col items-center gap-5 p-6"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <h1 className="text-2xl font-semibold">{guard.display_name}</h1>
      <label className="sr-only" htmlFor="guard-pin">
        <Trans>PIN</Trans>
      </label>
      <input
        id="guard-pin"
        type="password"
        inputMode="numeric"
        autoComplete="off"
        autoFocus
        value={pin}
        maxLength={8}
        onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))}
        aria-invalid={error ? true : undefined}
        className="h-16 w-full rounded-md border bg-background text-center text-3xl tracking-[0.5em] outline-none focus-visible:ring-[3px] focus-visible:ring-ring"
      />
      {error ? (
        <p role="alert" className="text-center text-lg text-destructive">
          {error}
        </p>
      ) : null}
      <div className="grid w-full grid-cols-3 gap-3">
        {['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((d) => (
          <Button key={d} type="button" variant="outline" className="h-16 text-2xl" onClick={() => press(d)}>
            {d}
          </Button>
        ))}
        <Button type="button" variant="ghost" className="h-16" onClick={() => setGuard(null)}>
          <Trans>Back</Trans>
        </Button>
        <Button type="button" variant="outline" className="h-16 text-2xl" onClick={() => press('0')}>
          0
        </Button>
        <Button type="button" variant="ghost" className="h-16" aria-label={t`Delete a digit`} onClick={() => setPin((p) => p.slice(0, -1))}>
          <DeleteIcon className="size-6 rtl-flip" />
        </Button>
      </div>
      <Button type="submit" size="lg" className="h-16 w-full text-xl" disabled={pin.length < 4 || busy}>
        <Trans>Start the shift</Trans>
      </Button>
    </form>
  )
}

