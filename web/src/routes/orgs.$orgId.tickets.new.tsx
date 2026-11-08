import { createFileRoute } from '@tanstack/react-router'
import { NewTicketPage } from '@/features/tickets/NewTicketPage'

export const Route = createFileRoute('/orgs/$orgId/tickets/new')({
  component: NewTicketPage,
})
