import { createFileRoute } from '@tanstack/react-router'
import { MeetingsPage } from '@/features/meetings/MeetingsPage'

export const Route = createFileRoute('/orgs/$orgId/meetings/')({
  component: MeetingsPage,
})
