import { createFileRoute } from '@tanstack/react-router'
import { AnnouncementForm } from '@/features/announcements/AnnouncementForm'

export const Route = createFileRoute('/orgs/$orgId/announcements/new')({
  component: () => <AnnouncementForm />,
})
