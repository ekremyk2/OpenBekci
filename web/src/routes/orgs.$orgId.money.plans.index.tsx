import { createFileRoute } from '@tanstack/react-router'
import { PlansListPage } from '@/features/money/PlansPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/plans/')({
  component: () => (
    <Gate permission="money.read">
      <PlansListPage />
    </Gate>
  ),
})
