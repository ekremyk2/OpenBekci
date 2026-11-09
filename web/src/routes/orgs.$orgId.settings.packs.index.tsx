import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { NoAccess } from '@/features/integrations/NoAccess'
import { PacksPage } from '@/features/packs/PacksPage'

export const Route = createFileRoute('/orgs/$orgId/settings/packs/')({
  component: function PacksRoute() {
    const { can } = useSession()
    if (!can('packs.read')) return <NoAccess />
    return <PacksPage />
  },
})
