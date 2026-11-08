import { createFileRoute } from '@tanstack/react-router'
import { SITE_TABS, SiteDetailPage, type SiteTab } from '@/features/sites/SiteDetailPage'

export const Route = createFileRoute('/orgs/$orgId/sites/$siteId/')({
  validateSearch: (s: Record<string, unknown>): { tab?: SiteTab } => ({
    tab: SITE_TABS.find((t) => t === s.tab),
  }),
  component: function SiteRoute() {
    const { siteId } = Route.useParams()
    const { tab } = Route.useSearch()
    return <SiteDetailPage siteId={siteId} tab={tab ?? 'general'} />
  },
})
