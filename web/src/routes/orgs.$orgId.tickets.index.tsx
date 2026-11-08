import { createFileRoute } from '@tanstack/react-router'
import { parseTicketSearch, TicketsPage } from '@/features/tickets/TicketsPage'

export const Route = createFileRoute('/orgs/$orgId/tickets/')({
  validateSearch: parseTicketSearch,
  component: function TicketsRoute() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    return (
      <TicketsPage
        search={search}
        onSearch={(next) => void navigate({ search: next, replace: true })}
      />
    )
  },
})
