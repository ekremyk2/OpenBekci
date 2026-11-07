import { useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { Trans, useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { AppError } from '@/api/errors'
import { ReauthDialog, useReauthGuard } from '@/components/ReauthDialog'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import {
  idemHeader,
  ifMatch,
  minorMoney,
  moneyApi,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import {
  Field,
  Gate,
  FormRootError,
  PageHeader,
  QueryBoundary,
  ReasonDialog,
  SideBadge,
  Textarea,
  applyServerErrors,
} from './ui'
import { useUnits } from './pickers'

type Accrual = S['LateFeeAccrual']

export function LateFeesPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const units = useUnits(ctx)
  const door = (id: string) => units.data?.find((u) => u.id === id)?.door_number ?? id
  const accruals = useMoneyQuery(['late-fee-accruals', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/late-fee-accruals', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, nonzero: true, limit: 200 },
        },
      }),
    ).then((r) => r.items),
  )
  const bookings = useMoneyQuery(['late-fee-bookings', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/late-fee-bookings', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 200 } },
      }),
    ).then((r) => r.items),
  )
  const waivers = useMoneyQuery(['late-fee-waivers', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/late-fee-waivers', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const freezes = useMoneyQuery(['late-fee-freezes', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/late-fee-freezes', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, active: true } },
      }),
    ).then((r) => r.items),
  )
  const [waiveFor, setWaiveFor] = useState<Accrual | null>(null)
  const [freezeFor, setFreezeFor] = useState<Accrual | null>(null)
  const unfreeze = useMoneyMutation(async (f: S['LateFeeFreeze']) => {
    await moneyApi.PATCH('/v1/orgs/{org_id}/late-fee-freezes/{freeze_id}', {
      params: { path: { org_id: ctx.orgId, freeze_id: f.id }, header: ifMatch(f.version) },
      body: { to: todayIn(ctx.timeZone) },
    })
  })
  const freezeOf = (a: Accrual) =>
    freezes.data?.find((f) => f.unit_id === a.unit_id && f.debtor_kind === a.debtor_kind)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Late fees`}
        description={t`Fees are computed continuously on overdue principal, booked once a month, and can be waived or frozen with a reason.`}
      />
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Accrued today</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <QueryBoundary
            query={accruals}
            isEmpty={(d) => d.length === 0}
            emptyTitle={t`No late fees are accruing`}
            emptyDescription={t`Nobody has overdue principal beyond the grace period.`}
          >
            {(items) => (
              <Table aria-label={t`Accrued late fees`}>
                <TableHeader>
                  <TableRow>
                    <TableHead>
                      <Trans>Unit</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Side</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Overdue principal</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Accrued fee</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Booked</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Waived</Trans>
                    </TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((a) => {
                    const fz = freezeOf(a)
                    return (
                      <TableRow key={`${a.unit_id}-${a.debtor_kind}`}>
                        <TableCell>{a.unit_door_number}</TableCell>
                        <TableCell>
                          <SideBadge side={a.debtor_kind} />
                        </TableCell>
                        <TableCell className="text-end">
                          <Money minor={a.open_principal.amount} currency={ctx.currency} />
                        </TableCell>
                        <TableCell className="text-end">
                          <Money minor={a.accrued.amount} currency={ctx.currency} />
                          {a.frozen ? (
                            <Badge variant="info" className="ms-2">
                              <Trans>Frozen</Trans>
                            </Badge>
                          ) : null}
                        </TableCell>
                        <TableCell className="text-end">
                          <Money minor={a.booked.amount} currency={ctx.currency} />
                        </TableCell>
                        <TableCell className="text-end">
                          <Money minor={a.waived.amount} currency={ctx.currency} />
                        </TableCell>
                        <TableCell className="text-end whitespace-nowrap">
                          <Gate permission="money.post" fallback={null}>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={a.accrued.amount <= 0}
                              onClick={() => setWaiveFor(a)}
                            >
                              <Trans>Waive</Trans>
                            </Button>
                          </Gate>
                          <Gate permission="money.manage" fallback={null}>
                            {fz ? (
                              <Button variant="ghost" size="sm" onClick={() => unfreeze.mutate(fz)}>
                                <Trans>Unfreeze</Trans>
                              </Button>
                            ) : (
                              <Button variant="ghost" size="sm" onClick={() => setFreezeFor(a)}>
                                <Trans>Freeze</Trans>
                              </Button>
                            )}
                          </Gate>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            )}
          </QueryBoundary>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Monthly bookings</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <QueryBoundary
            query={bookings}
            isEmpty={(d) => d.length === 0}
            emptyTitle={t`Nothing booked yet`}
          >
            {(items) => (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>
                      <Trans>Period</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Unit</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Side</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Booked</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Expected to date</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Booked to date</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Delta</Trans>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((b) => (
                    <TableRow key={b.id}>
                      <TableCell>{b.period}</TableCell>
                      <TableCell>{door(b.unit_id)}</TableCell>
                      <TableCell>
                        <SideBadge side={b.debtor_kind} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={b.amount.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={b.expected_to_date.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={b.booked_to_date.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money
                          minor={b.delta.amount}
                          currency={ctx.currency}
                          signDisplay="exceptZero"
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </QueryBoundary>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Waivers</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <QueryBoundary query={waivers} isEmpty={(d) => d.length === 0} emptyTitle={t`No waivers`}>
            {(items) => (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>
                      <Trans>Date</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Unit</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Side</Trans>
                    </TableHead>
                    <TableHead className="text-end">
                      <Trans>Amount</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Reason</Trans>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((w) => (
                    <TableRow key={w.id}>
                      <TableCell>
                        <SiteDate value={w.created_at} timeZone={ctx.timeZone} />
                      </TableCell>
                      <TableCell>{door(w.unit_id)}</TableCell>
                      <TableCell>
                        <SideBadge side={w.debtor_kind} />
                      </TableCell>
                      <TableCell className="text-end">
                        <Money minor={w.amount.amount} currency={ctx.currency} />
                      </TableCell>
                      <TableCell>{w.reason}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </QueryBoundary>
        </CardContent>
      </Card>

      <PolicyCard ctx={ctx} />
      {waiveFor ? (
        <WaiveDialog ctx={ctx} accrual={waiveFor} onClose={() => setWaiveFor(null)} />
      ) : null}
      {freezeFor ? (
        <FreezeDialog ctx={ctx} accrual={freezeFor} onClose={() => setFreezeFor(null)} />
      ) : null}
    </div>
  )
}

export function WaiveDialog({
  ctx,
  accrual,
  onClose,
}: {
  ctx: MoneyCtx
  accrual: Accrual
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const guard = useReauthGuard()
  const [amount, setAmount] = useState<bigint | null>(BigInt(accrual.accrued.amount))
  const [error, setError] = useState<string>()
  const m = useMoneyMutation(async (reason: string) => {
    // The same key goes out again after a re-authentication challenge.
    const done = await guard.run(() =>
      moneyApi.POST('/v1/orgs/{org_id}/late-fee-waivers', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          debtor: { unit_id: accrual.unit_id, debtor_kind: accrual.debtor_kind },
          amount: minorMoney(amount!, ctx.currency),
          reason,
        },
      }),
    )
    if (!done) throw new Error('reauth_cancelled')
  })
  const invalid = amount === null || amount <= 0n || amount > BigInt(accrual.accrued.amount)
  return (
    <>
      <ReasonDialog
        open
        onOpenChange={(o) => !o && onClose()}
        title={t`Waive late fee`}
        description={`${accrual.unit_door_number} · ${accrual.debtor_kind === 'tenant' ? t`Tenant` : t`Owner`}`}
        confirmLabel={t`Waive fee`}
        pending={m.isPending || invalid}
        error={error}
        onConfirm={(reason) =>
          m.mutateAsync(reason).then(
            () => {
              idem.renew()
              toast.info(t`Late fee waived.`)
              onClose()
            },
            (e) =>
              setError(
                e instanceof AppError && e.isValidation
                  ? t`The amount is more than the accrued fee.`
                  : t`Could not waive the fee.`,
              ),
          )
        }
      >
        <p className="text-sm text-muted-foreground">
          <Trans>
            Accrued so far: <Money minor={accrual.accrued.amount} currency={ctx.currency} />.
            Waivers above the site threshold ask you to confirm your password.
          </Trans>
        </p>
        <Field
          label={t`Amount to waive`}
          htmlFor="waive-amount"
          error={invalid ? t`Enter an amount up to the accrued fee.` : undefined}
        >
          <MoneyInput
            id="waive-amount"
            currency={ctx.currency}
            value={amount}
            onChange={setAmount}
          />
        </Field>
      </ReasonDialog>
      {guard.dialog}
    </>
  )
}

function FreezeDialog({
  ctx,
  accrual,
  onClose,
}: {
  ctx: MoneyCtx
  accrual: Accrual
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const schema = useMemo(
    () =>
      z.object({
        from: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, t`Enter a date.`),
        to: z.string(),
        reason: z
          .string()
          .trim()
          .min(1, t`A reason is required.`),
      }),
    [t],
  )
  type V = z.infer<typeof schema>
  const form = useForm<V>({
    resolver: zodResolver(schema),
    defaultValues: { from: todayIn(ctx.timeZone), to: '', reason: '' },
  })
  const m = useMoneyMutation(async (v: V) => {
    await moneyApi.POST('/v1/orgs/{org_id}/late-fee-freezes', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        debtor: { unit_id: accrual.unit_id, debtor_kind: accrual.debtor_kind },
        from: v.from,
        to: v.to || null,
        reason: v.reason,
      },
    })
  })
  const err = form.formState.errors
  return (
    <Dialogish
      onClose={onClose}
      title={t`Freeze late fees`}
      description={t`No fee accrues for ${accrual.unit_door_number} while it is frozen, for example during legal collection.`}
    >
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) =>
          void form.handleSubmit(async (v) => {
            try {
              await m.mutateAsync(v)
              idem.renew()
              toast.info(t`Late fees frozen.`)
              onClose()
            } catch (er) {
              applyServerErrors(form, er, t`Could not freeze the fees.`)
            }
          })(e)
        }
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t`From`} htmlFor="fz-from" error={err.from?.message}>
            <Input id="fz-from" type="date" dir="ltr" {...form.register('from')} />
          </Field>
          <Field label={t`Until (optional)`} htmlFor="fz-to">
            <Input id="fz-to" type="date" dir="ltr" {...form.register('to')} />
          </Field>
        </div>
        <Field label={t`Reason`} htmlFor="fz-reason" error={err.reason?.message}>
          <Textarea id="fz-reason" {...form.register('reason')} />
        </Field>
        <FormRootError message={err.root?.message} />
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>
            <Trans>Cancel</Trans>
          </Button>
          <Button type="submit" disabled={form.formState.isSubmitting}>
            <Trans>Freeze</Trans>
          </Button>
        </div>
      </form>
    </Dialogish>
  )
}

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
function Dialogish({
  onClose,
  title,
  description,
  children,
}: {
  onClose: () => void
  title: React.ReactNode
  description?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  )
}

function PolicyCard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const q = useMoneyQuery(['late-fee-policy', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/late-fee-policy', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ),
  )
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Late-fee policy</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <QueryBoundary query={q} skeleton="page">
          {(p) => (
            <PolicyForm ctx={ctx} policy={p} key={`${p.rate_pct_per_month}-${p.grace_days}`} />
          )}
        </QueryBoundary>
        <span className="sr-only">{t`Policy`}</span>
      </CardContent>
    </Card>
  )
}

function PolicyForm({ ctx, policy }: { ctx: MoneyCtx; policy: S['LateFeePolicy'] }) {
  const { t } = useLingui()
  const schema = useMemo(
    () =>
      z.object({
        rate: z
          .number({ error: t`Enter a rate.` })
          .min(0, t`Enter a rate.`)
          .max(
            policy.max_rate_pct_per_month,
            t`The rate cannot be above the statutory cap of ${policy.max_rate_pct_per_month}% per month.`,
          ),
        grace: z
          .number({ error: t`Enter the grace days.` })
          .int()
          .min(0, t`Enter the grace days.`)
          .max(60),
        threshold: z.custom<bigint | null>((v) => v === null || typeof v === 'bigint', {
          message: t`Enter an amount.`,
        }),
      }),
    [t, policy.max_rate_pct_per_month],
  )
  type V = z.infer<typeof schema>
  const form = useForm<V>({
    resolver: zodResolver(schema),
    defaultValues: {
      rate: policy.rate_pct_per_month,
      grace: policy.grace_days,
      threshold: policy.waiver_reauth_threshold
        ? BigInt(policy.waiver_reauth_threshold.amount)
        : null,
    },
  })
  const m = useMoneyMutation(async (v: V) => {
    await moneyApi.PUT('/v1/orgs/{org_id}/late-fee-policy', {
      params: {
        path: { org_id: ctx.orgId },
        query: { site_id: ctx.siteId },
        header: ifMatch(policy.version),
      },
      body: {
        rate_pct_per_month: v.rate,
        grace_days: v.grace,
        waiver_reauth_threshold:
          v.threshold === null ? null : minorMoney(v.threshold, ctx.currency),
      },
    })
  })
  const err = form.formState.errors
  const [threshold, setThreshold] = useState<bigint | null>(form.getValues('threshold'))
  return (
    <form
      className="grid gap-4 sm:grid-cols-3"
      noValidate
      onSubmit={(e) =>
        void form.handleSubmit(async (v) => {
          try {
            await m.mutateAsync(v)
            toast.info(t`Policy saved.`)
          } catch (er) {
            applyServerErrors(form, er, t`Could not save the policy.`)
          }
        })(e)
      }
    >
      <Field
        label={t`Rate per month (%)`}
        htmlFor="lf-rate"
        error={err.rate?.message}
        hint={t`Statutory cap: ${policy.max_rate_pct_per_month}% per month.`}
      >
        <Input
          id="lf-rate"
          type="number"
          step="0.1"
          {...form.register('rate', { valueAsNumber: true })}
        />
      </Field>
      <Field label={t`Grace days`} htmlFor="lf-grace" error={err.grace?.message}>
        <Input id="lf-grace" type="number" {...form.register('grace', { valueAsNumber: true })} />
      </Field>
      <Field label={t`Waivers above this ask for re-authentication`} htmlFor="lf-threshold">
        <MoneyInput
          id="lf-threshold"
          currency={ctx.currency}
          value={threshold}
          onChange={(v) => {
            setThreshold(v)
            form.setValue('threshold', v)
          }}
        />
      </Field>
      <div className="sm:col-span-3">
        <FormRootError message={err.root?.message} />
      </div>
      <Gate permission="money.manage" fallback={null}>
        <div>
          <Button type="submit" disabled={form.formState.isSubmitting}>
            <Trans>Save policy</Trans>
          </Button>
        </div>
      </Gate>
    </form>
  )
}

export { ReauthDialog }
