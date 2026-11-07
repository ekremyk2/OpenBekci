import { createFileRoute } from '@tanstack/react-router'
import { AnnouncementDetailPage } from '@/features/announcements/AnnouncementDetailPage'

export const Route = createFileRoute('/orgs/$orgId/announcements/$announcementId/')({
  component: function AnnouncementRoute() {
    const { announcementId } = Route.useParams()
    return <AnnouncementDetailPage announcementId={announcementId} />
  },
})
