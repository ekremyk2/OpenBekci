import { Link } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { moneyApi, unwrap, useMoneyCtx, useMoneyQuery, type MoneyCtx } from './api'
import { Gate, PageHeader, QueryBoundary, SideBadge, Stat } from './ui'
import { PortalHome } from './PortalPages'

export function OverviewPage() {
  const { can } = useSession()
  if (!can('money.read')) return <PortalHome />
  return <ManagerOverview />
}

function ManagerOverview() {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const { can, org } = useSession()
  const q = useMoneyQuery(['overview', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/money/overview', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ),
  )
  const cur = ctx.currency
  const links = [
    { to: 'plans', label: t`Dues plans`, text: t`Rates, split rules and live previews` },
    { to: 'charges', label: t`Charges`, text: t`Every receivable by period, unit and side` },
    { to: 'late-fees', label: t`Late fees`, text: t`Accrual, monthly bookings, waivers, freezes` },
    { to: 'payments', label: t`Payments`, text: t`Cash receipts, cash boxes, declared transfers` },
    { to: 'bank', label: t`Bank import`, text: t`Upload a statement and match lines to units` },
    { to: 'expenses', label: t`Expenses`, text: t`What was spent, from which account` },
    { to: 'collections', label: t`Collections`, text: t`Reminders, ihtarname and legal cases` },
    { to: 'statements', label: t`Statements`, text: t`Per unit side or per person, PDF` },
    { to: 'reports', label: t`Reports`, text: t`Gelir-gider, debtors, budget, aging` },
  ]
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t`Money overview`}
        description={t`Where the site stands today: what residents owe, what is in the bank and in cash.`}
      />
      <QueryBoundary query={q} skeleton="page">
        {(o) => {
          const top = [...o.aging.rows].sort((a, b) => b.total.amount - a.total.amount).slice(0, 5)
          return (
            <>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <Stat label={t`Owed by owners`}>
                  <Money minor={o.receivable_owner.amount} currency={cur} />
                </Stat>
                <Stat label={t`Owed by tenants`}>
                  <Money minor={o.receivable_tenant.amount} currency={cur} />
                </Stat>
                <Stat label={t`Late fees accrued`} hint={t`Not yet booked; shown continuously`}>
                  <Money minor={o.accrued_late_fees.amount} currency={cur} />
                </Stat>
                <Stat label={t`Collection rate this month`}>
                  <span data-testid="collection-rate">{o.collection_rate_pct}%</span>
                </Stat>
                <Stat label={t`Bank`}>
                  <Money minor={o.bank.amount} currency={cur} />
                </Stat>
                <Stat label={t`Cash`}>
                  <Money minor={o.cash.amount} currency={cur} />
                </Stat>
              </div>
              {can('reports.read') ? <FundBalances ctx={ctx} /> : null}
              <Card>
                <CardHeader>
                  <CardTitle>
                    <Trans>Largest balances</Trans>
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  {top.length === 0 ? (
                    <p className="text-sm text-muted-foreground">
                      <Trans>Nobody owes anything right now.</Trans>
                    </p>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>
                            <Trans>Unit</Trans>
                          </TableHead>
                          <TableHead>
                            <Trans>Side</Trans>
                          </TableHead>
                          <TableHead className="text-end">
                            <Trans>Open</Trans>
                          </TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {top.map((r) => (
                          <TableRow key={`${r.unit_id}-${r.debtor_kind}`}>
                            <TableCell>
                              <Link
                                to="/orgs/$orgId/money/statements"
                                params={{ orgId: org?.id ?? '' }}
                                search={{ unit: r.unit_id, side: r.debtor_kind }}
                                className="underline-offset-4 hover:underline"
                              >
                                {r.unit_door_number}
                              </Link>
                            </TableCell>
                            <TableCell>
                              <SideBadge side={r.debtor_kind} />
                            </TableCell>
                            <TableCell className="text-end">
                              <Money minor={r.total.amount} currency={cur} />
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                  <p className="mt-3 text-xs text-muted-foreground">
                    <Trans>
                      As of <SiteDate value={o.as_of} timeZone={ctx.timeZone} />
                    </Trans>
                  </p>
                </CardContent>
              </Card>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {links.map((l) => (
                  <Link
                    key={l.to}
                    to={`/orgs/$orgId/money/${l.to}` as '/orgs/$orgId'}
                    params={{ orgId: org?.id ?? '' }}
                    className="rounded-lg border p-4 outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring"
                  >
                    <div className="font-medium">{l.label}</div>
                    <div className="text-sm text-muted-foreground">{l.text}</div>
                  </Link>
                ))}
              </div>
            </>
          )
        }}
      </QueryBoundary>
    </div>
  )
}

function FundBalances({ ctx }: { ctx: MoneyCtx }) {
  const { t } = useLingui()
  const q = useMoneyQuery(['fund-balances', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/reports/fund-balances', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
      }),
    ),
  )
  const kind: Record<string, string> = { operating: t`Operating`, reserve: t`Reserve` }
  return (
    <Gate permission="reports.read" fallback={null}>
      <QueryBoundary query={q} skeleton="page">
        {(r) => (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {r.rows.map((f) => (
              <Stat
                key={f.fund_id}
                label={kind[f.kind] ? `${t`Fund`}: ${kind[f.kind]}` : f.name}
                hint={f.name}
              >
                <Money minor={f.balance.amount} currency={ctx.currency} />
              </Stat>
            ))}
          </div>
        )}
      </QueryBoundary>
    </Gate>
  )
}
