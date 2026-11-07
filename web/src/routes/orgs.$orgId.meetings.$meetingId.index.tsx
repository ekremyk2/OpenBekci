import { createFileRoute } from '@tanstack/react-router'
import { MeetingDetailPage } from '@/features/meetings/MeetingDetailPage'

export const Route = createFileRoute('/orgs/$orgId/meetings/$meetingId/')({
  component: function MeetingRoute() {
    const { meetingId } = Route.useParams()
    return <MeetingDetailPage meetingId={meetingId} />
  },
})
