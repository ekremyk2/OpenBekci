import { createFileRoute } from '@tanstack/react-router'
import { MeetingFormPage } from '@/features/meetings/MeetingFormPage'
import { Gate } from '@/features/meetings/kit/ui'

export const Route = createFileRoute('/orgs/$orgId/meetings/new')({
  component: () => (
    <Gate permission="meetings.manage">
      <MeetingFormPage />
    </Gate>
  ),
})
