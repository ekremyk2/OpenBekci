import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import type { Me } from '@/api/schema'
import { moneyAccess } from '@/lib/money-access'
import { meFixture } from '@/test/handlers'
import { MANAGER_PERMISSIONS, mountMoney, pick, pickerValue, recordKeys } from '@/test/money-utils'
import { server } from '@/test/setup'
import { dryRunTotals } from './OpeningBalancesPage'
import bundle from '../../../../api/openapi-draft.yaml?raw'
import generated from '../../api/idempotent.gen.ts?raw'
import { extractIdempotent, render } from '../../../scripts/gen-idempotent.mjs'

afterEach(() => server.events.removeAllListeners())

const withHeaders = () => {
  const seen: { method: string; url: string; headers: Headers }[] = []
  server.events.on('request:start', ({ request }) =>
    seen.push({ method: request.method, url: request.url, headers: request.headers }),
  )
  return seen
}

describe('cash collector', () => {
  it('defaults cash to the collector’s own box, not the first box of the site', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments/new')
    db.cashBoxes.unshift({
      ...db.cashBoxes[0]!,
      id: 'cb-0',
      name: 'Veli box',
      account_id: 'a-cash-2',
      collector_person_id: 'p-mehmet',
    })
    // first screen of the file: the lazy route chunk is transformed cold
    await screen.findByLabelText('Received into', {}, { timeout: 12_000 })
    await waitFor(() => expect(pickerValue('Received into')).toBe('100.01 Cash box – Ali'))
  })

  it('a transfer defaults to a bank account and cash goes back to the box', async () => {
    mountMoney('/orgs/org-1/money/payments/new')
    await screen.findByLabelText('Received into')
    await waitFor(() => expect(pickerValue('Received into')).toBe('100.01 Cash box – Ali'))
    await userEvent.selectOptions(screen.getByLabelText('Method'), 'bank_transfer')
    await waitFor(() => expect(pickerValue('Received into')).toBe('102.01 Garanti current account'))
    await userEvent.selectOptions(screen.getByLabelText('Method'), 'cash')
    await waitFor(() => expect(pickerValue('Received into')).toBe('100.01 Cash box – Ali'))
  })

  it('a staff collector without money.read lands on a minimal Record cash page', async () => {
    const { db } = mountMoney('/orgs/org-1/money', ['payments.record'])
    // a kapici: staff with a cash box, not an owner or tenant of any unit
    server.use(
      http.get('*/v1/me', () =>
        HttpResponse.json({
          ...meFixture,
          occupancies: [],
          organizations: [
            { ...meFixture.organizations[0]!, permissions: [{ permission: 'payments.record' }] },
          ],
        }),
      ),
    )
    expect(await screen.findByRole('heading', { name: 'Record cash' })).toBeInTheDocument()
    const nav = screen.getByRole('navigation', { name: 'Money sections' })
    expect(within(nav).getAllByRole('link')).toHaveLength(1)
    expect(screen.queryByLabelText('Method')).toBeNull()
    await waitFor(() => expect(pickerValue('Received into')).toBe('100.01 Cash box – Ali'))
    await pick(await screen.findByLabelText('Unit'), 'A3')
    await userEvent.type(screen.getByLabelText('Amount'), '100')
    await userEvent.click(screen.getByRole('button', { name: 'Record payment' }))
    await screen.findByTestId('receipt-no')
    const p = db.payments.at(-1)!
    expect(p.method).toBe('cash')
    expect(p.asset_account_id).toBe('a-cash-1')
  })

  it('the route is open to payments.record or money.read, not to residents', async () => {
    mountMoney('/orgs/org-1/money/payments/new', ['statements.read_own'])
    expect(await screen.findByText('You do not have access to this')).toBeInTheDocument()
  })
})

