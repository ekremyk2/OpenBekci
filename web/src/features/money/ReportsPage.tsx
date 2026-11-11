import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { MoneyInput } from '@/components/forms/MoneyInput'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
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
  monthRange,
  startOfYear,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { useAccounts, useFunds } from './pickers'
import { ExportButtons, Field, Gate, PageHeader, QueryBoundary, Select, SideBadge } from './ui'

export type ReportId =
  | 'income-expense'
  | 'debtors'
  | 'budget'
  | 'trial-balance'
  | 'aging'
  | 'funds'

export function ReportsPage({ report }: { report: ReportId }) {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const { can } = useSession()
  const navigate = useNavigate()
  const all: { id: ReportId; label: string; perm: string }[] = [
    { id: 'income-expense', label: t`Income and expense`, perm: 'reports.read' },
    { id: 'debtors', label: t`Debtors`, perm: 'reports.debtors' },
    { id: 'budget', label: t`Budget vs actual`, perm: 'reports.read' },
    { id: 'trial-balance', label: t`Trial balance`, perm: 'reports.read' },
    { id: 'aging', label: t`Receivables aging`, perm: 'reports.read' },
    { id: 'funds', label: t`Fund balances`, perm: 'reports.read' },
  ]
  const visible = all.filter(
    (r) =>
      can(r.perm) ||
      (r.id === 'income-expense' && can('reports.aggregate')) ||
      (r.id === 'budget' && can('reports.aggregate')),
  )
  const current = visible.find((r) => r.id === report) ?? visible[0]
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Reports`}
        description={t`Cash-basis income and expense, debtors, budget, and the accountant's views. Every report exports to CSV and Excel.`}
      />
      <div role="tablist" aria-label={t`Reports`} className="flex flex-wrap gap-1">
        {visible.map((r) => (
          <Button
            key={r.id}
            role="tab"
            aria-selected={current?.id === r.id}
            variant={current?.id === r.id ? 'default' : 'outline'}
            size="sm"
            onClick={() => void navigate({ to: '.', search: { report: r.id }, replace: true })}
          >
            {r.label}
          </Button>
        ))}
      </div>
      {!current ? null : current.id === 'income-expense' ? (
        <IncomeExpense ctx={ctx} />
      ) : current.id === 'debtors' ? (
        <Debtors ctx={ctx} />
      ) : current.id === 'budget' ? (
        <BudgetReport ctx={ctx} />
      ) : current.id === 'trial-balance' ? (
        <TrialBalance ctx={ctx} />
      ) : current.id === 'aging' ? (
        <Aging ctx={ctx} />
      ) : (
        <Funds ctx={ctx} />
      )}
    </div>
  )
}

function useRange(ctx: MoneyCtx) {
  const today = todayIn(ctx.timeZone)
  const [from, setFrom] = useState(startOfYear(today))
  const [to, setTo] = useState(monthRange(today).to < today ? monthRange(today).to : today)
  return { from, to, setFrom, setTo }
}

function RangeFields({ r }: { r: ReturnType<typeof useRange> }) {
  const { t } = useLingui()
  return (
    <>
      <Field label={t`From`} htmlFor="r-from">
        <Input
          id="r-from"
          type="date"
          dir="ltr"
          value={r.from}
          onChange={(e) => r.setFrom(e.target.value)}
        />
      </Field>
      <Field label={t`To`} htmlFor="r-to">
        <Input
          id="r-to"
          type="date"
          dir="ltr"
          value={r.to}
          onChange={(e) => r.setTo(e.target.value)}
        />
      </Field>
    </>
  )
}

