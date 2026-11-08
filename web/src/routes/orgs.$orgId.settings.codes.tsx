import { createFileRoute } from '@tanstack/react-router'
import { CodesDebugPage } from '@/features/settings/CodesDebugPage'

export const Route = createFileRoute('/orgs/$orgId/settings/codes')({
  component: CodesDebugPage,
})
