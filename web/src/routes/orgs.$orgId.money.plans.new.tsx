import { createFileRoute } from '@tanstack/react-router'
import { NewPlanPage } from '@/features/money/PlansPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/plans/new')({
  component: () => (
    <Gate permission="money.read">
      <NewPlanPage />
    </Gate>
  ),
})
