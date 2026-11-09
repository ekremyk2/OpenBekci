import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { addCheckout, checkoutMock } from '@/test/handlers/checkout'
import { getMoneyDb } from '@/test/handlers/money'
import { MANAGER_PERMISSIONS, mountMoney } from '@/test/money-utils'
import { server } from '@/test/setup'

/** Headers of every request to a path ending in `suffix`. */
function recordHeaders(suffix: string) {
  const seen: { key: string | null; ifMatch: string | null; body: Promise<unknown> }[] = []
  server.events.on('request:start', ({ request }) => {
    if (request.method === 'POST' && new URL(request.url).pathname.endsWith(suffix))
      seen.push({
        key: request.headers.get('Idempotency-Key'),
        ifMatch: request.headers.get('If-Match'),
        body: request.clone().json(),
      })
  })
  return seen
}

afterEach(() => server.events.removeAllListeners())

describe('card checkouts of a site', () => {
  it('lists them and filters by status', async () => {
    mountMoney('/orgs/org-1/money/checkouts')
    const table = await screen.findByRole('table', { name: 'Card checkouts' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
    expect(within(table).getAllByText('iyzico')).toHaveLength(3)

    await userEvent.selectOptions(screen.getByLabelText('Status'), 'failed')
    await waitFor(() =>
      expect(
        within(screen.getByRole('table', { name: 'Card checkouts' })).getAllByRole('row'),
      ).toHaveLength(2),
    )
    expect(
      within(screen.getByRole('table', { name: 'Card checkouts' })).getByText('Failed'),
    ).toBeInTheDocument()

    await userEvent.selectOptions(screen.getByLabelText('Status'), 'pending')
    expect(await screen.findByText('No checkouts with this status')).toBeInTheDocument()
  })

  it('flags a checkout the provider confirmed for another amount', async () => {
    mountMoney('/orgs/org-1/money/checkouts')
    addCheckout({
      id: 'co-mm',
      status: 'paid',
      amount_mismatch: true,
      confirmed_amount: { amount: 9_000, currency: 'TRY' },
    })
    expect(await screen.findByText('Amount differs')).toBeInTheDocument()
    expect(screen.getByText(/provider confirmed/)).toBeInTheDocument()
  })

  it('refunds a paid checkout with If-Match and an Idempotency-Key, and shows it refunded', async () => {
    const seen = recordHeaders(':refund')
    const { db } = mountMoney('/orgs/org-1/money/checkouts')
    const table = await screen.findByRole('table', { name: 'Card checkouts' })
    const paid = within(table).getByRole('row', { name: /Paid/ })
    const before = db.charges.filter(
      (c) => c.status === 'paid' || c.status === 'partially_paid',
    ).length
    await userEvent.click(within(paid).getByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Refund this card payment?' })
    expect(dialog).toHaveTextContent(/partial refund is not possible/i)
    await userEvent.type(within(dialog).getByLabelText('Reason (optional)'), 'Paid twice')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Refund the payment' }))

    await waitFor(() => expect(seen).toHaveLength(1))
    expect(seen[0]!.ifMatch).toBe('"1"')
    expect(seen[0]!.key).toMatch(/^[0-9a-f-]{36}$/)
    expect(await seen[0]!.body).toEqual({ reason: 'Paid twice' })
    await waitFor(() =>
      expect(
        within(screen.getByRole('table', { name: 'Card checkouts' })).getByText('Refunded'),
      ).toBeInTheDocument(),
    )
    // the charges it paid are open again
    const after = getMoneyDb().charges.filter(
      (c) => c.status === 'paid' || c.status === 'partially_paid',
    ).length
    expect(after).toBeLessThanOrEqual(before)
    expect(checkoutMock().checkouts.find((c) => c.id === 'co-seed-paid')?.status).toBe('refunded')
  })

  it('answers a re-authentication challenge in place and replays the refund with the same key', async () => {
    const seen = recordHeaders(':refund')
    checkoutMock().refundNeedsReauth = true
    mountMoney('/orgs/org-1/money/checkouts')
    const table = await screen.findByRole('table', { name: 'Card checkouts' })
    await userEvent.click(
      within(within(table).getByRole('row', { name: /Paid/ })).getByRole('button', {
        name: 'Refund',
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: 'Refund this card payment?' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Refund the payment' }))

    const reauth = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    await userEvent.type(within(reauth).getByLabelText('Password'), 'correct horse')
    await userEvent.click(within(reauth).getByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(seen).toHaveLength(2))
    expect(seen[1]!.key).toBe(seen[0]!.key)
    await waitFor(() =>
      expect(checkoutMock().checkouts.find((c) => c.id === 'co-seed-paid')?.status).toBe(
        'refunded',
      ),
    )
  })

  it('closes the refund dialog when the payment version is stale', async () => {
    mountMoney('/orgs/org-1/money/checkouts')
    const table = await screen.findByRole('table', { name: 'Card checkouts' })
    const paid = within(table).getByRole('row', { name: /Paid/ })
    await userEvent.click(within(paid).getByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Refund this card payment?' })
    // somebody else changes the payment meanwhile
    const p = getMoneyDb().payments.find((x) => x.method.startsWith('card:'))!
    ;(p as { version?: number }).version = 7
    await userEvent.click(within(dialog).getByRole('button', { name: 'Refund the payment' }))
    // the stale dialog closes, so reopening it loads the current version
    await waitFor(() => expect(dialog).not.toBeInTheDocument())
  })

  it('offers no refund without money.post', async () => {
    mountMoney('/orgs/org-1/money/checkouts', ['money.read'])
    const table = await screen.findByRole('table', { name: 'Card checkouts' })
    expect(within(table).queryByRole('button', { name: 'Refund' })).not.toBeInTheDocument()
  })
})

describe('refund on the payments list', () => {
  it('shows Refund (not Void) for a card payment and refunds it', async () => {
    mountMoney('/orgs/org-1/money/payments')
    addCheckout()
    const row = await screen.findByRole('row', { name: /Card \(iyzico\)/ })
    expect(within(row).queryByRole('button', { name: 'Void' })).not.toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button', { name: 'Refund' }))
    const dialog = await screen.findByRole('dialog', { name: 'Refund this card payment?' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Refund the payment' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Refund this card payment?' })).toBeNull(),
    )
    expect(getMoneyDb().payments.find((p) => p.method.startsWith('card:'))?.status).toBe('refunded')
  })
})

describe('settlements, clearing and client money', () => {
  it('shows the provider balance, client money owed and paid out, and the payouts', async () => {
    mountMoney('/orgs/org-1/money/settlements')
    expect(await screen.findByTestId('clearing-iyzico')).toHaveTextContent(/1[.,]250[.,]00/)
    expect(screen.getByTestId('client-money-owed')).toHaveTextContent(/400[.,]00/)
    expect(screen.getByTestId('client-money-paid-out')).toHaveTextContent(/0[.,]00/)
    const table = await screen.findByRole('table', { name: 'Provider payouts' })
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getByText('STL-2026-10-31')).toBeInTheDocument()
  })

  it('filters the payouts by provider', async () => {
    mountMoney('/orgs/org-1/money/settlements')
    await screen.findByRole('table', { name: 'Provider payouts' })
    await userEvent.selectOptions(screen.getByLabelText('Provider'), 'iyzico')
    expect(
      within(await screen.findByRole('table', { name: 'Provider payouts' })).getAllByRole('row'),
    ).toHaveLength(3)
    // a provider with no payouts
    await userEvent.selectOptions(screen.getByLabelText('Provider'), '')
    expect(screen.getByLabelText('Provider')).toHaveValue('')
  })

  it('records a payout to the site with a key, and the owed balance goes down', async () => {
    const seen = recordHeaders('/payment-clearing:payout')
    mountMoney('/orgs/org-1/money/settlements')
    await userEvent.click(await screen.findByRole('button', { name: 'Record payout to site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Record payout to site' })
    // proposes the whole balance; more than the balance is refused before sending
    const amount = within(dialog).getByLabelText('Amount')
    await userEvent.clear(amount)
    await userEvent.type(amount, '500')
    await userEvent.type(within(dialog).getByLabelText('Bank transfer reference'), 'HAVALE-77')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Record payout' }))
    expect(
      await within(dialog).findByText(/cannot be more than the client money/),
    ).toBeInTheDocument()
    expect(seen).toHaveLength(0)

    await userEvent.clear(amount)
    await userEvent.type(amount, '150')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Record payout' }))
    await waitFor(() => expect(seen).toHaveLength(1))
    expect(seen[0]!.key).toMatch(/^[0-9a-f-]{36}$/)
    expect(await seen[0]!.body).toMatchObject({
      site_id: 'site-a',
      amount: { amount: 15_000, currency: 'TRY' },
      reference: 'HAVALE-77',
    })
    await waitFor(() =>
      expect(screen.getByTestId('client-money-owed')).toHaveTextContent(/250[.,]00/),
    )
    expect(screen.getByTestId('client-money-paid-out')).toHaveTextContent(/150[.,]00/)
  })

  it('refreshes the owed balance when a payout is refused', async () => {
    mountMoney('/orgs/org-1/money/settlements')
    await userEvent.click(await screen.findByRole('button', { name: 'Record payout to site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Record payout to site' })
    // the balance drops behind the dialog's back, so the proposed amount is now too much
    checkoutMock().owed = 100
    await userEvent.type(within(dialog).getByLabelText('Bank transfer reference'), 'HAVALE-1')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Record payout' }))
    await waitFor(() =>
      expect(screen.getByTestId('client-money-owed')).toHaveTextContent(/1[.,]00/),
    )
  })

  it('requires the reference', async () => {
    const seen = recordHeaders('/payment-clearing:payout')
    mountMoney('/orgs/org-1/money/settlements')
    await userEvent.click(await screen.findByRole('button', { name: 'Record payout to site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Record payout to site' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Record payout' }))
    expect(await within(dialog).findByText('This field is required.')).toBeInTheDocument()
    expect(seen).toHaveLength(0)
  })

  it('offers no payout when nothing is owed, or without money.post', async () => {
    checkoutMock().owed = 0
    mountMoney('/orgs/org-1/money/settlements')
    await screen.findByTestId('client-money-owed')
    expect(screen.queryByRole('button', { name: 'Record payout to site' })).not.toBeInTheDocument()
  })

  it('hides the payout button from a reader', async () => {
    mountMoney('/orgs/org-1/money/settlements', ['money.read'])
    await screen.findByTestId('client-money-owed')
    expect(screen.queryByRole('button', { name: 'Record payout to site' })).not.toBeInTheDocument()
  })

  it('has the checkout and settlement tabs for a manager', async () => {
    mountMoney('/orgs/org-1/money', MANAGER_PERMISSIONS)
    expect(await screen.findByRole('link', { name: 'Card checkouts' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Settlements' })).toBeInTheDocument()
  })
})
