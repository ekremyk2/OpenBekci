import { createFileRoute } from '@tanstack/react-router'
import { ChargesPage } from '@/features/money/ChargesPage'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/charges')({
  component: () => (
    <Gate permission="money.read">
      <ChargesPage />
    </Gate>
  ),
})