function AsOf({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { t } = useLingui()
  return (
    <Field label={t`As of`} htmlFor="r-asof">
      <Input
        id="r-asof"
        type="date"
        dir="ltr"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  )
}

function IncomeExpense({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const r = useRange(ctx)
  const q = useMoneyQuery(['rep-ie', ctx.siteId, r.from, r.to], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/reports/income-expense', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, from: r.from, to: r.to },
        },
      }),
    ),
  )
  const rows = (title: string, items: S['ReportAmountRow'][], total: S['Money']) => (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <Table>
          <TableBody>
            {items.map((i) => (
              <TableRow key={i.account_id}>
                <TableCell className="tabular-nums">{i.code}</TableCell>
                <TableCell>{i.name}</TableCell>
                <TableCell className="text-end">
                  <Money minor={i.amount.amount} currency={ctx.currency} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
          <TableFooter>
            <TableRow>
              <TableCell colSpan={2}>
                <Trans>Total</Trans>
              </TableCell>
              <TableCell className="text-end">
                <Money minor={total.amount} currency={ctx.currency} />
              </TableCell>
            </TableRow>
          </TableFooter>
        </Table>
      </CardContent>
    </Card>
  )
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        <RangeFields r={r} />
        <ExportButtons
          endpoint="/v1/orgs/{org_id}/reports/income-expense"
          query={{ site_id: ctx.siteId, from: r.from, to: r.to }}
          basename="income-expense"
        />
      </div>
      <QueryBoundary
        query={q}
        skeleton="page"
        isEmpty={(d) => d.income.length === 0 && d.expense.length === 0}
        emptyTitle={t`No income or expense in this period`}
      >
        {(d) => (
          <>
            <div className="grid gap-4 lg:grid-cols-2">
              {rows(t`Income`, d.income, d.total_income)}
              {rows(t`Expense`, d.expense, d.total_expense)}
            </div>
            <p className="text-lg font-semibold">
              <Trans>
                Net: <Money minor={d.net.amount} currency={ctx.currency} />
              </Trans>
            </p>
          </>
        )}
      </QueryBoundary>
    </div>
  )
}

function Debtors({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const [asOf, setAsOf] = useState(todayIn(ctx.timeZone))
  const [minMonths, setMinMonths] = useState('0')
  const q = useMoneyQuery(['rep-debtors', ctx.siteId, asOf, minMonths], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/reports/debtors', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, as_of: asOf, min_months: Number(minMonths) },
        },
      }),
    ),
  )
  return (
    <Gate permission="reports.debtors">
      <div className="flex flex-col gap-4">
        <p className="rounded-md bg-muted p-3 text-sm">
          <Trans>Visible to the manager and the board only. Never share it publicly.</Trans>
        </p>
        <div className="flex flex-wrap items-end gap-4">
          <AsOf value={asOf} onChange={setAsOf} />
          <Field label={t`At least this many months overdue`} htmlFor="d-min">
            <Input
              id="d-min"
              type="number"
              min={0}
              value={minMonths}
              onChange={(e) => setMinMonths(e.target.value)}
            />
          </Field>
          <ExportButtons
            endpoint="/v1/orgs/{org_id}/reports/debtors"
            query={{ site_id: ctx.siteId, as_of: asOf, min_months: minMonths }}
            basename="debtors"
          />
        </div>
        <QueryBoundary query={q} isEmpty={(d) => d.rows.length === 0} emptyTitle={t`No debtors`}>
          {(d) => (
            <Table>
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
                    <Trans>Principal</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Late fee</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Total</Trans>
                  </TableHead>
                  <TableHead className="text-end">
                    <Trans>Months overdue</Trans>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.rows.map((r) => (
                  <TableRow key={`${r.unit_id}-${r.debtor_kind}`}>
                    <TableCell>{r.unit_door_number}</TableCell>
                    <TableCell>
                      <SideBadge side={r.debtor_kind} />
                    </TableCell>
                    <TableCell>
                      {r.debtor_display_name ?? '–'}{' '}
                      {r.in_collection ? (
                        <Badge variant="warning">
                          <Trans>In collection</Trans>
                        </Badge>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={r.principal.amount} currency={ctx.currency} />
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={r.late_fee.amount} currency={ctx.currency} />
                    </TableCell>
                    <TableCell className="text-end">
                      <Money minor={r.total.amount} currency={ctx.currency} />
                    </TableCell>
                    <TableCell className="text-end tabular-nums">{r.months_overdue}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
              <TableFooter>
                <TableRow>
                  <TableCell colSpan={3}>
                    <Trans>Total</Trans>
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={d.total_principal.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={d.total_late_fee.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={d.total.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell />
                </TableRow>
              </TableFooter>
            </Table>
          )}
        </QueryBoundary>
      </div>
    </Gate>
  )
}

function BudgetReport({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const years = useMoneyQuery(['fiscal-years', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/fiscal-years', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ).then((r) => r.items),
  )
  const [yearId, setYearId] = useState('')
  const year = yearId || years.data?.[0]?.id || ''
  const report = useMoneyQuery(
    ['rep-budget', ctx.siteId, year],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/reports/budget-vs-actual', {
          params: {
            path: { org_id: ctx.orgId },
            query: { site_id: ctx.siteId, fiscal_year_id: year },
          },
        }),
      ),
    { enabled: !!year },
  )
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        <Field label={t`Fiscal year`} htmlFor="b-year">
          <Select
            id="b-year"
            className="w-auto"
            value={year}
            onChange={(e) => setYearId(e.target.value)}
          >
            {(years.data ?? []).map((y) => (
              <option key={y.id} value={y.id}>
                {y.start.slice(0, 4)}
              </option>
            ))}
          </Select>
        </Field>
        <ExportButtons
          endpoint="/v1/orgs/{org_id}/reports/budget-vs-actual"
          query={{ site_id: ctx.siteId, fiscal_year_id: year }}
          basename="budget-vs-actual"
        />
      </div>
      <QueryBoundary
        query={report}
        isEmpty={(d) => d.rows.length === 0}
        emptyTitle={t`No budget lines yet`}
        emptyDescription={t`Set a category budget below to compare it with what was spent.`}
      >
        {(d) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Category</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Budget</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Actual</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Variance</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {d.rows.map((r) => (
                <TableRow key={r.account_id}>
                  <TableCell>
                    {r.code} {r.name}
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={r.budget.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={r.actual.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={r.variance.amount} currency={ctx.currency} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
            <TableFooter>
              <TableRow>
                <TableCell>
                  <Trans>Total</Trans>
                </TableCell>
                <TableCell className="text-end">
                  <Money minor={d.total_budget.amount} currency={ctx.currency} />
                </TableCell>
                <TableCell className="text-end">
                  <Money minor={d.total_actual.amount} currency={ctx.currency} />
                </TableCell>
                <TableCell className="text-end">
                  <Money
                    minor={d.total_budget.amount - d.total_actual.amount}
                    currency={ctx.currency}
                  />
                </TableCell>
              </TableRow>
            </TableFooter>
          </Table>
        )}
      </QueryBoundary>
      {year ? (
        <Gate permission="money.manage" fallback={null}>
          <BudgetEditor ctx={ctx} fiscalYearId={year} />
        </Gate>
      ) : null}
    </div>
  )
}

function BudgetEditor({ ctx, fiscalYearId }: { ctx: MoneyCtx; fiscalYearId: string }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const cats = useAccounts(ctx, 'expense_category')
  const funds = useFunds(ctx)
  const budgets = useMoneyQuery(['budgets', ctx.siteId, fiscalYearId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/budgets', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, fiscal_year_id: fiscalYearId },
        },
      }),
    ).then((r) => r.items[0] ?? null),
  )
  const budget = budgets.data
  const lines = useMoneyQuery(
    ['budget-lines', budget?.id],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/budgets/{budget_id}/lines', {
          params: { path: { org_id: ctx.orgId, budget_id: budget!.id } },
        }),
      ),
    { enabled: !!budget },
  )
  const create = useMoneyMutation(async () => {
    await moneyApi.POST('/v1/orgs/{org_id}/budgets', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body: { site_id: ctx.siteId, fiscal_year_id: fiscalYearId },
    })
  })
  const [edits, setEdits] = useState<Record<string, bigint | null>>({})
  const save = useMoneyMutation(async () => {
    const fund = funds.data?.find((f) => f.is_default)?.id ?? ''
    const next = (cats.data ?? [])
      .map((c) => {
        const v =
          c.id in edits
            ? edits[c.id]
            : BigInt(
                lines.data?.lines.find((l) => l.expense_category_account_id === c.id)?.amount
                  .amount ?? 0,
              )
        return v && v > 0n
          ? {
              expense_category_account_id: c.id,
              fund_id:
                lines.data?.lines.find((l) => l.expense_category_account_id === c.id)?.fund_id ??
                fund,
              amount: minorMoney(v, ctx.currency),
            }
          : null
      })
      .filter((x): x is S['BudgetLine'] => x !== null)
    await moneyApi.PUT('/v1/orgs/{org_id}/budgets/{budget_id}/lines', {
      params: {
        path: { org_id: ctx.orgId, budget_id: budget!.id },
        header: ifMatch(lines.data!.version),
      },
      body: { lines: next },
    })
  })
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Category budget</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <QueryBoundary query={budgets} skeleton="page">
          {(b) =>
            !b ? (
              <div className="flex items-center gap-3">
                <p className="text-sm text-muted-foreground">
                  <Trans>
                    There is no budget for this year yet. It does not drive dues; it is only
                    compared with spending.
                  </Trans>
                </p>
                <Button
                  size="sm"
                  onClick={() => create.mutate(undefined, { onSuccess: () => idem.renew() })}
                >
                  <Trans>Create budget</Trans>
                </Button>
              </div>
            ) : (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  {(cats.data ?? []).map((c) => (
                    <Field key={c.id} label={c.name} htmlFor={`bl-${c.id}`}>
                      <MoneyInput
                        id={`bl-${c.id}`}
                        currency={ctx.currency}
                        value={
                          c.id in edits
                            ? edits[c.id]!
                            : lines.data?.lines.find((l) => l.expense_category_account_id === c.id)
                              ? BigInt(
                                  lines.data.lines.find(
                                    (l) => l.expense_category_account_id === c.id,
                                  )!.amount.amount,
                                )
                              : null
                        }
                        onChange={(v) => setEdits((e) => ({ ...e, [c.id]: v }))}
                      />
                    </Field>
                  ))}
                </div>
                <div>
                  <Button
                    disabled={save.isPending}
                    onClick={() =>
                      save.mutate(undefined, {
                        onSuccess: () => {
                          setEdits({})
                          toast.info(t`Budget saved.`)
                        },
                        onError: () => toast.error(t`Could not save the budget.`),
                      })
                    }
                  >
                    <Trans>Save budget</Trans>
                  </Button>
                </div>
              </>
            )
          }
        </QueryBoundary>
      </CardContent>
    </Card>
  )
}

