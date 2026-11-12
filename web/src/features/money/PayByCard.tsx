import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { LoaderCircleIcon } from 'lucide-react'
import { AppError } from '@/api/errors'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useBodyIdempotencyKey } from '@/lib/idempotency'
import { useSiteCard } from './checkout'
import { idemHeader, moneyApi, unwrap, useMoneyMutation, type MoneyCtx, type S } from './api'
import { FormRootError, Field, Select } from './ui'
import { moneyErrorText } from './labels'

/** The one place the page leaves for the provider (a top-level navigation, never an iframe). */
export const navigation = {
  go(url: string) {
    window.location.assign(url)
  },
}

/** Only web pages: a `javascript:` or `data:` URL from a broken provider must never be followed. */
export function isSafeCheckoutUrl(url: string): boolean {
  try {
    const u = new URL(url)
    return u.protocol === 'https:' || u.protocol === 'http:'
  } catch {
    return false
  }
}

/** The "Pay by card" card of the Pay screen; absent when the site has no card payment. */
export function PayByCardSection({ ctx, sides }: { ctx: MoneyCtx; sides: S['MySide'][] }) {
  const { can } = useSession()
  const card = useSiteCard(ctx)
  if (!can('payments.checkout') || !card.data?.card.available) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Pay by card</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <p>
          <Trans>
            Pay your open charges, chosen charges or an amount with your card. The payment page is
            run by the payment provider, and the receipt arrives as soon as it is confirmed.
          </Trans>
        </p>
        <div>
          <PayByCardButton ctx={ctx} sides={sides} />
        </div>
      </CardContent>
    </Card>
  )
}

type Mode = 'all' | 'charges' | 'amount'

const sideKey = (s: { unit_id: string; debtor_kind: string }) => `${s.unit_id}:${s.debtor_kind}`

/**
 * "Pay by card": shown to a resident only when the site settings say `payments.card.available`
 * and the session may start a checkout. Opens the dialog; on success the page navigates to the
 * provider's `checkout_url`.
 */
export function PayByCardButton({
  ctx,
  sides,
  side,
  variant = 'default',
}: {
  ctx: MoneyCtx
  sides: S['MySide'][]
  /** Fix the side (a card on the balance screen); otherwise the dialog lets the payer choose. */
  side?: S['MySide']
  variant?: 'default' | 'outline'
}) {
  const { can } = useSession()
  const card = useSiteCard(ctx)
  const [open, setOpen] = useState(false)
  if (!can('payments.checkout') || !card.data?.card.available) return null
  return (
    <>
      <Button variant={variant} onClick={() => setOpen(true)}>
        <Trans>Pay by card</Trans>
      </Button>
      {open ? (
        <PayByCardDialog ctx={ctx} sides={side ? [side] : sides} onClose={() => setOpen(false)} />
      ) : null}
    </>
  )
}

