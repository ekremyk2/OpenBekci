import { createFileRoute } from '@tanstack/react-router'
import { TicketDetailPage } from '@/features/tickets/TicketDetailPage'

export const Route = createFileRoute('/orgs/$orgId/tickets/$ticketId')({
  component: function TicketRoute() {
    const { ticketId } = Route.useParams()
    return <TicketDetailPage ticketId={ticketId} />
  },
})
