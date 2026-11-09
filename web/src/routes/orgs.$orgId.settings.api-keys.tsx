import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ApiKeysPage } from '@/features/integrations/ApiKeysPage'
import { NoAccess } from '@/features/integrations/NoAccess'

export const Route = createFileRoute('/orgs/$orgId/settings/api-keys')({
  component: function ApiKeysRoute() {
    const { can } = useSession()
    if (!can('apikeys.read') && !can('apikeys.manage')) return <NoAccess />
    return <ApiKeysPage />
  },
})
