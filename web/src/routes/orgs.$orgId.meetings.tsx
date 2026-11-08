import { createFileRoute } from '@tanstack/react-router'
import { MeetingsLayout } from '@/features/meetings/MeetingsLayout'

export const Route = createFileRoute('/orgs/$orgId/meetings')({
  component: MeetingsLayout,
})
