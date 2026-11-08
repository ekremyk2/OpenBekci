import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch } from 'react-hook-form'
import { Trans, useLingui } from '@lingui/react/macro'
import { z } from 'zod'
import { useIdempotencyKey } from '@/lib/idempotency'
import { Money } from '@/components/data/Money'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { SiteDate } from '@/components/data/SiteDate'
import { SidePicker } from '@/components/forms/SidePicker'
import { Button, buttonVariants } from '@/components/ui/button'
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
import { toast } from '@/lib/toast'
import { formatMoney } from '@/lib/money'
import { useLocale } from '@/app/locale'
import {
  idemHeader,
  ifMatch,
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
  isoDate,
  minorToMoney,
  MoneyFormField,
  positiveMinor,
  useDebounced,
  useIdempotencyKeys,
  useZodMessages,
} from './forms'
import { CHARGE_TYPES, defaultDebtorFor, useLabels } from './labels'
import { AllocationKeyPicker, FundPicker, ScopePicker, useAllocationKeys } from './pickers'
import {
  applyServerErrors,
  Field,
  FormRootError,
  Gate,
  PageHeader,
  QueryBoundary,
  Select,
  SideBadge,
  StatusBadge,
} from './ui'

/* ───────────────────────────── list ───────────────────────────── */

