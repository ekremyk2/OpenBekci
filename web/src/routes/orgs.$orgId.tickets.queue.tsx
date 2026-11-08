import { createFileRoute } from '@tanstack/react-router'
import { TicketsPage } from '@/features/tickets/TicketsPage'
import { parseTicketSearch } from '@/features/tickets/search'

export const Route = createFileRoute('/orgs/$orgId/tickets/queue')({
  validateSearch: parseTicketSearch,
  component: function TicketQueueRoute() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    return (
      <TicketsPage
        view="queue"
        search={search}
        onSearch={(next) => void navigate({ search: next, replace: true })}
      />
    )
  },
})
