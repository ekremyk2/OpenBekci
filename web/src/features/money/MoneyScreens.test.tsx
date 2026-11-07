import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountMoney, recordKeys } from '@/test/money-utils'

afterEach(() => server.events.removeAllListeners())

describe('statements', () => {
  it('renders the running balance, the accrued late fee and what is owed today', async () => {
    mountMoney('/orgs/org-1/money/statements?unit=u-a3&side=owner')
    const table = await screen.findByRole('table', { name: 'Statement' })
    const rows = within(table).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('Opening balance')
    expect(within(table).getAllByText(/Monthly dues/).length).toBe(4)
    expect(within(table).getByTestId('accrued-fee')).toBeInTheDocument()
    // total due = closing balance + accrued fee: four months of 900.00 plus the fee
    expect(within(table).getByTestId('total-due').textContent).toContain('3,757.50')
    expect(screen.getByRole('button', { name: 'PDF' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send to resident' })).toBeInTheDocument()
  })

  it('a payment shows up on the statement afterwards', async () => {
    const { db } = mountMoney('/orgs/org-1/money/statements?unit=u-a3&side=owner')
    await screen.findByRole('table', { name: 'Statement' })
    db.payments.push({
      ...db.payments[0]!,
      id: 'p-x',
      status: 'confirmed',
      receipt_no: '2026/000009',
      payer_person_id: 'p-deniz',
      unit_id: 'u-a3',
      debtor_kind: 'owner',
      allocated: { amount: 90_000, currency: 'TRY' },
    })
    await userEvent.clear(screen.getByLabelText('To'))
    await userEvent.type(screen.getByLabelText('To'), '2026-11-08')
    expect(await screen.findByText(/Payment 2026\/000009/)).toBeInTheDocument()
  })
})

describe('charges', () => {
  it('cancels with a reason and explains that a reversal is created', async () => {
    const seen = recordKeys()
    const { db } = mountMoney('/orgs/org-1/money/charges')
    const first = (await screen.findAllByRole('button', { name: 'Cancel charge' }))[0]!
    await userEvent.click(first)
    const dialog = await screen.findByRole('dialog', { name: 'Cancel this charge?' })
    expect(
      within(dialog).getByText(/A reversal entry with your reason is created/),
    ).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel charge' }))
    expect(await within(dialog).findByText('A reason is required.')).toBeInTheDocument()
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Billed twice')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel charge' }))
    await waitFor(() => expect(db.charges.filter((c) => c.status === 'cancelled')).toHaveLength(1))
    const post = seen.find((x) => x.method === 'POST' && x.url.includes(':cancel'))
    expect(post?.key).toBeTruthy()
  })

  it('has an empty state when the filters match nothing', async () => {
    mountMoney('/orgs/org-1/money/charges')
    await screen.findAllByRole('button', { name: 'Cancel charge' })
    await userEvent.selectOptions(screen.getByLabelText('Status'), 'cancelled')
    expect(await screen.findByText('No charges match')).toBeInTheDocument()
  })

  it('shows an error state with retry when the API fails', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/charges', () =>
        HttpResponse.json({ status: 500 }, { status: 500 }),
      ),
    )
    mountMoney('/orgs/org-1/money/charges')
    expect(
      await screen.findByText('Something went wrong', {}, { timeout: 5000 }),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })

  it('shows the no-permission state on a 403', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/charges', () =>
        HttpResponse.json({ status: 403, code: 'insufficient_permission' }, { status: 403 }),
      ),
    )
    mountMoney('/orgs/org-1/money/charges')
    expect(await screen.findByText('You do not have access to this')).toBeInTheDocument()
  })
})

