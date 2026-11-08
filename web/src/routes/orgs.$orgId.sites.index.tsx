import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { SitesPage } from '@/features/sites/SitesPage'

export const Route = createFileRoute('/orgs/$orgId/sites/')({
  validateSearch: (s: Record<string, unknown>): { q?: string } => ({
    q: typeof s.q === 'string' && s.q ? s.q : undefined,
  }),
  component: function SitesRoute() {
    const { q } = Route.useSearch()
    const navigate = useNavigate()
    return (
      <SitesPage
        q={q}
        onSearch={(next) =>
          void navigate({ to: '.', search: { q: next || undefined }, replace: true })
        }
      />
    )
  },
})
