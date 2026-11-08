import { createFileRoute } from '@tanstack/react-router'
import { BankImportsPage } from '@/features/money/BankPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/bank/')({
  component: () => (
    <Gate permission="money.read">
      <BankImportsPage />
    </Gate>
  ),
})
