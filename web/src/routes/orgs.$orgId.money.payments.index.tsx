import { createFileRoute } from '@tanstack/react-router'
import { PaymentsPage } from '@/features/money/PaymentsPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/payments/')({
  component: () => (
    <Gate permission="money.read">
      <PaymentsPage />
    </Gate>
  ),
})
