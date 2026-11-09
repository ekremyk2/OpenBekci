import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch } from 'react-hook-form'
import { Trans, useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useBodyIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import {
  idemHeader,
  moneyApi,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { isoDate, minorToMoney, MoneyFormField, positiveMinor, useZodMessages } from './forms'
import {
  applyServerErrors,
  Field,
  FormRootError,
  Gate,
  PageHeader,
  QueryBoundary,
  Select,
} from './ui'

/** The clearing balance and client money of the site, then the provider payouts posted for it. */
export function SettlementsPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const [provider, setProvider] = useState('')
  const [payoutOpen, setPayoutOpen] = useState(false)
  const clearing = useMoneyQuery(['payment-clearing', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/payment-clearing', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ),
  )
  const settlements = useMoneyQuery(['payment-settlements', ctx.siteId, provider], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/payment-settlements', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, provider: provider || undefined, limit: 200 },
        },
      }),
    ),
  )
  // providers to filter by: the ones with a clearing balance row plus the ones seen in payouts
  const providers = useMemo(() => {
    const ids = new Set<string>(clearing.data?.providers.map((p) => p.provider) ?? [])
    for (const s of settlements.data?.items ?? []) ids.add(s.provider)
    if (provider) ids.add(provider)
    return [...ids].sort()
  }, [clearing.data, settlements.data, provider])

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Card settlements`}
        description={t`What the payment providers have not paid out yet, the client money held for this site, and the payouts posted so far.`}
        actions={
          <Link
            to="/orgs/$orgId/money/checkouts"
            params={{ orgId: ctx.orgId }}
            className={buttonVariants({ variant: 'outline' })}
          >
            <Trans>Card checkouts</Trans>
          </Link>
        }
      />
      <QueryBoundary query={clearing} skeleton="page">
        {(c) => <ClearingCards ctx={ctx} clearing={c} onPayout={() => setPayoutOpen(true)} />}
      </QueryBoundary>
      <section aria-labelledby="settle-title" className="flex flex-col gap-3">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <h2 id="settle-title" className="text-lg font-semibold">
            <Trans>Provider payouts</Trans>
          </h2>
          <Field label={t`Provider`} htmlFor="st-provider">
            <Select
              id="st-provider"
              className="w-auto"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
            >
              <option value="">{t`All providers`}</option>
              {providers.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <QueryBoundary
          query={settlements}
          isEmpty={(d) => d.items.length === 0}
          emptyTitle={t`No payouts yet`}
          emptyDescription={t`A periodic job posts each provider's payouts here.`}
        >
          {({ items, next_cursor }) => (
            <>
              {next_cursor ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>
                    Showing the newest 200 payouts. Filter by provider to narrow the list.
                  </Trans>
                </p>
              ) : null}
              <Table aria-label={t`Provider payouts`}>
                <TableHeader>
                  <TableRow>
                    <TableHead>
                      <Trans>Settled on</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Provider</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Reference</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Gross</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Fee</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Net to this site</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Collected by</Trans>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((s) => (
                    <SettlementRow key={`${s.provider}:${s.ref}:${s.site_id}`} s={s} ctx={ctx} />
                  ))}
                </TableBody>
              </Table>
            </>
          )}
        </QueryBoundary>
      </section>
      {payoutOpen && clearing.data ? (
        <PayoutDialog
          ctx={ctx}
          owed={clearing.data.client_money_owed.amount}
          onClose={() => setPayoutOpen(false)}
        />
      ) : null}
    </div>
  )
}

function SettlementRow({ s, ctx }: { s: S['ProviderSettlement']; ctx: MoneyCtx }) {
  return (
    <TableRow>
      <TableCell>
        <SiteDate value={s.settled_on} timeZone={ctx.timeZone} />
      </TableCell>
      <TableCell>{s.provider}</TableCell>
      <TableCell className="font-mono text-xs">{s.ref}</TableCell>
      <TableCell className="text-end">
        {s.gross ? <Money minor={s.gross.amount} currency={s.gross.currency} /> : '–'}
      </TableCell>
      <TableCell className="text-end">
        {s.fee ? <Money minor={s.fee.amount} currency={s.fee.currency} /> : '–'}
      </TableCell>
      <TableCell className="text-end">
        <Money minor={s.net.amount} currency={s.net.currency} />
      </TableCell>
      <TableCell>
        {s.merchant_of_record === 'organization' ? (
          <Trans>Organization</Trans>
        ) : (
          <Trans>Site</Trans>
        )}
      </TableCell>
    </TableRow>
  )
}

