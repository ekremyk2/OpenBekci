import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { DirectoryPage } from '@/features/people/DirectoryPage'

const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)

export const Route = createFileRoute('/orgs/$orgId/directory')({
  validateSearch: (s: Record<string, unknown>): { site?: string; q?: string } => ({
    site: str(s.site),
    q: str(s.q),
  }),
  component: function DirectoryRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('directory.read')) return <ForbiddenPage />
    return <DirectoryPage search={search} />
  },
})