function PayByCardDialog({
  ctx,
  sides,
  onClose,
}: {
  ctx: MoneyCtx
  sides: S['MySide'][]
  onClose: () => void
}) {
  const { t } = useLingui()
  const [key, setKey] = useState(sideKey(sides[0]!))
  const [mode, setMode] = useState<Mode>('all')
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [amount, setAmount] = useState<bigint | null>(null)
  const [error, setError] = useState<string>()
  const [leaving, setLeaving] = useState(false)
  const side = sides.find((s) => sideKey(s) === key) ?? sides[0]!

  const charges = useQuery({
    queryKey: ['money', ctx.orgId, 'pay-charges', ctx.siteId, side.unit_id, side.debtor_kind],
    enabled: mode === 'charges',
    staleTime: 0,
    queryFn: () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/charges', {
          params: {
            path: { org_id: ctx.orgId },
            query: {
              site_id: ctx.siteId,
              unit_id: side.unit_id,
              debtor_kind: side.debtor_kind,
              limit: 200,
            },
          },
        }),
      ).then((r) =>
        r.items
          .filter((c) => c.balance.amount > 0 && c.status !== 'cancelled' && c.status !== 'paid')
          .sort((a, b) => a.due_date.localeCompare(b.due_date)),
      ),
  })
  const chosen = useMemo(
    () => (charges.data ?? []).filter((c) => picked.has(c.id)),
    [charges.data, picked],
  )
  const chosenTotal = chosen.reduce((a, c) => a + c.balance.amount, 0)

  const body = useMemo(
    () => ({
      unit_id: side.unit_id,
      debtor_kind: side.debtor_kind as 'owner' | 'tenant',
      client: 'web' as const,
      ...(mode === 'charges' ? { charge_ids: chosen.map((c) => c.id) } : {}),
      ...(mode === 'amount' && amount !== null
        ? { amount: { amount: Number(amount), currency: ctx.currency } }
        : {}),
    }),
    [side, mode, chosen, amount, ctx.currency],
  )
  // the key follows the body: a retry replays it, an edited choice gets a fresh one
  const idem = useBodyIdempotencyKey(body)

  const start = useMoneyMutation(async () => {
    const checkout = await unwrap(
      moneyApi.POST('/v1/orgs/{org_id}/checkouts', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body,
      }),
    )
    if (!checkout.checkout_url || !isSafeCheckoutUrl(checkout.checkout_url))
      throw new AppError({ status: 502, code: 'pack_error' })
    return checkout.checkout_url
  })

  const missing =
    mode === 'charges'
      ? chosen.length === 0
      : mode === 'amount'
        ? !amount || amount <= 0n
        : side.total.amount <= 0

  function submit() {
    setError(undefined)
    start.mutate(undefined, {
      onSuccess: (url) => {
        setLeaving(true)
        navigation.go(url)
      },
      onError: (e) =>
        setError(moneyErrorText(e, t`Could not start the card payment. Please try again.`)),
    })
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !leaving && onClose()}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!missing) submit()
          }}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Pay by card</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                You will go to the payment provider&apos;s page to enter your card. Your card
                details never reach this site.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          {sides.length > 1 ? (
            <Field label={t`Paying for`} htmlFor="pc-side">
              <Select
                id="pc-side"
                value={key}
                onChange={(e) => {
                  setKey(e.target.value)
                  setPicked(new Set())
                }}
              >
                {sides.map((s) => (
                  <option key={sideKey(s)} value={sideKey(s)}>
                    {s.unit_door_number} – {s.debtor_kind === 'tenant' ? t`Tenant` : t`Owner`}
                  </option>
                ))}
              </Select>
            </Field>
          ) : null}
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-1 text-sm font-medium">
              <Trans>What to pay</Trans>
            </legend>
            {(
              [
                ['all', t`Everything that is open`],
                ['charges', t`Choose charges`],
                ['amount', t`An amount`],
              ] as const
            ).map(([value, label]) => (
              <label key={value} className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="pc-mode"
                  value={value}
                  checked={mode === value}
                  onChange={() => setMode(value)}
                />
                {label}
              </label>
            ))}
          </fieldset>
          {mode === 'all' ? (
            <p className="text-sm text-muted-foreground">
              <Trans>
                Your open charges are paid oldest first. Today you owe{' '}
                <Money minor={side.total.amount} currency={ctx.currency} />.
              </Trans>
              {side.total.amount <= 0 ? (
                <>
                  {' '}
                  <Trans>
                    Nothing is open, so there is nothing to pay. Choose an amount to prepay.
                  </Trans>
                </>
              ) : null}
            </p>
          ) : null}
          {mode === 'charges' ? (
            charges.isPending ? (
              <p className="text-sm text-muted-foreground" role="status">
                <Trans>Loading your open charges…</Trans>
              </p>
            ) : charges.isError ? (
              <p role="alert" className="text-sm text-destructive">
                <Trans>Could not load your charges.</Trans>
              </p>
            ) : charges.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                <Trans>Nothing is open on this side.</Trans>
              </p>
            ) : (
              <div className="flex flex-col gap-2">
                <ul aria-label={t`Open charges`} className="flex flex-col gap-1">
                  {charges.data.map((c) => (
                    <li key={c.id}>
                      <label className="flex items-start gap-3 text-sm">
                        <Checkbox
                          className="mt-0.5"
                          checked={picked.has(c.id)}
                          onChange={(e) =>
                            setPicked((prev) => {
                              const next = new Set(prev)
                              if (e.target.checked) next.add(c.id)
                              else next.delete(c.id)
                              return next
                            })
                          }
                        />
                        <span className="flex flex-1 flex-wrap justify-between gap-x-3">
                          <span>
                            {c.description}{' '}
                            <span className="text-muted-foreground">
                              (<SiteDate value={c.due_date} timeZone={ctx.timeZone} />)
                            </span>
                          </span>
                          <Money minor={c.balance.amount} currency={ctx.currency} />
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
                <p className="text-sm font-medium">
                  <Trans>
                    Total <Money minor={chosenTotal} currency={ctx.currency} />
                  </Trans>
                </p>
              </div>
            )
          ) : null}
          {mode === 'amount' ? (
            <Field
              label={t`Amount`}
              htmlFor="pc-amount"
              hint={t`More than you owe becomes a prepayment credit.`}
            >
              <MoneyInput
                id="pc-amount"
                value={amount}
                onChange={setAmount}
                currency={ctx.currency}
              />
            </Field>
          ) : null}
          <FormRootError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" disabled={leaving} onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={start.isPending || leaving || missing}>
              {start.isPending || leaving ? (
                <LoaderCircleIcon className="animate-spin" aria-hidden="true" />
              ) : null}
              {leaving ? (
                <Trans>Taking you to the payment page…</Trans>
              ) : (
                <Trans>Continue to payment</Trans>
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
