import { createFileRoute } from '@tanstack/react-router'
import { TicketsPage } from '@/features/tickets/TicketsPage'
import { parseTicketSearch } from '@/features/tickets/search'

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
