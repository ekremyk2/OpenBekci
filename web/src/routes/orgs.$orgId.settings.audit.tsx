import { createFileRoute } from '@tanstack/react-router'
import { AuditPage, parseAuditSearch } from '@/features/audit/AuditPage'

export const Route = createFileRoute('/orgs/$orgId/settings/audit')({
  validateSearch: parseAuditSearch,
  component: function AuditRoute() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    return (
      <AuditPage
        search={search}
        onSearch={(next) => void navigate({ search: next, replace: true })}
      />
    )
  },
})