describe('who sees Money', () => {
  const base: Me = {
    ...meFixture,
    organizations: [{ ...meFixture.organizations[0]!, permissions: [], roles: [] }],
  }
  const perms = (...p: string[]): Me => ({
    ...base,
    organizations: [
      { ...base.organizations[0]!, permissions: p.map((permission) => ({ permission })) },
    ],
  })

  it('a household member without may_see_money gets no Money, even with statements.read_own', () => {
    const me: Me = {
      ...perms('statements.read_own'),
      occupancies: meFixture.occupancies.map((o) => ({
        ...o,
        kind: 'household',
        may_see_money: false,
      })),
    }
    expect(moneyAccess(me, 'org-1').any).toBe(false)
  })

  it('an owner with reports.aggregate reaches Reports; a collector reaches Record cash', async () => {
    expect(moneyAccess(perms('reports.aggregate'), 'org-1').transparency).toBe(true)
    expect(moneyAccess(perms('payments.record'), 'org-1')).toMatchObject({ collector: true })
    mountMoney('/orgs/org-1/money', ['reports.aggregate'])
    const nav = await screen.findByRole('navigation', { name: 'Money sections' })
    expect(within(nav).getByRole('link', { name: 'Reports' })).toBeInTheDocument()
  })
})

describe('versioned writes and previews', () => {
  it('sends If-Match with the entity version on a plan update', async () => {
    const seen = withHeaders()
    const { db } = mountMoney('/orgs/org-1/money/plans/plan-dues')
    const name = await screen.findByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Dues renamed')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() =>
      expect(db.plans.find((p) => p.id === 'plan-dues')?.name).toBe('Dues renamed'),
    )
    const patch = seen.find(
      (r) => r.method === 'PATCH' && r.url.includes('/charge-plans/plan-dues'),
    )
    expect(patch?.headers.get('If-Match')).toBe('"1"')
  })

  it('the preview lines add up to the plan total', async () => {
    mountMoney('/orgs/org-1/money/plans/new')
    await screen.findByRole('heading', { name: 'New dues plan' })
    await userEvent.type(screen.getByLabelText('Name'), 'Sum check')
    await userEvent.type(screen.getByLabelText('Amount per period'), '6000')
    const table = await screen.findByRole('table', { name: 'What each unit side would pay' })
    const minor = (txt: string) => Math.round(Number(txt.replace(/[^\d.]/g, '')) * 100)
    const rows = within(table).getAllByRole('row').slice(1)
    const sum = rows.reduce(
      (a, r) => a + minor(within(r).getAllByRole('cell').at(-1)!.textContent!),
      0,
    )
    expect(sum).toBe(600_000)
    expect(screen.queryByText(/do not add up/)).toBeNull()
  })
})

describe('idempotent expense saves', () => {
  it('reuses the expense and receipt keys when a save is retried', async () => {
    const seen = recordKeys()
    server.use(
      http.post(
        '*/v1/orgs/:orgId/expenses',
        () => HttpResponse.json({ status: 503 }, { status: 503 }),
        { once: true },
      ),
    )
    const { db } = mountMoney('/orgs/org-1/money/expenses')
    await userEvent.click(await screen.findByRole('button', { name: 'New expense' }))
    const dialog = await screen.findByRole('dialog', { name: 'New expense' })
    await userEvent.type(within(dialog).getByLabelText('What was it for?'), 'Cleaning')
    await pick(within(dialog).getByLabelText('Category'), /Cleaning/)
    await userEvent.type(within(dialog).getByLabelText('Amount'), '80')
    await pick(within(dialog).getByLabelText('Paid from'), /Cash box – Ali/)
    await userEvent.click(within(dialog).getByLabelText('Repeat every month'))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save expense' }))
    await within(dialog).findByRole('alert')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save expense' }))
    await waitFor(() => expect(db.recurring).toHaveLength(1))
    const keys = (suffix: string) =>
      seen.filter((r) => r.method === 'POST' && r.url.endsWith(suffix)).map((r) => r.key)
    const expenseKeys = keys('/expenses')
    expect(expenseKeys).toHaveLength(2)
    expect(expenseKeys[0]).toBe(expenseKeys[1])
    expect(db.expenses).toHaveLength(1) // the replay did not book it twice
  })
})

