import { createFileRoute } from '@tanstack/react-router'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { useSession } from '@/app/session'
import { UnitsPage, type UnitsSearch } from '@/features/units/UnitsPage'

const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)

export const Route = createFileRoute('/orgs/$orgId/units/')({
  validateSearch: (s: Record<string, unknown>): UnitsSearch => ({
    site: str(s.site),
    block: str(s.block),
    type: str(s.type),
    vacant: s.vacant === 'yes' || s.vacant === 'no' ? s.vacant : undefined,
    q: str(s.q),
  }),
  component: function UnitsRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('units.read')) return <ForbiddenPage />
    return <UnitsPage search={search} />
  },
})
