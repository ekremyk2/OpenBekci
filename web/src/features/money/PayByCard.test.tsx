import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { checkoutMock, addCheckout } from '@/test/handlers/checkout'
import { nextId } from '@/test/handlers/money-db'
import { mountMoney } from '@/test/money-utils'
import { server } from '@/test/setup'
import { isSafeCheckoutUrl, navigation } from './PayByCard'

const RESIDENT = ['statements.read_own', 'payments.declare', 'payments.checkout']

/** Bodies and keys of every checkout creation. */
function recordCheckouts() {
  const seen: { key: string | null; body: Promise<Record<string, unknown>> }[] = []
  server.events.on('request:start', ({ request }) => {
    if (request.method === 'POST' && new URL(request.url).pathname.endsWith('/checkouts'))
      seen.push({
        key: request.headers.get('Idempotency-Key'),
        body: request.clone().json() as Promise<Record<string, unknown>>,
      })
  })
  return seen
}

/** The signed-in resident owns A1, whose dues go to the tenant: give the owner side two open charges. */
function owe(db: ReturnType<typeof mountMoney>['db']) {
  const template = db.charges.find((c) => c.unit_door_number === 'A1')!
  for (const [period, due] of [
    ['2026-10', '2026-10-05'],
    ['2026-11', '2026-11-05'],
  ] as const)
    db.charges.push({
      ...template,
      id: nextId(db, 'ch'),
      debtor_kind: 'owner',
      period,
      due_date: due,
      description: `Reserve fund ${period}`,
      amount: { amount: 20_000, currency: 'TRY' },
      paid: { amount: 0, currency: 'TRY' },
      balance: { amount: 20_000, currency: 'TRY' },
      status: 'open',
    })
}

afterEach(() => {
  vi.restoreAllMocks()
  server.events.removeAllListeners()
})

