import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { seedBankImport } from '@/test/handlers/money-db'
import { mountMoney } from '@/test/money-utils'
import { server } from '@/test/setup'

describe('bank matching: paging and keyboard safety', () => {
  it('pages long statements with a cursor and never asks for more than 200 lines', async () => {
    const { db } = mountMoney('/orgs/org-1/money/bank/imp-1')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))
    const base = db.bankTx.find((t) => t.id === 'imp-1-tx1')!
    for (let i = 0; i < 450; i++)
      db.bankTx.push({
        ...base,
        id: `imp-1-bulk${i}`,
        match_status: 'unmatched',
        suggestions: [],
        description: `bulk ${i}`,
      })
    const limits: string[] = []
    server.events.on('request:start', ({ request }) => {
      const u = new URL(request.url)
      if (u.pathname.endsWith('/bank-transactions')) limits.push(u.searchParams.get('limit') ?? '')
    })
    const grid = await screen.findByRole('grid', { name: 'Bank lines' })
    // header row + 200 lines
    await waitFor(() => expect(within(grid).getAllByRole('row')).toHaveLength(201))
    await userEvent.click(screen.getByRole('button', { name: 'Load more lines' }))
    await waitFor(() => expect(within(grid).getAllByRole('row')).toHaveLength(401))
    await userEvent.click(screen.getByRole('button', { name: 'Load more lines' }))
    // 450 bulk + 5 seeded lines, header row, minus the matched/ignored ones that are hidden
    await waitFor(() => expect(within(grid).getAllByRole('row').length).toBeGreaterThan(450))
    expect(screen.queryByRole('button', { name: 'Load more lines' })).toBeNull()
    expect(limits.length).toBeGreaterThanOrEqual(3)
    expect(limits.every((l) => Number(l) >= 1 && Number(l) <= 200)).toBe(true)
  })

  it('does nothing when keys are pressed on a focused row button', async () => {
    const { db } = mountMoney('/orgs/org-1/money/bank/imp-1')
    db.imports.push(seedBankImport(db, 'imp-1', 'november.csv'))
    const grid = await screen.findByRole('grid', { name: 'Bank lines' })
    await waitFor(() => expect(within(grid).getAllByRole('row').length).toBe(6))
    const second = within(grid).getAllByRole('row')[2]!
    // the cursor stays on the first row; focus the "Match manually" button of the second one
    const manual = within(second).getByRole('button', { name: 'Match manually' })
    manual.focus()
    await userEvent.keyboard('{Enter}')
    expect(
      await screen.findByText('Match manually', { selector: 'div,h3,h2,[class*=title]' }),
    ).toBeVisible()
    expect(db.bankTx.filter((t) => t.match_status === 'matched')).toHaveLength(0)
    expect(db.payments.filter((p) => p.bank_transaction_id)).toHaveLength(0)
    // i, j and k from a focused button are ignored too
    within(second).getByRole('button', { name: 'Ignore' }).focus()
    await userEvent.keyboard('ijk')
    expect(db.bankTx.filter((t) => t.match_status === 'ignored')).toHaveLength(0)
  })

  it('the mock server rejects limits above the contract cap', async () => {
    mountMoney('/orgs/org-1/money')
    const res = await fetch('http://localhost/v1/orgs/org-1/bank-transactions?limit=500')
    expect(res.status).toBe(422)
    const ok = await fetch('http://localhost/v1/orgs/org-1/bank-transactions?limit=200')
    expect(ok.status).toBe(200)
  })
})
