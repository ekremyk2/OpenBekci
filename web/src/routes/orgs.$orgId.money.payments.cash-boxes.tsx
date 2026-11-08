import { createFileRoute } from '@tanstack/react-router'
import { CashBoxesPage } from '@/features/money/PaymentsPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/payments/cash-boxes')({
  component: () => (
    <Gate permission="money.read">
      <CashBoxesPage />
    </Gate>
  ),
})
