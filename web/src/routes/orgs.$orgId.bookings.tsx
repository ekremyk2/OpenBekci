import { createFileRoute } from '@tanstack/react-router'
import { BookingsPage } from '@/features/bookings/BookingsPage'
import { SiteGate } from '@/features/meetings/kit/SiteGate'

export const Route = createFileRoute('/orgs/$orgId/bookings')({
  component: () => (
    <SiteGate anyOf={['bookings.read', 'bookings.create', 'bookings.manage']}>
      <BookingsPage />
    </SiteGate>
  ),
})
