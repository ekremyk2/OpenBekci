import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '@/test/setup'
import { mountGov } from '@/test/gov-utils'
import { communityDb } from '@/test/handlers/community'

afterEach(() => server.events.removeAllListeners())

describe('visitor QR is shown once', () => {
  it('shows the QR and share links after pre-registering, and never again', async () => {
    mountGov('/orgs/org-1/visitors')
    await userEvent.click(await screen.findByRole('button', { name: /Pre-register a visitor/ }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText("Visitor's name"), 'Deniz Acar')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create the pass' }))

    const code = await screen.findByTestId('qr-code-text')
    expect(code.textContent).toMatch(/^obk1\./)
    expect(await screen.findByRole('img', { name: 'Visitor QR code' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'WhatsApp' }).getAttribute('href')).toContain('https://wa.me/?text=')
    expect(screen.getByRole('link', { name: 'SMS' }).getAttribute('href')).toMatch(/^sms:/)
    expect(screen.getByText(/shown only now/)).toBeInTheDocument()
    const token = code.textContent!

    await userEvent.click(screen.getByRole('button', { name: 'Done' }))
    await waitFor(() => expect(screen.queryByTestId('qr-code-text')).not.toBeInTheDocument())
    // the history lists the visitor but never the token
    await userEvent.click(screen.getByRole('tab', { name: 'Visitors' }))
    expect(await screen.findByText('Deniz Acar')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain(token)
  })
})

describe('approval inbox', () => {
  it('lists pending requests with a badge and sends the answer', async () => {
    mountGov('/orgs/org-1/visitors')
    const db = communityDb()
    db.requests.push({
      id: 'ar-1',
      org_id: 'org-1',
      entry_id: 've-1',
      unit_id: 'u-a1',
      unit_door_number: 'A1',
      visitor_name: 'Courier Can',
      plate: null,
      requested_at: new Date().toISOString(),
      expires_at: new Date(Date.now() + 120_000).toISOString(),
      status: 'pending',
      decided_by: null,
      decided_at: null,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    })
    expect(await screen.findByText('Courier Can')).toBeInTheDocument()
    expect(await screen.findByTestId('approval-badge')).toHaveTextContent('1')
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(db.requests[0]!.status).toBe('approved'))
    expect(await screen.findByText('No visitor is waiting')).toBeInTheDocument()
  })

  it('shows the empty state', async () => {
    mountGov('/orgs/org-1/visitors')
    expect(await screen.findByText('No visitor is waiting')).toBeInTheDocument()
  })
})

describe('booking rules', () => {
  it('explains a broken rule and blocks the booking, then confirms a valid one with its fee', async () => {
    mountGov('/orgs/org-1/bookings')
    await userEvent.click(await screen.findByRole('button', { name: 'Book' }))
    const dialog = await screen.findByRole('dialog')
    await waitFor(() => expect(within(dialog).getByLabelText('Facility')).toBeEnabled())
    expect(await within(dialog).findByText(/Fee:/)).toBeInTheDocument()

    await userEvent.clear(within(dialog).getByLabelText('From'))
    await userEvent.type(within(dialog).getByLabelText('From'), '03:00')
    await userEvent.clear(within(dialog).getByLabelText('Until'))
    await userEvent.type(within(dialog).getByLabelText('Until'), '04:00')
    expect(await within(dialog).findByText('The facility is closed at that time.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Book now' })).toBeDisabled()
  })

  it('residents see other bookings only as taken', async () => {
    mountGov('/orgs/org-1/bookings', ['bookings.read', 'bookings.create'])
    await screen.findByText('Slots booked by others are shown only as "taken".')
    await userEvent.click(screen.getByRole('button', { name: 'List' }))
    expect(document.body.textContent).not.toContain('Mehmet Demir')
  })
})

describe('access admin', () => {
  it('shows an enrolment token once after creating an agent', async () => {
    mountGov('/orgs/org-1/access')
    await userEvent.click(await screen.findByRole('button', { name: 'New agent' }))
    await userEvent.type(await screen.findByLabelText('Name'), 'Back gate PC')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect((await screen.findByTestId('enrol-token')).textContent).toMatch(/^obk-enrol-/)
    await userEvent.click(screen.getByRole('button', { name: 'I have saved it' }))
    await waitFor(() => expect(screen.queryByTestId('enrol-token')).not.toBeInTheDocument())
    expect(await screen.findByText('Back gate PC')).toBeInTheDocument()
  })

  it('flags agents: mock driver, clock suspect and offline', async () => {
    mountGov('/orgs/org-1/access')
    expect(await screen.findByText('Gate booth PC')).toBeInTheDocument()
    expect(screen.getAllByText(/\(mock\)/).length).toBeGreaterThan(0)
    expect(screen.getByText('Clock suspect')).toBeInTheDocument()
    expect(screen.getByText('Offline')).toBeInTheDocument()
  })

  it('has no manage buttons for a read-only user', async () => {
    mountGov('/orgs/org-1/access', ['access.read'])
    await screen.findByText('Gate booth PC')
    expect(screen.queryByRole('button', { name: 'New agent' })).not.toBeInTheDocument()
  })
})
