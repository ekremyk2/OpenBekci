import { createFileRoute } from '@tanstack/react-router'
import { InvitePage } from '@/features/auth/InvitePage'

export const Route = createFileRoute('/invite/$token')({
  component: function Invite() {
    const { token } = Route.useParams()
    return <InvitePage token={token} />
  },
})
