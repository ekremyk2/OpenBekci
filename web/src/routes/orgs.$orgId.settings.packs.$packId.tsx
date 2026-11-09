import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { NoAccess } from '@/features/integrations/NoAccess'
import { PackDetailPage } from '@/features/packs/PackDetailPage'

export const Route = createFileRoute('/orgs/$orgId/settings/packs/$packId')({
  component: function PackRoute() {
    const { can } = useSession()
    const { packId } = Route.useParams()
    if (!can('packs.read')) return <NoAccess />
    return <PackDetailPage packId={packId} />
  },
})
