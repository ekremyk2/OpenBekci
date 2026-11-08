import { describe, expect, it } from 'vitest'
import { getMoneyDb } from '@/test/handlers/money'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/render'
import { db } from '@/test/handlers/db'
import { problem } from '@/test/handlers/http'

describe('site form', () => {
  it('validates required fields before sending anything', async () => {
    let posted = false
    server.use(http.post('*/v1/orgs/:orgId/sites', () => ((posted = true), HttpResponse.json({}))))
    await mountApp('/orgs/org-1/sites/new')
    await userEvent.click(await screen.findByRole('button', { name: 'Create site' }))
    expect(await screen.findByText('Enter the site name')).toBeInTheDocument()
    expect(screen.getByText('Enter the street address')).toBeInTheDocument()
    expect(posted).toBe(false)
  })

  it('maps the server field error onto the name field', async () => {
    await mountApp('/orgs/org-1/sites/new')
    await userEvent.type(await screen.findByLabelText('Site name'), 'Güneş Sitesi')
    await userEvent.type(screen.getByLabelText('Street address'), 'Atatürk Cd. 1')
    await userEvent.click(screen.getByRole('button', { name: 'Create site' }))
    const field = await screen.findByText('A site with this name already exists')
    expect(field).toHaveAttribute('role', 'alert')
    expect(screen.getByLabelText('Site name')).toHaveAttribute('aria-invalid', 'true')
  })

  it('reuses one Idempotency-Key when a failed submission is retried, and creates the site', async () => {
    const keys: (string | null)[] = []
    let calls = 0
    server.use(
      http.post('*/v1/orgs/:orgId/sites', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'))
        if (++calls === 1) return problem(500, 'internal_error')
        return HttpResponse.json(
          { ...db.sites[0]!, id: 'site-new', name: 'Yeni Site' },
          { status: 201 },
        )
      }),
    )
    await mountApp('/orgs/org-1/sites/new')
    await userEvent.type(await screen.findByLabelText('Site name'), 'Yeni Site')
    await userEvent.type(screen.getByLabelText('Street address'), 'Bağdat Cd. 5')
    const submit = screen.getByRole('button', { name: 'Create site' })
    await userEvent.click(submit)
    expect(await screen.findByText('Something went wrong. Please try again.')).toBeInTheDocument()
    await userEvent.click(submit)
    await waitFor(() => expect(calls).toBe(2))
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
  })
})

describe('site detail', () => {
  it('shows the bank IBAN masked and reveals it on request', async () => {
    await mountApp('/orgs/org-1/sites/site-a?tab=bank')
    const iban = await screen.findByTestId('iban')
    expect(iban).toHaveTextContent('TR33 •••• •••• •••• 1326')
    await userEvent.click(screen.getByRole('button', { name: 'Show' }))
    expect(screen.getByTestId('iban')).toHaveTextContent('TR33 0006 1005 1978 6457 8413 26')
  })

  it('hides the bank tab without sites.bank_details', async () => {
    await mountApp('/orgs/org-1/sites/site-a', { permissions: ['sites.read'] })
    await screen.findByRole('heading', { name: 'Güneş Sitesi' })
    expect(screen.queryByRole('tab', { name: 'Bank details' })).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Export and offboard' })).not.toBeInTheDocument()
  })

  it('moving the lock date back asks to re-authenticate, then replays the request with the same key', async () => {
    const keys: (string | null)[] = []
    server.use(
      http.put('*/v1/orgs/:orgId/sites/:siteId/lock-date', async ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'))
        if (Date.now() >= db.reauthUntil) return problem(403, 'reauth_required')
        return HttpResponse.json({
          site_id: 'site-a',
          lock_date: '2026-01-31',
          changed_by: null,
          changed_at: null,
        })
      }),
    )
    await mountApp('/orgs/org-1/sites/site-a?tab=lock', {
      seed: () => {
        getMoneyDb().lockDate = '2026-02-28' // the lock date lives in the money model
      },
    })
    const input = await screen.findByLabelText('Lock everything up to and including')
    await userEvent.clear(input)
    await userEvent.type(input, '2026-01-31')
    await userEvent.click(screen.getByRole('button', { name: 'Set lock date' }))

    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Password'), 'correct-password')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(keys).toHaveLength(2))
    expect(keys[1]).toBe(keys[0])
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('cancelling the re-authentication dialog leaves the original error and does not replay', async () => {
    let calls = 0
    server.use(
      http.put('*/v1/orgs/:orgId/sites/:siteId/lock-date', () => {
        calls++
        return problem(403, 'reauth_required')
      }),
    )
    await mountApp('/orgs/org-1/sites/site-a?tab=lock')
    await userEvent.type(
      await screen.findByLabelText('Lock everything up to and including'),
      '2026-01-31',
    )
    await userEvent.click(screen.getByRole('button', { name: 'Set lock date' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(calls).toBe(1)
  })

  it('offboarding needs the exact site name and re-authentication', async () => {
    await mountApp('/orgs/org-1/sites/site-a?tab=transfer')
    await userEvent.click(await screen.findByRole('button', { name: 'Offboard site…' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Site name'), 'wrong')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Offboard site' }))
    expect(
      await within(dialog).findByText('Type the site name exactly to confirm'),
    ).toBeInTheDocument()
    await userEvent.clear(within(dialog).getByLabelText('Site name'))
    await userEvent.type(within(dialog).getByLabelText('Site name'), 'Güneş Sitesi')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Offboard site' }))
    // the mock answers 403 reauth_required first: a second dialog (re-auth) opens on top
    const reauth = await screen.findByRole('heading', { name: 'Confirm it is you' })
    const reauthDialog = reauth.closest('[role="dialog"]') as HTMLElement
    await userEvent.type(within(reauthDialog).getByLabelText('Password'), 'correct-password')
    await userEvent.click(within(reauthDialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(db.transfers.some((t) => t.direction === 'offboard')).toBe(true))
  })
})
