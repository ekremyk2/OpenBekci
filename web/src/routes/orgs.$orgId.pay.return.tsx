import { createFileRoute } from '@tanstack/react-router'
import { PayReturnPage } from '@/features/money/PayReturnPage'

/** Where the payment provider returns the payer (`Checkout.return_url`). */
export const Route = createFileRoute('/orgs/$orgId/pay/return')({
  validateSearch: (s: Record<string, unknown>): { checkout_id?: string } => ({
    checkout_id:
      typeof s.checkout_id === 'string' && s.checkout_id !== '' ? s.checkout_id : undefined,
  }),
  component: function PayReturnRoute() {
    const { orgId } = Route.useParams()
    const { checkout_id } = Route.useSearch()
    return <PayReturnPage orgId={orgId} checkoutId={checkout_id} />
  },
})