describe('pickers', () => {
  it('search the server and say when the list is cut off', async () => {
    const { db } = mountMoney('/orgs/org-1/money/payments/new')
    const first = db.units[0]!
    for (let i = 0; i < 250; i++)
      db.units.push({ ...first, id: `u-bulk-${i}`, door_number: `Z${String(i).padStart(3, '0')}` })
    const unit = await screen.findByLabelText('Unit')
    await userEvent.click(unit)
    expect(await screen.findByText(/Showing the first 200/)).toBeInTheDocument()
    await userEvent.type(unit, 'A3')
    await waitFor(() => expect(screen.queryByText(/Showing the first 200/)).toBeNull())
    const results = screen.getByRole('list', { name: 'Search results' })
    await userEvent.click(await within(results).findByText('A3'))
    expect(pickerValue('Unit')).toBe('A3')
  })
})

describe('re-authentication', () => {
  it('offers an SMS code, and cancelling shows no error', async () => {
    mountMoney('/orgs/org-1/money/opening-balances')
    await userEvent.type(await screen.findByLabelText('A1 Owner principal'), '10')
    await userEvent.click(screen.getByRole('button', { name: 'Check (dry run)' }))
    let dialog = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(screen.queryByText('The dry run failed.')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Check (dry run)' }))
    dialog = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Send me a code instead' }))
    await userEvent.type(within(dialog).getByLabelText('Code from the SMS'), '123456')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    expect(await screen.findByText(/Dry run: 1 of 1 rows are fine/)).toBeInTheDocument()
  })
})

describe('opening balances import', () => {
  it('keys units by block and door number, follows the job, and documents the layout', async () => {
    const { db } = mountMoney('/orgs/org-1/money/opening-balances')
    // door numbers repeat across blocks: the second A1 lives in block B
    db.units.push({ ...db.units[0]!, id: 'u-a1-b', block_id: 'blk-b' })
    const inputs = await screen.findAllByLabelText('A1 Owner principal')
    await waitFor(async () =>
      expect(await screen.findAllByLabelText('A1 Owner principal')).toHaveLength(2),
    )
    const both = screen.getAllByLabelText('A1 Owner principal')
    expect(inputs.length).toBeGreaterThan(0)
    await userEvent.type(both[0]!, '1234.50')
    await userEvent.type(both[1]!, '100')
    expect(screen.getByTestId('file-layout')).toHaveTextContent('door_number')
    await userEvent.click(screen.getByRole('button', { name: 'Check (dry run)' }))
    const reauth = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    await userEvent.type(within(reauth).getByLabelText('Password'), 'secret')
    await userEvent.click(within(reauth).getByRole('button', { name: 'Confirm' }))
    expect(await screen.findByText(/Dry run: 2 of 2 rows are fine/)).toBeInTheDocument()
    expect(screen.getByTestId('dry-run-totals')).toHaveTextContent(/1,334\.50/)
    await userEvent.click(screen.getByRole('button', { name: 'Save opening balances' }))
    await waitFor(() => expect(db.openings).toHaveLength(2))
    expect(new Set(db.openings.map((o) => o.unit_id))).toEqual(new Set(['u-a1', 'u-a1-b']))
    expect(
      await screen.findByText('The opening balances were imported.', {}, { timeout: 8000 }),
    ).toBeInTheDocument()
  }, 15_000)

  it('totals in integer minor units and rejects what it cannot read', () => {
    const rows = [
      { data: { kind: 'unit_receivable', principal: '1.234,50' } },
      { data: { kind: 'unit_receivable', principal: '0,10' } },
      { data: { kind: 'bank', principal: '5.000,00' } },
    ]
    expect(dryRunTotals(rows, 'TRY', 'tr')).toEqual({
      receivables: 123_460n,
      accounts: 500_000n,
      unreadable: 0,
    })
    expect(dryRunTotals([{ data: { principal: 'abc' } }], 'TRY', 'tr').unreadable).toBe(1)
    expect(dryRunTotals([{ data: {} }], 'TRY', 'en').unreadable).toBe(1)
  })
})

describe('generated idempotency list', () => {
  it('is in sync with the contract bundle', () => {
    const fresh = render(extractIdempotent(bundle))
    expect(generated.replace(/\r\n/g, '\n')).toBe(fresh)
  })
})

describe('permissions list', () => {
  it('keeps the manager set stable for the helpers above', () => {
    expect(MANAGER_PERMISSIONS).toContain('payments.record')
  })
})
