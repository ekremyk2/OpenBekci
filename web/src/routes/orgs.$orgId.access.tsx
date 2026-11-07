import { createFileRoute } from '@tanstack/react-router'
import { AccessPage } from '@/features/access/AccessPage'
import { SiteGate } from '@/features/meetings/kit/SiteGate'

export const Route = createFileRoute('/orgs/$orgId/access')({
  component: () => (
    <SiteGate anyOf={['access.read']}>
      <AccessPage />
    </SiteGate>
  ),
})