function ClearingCards({
  ctx,
  clearing,
  onPayout,
}: {
  ctx: MoneyCtx
  clearing: S['PaymentClearing']
  onPayout: () => void
}) {
  const centrally = clearing.merchant_of_record === 'organization'
  const owed = clearing.client_money_owed.amount
  const paidOut = clearing.client_money_paid_out?.amount
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Trans>Waiting at the providers</Trans>
            <Badge variant="outline">
              {centrally ? <Trans>Organization collects</Trans> : <Trans>Site collects</Trans>}
            </Badge>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {clearing.providers.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>No provider has handled money for this site yet.</Trans>
            </p>
          ) : (
            <dl className="grid gap-2 text-sm" aria-label={ctx.siteName}>
              {clearing.providers.map((p) => (
                <div key={p.provider} className="flex justify-between gap-3">
                  <dt>{p.provider}</dt>
                  <dd className="font-medium" data-testid={`clearing-${p.provider}`}>
                    <Money minor={p.balance.amount} currency={p.balance.currency} />
                  </dd>
                </div>
              ))}
            </dl>
          )}
          <p className="mt-3 text-xs text-muted-foreground">
            <Trans>
              Card payments received minus the payouts posted: money the provider still owes.
            </Trans>
          </p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Client money</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <dl className="grid gap-2 text-sm">
            <div className="flex justify-between gap-3">
              <dt>
                <Trans>Held for this site, not passed on</Trans>
              </dt>
              <dd className="text-lg font-semibold" data-testid="client-money-owed">
                <Money minor={owed} currency={clearing.client_money_owed.currency} />
              </dd>
            </div>
            {paidOut !== undefined ? (
              <div className="flex justify-between gap-3">
                <dt>
                  <Trans>Paid out to the site so far</Trans>
                </dt>
                <dd data-testid="client-money-paid-out">
                  <Money minor={paidOut} currency={clearing.client_money_paid_out!.currency} />
                </dd>
              </div>
            ) : null}
          </dl>
          {!centrally && owed === 0 ? (
            <p className="text-xs text-muted-foreground">
              <Trans>
                The site collects card payments into its own provider account, so the organization
                holds no client money for it.
              </Trans>
            </p>
          ) : null}
          <Gate permission="money.post" fallback={null}>
            {owed > 0 ? (
              <div>
                <Button onClick={onPayout}>
                  <Trans>Record payout to site</Trans>
                </Button>
              </div>
            ) : null}
          </Gate>
        </CardContent>
      </Card>
    </div>
  )
}

/** The organization handed client money over to the site's bank account. */
function PayoutDialog({
  ctx,
  owed,
  onClose,
}: {
  ctx: MoneyCtx
  owed: number
  onClose: () => void
}) {
  const { t } = useLingui()
  const msg = useZodMessages()
  const today = todayIn(ctx.timeZone)
  const schema = useMemo(
    () =>
      z.object({
        amount: positiveMinor(msg.amount).refine((v) => v <= BigInt(owed), {
          message: t`The amount cannot be more than the client money held for the site.`,
        }),
        reference: z.string().trim().min(1, msg.required).max(200),
        paid_on: isoDate(msg.date).refine((d) => d <= today, {
          message: t`The date cannot be in the future.`,
        }),
      }),
    [msg, owed, today, t],
  )
  type V = z.infer<typeof schema>
  const form = useForm<V>({
    resolver: zodResolver(schema),
    defaultValues: { amount: BigInt(owed), reference: '', paid_on: today },
  })
  const values = useWatch({ control: form.control })
  // the key follows the request: a retry replays it, an edit gets a fresh one
  const idem = useBodyIdempotencyKey({
    a: values.amount?.toString(),
    r: values.reference,
    d: values.paid_on,
  })
  const payout = useMoneyMutation(async (v: V) => {
    await unwrap(
      moneyApi.POST('/v1/orgs/{org_id}/payment-clearing:payout', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          amount: minorToMoney(v.amount, ctx.currency),
          reference: v.reference.trim(),
          paid_on: v.paid_on,
        },
      }),
    )
  })
  const err = form.formState.errors
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) =>
            void form.handleSubmit(async (v) => {
              try {
                await payout.mutateAsync(v)
                toast.info(t`The payout was recorded.`)
                onClose()
              } catch (er) {
                applyServerErrors(form, er, t`Could not record the payout.`, {
                  locked_period: t`This period is locked.`,
                })
              }
            })(e)
          }
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Record payout to site</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                You passed client money on to {ctx.siteName}&apos;s bank account. The balance the
                organization owes the site goes down by this amount (now{' '}
                <Money minor={owed} currency={ctx.currency} />
                ).
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <MoneyFormField
            id="po-amount"
            control={form.control}
            name="amount"
            label={t`Amount`}
            currency={ctx.currency}
            error={err.amount?.message}
          />
          <Field label={t`Bank transfer reference`} htmlFor="po-ref" error={err.reference?.message}>
            <Input id="po-ref" maxLength={200} {...form.register('reference')} />
          </Field>
          <Field label={t`Date the money arrived`} htmlFor="po-date" error={err.paid_on?.message}>
            <Input id="po-date" type="date" dir="ltr" max={today} {...form.register('paid_on')} />
          </Field>
          <FormRootError message={err.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={form.formState.isSubmitting}>
              <Trans>Record payout</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
