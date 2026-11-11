import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mountMoney, pick, pickerValue } from '@/test/money-utils'

async function fillPayment(unit: string, amount: string) {
  await pick(await screen.findByLabelText('Unit'), unit)
  await userEvent.type(screen.getByLabelText('Amount'), amount)
}

describe('record a payment', () => {
  it('shows the automatic allocation (oldest first) and the gap-free receipt number', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments/new')
    await screen.findByRole('heading', { name: 'Record a payment' })
    await waitFor(() => expect(pickerValue('Received into')).not.toBe(''))

    await fillPayment('A3', '1000')
    const table = await screen.findByRole(
      'table',
      { name: 'Automatic allocation' },
      { timeout: 4000 },
    )
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(2) // August in full (900), September partly (100)
    expect(rows[0]).toHaveTextContent(/900/)
    expect(rows[1]).toHaveTextContent(/100/)

    await userEvent.click(screen.getByRole('button', { name: 'Record payment' }))
    expect(await screen.findByTestId('receipt-no')).toHaveTextContent('2026/000001')

    // the payment reduced the charges it was allocated to
    const a3 = db.charges
      .filter((c) => c.unit_door_number === 'A3' && c.debtor_kind === 'owner')
      .sort((a, b) => a.due_date.localeCompare(b.due_date))
    expect(a3[0]!.status).toBe('paid')
    expect(a3[1]!.paid.amount).toBe(10_000)

    // a second payment gets the next number without a gap
    await userEvent.click(screen.getByRole('button', { name: 'Record another' }))
    await fillPayment('B2', '500')
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Record payment' })).toBeEnabled(),
    )
    await screen.findByRole('table', { name: 'Automatic allocation' }, { timeout: 4000 })
    await userEvent.click(screen.getByRole('button', { name: 'Record payment' }))
    await waitFor(() => expect(screen.getByTestId('receipt-no')).toHaveTextContent('2026/000002'))
  })

  it('lets the manager pick the charge to pay by hand and rejects over-allocation', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments/new')
    await screen.findByRole('heading', { name: 'Record a payment' })
    await waitFor(() => expect(pickerValue('Received into')).not.toBe(''))
    await fillPayment('A3', '900')
    await screen.findByRole('table', { name: 'Automatic allocation' }, { timeout: 4000 })
    await userEvent.click(screen.getByRole('button', { name: 'Choose charges myself' }))

    const manual = await screen.findByRole('table', { name: 'Manual allocation' })
    const inputs = within(manual).getAllByRole('textbox')
    // move the whole amount from the oldest charge to the newest open one
    await userEvent.clear(inputs[0]!)
    await userEvent.clear(inputs[inputs.length - 1]!)
    await userEvent.type(inputs[inputs.length - 1]!, '900')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    await userEvent.type(inputs[0]!, '50')
    expect(await screen.findByText(/more than the payment/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Record payment' })).toBeDisabled()
    await userEvent.clear(inputs[0]!)

    await userEvent.click(screen.getByRole('button', { name: 'Record payment' }))
    await screen.findByTestId('receipt-no')
    const newest = db.charges
      .filter((c) => c.unit_door_number === 'A3' && c.debtor_kind === 'owner')
      .sort((a, b) => b.due_date.localeCompare(a.due_date))[0]!
    expect(newest.status).toBe('paid')
  })

  it('records a payment against a person (carried debt), not a unit side', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments/new')
    await screen.findByRole('heading', { name: 'Record a payment' })
    await waitFor(() => expect(pickerValue('Received into')).not.toBe(''))
    await userEvent.selectOptions(screen.getByLabelText('For'), 'person')
    const person = await screen.findByLabelText('Person')
    await pick(person, 'Deniz Çelik')
    await userEvent.type(screen.getByLabelText('Amount'), '900')
    await screen.findByRole(
      'table',
      { name: 'Automatic allocation' },
      { timeout: 4000 },
    )
    await userEvent.click(screen.getByRole('button', { name: 'Record payment' }))
    await screen.findByTestId('receipt-no')
    expect(db.payments.at(-1)!.allocations).toHaveLength(1)
    expect(
      db.charges.filter((c) => c.debtor_person_id === 'p-deniz' && c.status === 'paid'),
    ).toHaveLength(1)
  })

  it('confirms a transfer declared from the portal', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments')
    const section = await screen.findByText('Declared by residents, awaiting confirmation')
    expect(section).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm payment' }))
    await waitFor(() =>
      expect(db.payments.find((p) => p.id === 'pay-declared-1')?.status).toBe('confirmed'),
    )
    expect(db.payments.find((p) => p.id === 'pay-declared-1')?.receipt_no).toMatch(/^2026\//)
  })
})
