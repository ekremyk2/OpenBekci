import { createFileRoute } from '@tanstack/react-router'
import { ConsentsPage } from '@/features/people/ConsentsPage'

export const Route = createFileRoute('/orgs/$orgId/me/consents')({
  component: ConsentsPage,
})
