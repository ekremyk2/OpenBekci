import { createFileRoute } from '@tanstack/react-router'
import { PortalPay } from '@/features/money/PortalPages'

export const Route = createFileRoute('/orgs/$orgId/money/pay')({
  component: PortalPay,
})
