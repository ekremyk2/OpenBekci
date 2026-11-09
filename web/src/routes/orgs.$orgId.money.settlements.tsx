import { createFileRoute } from '@tanstack/react-router'
import { SettlementsPage } from '@/features/money/SettlementsPage'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/settlements')({
  component: () => (
    <Gate permission="money.read">
      <SettlementsPage />
    </Gate>
  ),
})
