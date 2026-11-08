import { createFileRoute } from '@tanstack/react-router'
import {
  AnnouncementsPage,
  type AnnouncementFilter,
} from '@/features/announcements/AnnouncementsPage'

const FILTERS = ['all', 'draft', 'scheduled', 'published', 'archived']

export const Route = createFileRoute('/orgs/$orgId/announcements/')({
  validateSearch: (raw: Record<string, unknown>): { status?: AnnouncementFilter } => ({
    status: FILTERS.includes(raw.status as string) ? (raw.status as AnnouncementFilter) : undefined,
  }),
  component: function AnnouncementsRoute() {
    const { status } = Route.useSearch()
    const navigate = Route.useNavigate()
    return (
      <AnnouncementsPage
        status={status ?? 'all'}
        onStatus={(s) => void navigate({ search: { status: s }, replace: true })}
      />
    )
  },
})