export function PlansListPage() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const labels = useLabels()
  const keys = useAllocationKeys(ctx)
  const q = useMoneyQuery(['plans', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/charge-plans', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const gen = useMoneyQuery(['charge-generation', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/charge-generation', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ),
  )
  const keyName = (id: string) => keys.data?.find((k) => k.id === id)?.name ?? id
  const actions = (
    <Gate permission="money.manage" fallback={null}>
      <Link
        to="/orgs/$orgId/money/expense-sharing"
        params={{ orgId: ctx.orgId }}
        className={buttonVariants({ variant: 'outline' })}
      >
        <Trans>Share an expense</Trans>
      </Link>
      <Link
        to="/orgs/$orgId/money/plans/new"
        params={{ orgId: ctx.orgId }}
        className={buttonVariants()}
      >
        <Trans>New plan</Trans>
      </Link>
    </Gate>
  )
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Dues plans`}
        description={t`A plan creates the charges of every period. Amounts change through effective-dated rates, never by editing history.`}
        actions={actions}
      />
      {gen.data && !gen.data.up_to_date ? (
        <p role="status" className="rounded-md bg-warning/15 p-3 text-sm">
          <Trans>Some periods are still being generated.</Trans>
        </p>
      ) : null}
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No dues plans yet`}
        emptyDescription={t`Create the monthly dues plan; you will see what every unit pays before saving.`}
      >
        {(plans) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Name</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Type</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Paid by</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Split by</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Current rate</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Schedule</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {plans.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <Link
                      to="/orgs/$orgId/money/plans/$planId"
                      params={{ orgId: ctx.orgId, planId: p.id }}
                      className="font-medium underline-offset-4 hover:underline"
                    >
                      {p.name}
                    </Link>
                  </TableCell>
                  <TableCell>{labels.chargeType[p.charge_type] ?? p.charge_type}</TableCell>
                  <TableCell>
                    <SideBadge side={p.default_debtor} />
                  </TableCell>
                  <TableCell>
                    {keyName(p.allocation_key_id)}
                    <span className="text-xs text-muted-foreground">
                      {' · '}
                      {p.scope.type === 'site'
                        ? t`whole site`
                        : p.scope.type === 'block'
                          ? t`one block`
                          : t`tagged units`}
                    </span>
                  </TableCell>
                  <TableCell className="text-end">
                    {p.current_rate ? (
                      <Money minor={p.current_rate.amount.amount} currency={ctx.currency} />
                    ) : (
                      '–'
                    )}
                    <div className="text-xs text-muted-foreground">
                      {p.amount_mode === 'per_unit' ? t`per unit` : t`split in total`}
                    </div>
                  </TableCell>
                  <TableCell>
                    {labels.frequency[p.frequency] ?? p.frequency}
                    {p.due_day ? ` · ${t`day`} ${p.due_day}` : ''}
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={p.status} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}

/* ───────────────────────────── preview ───────────────────────────── */

export function PreviewTable({
  preview,
  ctx,
  loading,
  error,
}: {
  preview?: S['ChargePlanPreview']
  ctx: MoneyCtx
  loading?: boolean
  error?: boolean
}) {
  const { t } = useLingui()
  if (error) {
    return (
      <p className="text-sm text-muted-foreground">{t`The preview is not available for these settings yet.`}</p>
    )
  }
  if (!preview) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        {loading
          ? t`Calculating…`
          : t`Fill in the amount and split rule to see what each unit pays.`}
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-2" aria-busy={loading || undefined}>
      <Table aria-label={t`What each unit side would pay`}>
        <TableHeader>
          <TableRow>
            <TableHead>
              <Trans>Unit</Trans>
            </TableHead>
            <TableHead>
              <Trans>Side</Trans>
            </TableHead>
            <TableHead>
              <Trans>Person</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Weight</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Amount</Trans>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {preview.lines.map((l) => (
            <TableRow key={`${l.unit_id}-${l.debtor_kind}`}>
              <TableCell>{l.door_number}</TableCell>
              <TableCell>
                <SideBadge side={l.debtor_kind} />
              </TableCell>
              <TableCell>
                {l.effective_debtor.display_name ?? l.debtor_display_name ?? '–'}
              </TableCell>
              <TableCell className="text-end tabular-nums">{l.weight}</TableCell>
              <TableCell className="text-end">
                <Money minor={l.amount.amount} currency={ctx.currency} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="text-sm">
        <Trans>
          Total <Money minor={preview.total.amount} currency={ctx.currency} /> across{' '}
          {preview.unit_count} units
        </Trans>
        {preview.remainder_distributed ? (
          <span className="block text-xs text-muted-foreground">
            <Trans>Rounding remainder was distributed to the largest shares.</Trans>
          </span>
        ) : null}
      </p>
      {preview.lines.reduce((acc, l) => acc + l.amount.amount, 0) !== preview.total.amount ? (
        <p role="alert" className="text-sm text-destructive">
          <Trans>The lines do not add up to the total. Do not save this plan.</Trans>
        </p>
      ) : null}
    </div>
  )
}

/* ───────────────────────────── create / edit ───────────────────────────── */

function usePlanSchema() {
  const msg = useZodMessages()
  return useMemo(
    () =>
      z.object({
        name: z.string().trim().min(1, msg.required),
        kind: z.enum(['recurring', 'one_off']),
        charge_type: z.string().min(1),
        default_debtor: z.enum(['owner', 'tenant']),
        amount_mode: z.enum(['per_unit', 'total_allocated']),
        amount: positiveMinor(msg.amount),
        allocation_key_id: z.string().min(1, msg.pick),
        scope: z
          .object({ type: z.enum(['site', 'block', 'unit_tag']), id: z.string().nullable() })
          .refine((s) => s.type === 'site' || !!s.id, { message: msg.pick }),
        fund_id: z.string().min(1, msg.pick),
        frequency: z.enum(['monthly', 'quarterly', 'yearly', 'once']),
        due_day: z.number().int().min(1).max(28),
        start: isoDate(msg.date),
        end: z.string(),
        decision_id: z.string(),
      }),
    [msg],
  )
}

type PlanFormValues = z.infer<ReturnType<typeof usePlanSchema>>

export function NewPlanPage() {
  const ctx = useMoneyCtx()!
  return (
    <Gate permission="money.manage">
      <PlanForm ctx={ctx} />
    </Gate>
  )
}

function PlanForm({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const labels = useLabels()
  const navigate = useNavigate()
  const idem = useIdempotencyKey()
  const schema = usePlanSchema()
  const funds = useMoneyQuery(['funds', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/funds', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ).then((r) => r.items),
  )
  const keys = useAllocationKeys(ctx)
  const form = useForm<PlanFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: '',
      kind: 'recurring',
      charge_type: 'dues',
      default_debtor: 'tenant',
      amount_mode: 'total_allocated',
      amount: null as unknown as bigint,
      allocation_key_id: '',
      scope: { type: 'site', id: null },
      fund_id: '',
      frequency: 'monthly',
      due_day: 5,
      start: todayIn(ctx.timeZone).slice(0, 8) + '01',
      end: '',
      decision_id: '',
    },
  })
  // Law 634 defaults: arsa payı for everything, the operating fund; picked as soon as the lists arrive.
  const share = keys.data?.find((k) => k.kind === 'share')
  const opFund = funds.data?.find((f) => f.is_default)
  const v = useWatch({ control: form.control })
  useEffect(() => {
    if (share && !form.getValues('allocation_key_id')) form.setValue('allocation_key_id', share.id)
    if (opFund && !form.getValues('fund_id')) form.setValue('fund_id', opFund.id)
  }, [share, opFund, form])

  const body = useMemo<S['ChargePlanCreate'] | null>(() => {
    if (!v.amount || typeof v.amount !== 'bigint' || v.amount <= 0n) return null
    if (!v.allocation_key_id || (v.scope?.type !== 'site' && !v.scope?.id)) return null
    return {
      site_id: ctx.siteId,
      name: v.name || t`Preview`,
      kind: v.kind ?? 'recurring',
      charge_type: (v.charge_type ?? 'dues') as S['ChargePlanCreate']['charge_type'],
      default_debtor: v.default_debtor,
      frequency: v.frequency,
      due_day: v.due_day ?? null,
      start: v.start ?? todayIn(ctx.timeZone),
      amount_mode: v.amount_mode ?? 'total_allocated',
      allocation_key_id: v.allocation_key_id,
      scope: { type: v.scope.type ?? 'site', id: v.scope.id ?? null },
      amount: minorToMoney(v.amount, ctx.currency),
    }
  }, [v, ctx, t])
  const debounced = useDebounced(body)
  const preview = useMoneyQuery(
    ['plan-preview', debounced],
    () =>
      unwrap(
        moneyApi.POST('/v1/orgs/{org_id}/charge-plans:preview', {
          params: { path: { org_id: ctx.orgId } },
          body: { plan: debounced!, amount: debounced!.amount },
        }),
      ),
    { enabled: debounced !== null, retry: false },
  )

  const create = useMoneyMutation(async (values: PlanFormValues) => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/charge-plans', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: {
        site_id: ctx.siteId,
        name: values.name,
        kind: values.kind,
        charge_type: values.charge_type as S['ChargePlanCreate']['charge_type'],
        default_debtor: values.default_debtor,
        frequency: values.frequency,
        due_day: values.due_day,
        start: values.start,
        end: values.end || null,
        amount_mode: values.amount_mode,
        allocation_key_id: values.allocation_key_id,
        scope: values.scope,
        fund_id: values.fund_id,
        decision_id: values.decision_id || null,
        amount: minorToMoney(values.amount, ctx.currency),
      },
    })
    return res.data!
  })

  async function onSubmit(values: PlanFormValues) {
    try {
      const plan = await create.mutateAsync(values)
      idem.renew()
      toast.info(t`Plan saved.`)
      await navigate({
        to: '/orgs/$orgId/money/plans/$planId',
        params: { orgId: ctx.orgId, planId: plan.id },
      })
    } catch (e) {
      applyServerErrors(form, e, t`Could not save the plan.`)
    }
  }

  const err = form.formState.errors
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`New dues plan`}
        description={t`The table on the right updates as you change the amount or split rule.`}
      />
      <div className="grid gap-6 lg:grid-cols-2">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
          noValidate
        >
          <Field label={t`Name`} htmlFor="plan-name" error={err.name?.message}>
            <Input
              id="plan-name"
              {...form.register('name')}
              aria-invalid={!!err.name || undefined}
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t`Kind`} htmlFor="plan-kind">
              <Select id="plan-kind" {...form.register('kind')}>
                <option value="recurring">{t`Recurring`}</option>
                <option value="one_off">{t`One-off`}</option>
              </Select>
            </Field>
            <Field label={t`Charge type`} htmlFor="plan-type">
              <Select
                id="plan-type"
                {...form.register('charge_type', {
                  onChange: (e: React.ChangeEvent<HTMLSelectElement>) =>
                    form.setValue('default_debtor', defaultDebtorFor(e.target.value)),
                })}
              >
                {CHARGE_TYPES.map((c) => (
                  <option key={c} value={c}>
                    {labels.chargeType[c]}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Field
            label={t`Paid by`}
            hint={t`Dues are the tenant's when a unit has one; special assessments are the owner's. Override per plan here.`}
          >
            <SidePicker
              value={v.default_debtor ?? 'owner'}
              onChange={(s) => form.setValue('default_debtor', s)}
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t`Amount is`} htmlFor="plan-mode">
              <Select id="plan-mode" {...form.register('amount_mode')}>
                <option value="total_allocated">{t`A total split between units`}</option>
                <option value="per_unit">{t`A fixed amount per unit`}</option>
              </Select>
            </Field>
            <MoneyFormField
              id="plan-amount"
              control={form.control}
              name="amount"
              label={t`Amount per period`}
              currency={ctx.currency}
              error={err.amount?.message}
            />
          </div>
          <Field label={t`Split rule`} htmlFor="plan-key" error={err.allocation_key_id?.message}>
            <AllocationKeyPicker
              ctx={ctx}
              id="plan-key"
              value={v.allocation_key_id ?? ''}
              onChange={(id) =>
                form.setValue('allocation_key_id', id, { shouldDirty: true, shouldValidate: true })
              }
            />
          </Field>
          <Field label={t`Applies to`} error={err.scope?.message ?? err.scope?.root?.message}>
            <ScopePicker
              ctx={ctx}
              value={v.scope as S['ChargeScope']}
              onChange={(s) => form.setValue('scope', s, { shouldValidate: true })}
            />
          </Field>
          <Field label={t`Fund`} htmlFor="plan-fund" error={err.fund_id?.message}>
            <FundPicker
              ctx={ctx}
              id="plan-fund"
              value={v.fund_id ?? ''}
              onChange={(id) => form.setValue('fund_id', id, { shouldValidate: true })}
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label={t`Frequency`} htmlFor="plan-freq">
              <Select id="plan-freq" {...form.register('frequency')}>
                {Object.entries(labels.frequency).map(([k, l]) => (
                  <option key={k} value={k}>
                    {l}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label={t`Due day`}
              htmlFor="plan-due"
              error={err.due_day?.message && t`Use a day from 1 to 28.`}
            >
              <Input
                id="plan-due"
                type="number"
                min={1}
                max={28}
                {...form.register('due_day', { valueAsNumber: true })}
              />
            </Field>
            <Field label={t`Starts`} htmlFor="plan-start" error={err.start?.message}>
              <Input id="plan-start" type="date" dir="ltr" {...form.register('start')} />
            </Field>
          </div>
          <Field
            label={t`Decision reference (optional)`}
            htmlFor="plan-decision"
            hint={t`The assembly or board decision this amount comes from.`}
          >
            <Input id="plan-decision" {...form.register('decision_id')} />
          </Field>
          <FormRootError message={err.root?.message} />
          <div className="flex gap-2">
            <Button type="submit" disabled={form.formState.isSubmitting}>
              <Trans>Save plan</Trans>
            </Button>
            <Link
              to="/orgs/$orgId/money/plans"
              params={{ orgId: ctx.orgId }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <Trans>Cancel</Trans>
            </Link>
          </div>
        </form>
        <Card className="self-start">
          <CardHeader>
            <CardTitle>
              <Trans>Preview</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <PreviewTable
              preview={preview.data}
              ctx={ctx}
              loading={preview.isFetching}
              error={preview.isError}
            />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

export function PlanDetailPage({ planId }: { planId: string }) {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const q = useMoneyQuery(['plan', planId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/charge-plans/{plan_id}', {
        params: { path: { org_id: ctx.orgId, plan_id: planId } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-4">
      <QueryBoundary query={q} skeleton="page">
        {(plan) => (
          <>
            <PageHeader
              title={plan.name}
              description={t`Edit the plan, or change the amount by adding a rate that starts on a date.`}
              actions={<StatusBadge status={plan.status} />}
            />
            <PlanSettings plan={plan} ctx={ctx} />
            <RatesCard plan={plan} ctx={ctx} />
          </>
        )}
      </QueryBoundary>
    </div>
  )
}

function PlanSettings({ plan, ctx }: { plan: S['ChargePlan']; ctx: MoneyCtx }) {
  const { t } = useLingui()
  const [name, setName] = useState(plan.name)
  const [dueDay, setDueDay] = useState(String(plan.due_day ?? 5))
  const [status, setStatus] = useState(plan.status)
  const [key, setKey] = useState(plan.allocation_key_id)
  const [scope, setScope] = useState(plan.scope)
  const [error, setError] = useState<string | null>(null)
  const save = useMoneyMutation(async () => {
    const res = await moneyApi.PATCH('/v1/orgs/{org_id}/charge-plans/{plan_id}', {
      params: { path: { org_id: ctx.orgId, plan_id: plan.id }, header: ifMatch(plan.version) },
      body: { name, due_day: Number(dueDay), status, allocation_key_id: key, scope },
    })
    return res.data!
  })
  return (
    <Gate permission="money.manage" fallback={null}>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Plan settings</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <form
            className="grid gap-4 sm:grid-cols-2"
            onSubmit={(e) => {
              e.preventDefault()
              setError(null)
              save.mutateAsync().then(
                () => toast.info(t`Plan saved.`),
                () => setError(t`Could not save the plan.`),
              )
            }}
          >
            <Field label={t`Name`} htmlFor="ps-name">
              <Input id="ps-name" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label={t`Status`} htmlFor="ps-status">
              <Select
                id="ps-status"
                value={status}
                onChange={(e) => setStatus(e.target.value as S['ChargePlan']['status'])}
              >
                <option value="active">{t`Active`}</option>
                <option value="paused">{t`Paused`}</option>
                <option value="archived">{t`Archived`}</option>
              </Select>
            </Field>
            <Field label={t`Due day`} htmlFor="ps-due">
              <Input
                id="ps-due"
                type="number"
                min={1}
                max={28}
                value={dueDay}
                onChange={(e) => setDueDay(e.target.value)}
              />
            </Field>
            <Field label={t`Split rule`} htmlFor="ps-key">
              <AllocationKeyPicker ctx={ctx} id="ps-key" value={key} onChange={setKey} />
            </Field>
            <Field label={t`Applies to`} className="sm:col-span-2">
              <ScopePicker ctx={ctx} value={scope} onChange={setScope} idPrefix="ps-scope" />
            </Field>
            <div className="sm:col-span-2">
              <FormRootError message={error ?? undefined} />
            </div>
            <div>
              <Button type="submit" disabled={save.isPending}>
                <Trans>Save changes</Trans>
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </Gate>
  )
}

function RatesCard({ plan, ctx }: { plan: S['ChargePlan']; ctx: MoneyCtx }) {
  const { t } = useLingui()
  const { intl } = useLocale()
  const idem = useIdempotencyKey()
  const msg = useZodMessages()
  const rates = useMoneyQuery(['plan-rates', plan.id], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/charge-plans/{plan_id}/rates', {
        params: { path: { org_id: ctx.orgId, plan_id: plan.id }, query: { limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const schema = useMemo(
    () =>
      z.object({
        from: isoDate(msg.date),
        amount: positiveMinor(msg.amount),
        decision_id: z.string(),
      }),
    [msg],
  )
  type RateValues = z.infer<typeof schema>
  const form = useForm<RateValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      from: todayIn(ctx.timeZone),
      amount: null as unknown as bigint,
      decision_id: '',
    },
  })
  const v = useWatch({ control: form.control })
  const debounced = useDebounced(typeof v.amount === 'bigint' && v.amount > 0n ? v.amount : null)
  const preview = useMoneyQuery(
    ['rate-preview', plan.id, String(debounced)],
    () =>
      unwrap(
        moneyApi.POST('/v1/orgs/{org_id}/charge-plans:preview', {
          params: { path: { org_id: ctx.orgId } },
          body: { plan_id: plan.id, amount: minorToMoney(debounced!, ctx.currency) },
        }),
      ),
    { enabled: debounced !== null, retry: false },
  )
  const add = useMoneyMutation(async (values: RateValues) => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/charge-plans/{plan_id}/rates', {
      params: { path: { org_id: ctx.orgId, plan_id: plan.id }, header: idemHeader(idem.key) },
      body: {
        from: values.from,
        amount: minorToMoney(values.amount, ctx.currency),
        decision_id: values.decision_id || null,
      },
    })
    return res.data!
  })
  const rateKeys = useIdempotencyKeys()
  const remove = useMoneyMutation(async (rateId: string) => {
    await moneyApi.DELETE('/v1/orgs/{org_id}/charge-plans/{plan_id}/rates/{rate_id}', {
      params: {
        path: { org_id: ctx.orgId, plan_id: plan.id, rate_id: rateId },
        header: idemHeader(rateKeys.get(rateId)),
      },
    })
    rateKeys.done(rateId)
  })
  async function onSubmit(values: RateValues) {
    try {
      await add.mutateAsync(values)
      idem.renew()
      form.reset({ from: values.from, amount: null as unknown as bigint, decision_id: '' })
      toast.info(t`Rate added.`)
    } catch (e) {
      applyServerErrors(form, e, t`Could not add the rate.`)
    }
  }
  const err = form.formState.errors
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Rates</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <QueryBoundary query={rates} isEmpty={(d) => d.length === 0} emptyTitle={t`No rates yet`}>
          {(items) => (
            <Table aria-label={t`Rate history`}>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <Trans>From</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>To</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Amount</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Decision</Trans>
                  </TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell>
                      <SiteDate value={r.from} timeZone={ctx.timeZone} />
                    </TableCell>
                    <TableCell>
                      {r.to ? <SiteDate value={r.to} timeZone={ctx.timeZone} /> : t`open`}
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={r.amount.amount} currency={ctx.currency} />
                    </TableCell>
                    <TableCell>{r.decision_id ?? '–'}</TableCell>
                    <TableCell className="text-end">
                      <Gate permission="money.manage" fallback={null}>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={remove.isPending}
                          aria-label={`${t`Delete rate`} ${formatMoney(r.amount.amount, ctx.currency, intl)}`}
                          onClick={() => remove.mutate(r.id)}
                        >
                          <Trans>Delete</Trans>
                        </Button>
                      </Gate>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </QueryBoundary>
        <Gate permission="money.manage" fallback={null}>
          <div className="grid gap-6 lg:grid-cols-2">
            <form
              className="flex flex-col gap-4"
              onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
              noValidate
            >
              <h3 className="font-medium">
                <Trans>Add a rate</Trans>
              </h3>
              <Field label={t`Starts on`} htmlFor="rate-from" error={err.from?.message}>
                <Input id="rate-from" type="date" dir="ltr" {...form.register('from')} />
              </Field>
              <MoneyFormField
                id="rate-amount"
                control={form.control}
                name="amount"
                label={t`New amount`}
                currency={ctx.currency}
                error={err.amount?.message}
              />
              <Field label={t`Decision reference (optional)`} htmlFor="rate-decision">
                <Input id="rate-decision" {...form.register('decision_id')} />
              </Field>
              <FormRootError message={err.root?.message} />
              <div>
                <Button type="submit" disabled={form.formState.isSubmitting}>
                  <Trans>Add rate</Trans>
                </Button>
              </div>
            </form>
            <div>
              <h3 className="mb-2 font-medium">
                <Trans>What each unit would pay</Trans>
              </h3>
              <PreviewTable
                preview={preview.data}
                ctx={ctx}
                loading={preview.isFetching}
                error={preview.isError}
              />
            </div>
          </div>
        </Gate>
      </CardContent>
    </Card>
  )
}

/* ───────────────────────────── one-off expense sharing ───────────────────────────── */

export function ExpenseSharingPage() {
  const ctx = useMoneyCtx()!
  return (
    <Gate permission="money.post">
      <ExpenseSharingWizard ctx={ctx} />
    </Gate>
  )
}

function ExpenseSharingWizard({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const labels = useLabels()
  const navigate = useNavigate()
  const idem = useIdempotencyKey()
  const keys = useAllocationKeys(ctx)
  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [description, setDescription] = useState('')
  const [total, setTotal] = useState<bigint | null>(null)
  const [due, setDue] = useState(todayIn(ctx.timeZone))
  const [chargeType, setChargeType] = useState<string>('special_assessment')
  const [side, setSide] = useState<'owner' | 'tenant'>('owner')
  const [keyId, setKeyId] = useState('')
  const [scope, setScope] = useState<S['ChargeScope']>({ type: 'site', id: null })
  const [touched, setTouched] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const effectiveKey = keyId || keys.data?.find((k) => k.kind === 'share')?.id || ''

  const body: S['ExpenseAllocationRequest'] | null =
    total && total > 0n && description.trim() && effectiveKey && (scope.type === 'site' || scope.id)
      ? {
          site_id: ctx.siteId,
          total: minorToMoney(total, ctx.currency),
          description: description.trim(),
          allocation_key_id: effectiveKey,
          scope,
          debtor_kind: side,
          due_date: due,
          charge_type: chargeType as S['ExpenseAllocationRequest']['charge_type'],
        }
      : null
  const preview = useMoneyQuery(
    ['expense-share-preview', body],
    () =>
      unwrap(
        moneyApi.POST('/v1/orgs/{org_id}/expense-allocations:preview', {
          params: { path: { org_id: ctx.orgId } },
          body: body!,
        }),
      ),
    { enabled: body !== null && step >= 2, retry: false },
  )
  const create = useMoneyMutation(async () => {
    const res = await moneyApi.POST('/v1/orgs/{org_id}/expense-allocations', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: body!,
    })
    return res.data!
  })
  const step1Ok = !!total && total > 0n && description.trim() !== ''
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Share an expense`}
        description={t`Turn one cost into charges on units with the same split rules as dues. Step ${step} of 3.`}
      />
      {step === 1 ? (
        <form
          className="flex max-w-xl flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            setTouched(true)
            if (step1Ok) setStep(2)
          }}
          noValidate
        >
          <Field
            label={t`What is it for?`}
            htmlFor="es-desc"
            error={touched && !description.trim() ? t`This field is required.` : undefined}
          >
            <Input
              id="es-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <Field
            label={t`Total amount`}
            htmlFor="es-total"
            error={
              touched && !(total && total > 0n) ? t`Enter an amount greater than zero.` : undefined
            }
          >
            <MoneyFormFieldBare
              id="es-total"
              currency={ctx.currency}
              value={total}
              onChange={setTotal}
            />
          </Field>
          <Field label={t`Due date`} htmlFor="es-due">
            <Input
              id="es-due"
              type="date"
              dir="ltr"
              value={due}
              onChange={(e) => setDue(e.target.value)}
            />
          </Field>
          <Field label={t`Charge type`} htmlFor="es-type">
            <Select
              id="es-type"
              value={chargeType}
              onChange={(e) => {
                setChargeType(e.target.value)
                setSide(defaultDebtorFor(e.target.value))
              }}
            >
              {CHARGE_TYPES.map((c) => (
                <option key={c} value={c}>
                  {labels.chargeType[c]}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t`Paid by`}>
            <SidePicker value={side} onChange={setSide} />
          </Field>
          <div>
            <Button type="submit">
              <Trans>Next: choose the split</Trans>
            </Button>
          </div>
        </form>
      ) : null}
      {step >= 2 ? (
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="flex flex-col gap-4">
            <Field label={t`Split rule`} htmlFor="es-key">
              <AllocationKeyPicker ctx={ctx} id="es-key" value={effectiveKey} onChange={setKeyId} />
            </Field>
            <Field label={t`Applies to`}>
              <ScopePicker ctx={ctx} value={scope} onChange={setScope} idPrefix="es-scope" />
            </Field>
            <FormRootError message={error ?? undefined} />
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => setStep(1)}>
                <Trans>Back</Trans>
              </Button>
              <Button
                disabled={!body || create.isPending || !preview.data}
                onClick={() => {
                  setError(null)
                  create.mutateAsync().then(
                    async () => {
                      idem.renew()
                      toast.info(t`Charges created.`)
                      await navigate({
                        to: '/orgs/$orgId/money/charges',
                        params: { orgId: ctx.orgId },
                      })
                    },
                    () => setError(t`Could not create the charges.`),
                  )
                }}
              >
                <Trans>Create charges</Trans>
              </Button>
            </div>
          </div>
          <Card className="self-start">
            <CardHeader>
              <CardTitle>
                <Trans>Preview</Trans>
              </CardTitle>
            </CardHeader>
            <CardContent>
              <PreviewTable
                preview={preview.data}
                ctx={ctx}
                loading={preview.isFetching}
                error={preview.isError}
              />
            </CardContent>
          </Card>
        </div>
      ) : null}
    </div>
  )
}

function MoneyFormFieldBare({
  id,
  currency,
  value,
  onChange,
}: {
  id: string
  currency: string
  value: bigint | null
  onChange: (v: bigint | null) => void
}) {
  return <MoneyInput id={id} currency={currency} value={value} onChange={onChange} />
}
