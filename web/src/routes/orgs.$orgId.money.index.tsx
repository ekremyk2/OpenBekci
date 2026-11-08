import { createFileRoute } from '@tanstack/react-router'
import { OverviewPage } from '@/features/money/OverviewPage'

export const Route = createFileRoute('/orgs/$orgId/money/')({
  component: OverviewPage,
})
