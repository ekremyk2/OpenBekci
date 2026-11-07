import { createFileRoute } from '@tanstack/react-router'
import { parseTicketSearch, TicketsPage } from '@/features/tickets/TicketsPage'

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
