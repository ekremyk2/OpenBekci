import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountMoney, recordKeys } from '@/test/money-utils'

afterEach(() => server.events.removeAllListeners())

const problem = (status: number, code: string) =>
  HttpResponse.json(
    { status, code },
    { status, headers: { 'content-type': 'application/problem+json' } },
  )

describe('new dues plan', () => {
  it('shows field errors, then a live preview of what every unit side pays', async () => {
    mountMoney('/orgs/org-1/money/plans/new')
    await screen.findByRole('heading', { name: 'New dues plan' })

    await userEvent.click(screen.getByRole('button', { name: 'Save plan' }))
    expect(await screen.findByText('This field is required.')).toBeInTheDocument()
    expect(screen.getByText('Enter an amount greater than zero.')).toBeInTheDocument()

    await userEvent.type(screen.getByLabelText('Name'), 'Monthly dues 2027')
    await userEvent.type(screen.getByLabelText('Amount per period'), '6000')

    const table = await screen.findByRole(
      'table',
      { name: 'What each unit side would pay' },
      { timeout: 4000 },
    )
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(7) // header + six units
    // dues are the tenant's where a tenant lives (A1, A2), the owner's elsewhere; share-weighted split
    const a1 = within(table).getByRole('row', { name: /A1/ })
    expect(within(a1).getByText('Tenant')).toBeInTheDocument()
    expect(within(a1).getByText(/600/)).toBeInTheDocument()
    expect(within(table.closest('div')!.parentElement!).getByText(/6 units/)).toBeInTheDocument()
  })

  it('retries a failed save with the same Idempotency-Key and then opens the plan', async () => {
    const seen = recordKeys()
    server.use(
      http.post('*/v1/orgs/:orgId/charge-plans', () => problem(503, 'unavailable'), { once: true }),
    )
    const { db } = mountMoney('/orgs/org-1/money/plans/new')
    await screen.findByRole('heading', { name: 'New dues plan' })
    await userEvent.type(screen.getByLabelText('Name'), 'Elevator fund')
    await userEvent.type(screen.getByLabelText('Amount per period'), '1200')
    await waitFor(() =>
      expect((screen.getByLabelText('Split rule') as HTMLSelectElement).value).not.toBe(''),
    )

    await userEvent.click(screen.getByRole('button', { name: 'Save plan' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/could not save|unavailable/i)
    await userEvent.click(screen.getByRole('button', { name: 'Save plan' }))

    await screen.findByRole('heading', { name: 'Elevator fund' })
    const posts = seen.filter((r) => r.method === 'POST' && r.url.endsWith('/charge-plans'))
    expect(posts).toHaveLength(2)
    expect(posts[0]!.key).toBeTruthy()
    expect(posts[1]!.key).toBe(posts[0]!.key)
    expect(db.plans.some((p) => p.name === 'Elevator fund')).toBe(true)
  })
})

describe('plan rates', () => {
  it('adds an effective-dated rate and previews the per-unit amounts', async () => {
    const { db } = mountMoney('/orgs/org-1/money/plans/plan-dues')
    await screen.findByRole('heading', { name: 'Monthly dues' })
    await screen.findByRole('table', { name: 'Rate history' })

    await userEvent.clear(screen.getByLabelText('Starts on'))
    await userEvent.type(screen.getByLabelText('Starts on'), '2027-01-01')
    await userEvent.type(screen.getByLabelText('New amount'), '7000')
    await screen.findAllByRole(
      'table',
      { name: 'What each unit side would pay' },
      { timeout: 4000 },
    )
    await userEvent.type(
      screen.getByLabelText('Decision reference (optional)', { selector: '#rate-decision' }),
      'GK-2026/12',
    )
    await userEvent.click(screen.getByRole('button', { name: 'Add rate' }))

    await waitFor(() => expect(db.rates.filter((r) => r.plan_id === 'plan-dues')).toHaveLength(2))
    const history = await screen.findByRole('table', { name: 'Rate history' })
    await waitFor(() => expect(within(history).getAllByRole('row')).toHaveLength(3))
    expect(within(history).getByText('GK-2026/12')).toBeInTheDocument()
  })
})
