import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { seedBankImport } from '@/test/handlers/money-db'
import { mountMoney } from '@/test/money-utils'

describe('bank statement matching', () => {
  it('moves with j/k, confirms the suggestion with Enter and ignores with i', async () => {
    const { db } = mountMoney('/orgs/org-1/money/bank/imp-1')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))

    const grid = await screen.findByRole('grid', { name: 'Bank lines' })
    await waitFor(() => expect(within(grid).getAllByRole('row').length).toBeGreaterThan(4))
    const rows = () => within(grid).getAllByRole('row').slice(1)
    expect(rows()[0]).toHaveAttribute('aria-selected', 'true')

    grid.focus()
    await userEvent.keyboard('j')
    expect(rows()[1]).toHaveAttribute('aria-selected', 'true')
    await userEvent.keyboard('k')
    expect(rows()[0]).toHaveAttribute('aria-selected', 'true')

    // Enter confirms the suggested unit of the selected line: a payment appears, the line is done
    const before = db.payments.length
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(db.payments.length).toBe(before + 1))
    const matched = db.bankTx.filter((t) => t.match_status === 'matched')
    expect(matched).toHaveLength(1)
    expect(matched[0]!.payment_id).toBeTruthy()
    await waitFor(() => expect(rows().length).toBe(4)) // matched lines leave the work list

    // i ignores the (now first) line
    await userEvent.keyboard('i')
    await waitFor(() =>
      expect(db.bankTx.filter((t) => t.match_status === 'ignored')).toHaveLength(1),
    )
  })

  it('needs a manual match for a line without a suggestion', async () => {
    const { db } = mountMoney('/orgs/org-1/money/bank/imp-1')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))
    const grid = await screen.findByRole('grid', { name: 'Bank lines' })
    await waitFor(() => expect(within(grid).getAllByRole('row').length).toBe(6))
    // the fourth line has no suggestion: Enter opens the manual panel instead of guessing
    const row = within(grid).getAllByRole('row')[4]!
    await userEvent.click(row)
    grid.focus()
    await userEvent.keyboard('{Enter}')
    const panel = await screen.findByText('Match manually', {
      selector: 'div,h3,h2,[class*=title]',
    })
    expect(panel).toBeInTheDocument()
    expect(db.bankTx.filter((t) => t.match_status === 'matched')).toHaveLength(0)
  })

  it('shows statement and ledger balances on the import header', async () => {
    const { db } = mountMoney('/orgs/org-1/money/bank/imp-1')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))
    const recon = await screen.findByLabelText('Reconciliation balances')
    expect(within(recon).getByText('Statement opening')).toBeInTheDocument()
    expect(within(recon).getByText('Ledger closing')).toBeInTheDocument()
    expect(within(recon).getByTestId('recon-diff')).toBeInTheDocument()
  })
})
