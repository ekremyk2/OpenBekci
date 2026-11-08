import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { PeoplePage, type PeopleSearch } from '@/features/people/PeoplePage'

const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)

export const Route = createFileRoute('/orgs/$orgId/people/')({
  validateSearch: (s: Record<string, unknown>): PeopleSearch => ({
    site: str(s.site),
    q: str(s.q),
    join: str(s.join),
  }),
  component: function PeopleRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('people.read')) return <ForbiddenPage />
    return <PeoplePage search={search} />
  },
})
