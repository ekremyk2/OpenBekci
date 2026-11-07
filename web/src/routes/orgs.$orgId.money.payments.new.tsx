import { createFileRoute } from '@tanstack/react-router'
import { NewPaymentPage } from '@/features/money/PaymentsPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/payments/new')({
  component: () => (
    <Gate permission="money.read">
      <NewPaymentPage />
    </Gate>
  ),
})
