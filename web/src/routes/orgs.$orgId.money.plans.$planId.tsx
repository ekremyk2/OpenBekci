import { createFileRoute } from '@tanstack/react-router'
import { PlanDetailPage } from '@/features/money/PlansPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/plans/$planId')({
  component: function PlanRoute() {
    const { planId } = Route.useParams()
    return (
      <Gate permission="money.read">
        <PlanDetailPage planId={planId} />
      </Gate>
    )
  },
})
