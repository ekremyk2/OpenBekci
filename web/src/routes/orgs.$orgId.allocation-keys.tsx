import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { AllocationKeysPage } from '@/features/setup/AllocationKeysPage'

const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)

export const Route = createFileRoute('/orgs/$orgId/allocation-keys')({
  validateSearch: (s: Record<string, unknown>): { site?: string; key?: string } => ({
    site: str(s.site),
    key: str(s.key),
  }),
  component: function KeysRoute() {
    const { can } = useSession()
    const search = Route.useSearch()
    if (!can('units.read')) return <ForbiddenPage />
    return <AllocationKeysPage search={search} />
  },
})
