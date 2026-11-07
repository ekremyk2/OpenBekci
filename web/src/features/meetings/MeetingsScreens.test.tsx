import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountGov, recordRequests } from '@/test/gov-utils'
import { governanceDb } from '@/test/handlers/governance'
import { communityDb } from '@/test/handlers/community'

afterEach(() => server.events.removeAllListeners())

describe('call letter and delivery checklist', () => {
  it('shows progress per owner and records a delivery with its tracking number', async () => {
    mountGov('/orgs/org-1/meetings/meeting-2')
    expect(await screen.findByText('0 of 6 delivered')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Call letter \(PDF\)/ })).toBeInTheDocument()

    await userEvent.click(within(await screen.findByRole('row', { name: /Ayşe Yılmaz/ })).getByRole('button', { name: 'Record' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('Registered mail needs the tracking number.')).toBeInTheDocument()

    await userEvent.type(within(dialog).getByLabelText('Tracking number'), 'PTT123456')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('1 of 6 delivered')).toBeInTheDocument()
    const row = screen.getByRole('row', { name: /Ayşe Yılmaz/ })
    expect(within(row).getByText('Registered mail')).toBeInTheDocument()
    expect(within(row).getByText('PTT123456')).toBeInTheDocument()
  })

  it('labels poll results as non-binding', async () => {
    const { findByRole } = mountGov('/orgs/org-1/meetings/meeting-2')
    await userEvent.click(await findByRole('tab', { name: 'Polls' }))
    expect((await screen.findAllByText('Non-binding')).length).toBeGreaterThan(0)
  })
})

describe('attendance desk', () => {
  it('warns about proxy caps and updates the quorum as owners are ticked', async () => {
    mountGov('/orgs/org-1/meetings/meeting-2/desk')
    await screen.findByText('0 of 6 owners (0%)')

    await userEvent.click(await screen.findByRole('button', { name: 'Mark Mehmet Demir present' }))
    expect(await screen.findByText('1 of 6 owners (16.7%)')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Record a proxy for Ali Arslan' }))
    await userEvent.selectOptions(screen.getByLabelText('Represented by'), 'Mehmet Demir')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('maximum share of all votes')
    expect(screen.getByRole('button', { name: 'Record anyway' })).toBeInTheDocument()
    // the limit comes from the site settings
    expect(screen.getByText(/Limit: 2 proxies per person/)).toBeInTheDocument()
  })

  it('checks an owner in by QR code and counts it in the quorum', async () => {
    mountGov('/orgs/org-1/meetings/meeting-2/desk')
    await screen.findByText('0 of 6 owners (0%)')
    await userEvent.type(await screen.findByLabelText('QR code'), 'obk1.att.meeting-2.p-zeynep')
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    expect(await screen.findByText('1 of 6 owners (16.7%)')).toBeInTheDocument()
    expect(governanceDb().attendance[0]?.source).toBe('qr')
  })
})

describe('apply a decision', () => {
  it('confirms before creating and keeps one Idempotency-Key across a retry', async () => {
    const seen = recordRequests()
    let calls = 0
    server.use(
      http.post(
        '*/v1/orgs/:orgId/decisions/:decisionAction',
        async ({ request }) => {
          const dry = new URL(request.url).searchParams.get('dry_run') === 'true'
          if (!dry && calls++ === 0)
            return HttpResponse.json({ status: 503, code: 'unavailable' }, { status: 503, headers: { 'content-type': 'application/problem+json' } })
          return undefined
        },
      ),
    )
    mountGov('/orgs/org-1/meetings/decisions')
    await userEvent.click(await screen.findByRole('button', { name: 'Apply effect' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.selectOptions(within(dialog).getByLabelText('What should be created'), 'Budget marked final')
    await userEvent.selectOptions(await within(dialog).findByLabelText('Budget'), 'fy-2027')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Review' }))
    // nothing is created until the confirmation
    await within(dialog).findByRole('button', { name: 'Confirm and apply' })
    expect(governanceDb().decisions[0]?.effect).toBeNull()

    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm and apply' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/nothing is created twice/)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm and apply' }))
    await waitFor(() => expect(governanceDb().decisions[0]?.effect?.kind).toBe('budget_final'))

    const applies = seen.filter((r) => r.method === 'POST' && r.url.includes(':apply') && !r.url.includes('dry_run'))
    expect(applies).toHaveLength(2)
    expect(applies[0]!.headers.get('Idempotency-Key')).toBeTruthy()
    expect(applies[1]!.headers.get('Idempotency-Key')).toBe(applies[0]!.headers.get('Idempotency-Key'))
    const preview = seen.find((r) => r.url.includes('dry_run=true'))!
    expect(preview.headers.get('Idempotency-Key')).not.toBe(applies[0]!.headers.get('Idempotency-Key'))
  })
})

describe('governance settings', () => {
  it('validates ranges and saves with If-Match', async () => {
    const seen = recordRequests()
    mountGov('/orgs/org-1/meetings/settings')
    const field = await screen.findByLabelText('Send the call at least this many days before')
    await userEvent.clear(field)
    await userEvent.type(field, '999')
    await userEvent.click(screen.getByRole('button', { name: 'Save settings' }))
    expect(await screen.findByText('Enter a value from 0 to 365.')).toBeInTheDocument()
    await userEvent.clear(field)
    await userEvent.type(field, '20')
    await userEvent.click(screen.getByRole('button', { name: 'Save settings' }))
    await waitFor(() => expect(governanceDb().settings.settings.call_min_days).toBe(20))
    const put = seen.find((r) => r.method === 'PUT' && r.url.includes('governance-settings'))!
    expect(put.headers.get('If-Match')).toBe('"1"')
  })
})

describe('no permission', () => {
  it('shows the access message without meetings permissions', async () => {
    mountGov('/orgs/org-1/meetings', ['bookings.read'])
    expect(await screen.findByText('You do not have access to this')).toBeInTheDocument()
  })
  it('keeps the community db reachable for other suites', () => {
    expect(communityDb()).toBeDefined()
  })
})
