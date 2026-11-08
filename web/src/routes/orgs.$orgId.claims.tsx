import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { ClaimsPage } from '@/features/people/ClaimsPage'

export const Route = createFileRoute('/orgs/$orgId/claims')({
  validateSearch: (s: Record<string, unknown>): { status?: string } => ({
    status: typeof s.status === 'string' ? s.status : undefined,
  }),
  component: function ClaimsRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('claims.decide')) return <ForbiddenPage />
    return <ClaimsPage status={search.status ?? 'pending'} />
  },
})
