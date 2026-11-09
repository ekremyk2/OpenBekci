import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { NoAccess } from '@/features/integrations/NoAccess'
import { WebhooksPage } from '@/features/integrations/WebhooksPage'

const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)

export const Route = createFileRoute('/orgs/$orgId/settings/webhooks')({
  validateSearch: (s: Record<string, unknown>): { endpoint?: string } => ({
    endpoint: str(s.endpoint),
  }),
  component: function WebhooksRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    if (!can('webhooks.read') && !can('webhooks.manage')) return <NoAccess />
    return (
      <WebhooksPage
        endpointId={search.endpoint}
        onSelect={(endpoint) => void navigate({ search: { endpoint }, replace: true })}
      />
    )
  },
})
