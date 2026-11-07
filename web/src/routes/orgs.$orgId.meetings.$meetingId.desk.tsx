import { createFileRoute } from '@tanstack/react-router'
import { AttendanceDeskPage } from '@/features/meetings/AttendanceDeskPage'
import { Gate } from '@/features/meetings/kit/ui'

export const Route = createFileRoute('/orgs/$orgId/meetings/$meetingId/desk')({
  component: function DeskRoute() {
    const { meetingId } = Route.useParams()
    return (
      <Gate permission="attendance.manage">
        <AttendanceDeskPage meetingId={meetingId} />
      </Gate>
    )
  },
})
