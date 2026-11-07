import { createFileRoute } from '@tanstack/react-router'
import { FacilitiesPage } from '@/features/bookings/FacilitiesPage'
import { SiteGate } from '@/features/meetings/kit/SiteGate'

export const Route = createFileRoute('/orgs/$orgId/facilities')({
  component: () => (
    <SiteGate anyOf={['facilities.manage', 'bookings.read']}>
      <FacilitiesPage />
    </SiteGate>
  ),
})
