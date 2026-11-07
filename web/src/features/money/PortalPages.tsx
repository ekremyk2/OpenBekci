import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch } from 'react-hook-form'
import { Trans, useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { buttonVariants, Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useIdempotencyKey } from '@/lib/idempotency'
import {
  idemHeader,
  moneyApi,
  startOfYear,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { isoDate, MoneyFormField, positiveMinor, useZodMessages, minorToMoney } from './forms'
import { StatementView } from './StatementsPage'
import {
  applyServerErrors,
  Field,
  FormRootError,
  PageHeader,
  QueryBoundary,
  Select,
  SideBadge,
  Textarea,
} from './ui'

function useMyBalances(ctx: MoneyCtx) {
  return useMoneyQuery(['my-balances', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/me/balances', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ),
  )
}

/** Resident home: what I owe today per side, including the fee accrued so far. */
export function PortalHome() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const q = useMyBalances(ctx)
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`My balance`}
        description={t`What you owe today, including the late fee accrued so far.`}
      />
      <QueryBoundary
        query={q}
        skeleton="page"
        isEmpty={(d) => d.sides.length === 0}
        emptyTitle={t`Nothing to show`}
        emptyDescription={t`You are not linked to a unit with a balance.`}
      >
        {(d) => (
          <div className="grid gap-4 md:grid-cols-2">
            {d.sides.map((s) => (
              <Card key={`${s.unit_id}-${s.debtor_kind}`}>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    {s.unit_door_number} <SideBadge side={s.debtor_kind} />
                  </CardTitle>
                </CardHeader>
                <CardContent className="flex flex-col gap-2">
                  <div className="text-3xl font-semibold" data-testid="owe-today">
                    <Money minor={s.total.amount} currency={ctx.currency} />
                  </div>
                  <p className="text-sm text-muted-foreground">
                    <Trans>
                      Principal <Money minor={s.principal.amount} currency={ctx.currency} /> plus
                      late fee <Money minor={s.accrued_late_fee.amount} currency={ctx.currency} />
                    </Trans>
                  </p>
                  {s.credit.amount > 0 ? (
                    <p className="text-sm">
                      <Trans>
                        Prepayment credit: <Money minor={s.credit.amount} currency={ctx.currency} />
                      </Trans>
                    </p>
                  ) : null}
                  {s.next_due_date ? (
                    <p className="text-sm text-muted-foreground">
                      <Trans>
                        Next due: <SiteDate value={s.next_due_date} timeZone={ctx.timeZone} />
                      </Trans>
                    </p>
                  ) : null}
                  <div className="flex gap-2 pt-2">
                    <Link
                      to="/orgs/$orgId/money/pay"
                      params={{ orgId: ctx.orgId }}
                      className={buttonVariants()}
                    >
                      <Trans>Pay</Trans>
                    </Link>
                    <Link
                      to="/orgs/$orgId/money/statements"
                      params={{ orgId: ctx.orgId }}
                      search={{ unit: s.unit_id, side: s.debtor_kind }}
                      className={buttonVariants({ variant: 'outline' })}
                    >
                      <Trans>Statement</Trans>
                    </Link>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </QueryBoundary>
    </div>
  )
}

/** Resident statement: only the sides the occupancies allow. */
export function PortalStatement({ unit, side }: { unit?: string; side?: string }) {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const mine = useMyBalances(ctx)
  const [pick, setPick] = useState(unit && side ? `${unit}:${side}` : '')
  const first = mine.data?.sides[0]
  const key = pick || (first ? `${first.unit_id}:${first.debtor_kind}` : '')
  const [u, sd] = key.split(':') as [string, 'owner' | 'tenant']
  const today = todayIn(ctx.timeZone)
  const q = useMoneyQuery(
    ['portal-statement', key],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/units/{unit_id}/statements/{debtor_kind}', {
          params: {
            path: { org_id: ctx.orgId, unit_id: u, debtor_kind: sd },
            query: { from: startOfYear(today), to: today },
          },
        }),
      ),
    { enabled: !!u },
  )
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={t`My statement`} />
      <QueryBoundary
        query={mine}
        skeleton="page"
        isEmpty={(d) => d.sides.length === 0}
        emptyTitle={t`Nothing to show`}
      >
        {(d) => (
          <>
            {d.sides.length > 1 ? (
              <Field label={t`Side`} htmlFor="ps-side">
                <Select
                  id="ps-side"
                  className="w-auto"
                  value={key}
                  onChange={(e) => setPick(e.target.value)}
                >
                  {d.sides.map((s) => (
                    <option
                      key={`${s.unit_id}:${s.debtor_kind}`}
                      value={`${s.unit_id}:${s.debtor_kind}`}
                    >
                      {s.unit_door_number} – {s.debtor_kind === 'tenant' ? t`Tenant` : t`Owner`}
                    </option>
                  ))}
                </Select>
              </Field>
            ) : null}
            <QueryBoundary query={q} skeleton="table">
              {(s) => <StatementView statement={s} ctx={ctx} />}
            </QueryBoundary>
          </>
        )}
      </QueryBoundary>
    </div>
  )
}

/** Pay in 0.1: the site's bank details and an "I've paid by transfer" declaration. */
export function PortalPay() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const q = useMyBalances(ctx)
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Pay`}
        description={t`Transfer the amount to the account below, then tell us you paid. Your manager confirms it when the money arrives.`}
      />
      <QueryBoundary
        query={q}
        skeleton="page"
        isEmpty={(d) => d.sides.length === 0}
        emptyTitle={t`Nothing to pay`}
      >
        {(d) => <PayContent ctx={ctx} sides={d.sides} />}
      </QueryBoundary>
    </div>
  )
}

function PayContent({ ctx, sides }: { ctx: MoneyCtx; sides: S['MySide'][] }) {
  const { t } = useLingui()
  const msg = useZodMessages()
  const idem = useIdempotencyKey()
  const [done, setDone] = useState(false)
  const schema = useMemo(
    () =>
      z.object({
        side: z.string().min(1, msg.pick),
        amount: positiveMinor(msg.amount),
        paid_on: isoDate(msg.date),
        reference: z.string(),
        note: z.string(),
      }),
    [msg],
  )
  type V = z.infer<typeof schema>
  const first = sides[0]!
  const form = useForm<V>({
    resolver: zodResolver(schema),
    defaultValues: {
      side: `${first.unit_id}:${first.debtor_kind}`,
      amount:
        BigInt(first.total.amount) > 0n ? BigInt(first.total.amount) : (null as unknown as bigint),
      paid_on: todayIn(ctx.timeZone),
      reference: first.bank_details?.suggested_reference ?? '',
      note: '',
    },
  })
  const sideValue = useWatch({ control: form.control, name: 'side' })
  const selected = sides.find((s) => `${s.unit_id}:${s.debtor_kind}` === sideValue) ?? first
  const bank = selected.bank_details
  const declare = useMoneyMutation(async (v: V) => {
    const [unit_id, kind] = v.side.split(':') as [string, 'owner' | 'tenant']
    await moneyApi.POST('/v1/orgs/{org_id}/payments:declare', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        debtor: { unit_id, debtor_kind: kind },
        amount: minorToMoney(v.amount, ctx.currency),
        paid_on: v.paid_on,
        reference: v.reference || undefined,
        note: v.note || undefined,
      },
    })
  })
  const err = form.formState.errors
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card className="self-start">
        <CardHeader>
          <CardTitle>
            <Trans>Bank details</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {bank ? (
            <dl className="grid gap-3 text-sm">
              <div>
                <dt className="text-muted-foreground">
                  <Trans>IBAN</Trans>
                </dt>
                <dd className="font-mono text-base" dir="ltr">
                  {bank.iban}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">
                  <Trans>Account holder</Trans>
                </dt>
                <dd>{bank.holder}</dd>
              </div>
              {bank.bank_name ? (
                <div>
                  <dt className="text-muted-foreground">
                    <Trans>Bank</Trans>
                  </dt>
                  <dd>{bank.bank_name}</dd>
                </div>
              ) : null}
              <div>
                <dt className="text-muted-foreground">
                  <Trans>Reference to write</Trans>
                </dt>
                <dd>{bank.suggested_reference ?? bank.reference_hint}</dd>
              </div>
            </dl>
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>
                The site has not published bank details yet. Ask your manager how to pay.
              </Trans>
            </p>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>I've paid by transfer</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {done ? (
            <div role="status" className="flex flex-col gap-3">
              <p>
                <Trans>
                  Thank you. Your manager will confirm the payment when it shows up in the bank.
                </Trans>
              </p>
              <div>
                <Button
                  variant="outline"
                  onClick={() => {
                    setDone(false)
                    form.reset()
                  }}
                >
                  <Trans>Tell us about another payment</Trans>
                </Button>
              </div>
            </div>
          ) : (
            <form
              className="flex flex-col gap-4"
              noValidate
              onSubmit={(e) =>
                void form.handleSubmit(async (v) => {
                  try {
                    await declare.mutateAsync(v)
                    idem.renew()
                    setDone(true)
                  } catch (er) {
                    applyServerErrors(form, er, t`Could not send your declaration.`)
                  }
                })(e)
              }
            >
              {sides.length > 1 ? (
                <Field label={t`Paying for`} htmlFor="pd-side">
                  <Select id="pd-side" {...form.register('side')}>
                    {sides.map((s) => (
                      <option
                        key={`${s.unit_id}:${s.debtor_kind}`}
                        value={`${s.unit_id}:${s.debtor_kind}`}
                      >
                        {s.unit_door_number} – {s.debtor_kind === 'tenant' ? t`Tenant` : t`Owner`}
                      </option>
                    ))}
                  </Select>
                </Field>
              ) : null}
              <MoneyFormField
                id="pd-amount"
                control={form.control}
                name="amount"
                label={t`Amount you paid`}
                currency={ctx.currency}
                error={err.amount?.message}
              />
              <Field label={t`Date you paid`} htmlFor="pd-date" error={err.paid_on?.message}>
                <Input id="pd-date" type="date" dir="ltr" {...form.register('paid_on')} />
              </Field>
              <Field label={t`Reference you wrote (optional)`} htmlFor="pd-ref">
                <Input id="pd-ref" {...form.register('reference')} />
              </Field>
              <Field label={t`Note (optional)`} htmlFor="pd-note">
                <Textarea id="pd-note" {...form.register('note')} />
              </Field>
              <FormRootError message={err.root?.message} />
              <div>
                <Button type="submit" disabled={form.formState.isSubmitting}>
                  <Trans>I've paid</Trans>
                </Button>
              </div>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
