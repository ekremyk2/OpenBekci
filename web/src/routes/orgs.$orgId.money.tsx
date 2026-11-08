import { createFileRoute } from '@tanstack/react-router'
import { MoneyLayout } from '@/features/money/MoneyLayout'

export const Route = createFileRoute('/orgs/$orgId/money')({
  component: MoneyLayout,
})
