import { createFileRoute } from '@tanstack/react-router'
import { useSession } from '@/app/session'
import { ForbiddenPage } from '@/components/data/StatusPages'
import { MoveOutPage } from '@/features/people/MoveOutPage'

export const Route = createFileRoute('/orgs/$orgId/move-out/$occupancyId')({
  component: function MoveOutRoute() {
    const { can } = useSession()
    const params = Route.useParams()
    if (!can('people.move_out')) return <ForbiddenPage />
    return <MoveOutPage occupancyId={params.occupancyId} />
  },
})
