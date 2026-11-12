import { createFileRoute } from '@tanstack/react-router'
import { JoinPage } from '@/features/auth/JoinPage'

export const Route = createFileRoute('/join/$code')({
  component: function Join() {
    const { code } = Route.useParams()
    return <JoinPage code={code} />
  },
})
