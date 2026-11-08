import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Trans } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { SiteDate } from '@/components/data/SiteDate'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { must } from '@/lib/pagination'
import { SitePicker, useSite } from './site'

function Stat({ label, children }: { label: React.ReactNode; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">{label}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold">{children}</div>
      </CardContent>
    </Card>
  )
}

/** Counts-only staff dashboard (`dashboards.getStaffDashboard`, `dashboard.staff`): no money. */
export function StaffDashboardView() {
  const { org } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const query = useQuery({
    queryKey: ['dashboards.getStaffDashboard', orgId, site?.id],
    enabled: !!site,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/staff-dashboard', {
          params: { path: { org_id: orgId, site_id: site!.id } },
        }),
      ),
  })
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
              As of{' '}
              <SiteDate
                value={query.data.as_of}
                timeZone={site?.timezone ?? 'UTC'}
                kind="datetime"
              />
            </Trans>
          </p>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Stat label={<Trans>Assigned to you</Trans>}>
              <Link
                to="/orgs/$orgId/tickets/queue"
                params={{ orgId }}
                search={{}}
                className="hover:underline"
              >
                {query.data.assigned_open_tickets}
              </Link>
            </Stat>
            <Stat label={<Trans>Overdue</Trans>}>{query.data.assigned_overdue_tickets}</Stat>
            <Stat label={<Trans>Open common tickets</Trans>}>{query.data.open_common_tickets}</Stat>
            <Stat label={<Trans>Expected visitors today</Trans>}>
              {query.data.expected_visitors_today}
            </Stat>
            <Stat label={<Trans>Waiting deliveries</Trans>}>{query.data.waiting_deliveries}</Stat>
          </div>
        </>
      )}
    </div>
  )
}
