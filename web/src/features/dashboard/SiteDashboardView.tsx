import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { apiDraft } from '@/api/draft'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { Money } from '@/components/data/Money'
import { PageSkeleton } from '@/components/data/Skeletons'
import { SiteDate } from '@/components/data/SiteDate'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { must } from '@/lib/pagination'
import { SitePicker, useSite } from './site'

function Stat({
  label,
  children,
  hint,
}: {
  label: React.ReactNode
  children: React.ReactNode
  hint?: React.ReactNode
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">{label}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold">{children}</div>
        {hint ? <p className="mt-1 text-xs text-muted-foreground">{hint}</p> : null}
      </CardContent>
    </Card>
  )
}

/** Site dashboard (`dashboards.getSiteDashboard`): money, open tickets and what needs attention. */
export function SiteDashboardView() {
  const { org } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const query = useQuery({
    queryKey: ['dashboards.getSiteDashboard', orgId, site?.id],
    enabled: !!site,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/sites/{site_id}/dashboard', {
          params: { path: { org_id: orgId, site_id: site!.id } },
        }),
      ),
  })
  const tz = site?.timezone ?? 'UTC'

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">{site?.name ?? org?.name}</h1>
        <SitePicker />
      </div>
      {query.isPending ? (
        <PageSkeleton />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          <p className="text-xs text-muted-foreground">
            <Trans>
              As of <SiteDate value={query.data.as_of} timeZone={tz} kind="datetime" />
            </Trans>
          </p>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Stat
              label={<Trans>Overdue</Trans>}
              hint={<Trans>{query.data.overdue_units} units</Trans>}
            >
              <Money
                minor={query.data.overdue_total.amount}
                currency={query.data.overdue_total.currency}
              />
            </Stat>
            <Stat label={<Trans>Cash</Trans>}>
              <Money minor={query.data.cash.amount} currency={query.data.cash.currency} />
            </Stat>
            <Stat label={<Trans>Bank</Trans>}>
              <Money minor={query.data.bank.amount} currency={query.data.bank.currency} />
            </Stat>
            <Stat label={<Trans>Collection rate</Trans>}>{query.data.collection_rate_pct}%</Stat>
            <Stat label={<Trans>Owed by owners</Trans>}>
              <Money
                minor={query.data.receivable_owner.amount}
                currency={query.data.receivable_owner.currency}
              />
            </Stat>
            <Stat label={<Trans>Owed by tenants</Trans>}>
              <Money
                minor={query.data.receivable_tenant.amount}
                currency={query.data.receivable_tenant.currency}
              />
            </Stat>
            <Stat label={<Trans>Accrued late fees</Trans>}>
              <Money
                minor={query.data.accrued_late_fees.amount}
                currency={query.data.accrued_late_fees.currency}
              />
            </Stat>
            <Stat
              label={<Trans>Open tickets</Trans>}
              hint={<Trans>{query.data.open_common_tickets} in common areas</Trans>}
            >
              <Link
                to="/orgs/$orgId/tickets"
                params={{ orgId }}
                className="hover:underline"
                search={{}}
              >
                {query.data.open_tickets}
              </Link>
            </Stat>
          </div>

          <Card>
            <CardHeader>
              <CardTitle>
                <Trans>Needs attention</Trans>
              </CardTitle>
            </CardHeader>
            <CardContent>
              <ul className="flex flex-col gap-2 text-sm">
                <li>
                  <Trans>Unit claims to approve: {query.data.pending_unit_claims}</Trans>
                </li>
                <li>
                  <Trans>
                    Declared payments to confirm: {query.data.pending_declared_payments}
                  </Trans>
                </li>
                <li>
                  <Trans>Waiting deliveries: {query.data.waiting_deliveries}</Trans>
                </li>
                <li>
                  <Trans>Visitor approvals: {query.data.pending_visitor_approvals}</Trans>
                </li>
              </ul>
            </CardContent>
          </Card>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>
                  <Trans>Upcoming meeting</Trans>
                </CardTitle>
              </CardHeader>
              <CardContent className="text-sm">
                {query.data.upcoming_meeting ? (
                  <p>
                    {query.data.upcoming_meeting.title ?? <Trans>Assembly</Trans>}{' '}
                    <SiteDate
                      value={query.data.upcoming_meeting.scheduled_at}
                      timeZone={tz}
                      kind="datetime"
                    />
                  </p>
                ) : (
                  <p className="text-muted-foreground">
                    <Trans>No meeting is scheduled.</Trans>
                  </p>
                )}
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>
                  <Trans>Terms ending soon</Trans>
                </CardTitle>
              </CardHeader>
              <CardContent className="text-sm">
                {query.data.expiring_terms.length === 0 ? (
                  <p className="text-muted-foreground">
                    <Trans>No terms end soon.</Trans>
                  </p>
                ) : (
                  <ul className="flex flex-col gap-1">
                    {query.data.expiring_terms.map((term) => (
                      <li key={term.membership_id}>
                        {term.person_display_name} <SiteDate value={term.valid_to} timeZone={tz} />
                      </li>
                    ))}
                  </ul>
                )}
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </div>
  )
}
