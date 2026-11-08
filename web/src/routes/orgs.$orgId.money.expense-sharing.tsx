import { createFileRoute } from '@tanstack/react-router'
import { ExpenseSharingPage } from '@/features/money/PlansPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/expense-sharing')({
  component: () => (
    <Gate permission="money.read">
      <ExpenseSharingPage />
    </Gate>
  ),
})
