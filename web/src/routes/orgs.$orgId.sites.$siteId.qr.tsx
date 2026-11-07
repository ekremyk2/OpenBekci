import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { SiteQrPage } from '@/features/people/SiteQrPage'

export const Route = createFileRoute('/orgs/$orgId/sites/$siteId/qr')({
  component: function QrRoute() {
    const { can } = useSession()
    const { siteId } = Route.useParams()
    if (!can('claims.decide', { siteId })) return <ForbiddenPage />
    return <SiteQrPage siteId={siteId} />
  },
})