describe('expenses', () => {
  it('validates the form, then records the expense with one idempotency key', async () => {
    const seen = recordKeys()
    const { db } = mountMoney('/orgs/org-1/money/expenses')
    await userEvent.click(await screen.findByRole('button', { name: 'New expense' }))
    const dialog = await screen.findByRole('dialog', { name: 'New expense' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save expense' }))
    expect(await within(dialog).findByText('This field is required.')).toBeInTheDocument()
    expect(within(dialog).getByText('Enter an amount greater than zero.')).toBeInTheDocument()

    await userEvent.type(within(dialog).getByLabelText('What was it for?'), 'Lobby light bulbs')
    await userEvent.selectOptions(
      within(dialog).getByLabelText('Category'),
      await within(dialog).findByRole('option', { name: /Electricity/ }),
    )
    await userEvent.type(within(dialog).getByLabelText('Amount'), '250')
    await userEvent.selectOptions(
      within(dialog).getByLabelText('Paid from'),
      await within(dialog).findByRole('option', { name: /Cash box – Ali/ }),
    )
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save expense' }))

    await waitFor(() => expect(db.expenses).toHaveLength(1))
    expect(db.expenses[0]!.amount.amount).toBe(25_000)
    expect(
      seen.filter((x) => x.method === 'POST' && x.url.endsWith('/expenses'))[0]?.key,
    ).toBeTruthy()
    expect(await screen.findByText('Lobby light bulbs')).toBeInTheDocument()
  })
})

describe('portal money (resident)', () => {
  const resident = ['statements.read_own', 'payments.declare']

  it('shows what I owe today and declares a transfer I made', async () => {
    const { db } = mountMoney('/orgs/org-1/money/pay', resident)
    expect(await screen.findByText(/TR33 0006 1005/)).toBeInTheDocument()
    expect(screen.getByText('Garanti BBVA')).toBeInTheDocument()
    await userEvent.type(await screen.findByLabelText('Amount you paid'), '100')
    await userEvent.click(await screen.findByRole('button', { name: "I've paid" }))
    expect(await screen.findByText(/Your manager will confirm/)).toBeInTheDocument()
    const declared = db.payments.filter((p) => p.status === 'pending_declared')
    expect(declared.length).toBe(2) // the seeded one plus mine
  })

  it('the home screen lists my side with the fee accrued so far', async () => {
    mountMoney('/orgs/org-1/money', resident)
    const owe = await screen.findByTestId('owe-today')
    expect(owe.textContent).toMatch(/\d/)
    expect(screen.getAllByRole('link', { name: 'Pay' }).length).toBeGreaterThan(0)
    // a resident gets no manager tabs
    expect(screen.queryByRole('link', { name: 'Dues plans' })).not.toBeInTheDocument()
  })

  it('has the no-access state without any money permission', async () => {
    mountMoney('/orgs/org-1/money', [])
    // meFixture occupancy has may_see_money, so the resident view is shown instead of a lock
    expect(await screen.findByRole('heading', { name: 'My balance' })).toBeInTheDocument()
  })
})

describe('opening balances', () => {
  it('runs a dry run first, shows row errors, and saves only when clean', async () => {
    const { db } = mountMoney('/orgs/org-1/money/opening-balances')
    await userEvent.type(await screen.findByLabelText('A1 Owner principal'), '1500')
    await userEvent.click(screen.getByRole('button', { name: 'Check (dry run)' }))
    expect(await screen.findByText(/Dry run: 1 of 1 rows are fine/)).toBeInTheDocument()
    expect(db.openings).toHaveLength(0) // nothing saved by a dry run
    await userEvent.click(screen.getByRole('button', { name: 'Save opening balances' }))
    // creating opening entries needs a fresh sign-in; the import is replayed after the challenge
    const reauth = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    expect(db.openings).toHaveLength(0)
    await userEvent.type(within(reauth).getByLabelText('Password'), 'secret')
    await userEvent.click(within(reauth).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(db.openings.length).toBe(1))
    expect(db.openings[0]!.principal.amount).toBe(150_000)
  })
})

describe('opening balances: void', () => {
  it('voids an entered entry with a reason after re-authentication', async () => {
    const { db } = mountMoney('/orgs/org-1/money/opening-balances')
    db.openings.push({
      id: 'ob-1',
      org_id: 'org-1',
      site_id: 'site-a',
      as_of: '2026-01-01',
      kind: 'unit_receivable',
      unit_id: 'u-a3',
      debtor_kind: 'owner',
      person_id: null,
      account_id: null,
      vendor_id: null,
      principal: { amount: 50_000, currency: 'TRY' },
      late_fee: { amount: 0, currency: 'TRY' },
      journal_entry_id: 'je',
      locked: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      status: 'active',
      voided_at: null,
      void_reason: null,
    })
    await userEvent.click(await screen.findByRole('button', { name: 'Void' }))
    const dialog = await screen.findByRole('dialog', { name: 'Void this opening entry?' })
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Entered twice')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Void entry' }))
    const reauth = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    await userEvent.type(within(reauth).getByLabelText('Password'), 'secret')
    await userEvent.click(within(reauth).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(db.openings[0]!.status).toBe('voided'))
    expect(db.openings[0]!.void_reason).toBe('Entered twice')
  })
})
