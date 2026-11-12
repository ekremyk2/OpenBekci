import { AccessDenied } from '@/components/data/AccessDenied'
import { Link, Outlet } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { ScrollStrip } from '@/components/layout/ScrollStrip'
import { EmptyState } from '@/components/data/EmptyState'
import { moneyAccess } from '@/lib/money-access'
import { cn } from '@/lib/utils'
import { useMoneyCtx } from './api'
import { Select } from './ui'

interface Tab {
  to: string
  label: string
  /** Any of these permissions shows the tab. */
  any: string[]
  exact?: boolean
}

/** Money section frame: permission-filtered tabs, site switcher, breadcrumb-style heading. */
export function MoneyLayout() {
  const { t } = useLingui()
  const { can, org, me } = useSession()
  const ctx = useMoneyCtx()
  const access = moneyAccess(me, org?.id)
  const { manager, collector, resident, transparency } = access

  const managerTabs: Tab[] = [
    { to: '/orgs/$orgId/money', label: t`Overview`, any: ['money.read'], exact: true },
    { to: '/orgs/$orgId/money/plans', label: t`Dues plans`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/charges', label: t`Charges`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/late-fees', label: t`Late fees`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/payments', label: t`Payments`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/bank', label: t`Bank import`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/checkouts', label: t`Card checkouts`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/settlements', label: t`Settlements`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/expenses', label: t`Expenses`, any: ['money.read'] },
    { to: '/orgs/$orgId/money/collections', label: t`Collections`, any: ['money.read'] },
    {
      to: '/orgs/$orgId/money/statements',
      label: t`Statements`,
      any: ['statements.read', 'money.read'],
    },
    {
      to: '/orgs/$orgId/money/reports',
      label: t`Reports`,
      any: ['reports.read', 'reports.debtors', 'money.read'],
    },
    { to: '/orgs/$orgId/money/opening-balances', label: t`Opening balances`, any: ['money.read'] },
  ]
  const residentTabs: Tab[] = [
    { to: '/orgs/$orgId/money', label: t`My balance`, any: [], exact: true },
    { to: '/orgs/$orgId/money/pay', label: t`Pay`, any: [] },
    ...(can('payments.checkout')
      ? [{ to: '/orgs/$orgId/money/my-checkouts', label: t`Card payments`, any: [] as string[] }]
      : []),
    { to: '/orgs/$orgId/money/statements', label: t`Statement`, any: [] },
  ]
  const collectorTabs: Tab[] = [
    { to: '/orgs/$orgId/money/payments/new', label: t`Record cash`, any: [], exact: true },
  ]
  const reportsTab: Tab = { to: '/orgs/$orgId/money/reports', label: t`Reports`, any: [] }
  const tabs = manager
    ? managerTabs.filter((tab) => tab.any.some((p) => can(p)))
    : [
        ...(resident ? residentTabs : []),
        ...(collector ? collectorTabs : []),
        ...(transparency ? [reportsTab] : []),
      ]

  if (!access.any) {
    return <AccessDenied />
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm font-medium text-muted-foreground">
          <Trans>Money</Trans>
        </span>
        {ctx && ctx.sites.length > 1 ? (
          <Select
            aria-label={t`Site`}
            className="w-auto"
            value={ctx.siteId}
            onChange={(e) => ctx.setSiteId(e.target.value)}
          >
            {ctx.sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        ) : ctx ? (
          <span className="text-sm text-muted-foreground">{ctx.siteName}</span>
        ) : null}
      </div>
      <ScrollStrip label={t`Money sections`}>
        {tabs.map((tab) => (
          <Link
            key={tab.to}
            to={tab.to as '/orgs/$orgId'}
            params={{ orgId: org?.id ?? '' }}
            activeOptions={{ exact: !!tab.exact }}
            className={cn(
              'inline-flex min-h-11 snap-start items-center rounded-md px-3 text-sm font-medium whitespace-nowrap outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
              '[&.active]:bg-primary [&.active]:text-primary-foreground',
            )}
          >
            {tab.label}
          </Link>
        ))}
      </ScrollStrip>
      {ctx ? (
        // under 640px a wide table scrolls sideways under its first column, which stays put
        <div className="max-sm:[&_td:first-child]:sticky max-sm:[&_td:first-child]:start-0 max-sm:[&_td:first-child]:z-[1] max-sm:[&_td:first-child]:bg-background max-sm:[&_td:first-child]:shadow-[1px_0_0_var(--color-border)] max-sm:[&_th:first-child]:start-0 max-sm:[&_th:first-child]:z-20 max-sm:[&_th:first-child]:shadow-[1px_0_0_var(--color-border)] max-sm:[&_tr:hover_td:first-child]:bg-muted">
          <Outlet />
        </div>
      ) : (
        <EmptyState
          title={t`No site yet`}
          description={t`Set up a site first; money is kept per site.`}
        />
      )}
    </div>
  )
}
