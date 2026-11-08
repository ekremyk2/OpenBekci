import { createFileRoute } from '@tanstack/react-router'
import { ExpensesPage } from '@/features/money/ExpensesPage'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/expenses')({
  component: () => (
    <Gate permission="money.read">
      <ExpensesPage />
    </Gate>
  ),
})
