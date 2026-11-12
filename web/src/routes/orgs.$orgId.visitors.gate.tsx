import { createFileRoute } from '@tanstack/react-router'
import { GuardConsolePage } from '@/features/gate/GuardConsolePage'
import { SiteGate } from '@/features/meetings/kit/SiteGate'

export const Route = createFileRoute('/orgs/$orgId/visitors/gate')({
  component: () => (
    <SiteGate anyOf={['gate.operate', 'visitors.manage']}>
      <GuardConsolePage />
    </SiteGate>
  ),
})
