import { createFileRoute } from '@tanstack/react-router'
import { OrganizationSettings } from '@/features/settings/OrganizationSettings'

export const Route = createFileRoute('/orgs/$orgId/settings/')({
  component: OrganizationSettings,
})
