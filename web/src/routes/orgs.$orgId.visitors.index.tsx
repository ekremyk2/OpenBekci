import { createFileRoute } from '@tanstack/react-router'
import { SiteGate } from '@/features/meetings/kit/SiteGate'
import { VisitorsPage } from '@/features/visitors/VisitorsPage'

export const Route = createFileRoute('/orgs/$orgId/visitors/')({
  component: () => (
    <SiteGate anyOf={['visitors.read', 'visitors.manage', 'visitors.decide']}>
      <VisitorsPage />
    </SiteGate>
  ),
})
