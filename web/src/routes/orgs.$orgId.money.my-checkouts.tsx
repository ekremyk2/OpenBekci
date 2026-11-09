import { createFileRoute } from '@tanstack/react-router'
import { MyCheckoutsPage } from '@/features/money/CheckoutsPages'

export const Route = createFileRoute('/orgs/$orgId/money/my-checkouts')({
  component: MyCheckoutsPage,
})
