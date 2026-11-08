import { createFileRoute } from '@tanstack/react-router'
import { CollectionsPage } from '@/features/money/CollectionsPage'
import { Gate } from '@/features/money/ui'

export const Route = createFileRoute('/orgs/$orgId/money/collections')({
  component: () => (
    <Gate permission="money.read">
      <CollectionsPage />
    </Gate>
  ),
})
