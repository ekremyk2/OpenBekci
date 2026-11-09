import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { checkoutMock } from '@/test/handlers/checkout'
import { db } from '@/test/handlers/db'
import { db as opsDb } from '@/test/handlers/operations'
import { mountApp } from '@/test/render'
import { server } from '@/test/setup'
import { AppError } from '@/api/errors'
import { blockedBalances } from './MerchantOfRecord'

/** No money on its way: nothing at the provider, no client money. */
function settled() {
  const m = checkoutMock()
  m.seeded = true
  m.clearing = { iyzico: 0 }
  m.owed = 0
}

const orgSetting = () => (opsDb.org as unknown as { settings: Record<string, unknown> }).settings

describe('merchant of record, organization default', () => {
  it('is the site by default and an administrator can see the choice', async () => {
    await mountApp('/orgs/org-1/settings')
    const select = await screen.findByLabelText('Collected by')
    expect(select).toHaveValue('site')
    expect(select).toBeEnabled()
  })

  it('explains the balances that block a change, per site and per kind of money', async () => {
    await mountApp('/orgs/org-1/settings')
    await userEvent.selectOptions(await screen.findByLabelText('Collected by'), 'organization')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' }).pop()!)

    const alert = (await screen.findByText('The merchant of record cannot change yet')).closest(
      '[role="alert"]',
    ) as HTMLElement
    const items = within(alert).getAllByTestId('blocked-balance')
    expect(items).toHaveLength(2)
    // the site name and the amounts are in plain words, not account numbers alone
    expect(items[0]).toHaveTextContent('Güneş Sitesi')
    expect(items[0]).toHaveTextContent(/provider has not paid out/)
    expect(items[0]).toHaveTextContent(/1[.,]250[.,]00/)
    expect(items[1]).toHaveTextContent(/holds .*400[.,]00.* client money/)
    expect(
      within(alert).getByRole('link', { name: /settlements and client money/ }),
    ).toHaveAttribute('href', '/orgs/org-1/money/settlements')
    // nothing changed: the select shows what is stored again
    expect(screen.getByLabelText('Collected by')).toHaveValue('site')
    expect(orgSetting()['payments.merchant_of_record']).toBeUndefined()
  })

  it('saves once nothing is on its way, and sites that follow it take the new value', async () => {
    settled()
    await mountApp('/orgs/org-1/settings')
    await userEvent.selectOptions(await screen.findByLabelText('Collected by'), 'organization')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' }).pop()!)
    await waitFor(() => expect(orgSetting()['payments.merchant_of_record']).toBe('organization'))
    expect(screen.queryByText('The merchant of record cannot change yet')).not.toBeInTheDocument()
    expect(db.settings['site-a']!.payments?.merchant_of_record).toBe('organization')
    await waitFor(() => expect(screen.getByLabelText('Collected by')).toHaveValue('organization'))
  })

  it('is read only for someone who may not manage the organization', async () => {
    await mountApp('/orgs/org-1/settings', { permissions: ['org.read'] })
    expect(await screen.findByLabelText('Collected by')).toBeDisabled()
    expect(screen.getAllByText('Only an organization administrator can change this.').length).toBe(
      1,
    )
  })

  it('shows a server refusal that is not about balances as a plain message', async () => {
    settled()
    server.use(
      http.patch('*/v1/orgs/:orgId', () =>
        HttpResponse.json(
          { status: 403, code: 'insufficient_permission' },
          { status: 403, headers: { 'content-type': 'application/problem+json' } },
        ),
      ),
    )
    await mountApp('/orgs/org-1/settings')
    await userEvent.selectOptions(await screen.findByLabelText('Collected by'), 'organization')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' }).pop()!)
    expect(await screen.findByText('You do not have permission to do this.')).toBeInTheDocument()
  })
})

describe('merchant of record, per-site override', () => {
  it('follows the organization by default and shows who collects', async () => {
    await mountApp('/orgs/org-1/sites/site-a?tab=settings')
    const select = await screen.findByLabelText('For this site')
    expect(select).toHaveValue('')
    expect(screen.getByText(/this site collects its own card payments/i)).toBeInTheDocument()
  })

  it('is blocked by balances with the same explanation', async () => {
    await mountApp('/orgs/org-1/sites/site-a?tab=settings')
    await userEvent.selectOptions(
      await screen.findByLabelText('For this site'),
      'The organization collects centrally',
    )
    const buttons = screen.getAllByRole('button', { name: 'Save' })
    await userEvent.click(buttons[buttons.length - 1]!)
    const alert = (await screen.findByText('The merchant of record cannot change yet')).closest(
      '[role="alert"]',
    ) as HTMLElement
    expect(within(alert).getAllByTestId('blocked-balance')).toHaveLength(2)
    expect(db.settings['site-a']!.payments?.merchant_of_record_override).toBeNull()
  })

  it('sets and clears the override once the money has settled', async () => {
    settled()
    await mountApp('/orgs/org-1/sites/site-a?tab=settings')
    const select = await screen.findByLabelText('For this site')
    await userEvent.selectOptions(select, 'The organization collects centrally')
    const buttons = screen.getAllByRole('button', { name: 'Save' })
    await userEvent.click(buttons[buttons.length - 1]!)
    await waitFor(() =>
      expect(db.settings['site-a']!.payments?.merchant_of_record_override).toBe('organization'),
    )
    expect(db.settings['site-a']!.payments?.merchant_of_record).toBe('organization')
    await waitFor(() =>
      expect(screen.getByText(/organization collects this site/i)).toBeInTheDocument(),
    )

    await userEvent.selectOptions(screen.getByLabelText('For this site'), 'Follow the organization')
    const again = screen.getAllByRole('button', { name: 'Save' })
    await userEvent.click(again[again.length - 1]!)
    await waitFor(() =>
      expect(db.settings['site-a']!.payments?.merchant_of_record_override).toBeNull(),
    )
    expect(db.settings['site-a']!.payments?.merchant_of_record).toBe('site')
  })

  it('is read only without org.manage (the site manager cannot move the money)', async () => {
    await mountApp('/orgs/org-1/sites/site-a?tab=settings', {
      permissions: ['sites.read', 'sites.manage'],
    })
    expect(await screen.findByLabelText('For this site')).toBeDisabled()
  })
})

describe('blockedBalances', () => {
  it('reads the balances out of the 409 field errors, and only out of that error', () => {
    const balances = [{ site_id: 's1', account: '1190', kind: 'clearing', balance: 5 }]
    const e = new AppError({
      status: 409,
      code: 'merchant_change_blocked',
      fieldErrors: {
        'settings.payments.merchant_of_record': [{ code: 'open_balance', params: { balances } }],
      },
    })
    expect(blockedBalances(e)).toEqual(balances)
    expect(blockedBalances(new AppError({ status: 409, code: 'conflict' }))).toBeNull()
    expect(blockedBalances(new Error('x'))).toBeNull()
    expect(blockedBalances(new AppError({ status: 409, code: 'merchant_change_blocked' }))).toEqual(
      [],
    )
  })
})
