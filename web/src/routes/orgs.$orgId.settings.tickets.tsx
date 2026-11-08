import { createFileRoute } from '@tanstack/react-router'
import { TicketSettingsPage } from '@/features/tickets/TicketSettingsPage'

export const Route = createFileRoute('/orgs/$orgId/settings/tickets')({
  component: TicketSettingsPage,
})
