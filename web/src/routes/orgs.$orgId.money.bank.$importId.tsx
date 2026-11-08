import { createFileRoute } from '@tanstack/react-router'
import { BankMatchPage } from '@/features/money/BankPages'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/bank/$importId')({
  component: function BankRoute() {
    const { importId } = Route.useParams()
    return (
      <Gate permission="money.read">
        <BankMatchPage importId={importId} />
      </Gate>
    )
  },
})
