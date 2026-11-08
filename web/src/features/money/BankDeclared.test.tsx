import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { seedBankImport } from '@/test/handlers/money-db'
import { mountMoney } from '@/test/money-utils'
import { recordKeys } from '@/test/money-utils'

function addDeclaredLine(db: ReturnType<typeof mountMoney>['db']) {
  const declared = db.payments.find((p) => p.id === 'pay-declared-1')!
  const base = db.bankTx.find((t) => t.id === 'imp-1-tx1')!
  db.bankTx.push({
    ...base,
    id: 'imp-1-decl',
    booked_on: '2026-11-05',
    amount: declared.gross,
    counterparty: declared.payer_display_name,
    description: 'EFT A3',
    match_status: 'suggested',
    payment_id: null,
    suggestions: [
      {
        unit_id: declared.unit_id!,
        unit_door_number: 'A3',
        debtor_kind: 'owner',
        person_id: declared.payer_person_id,
        score: 0.95,
        reason: 'Amount and reference match',
      },
    ],
  })
  return declared
}

describe('declared transfers and bank lines', () => {
  it('matching a line to the declared payment confirms it once and links both', async () => {
    const { db } = mountMoney('/orgs/org-1/money/bank/imp-1')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))
    const declared = addDeclaredLine(db)
    const seen = recordKeys()
    const before = db.payments.filter((p) => p.status === 'confirmed').length

    const grid = await screen.findByRole('grid', { name: 'Bank lines' })
    const row = await waitFor(() => {
      const r = within(grid)
        .getAllByRole('row')
        .find((x) => x.id === 'tx-imp-1-decl')
      expect(r).toBeTruthy()
      return r!
    })
    // the suggestion cell offers the declared payment, not just the unit
    await waitFor(() => expect(within(row).getByText('Declared transfer')).toBeInTheDocument())
    await userEvent.click(within(row).getByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(declared.status).toBe('confirmed'))
    // booked exactly once: one more confirmed payment (the declared one), no second booking
    expect(db.payments.filter((p) => p.status === 'confirmed')).toHaveLength(before + 1)
    expect(
      db.payments.filter((p) => p.gross.amount === 90_000 && p.status === 'confirmed'),
    ).toHaveLength(1)
    expect(declared.bank_transaction_id).toBe('imp-1-decl')
    const line = db.bankTx.find((t) => t.id === 'imp-1-decl')!
    expect(line.payment_id).toBe(declared.id)
    expect(line.match_status).toBe('matched')
    const post = seen.find((r) => r.url.includes('imp-1-decl') && r.url.includes(':match'))
    expect(post?.key).toBeTruthy()
  })

  it('shows the linked pair as reconciled on Payments and offers no second confirmation', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))
    const declared = addDeclaredLine(db)
    const line = db.bankTx.find((t) => t.id === 'imp-1-decl')!
    // confirm the declared payment with the line from the dialog
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }))
    const dialog = await screen.findByRole('dialog')
    const select = await within(dialog).findByLabelText('Bank line it arrived as')
    await userEvent.selectOptions(
      select,
      await within(select).findByRole('option', { name: /EFT A3/ }),
    )
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm payment' }))
    await waitFor(() => expect(declared.status).toBe('confirmed'))
    expect(declared.bank_transaction_id).toBe(line.id)
    expect(line.payment_id).toBe(declared.id)
    expect(line.match_status).toBe('matched')
    // the list marks the payment reconciled; the declared card is gone
    expect(await screen.findByText('Reconciled')).toBeInTheDocument()
    expect(screen.queryByText('Declared by residents, awaiting confirmation')).toBeNull()
    // the server refuses to confirm it again (no double booking)
    const res = await fetch(`http://localhost/v1/orgs/org-1/payments/${declared.id}:confirm`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'Idempotency-Key': 'k-second' },
      body: JSON.stringify({ asset_account_id: 'a-bank', bank_transaction_id: line.id }),
    })
    expect(res.status).toBe(409)
  })
})
