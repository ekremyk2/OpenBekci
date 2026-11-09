import { createFileRoute } from '@tanstack/react-router'
import { CheckoutsPage } from '@/features/money/CheckoutsPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/checkouts')({
  component: () => (
    <Gate permission="money.read">
      <CheckoutsPage />
    </Gate>
  ),
})
