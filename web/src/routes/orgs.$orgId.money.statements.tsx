import { createFileRoute } from '@tanstack/react-router'
import { StatementsPage } from '@/features/money/StatementsPage'

export const Route = createFileRoute('/orgs/$orgId/money/statements')({
  validateSearch: (
    s: Record<string, unknown>,
  ): { unit?: string; side?: 'owner' | 'tenant'; person?: string } => ({
    unit: typeof s.unit === 'string' ? s.unit : undefined,
    side: s.side === 'owner' || s.side === 'tenant' ? s.side : undefined,
    person: typeof s.person === 'string' ? s.person : undefined,
  }),
  component: function StatementsRoute() {
    const search = Route.useSearch()
    return <StatementsPage search={search} />
  },
})
