import { createFileRoute } from '@tanstack/react-router'
import { AnnouncementEditPage } from '@/features/announcements/AnnouncementEditPage'

export const Route = createFileRoute('/orgs/$orgId/announcements/$announcementId/edit')({
  component: function AnnouncementEditRoute() {
    const { announcementId } = Route.useParams()
    return <AnnouncementEditPage announcementId={announcementId} />
  },
})
