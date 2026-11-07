import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { VendorsPage } from '@/features/people/VendorsPage'

export const Route = createFileRoute('/orgs/$orgId/vendors')({
  validateSearch: (s: Record<string, unknown>): { q?: string; archived?: boolean } => ({
    q: typeof s.q === 'string' && s.q ? s.q : undefined,
    archived: s.archived === true || s.archived === 'true' ? true : undefined,
  }),
  component: function VendorsRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('vendors.read')) return <ForbiddenPage />
    return <VendorsPage search={search} />
  },
})
