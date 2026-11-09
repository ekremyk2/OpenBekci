import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ChatSettingsPage } from '@/features/chat/ChatSettingsPage'
import { NoAccess } from '@/features/integrations/NoAccess'

export const Route = createFileRoute('/orgs/$orgId/settings/chat')({
  component: function ChatRoute() {
    const { can } = useSession()
    if (!can('chat.manage')) return <NoAccess />
    return <ChatSettingsPage />
  },
})
