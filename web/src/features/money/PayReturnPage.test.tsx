import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { addCheckout, checkoutMock } from '@/test/handlers/checkout'
import { mountMoney } from '@/test/money-utils'
import { server } from '@/test/setup'

const RESIDENT = ['statements.read_own', 'payments.declare', 'payments.checkout']

describe('card payment return page', () => {
  it('waits while pending, then shows paid with the receipt and the statement', async () => {
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-ret', RESIDENT)
    addCheckout({ id: 'co-ret' })
    // the provider settles on the second look: the first answer is still pending
    expect(await screen.findByText(/Confirming your payment/)).toBeInTheDocument()
    expect(
      await screen.findByText('Payment received', undefined, { timeout: 4500 }),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download receipt (PDF)' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'See my statement' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('says plainly when the provider confirmed another amount', async () => {
    checkoutMock().pollsToSettle = 1
    checkoutMock().outcome = 'mismatch'
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-mm', RESIDENT)
    addCheckout({ id: 'co-mm' })
    expect(await screen.findByText('Payment received')).toBeInTheDocument()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(/instead of the/)
    expect(alert).toHaveTextContent(/90[.,]00/) // what the provider confirmed (90% of 100.00)
    expect(alert).toHaveTextContent(/100[.,]00/) // what was chosen
  })

  it('shows a declined card with a way to try again', async () => {
    checkoutMock().pollsToSettle = 1
    checkoutMock().outcome = 'declined'
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-f', RESIDENT)
    addCheckout({ id: 'co-f' })
    expect(await screen.findByText('The payment did not go through')).toBeInTheDocument()
    expect(screen.getByText('Your card was declined. Nothing was charged.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Try again' })).toHaveAttribute(
      'href',
      '/orgs/org-1/money/pay',
    )
    expect(screen.queryByText('Download receipt (PDF)')).not.toBeInTheDocument()
  })

  it('shows a provider error differently from a decline', async () => {
    checkoutMock().pollsToSettle = 1
    checkoutMock().outcome = 'failed'
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-e', RESIDENT)
    addCheckout({ id: 'co-e' })
    expect(await screen.findByText('The payment did not go through')).toBeInTheDocument()
    expect(
      screen.getByText('The provider could not complete the payment. Nothing was charged.'),
    ).toBeInTheDocument()
  })

  it('shows an expired payment page', async () => {
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-x', RESIDENT)
    addCheckout({ id: 'co-x', status: 'expired', completed_at: new Date().toISOString() })
    expect(await screen.findByText('The payment page expired')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Try again' })).toBeInTheDocument()
  })

  it('shows a refunded payment', async () => {
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-r', RESIDENT)
    addCheckout({ id: 'co-r', status: 'refunded', payment_id: 'pay-x' })
    expect(await screen.findByText('This payment was refunded')).toBeInTheDocument()
  })

  it('stops and offers a manual check when the window closed but the provider is silent', async () => {
    let asked = 0
    server.use(
      http.get('*/v1/orgs/:orgId/checkouts/:id', () => {
        asked += 1
        return HttpResponse.json({
          ...addCheckout({ id: 'co-s' }),
          expires_at: new Date(Date.now() - 60_000).toISOString(),
        })
      }),
    )
    mountMoney('/orgs/org-1/pay/return?checkout_id=co-s', RESIDENT)
    expect(await screen.findByText('Still waiting for the provider')).toBeInTheDocument()
    expect(asked).toBe(1)
    await userEvent.click(screen.getByRole('button', { name: 'Check again' }))
    await waitFor(() => expect(asked).toBe(2))
    expect(screen.getByText('Still waiting for the provider')).toBeInTheDocument()
  })

  it('says it could not find an unknown payment', async () => {
    mountMoney('/orgs/org-1/pay/return?checkout_id=ghost', RESIDENT)
    expect(await screen.findByText('We could not find this payment')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Check again' })).toBeInTheDocument()
  })

  it('needs a checkout id', async () => {
    mountMoney('/orgs/org-1/pay/return', RESIDENT)
    expect(await screen.findByText('This page needs a payment to look up')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to Pay' })).toBeInTheDocument()
  })
})

describe('my card payments', () => {
  it('lists my checkouts with their statuses and links each to its page', async () => {
    mountMoney('/orgs/org-1/money/my-checkouts', RESIDENT)
    const table = await screen.findByRole('table', { name: 'My card payments' })
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(4) // header and the three seeded checkouts
    expect(within(table).getByText('Paid')).toBeInTheDocument()
    expect(within(table).getByText('Failed')).toBeInTheDocument()
    expect(within(table).getByText('Expired')).toBeInTheDocument()
    const links = within(table).getAllByRole('link', { name: 'Details' })
    expect(links[0]!.getAttribute('href')).toContain('/orgs/org-1/pay/return?checkout_id=co-seed')
  })

  it('has its own tab only for a resident who may pay by card', async () => {
    mountMoney('/orgs/org-1/money', RESIDENT)
    expect(await screen.findByRole('link', { name: 'Card payments' })).toBeInTheDocument()
  })
})
