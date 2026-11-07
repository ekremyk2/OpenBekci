import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { LayoutDashboardIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { PageSkeleton } from '@/components/data/Skeletons'
import { PageHeader } from '@/components/layout/PageHeader'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export function usePortfolio(orgId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['dashboards.getPortfolioDashboard', orgId],
    enabled,
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/dashboard/portfolio', {
        params: { path: { org_id: orgId } },
      })
      return data!
    },
  })
}

function Stat({ label, children }: { label: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold">{children}</dd>
    </div>
  )
}

/** Org-level dashboard for management companies: one card per site (overdue, cash, open tickets). */
export function PortfolioPage() {
  const { org } = useSession()
  const { t } = useLingui()
  const orgId = org?.id ?? ''
  const query = usePortfolio(orgId, !!org)
  const tz = org?.sites[0]?.timezone ?? 'UTC'
  if (query.isLoading) return <PageSkeleton />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const data = query.data!
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Portfolio</Trans>}
        description={
          <>
            <Trans>All your sites at a glance, as of</Trans>{' '}
            <SiteDate value={data.as_of} timeZone={tz} kind="datetime" />
          </>
        }
      />
      {data.sites.length === 0 ? (
        <EmptyState
          icon={LayoutDashboardIcon}
          title={<Trans>No sites to show</Trans>}
          description={
            <Trans>
              Sites you manage appear here with their overdue amounts, cash and open requests.
            </Trans>
          }
        />
      ) : (
        <>
          <dl
            className="grid grid-cols-2 gap-4 rounded-lg border p-4 sm:grid-cols-4"
            aria-label={t`Totals`}
          >
            <Stat label={<Trans>Overdue (all sites)</Trans>}>
              <Money minor={data.totals.overdue.amount} currency={data.totals.overdue.currency} />
            </Stat>
            <Stat label={<Trans>Cash</Trans>}>
              <Money minor={data.totals.cash.amount} currency={data.totals.cash.currency} />
            </Stat>
            <Stat label={<Trans>Bank</Trans>}>
              <Money minor={data.totals.bank.amount} currency={data.totals.bank.currency} />
            </Stat>
            <Stat label={<Trans>Open requests</Trans>}>{data.totals.open_tickets}</Stat>
          </dl>
          <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label={t`Sites`}>
            {data.sites.map((s) => (
              <li key={s.site_id}>
                <Card>
                  <CardHeader>
                    <CardTitle className="text-base">
                      <Link
                        to="/orgs/$orgId/sites/$siteId"
                        params={{ orgId, siteId: s.site_id }}
                        className="underline-offset-4 hover:underline"
                      >
                        {s.name}
                      </Link>
                    </CardTitle>
                  </CardHeader>
                  <CardContent>
                    <dl className="grid grid-cols-2 gap-3">
                      <Stat label={<Trans>Overdue</Trans>}>
                        <Money minor={s.overdue.amount} currency={s.overdue.currency} />
                        <span className="block text-xs font-normal text-muted-foreground">
                          <Trans>{s.overdue_units} units</Trans>
                        </span>
                      </Stat>
                      <Stat label={<Trans>Collected</Trans>}>{s.collection_rate_pct}%</Stat>
                      <Stat label={<Trans>Cash</Trans>}>
                        <Money minor={s.cash.amount} currency={s.cash.currency} />
                      </Stat>
                      <Stat label={<Trans>Open requests</Trans>}>{s.open_tickets}</Stat>
                    </dl>
                  </CardContent>
                </Card>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
