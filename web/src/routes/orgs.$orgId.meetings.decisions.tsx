import { createFileRoute } from '@tanstack/react-router'
import { DecisionsPage } from '@/features/meetings/DecisionsPage'

export const Route = createFileRoute('/orgs/$orgId/meetings/decisions')({
  component: DecisionsPage,
})