describe('Pay by card (resident)', () => {
  it('is offered only when the site reports card payment as available', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/sites/:siteId/settings', () =>
        HttpResponse.json({
          version: 1,
          payments: {
            card: { available: false, provider: null },
            merchant_of_record: 'site',
            merchant_of_record_override: null,
          },
        }),
      ),
    )
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await screen.findByRole('heading', { name: 'Pay' })
    await screen.findByText('Bank details')
    await new Promise((r) => setTimeout(r, 150))
    expect(screen.queryByRole('button', { name: 'Pay by card' })).not.toBeInTheDocument()
  })

  it('is not offered to a resident who may not start a checkout', async () => {
    mountMoney('/orgs/org-1/money/pay', ['statements.read_own', 'payments.declare'])
    await screen.findByText('Bank details')
    await new Promise((r) => setTimeout(r, 150))
    expect(screen.queryByRole('button', { name: 'Pay by card' })).not.toBeInTheDocument()
  })

  it('pays chosen charges: key, body and a full navigation to the provider', async () => {
    const go = vi.spyOn(navigation, 'go').mockImplementation(() => {})
    const seen = recordCheckouts()
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Choose charges' }))
    const list = await within(dialog).findByRole('list', { name: 'Open charges' })
    const boxes = within(list).getAllByRole('checkbox')
    expect(boxes.length).toBeGreaterThan(1)
    // nothing chosen yet: no way to continue
    expect(within(dialog).getByRole('button', { name: 'Continue to payment' })).toBeDisabled()
    await userEvent.click(boxes[0]!)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))

    await waitFor(() => expect(go).toHaveBeenCalledTimes(1))
    expect(go.mock.calls[0]![0]).toMatch(/^https:\/\/pay\.example\.test\/checkout\/co-/)
    expect(seen).toHaveLength(1)
    expect(seen[0]!.key).toMatch(/^[0-9a-f-]{36}$/)
    const body = await seen[0]!.body
    expect(body.client).toBe('web')
    expect(body.debtor_kind).toBe('owner')
    expect(body.charge_ids).toHaveLength(1)
    expect(body.amount).toBeUndefined()
    expect(checkoutMock().checkouts.some((c) => c.status === 'pending')).toBe(true)
  })

  it('pays everything that is open without sending charges or an amount', async () => {
    const go = vi.spyOn(navigation, 'go').mockImplementation(() => {})
    const seen = recordCheckouts()
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    expect(within(dialog).getByRole('radio', { name: 'Everything that is open' })).toBeChecked()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    await waitFor(() => expect(go).toHaveBeenCalled())
    const body = await seen[0]!.body
    expect(body.charge_ids).toBeUndefined()
    expect(body.amount).toBeUndefined()
  })

  it('pays an amount typed in', async () => {
    const go = vi.spyOn(navigation, 'go').mockImplementation(() => {})
    const seen = recordCheckouts()
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    await userEvent.click(within(dialog).getByRole('radio', { name: 'An amount' }))
    expect(within(dialog).getByRole('button', { name: 'Continue to payment' })).toBeDisabled()
    await userEvent.type(within(dialog).getByLabelText('Amount'), '250')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    await waitFor(() => expect(go).toHaveBeenCalled())
    const body = await seen[0]!.body
    expect(body.amount).toEqual({ amount: 25_000, currency: 'TRY' })
  })

  it('reuses the key on a retry of the same request and says why the first try failed', async () => {
    const go = vi.spyOn(navigation, 'go').mockImplementation(() => {})
    const seen = recordCheckouts()
    checkoutMock().providerAvailable = false
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    expect(
      await within(dialog).findByText('Card payment is not available for this site right now.'),
    ).toBeInTheDocument()
    expect(go).not.toHaveBeenCalled()

    checkoutMock().providerAvailable = true
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    await waitFor(() => expect(go).toHaveBeenCalled())
    expect(seen).toHaveLength(2)
    expect(seen[1]!.key).toBe(seen[0]!.key)
  })

  it('takes a new key when the choice changes', async () => {
    vi.spyOn(navigation, 'go').mockImplementation(() => {})
    const seen = recordCheckouts()
    checkoutMock().providerAvailable = false
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    await within(dialog).findByText('Card payment is not available for this site right now.')
    await userEvent.click(within(dialog).getByRole('radio', { name: 'An amount' }))
    await userEvent.type(within(dialog).getByLabelText('Amount'), '10')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    await waitFor(() => expect(seen).toHaveLength(2))
    expect(seen[1]!.key).not.toBe(seen[0]!.key)
  })

  it('never follows a checkout url that is not a web address', async () => {
    const go = vi.spyOn(navigation, 'go').mockImplementation(() => {})
    server.use(
      http.post('*/v1/orgs/:orgId/checkouts', () =>
        HttpResponse.json(
          { ...addCheckout(), checkout_url: 'javascript:alert(1)' },
          { status: 201 },
        ),
      ),
    )
    owe(mountMoney('/orgs/org-1/money/pay', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/payment provider could not/)
    expect(go).not.toHaveBeenCalled()
  })

  it('is also on the balance card, fixed to that side', async () => {
    const go = vi.spyOn(navigation, 'go').mockImplementation(() => {})
    owe(mountMoney('/orgs/org-1/money', RESIDENT).db)
    await userEvent.click(await screen.findByRole('button', { name: 'Pay by card' }))
    const dialog = await screen.findByRole('dialog', { name: 'Pay by card' })
    expect(within(dialog).queryByLabelText('Paying for')).not.toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Continue to payment' }))
    await waitFor(() => expect(go).toHaveBeenCalled())
  })
})

describe('isSafeCheckoutUrl', () => {
  it('accepts web addresses only', () => {
    expect(isSafeCheckoutUrl('https://pay.example.test/c/1')).toBe(true)
    expect(isSafeCheckoutUrl('http://localhost:3000/c')).toBe(true)
    for (const bad of ['javascript:alert(1)', 'data:text/html,x', 'not a url', ''])
      expect(isSafeCheckoutUrl(bad)).toBe(false)
  })
})
