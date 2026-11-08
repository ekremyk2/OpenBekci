import { createFileRoute } from '@tanstack/react-router'
import { UnitDetailPage } from '@/features/units/UnitDetailPage'

export const Route = createFileRoute('/orgs/$orgId/units/$unitId')({
  component: function UnitRoute() {
    return <UnitDetailPage unitId={Route.useParams().unitId} />
  },
})
