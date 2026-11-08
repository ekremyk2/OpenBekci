import { createFileRoute } from '@tanstack/react-router'
import { DefaultsEditor } from '@/features/notifications/DefaultsEditor'

export const Route = createFileRoute('/orgs/$orgId/settings/notifications')({
  component: DefaultsEditor,
})
