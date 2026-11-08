import { createFileRoute } from '@tanstack/react-router'
import { LateFeesPage } from '@/features/money/LateFeesPage'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/late-fees')({
  component: () => (
    <Gate permission="money.read">
      <LateFeesPage />
    </Gate>
  ),
})
