import { createFileRoute } from '@tanstack/react-router'
import { OpeningBalancesPage } from '@/features/money/OpeningBalancesPage'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/opening-balances')({
  component: () => (
    <Gate permission="money.read">
      <OpeningBalancesPage />
    </Gate>
  ),
})
