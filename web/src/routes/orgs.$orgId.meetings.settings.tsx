import { createFileRoute } from '@tanstack/react-router'
import { SettingsPage } from '@/features/meetings/SettingsPage'
import { Gate } from '@/features/meetings/kit/ui'

export const Route = createFileRoute('/orgs/$orgId/meetings/settings')({
  component: () => (
    <Gate permission="meetings.manage">
      <SettingsPage />
    </Gate>
  ),
})