function TrialBalance({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const [asOf, setAsOf] = useState(todayIn(ctx.timeZone))
  const q = useMoneyQuery(['rep-tb', ctx.siteId, asOf], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/reports/trial-balance', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, as_of: asOf } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        <AsOf value={asOf} onChange={setAsOf} />
        <ExportButtons
          endpoint="/v1/orgs/{org_id}/reports/trial-balance"
          query={{ site_id: ctx.siteId, as_of: asOf }}
          basename="trial-balance"
        />
      </div>
      <QueryBoundary query={q} isEmpty={(d) => d.rows.length === 0} emptyTitle={t`No postings yet`}>
        {(d) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Account</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Debit</Trans>
                </TableHead>
                <TableHead className="text-end">
                  <Trans>Credit</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {d.rows.map((r) => (
                <TableRow key={r.account_id}>
                  <TableCell>
                    {r.code} {r.name}
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={r.debit.amount} currency={ctx.currency} />
                  </TableCell>
                  <TableCell className="text-end">
                    <Money minor={r.credit.amount} currency={ctx.currency} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
            <TableFooter>
              <TableRow>
                <TableCell>
                  <Trans>Total</Trans>
                </TableCell>
                <TableCell className="text-end">
                  <Money minor={d.total_debit.amount} currency={ctx.currency} />
                </TableCell>
                <TableCell className="text-end">
                  <Money minor={d.total_credit.amount} currency={ctx.currency} />
                </TableCell>
              </TableRow>
            </TableFooter>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}

function Aging({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const [asOf, setAsOf] = useState(todayIn(ctx.timeZone))
  const q = useMoneyQuery(['rep-aging', ctx.siteId, asOf], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/reports/receivables-aging', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, as_of: asOf } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        <AsOf value={asOf} onChange={setAsOf} />
        <ExportButtons
          endpoint="/v1/orgs/{org_id}/reports/receivables-aging"
          query={{ site_id: ctx.siteId, as_of: asOf }}
          basename="receivables-aging"
        />
      </div>
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.rows.length === 0}
        emptyTitle={t`No open receivables`}
      >
        {(d) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Unit</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Side</Trans>
                </TableHead>
                {d.buckets.map((b) => (
                  <TableHead key={b} className="text-end">
                    {b}
                  </TableHead>
                ))}
                <TableHead className="text-end">
                  <Trans>Total</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {d.rows.map((r) => (
                <TableRow key={`${r.unit_id}-${r.debtor_kind}`}>
                  <TableCell>{r.unit_door_number}</TableCell>
                  <TableCell>
                    <SideBadge side={r.debtor_kind} />
                  </TableCell>
                  {r.buckets.map((b, i) => (
                    <TableCell key={i} className="text-end">
                      <Money minor={b.amount.amount} currency={ctx.currency} />
                    </TableCell>
                  ))}
                  <TableCell className="text-end">
                    <Money minor={r.total.amount} currency={ctx.currency} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
            <TableFooter>
              <TableRow>
                <TableCell colSpan={2}>
                  <Trans>Total</Trans>
                </TableCell>
                {d.totals.map((b, i) => (
                  <TableCell key={i} className="text-end">
                    <Money minor={b.amount.amount} currency={ctx.currency} />
                  </TableCell>
                ))}
                <TableCell className="text-end">
                  <Money
                    minor={d.totals.reduce((a, b) => a + b.amount.amount, 0)}
                    currency={ctx.currency}
                  />
                </TableCell>
              </TableRow>
            </TableFooter>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}

function Funds({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const [asOf, setAsOf] = useState(todayIn(ctx.timeZone))
  const q = useMoneyQuery(['rep-funds', ctx.siteId, asOf], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/reports/fund-balances', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, as_of: asOf } },
      }),
    ),
  )
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-4">
        <AsOf value={asOf} onChange={setAsOf} />
        <ExportButtons
          endpoint="/v1/orgs/{org_id}/reports/fund-balances"
          query={{ site_id: ctx.siteId, as_of: asOf }}
          basename="fund-balances"
        />
      </div>
      <QueryBoundary query={q} isEmpty={(d) => d.rows.length === 0} emptyTitle={t`No funds`}>
        {(d) => (
          <Table>
            <TableBody>
              {d.rows.map((f) => (
                <TableRow key={f.fund_id}>
                  <TableCell>{f.name}</TableCell>
                  <TableCell className="text-end">
                    <Money minor={f.balance.amount} currency={ctx.currency} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
            <TableFooter>
              <TableRow>
                <TableCell>
                  <Trans>Total</Trans>
                </TableCell>
                <TableCell className="text-end">
                  <Money minor={d.total.amount} currency={ctx.currency} />
                </TableCell>
              </TableRow>
            </TableFooter>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}
