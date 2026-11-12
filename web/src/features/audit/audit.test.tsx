import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { isoToZonedLocal, zonedLocalToIso } from '@/lib/zoned'
import { parseDiff } from './diff'

describe('parseDiff', () => {
  it('tells values, changes and hashed PII apart', () => {
    expect(
      parseDiff({
        email: { hash_before: 'a', hash_after: 'b' },
        name: { before: 'x', after: 'y' },
        rate: { from: 1, to: 2 },
        pinned: true,
      }),
    ).toEqual([
      { field: 'email', kind: 'hashed', before: 'a', after: 'b' },
      { field: 'name', kind: 'changed', before: 'x', after: 'y' },
      { field: 'rate', kind: 'changed', before: 1, after: 2 },
      { field: 'pinned', kind: 'set', value: true },
    ])
    expect(parseDiff(null)).toEqual([])
  })
})

describe('site-timezone wall clock', () => {
  it('converts between a wall-clock time in Istanbul (UTC+3) and an instant', () => {
    expect(zonedLocalToIso('2026-03-01T09:30', 'Europe/Istanbul')).toBe('2026-03-01T06:30:00.000Z')
    expect(isoToZonedLocal('2026-03-01T06:30:00.000Z', 'Europe/Istanbul')).toBe('2026-03-01T09:30')
    expect(zonedLocalToIso('2026-07-01T00:00', 'America/New_York')).toBe('2026-07-01T04:00:00.000Z')
    expect(zonedLocalToIso('garbage', 'UTC')).toBe('')
  })
})

describe('audit viewer', () => {
  it('paginates with a cursor and shows values and hashed fields distinctly', async () => {
    mountApp('/orgs/org-1/settings/audit', 'manager')
    const list = await screen.findByRole('list', { name: 'Audit events' })
    expect(within(list).getAllByRole('button')).toHaveLength(25)

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(within(list).getAllByRole('button')).toHaveLength(30))

    // ae-2 is a person.updated event: one plain value change and one hashed field
    const person = within(list).getAllByRole('button', { name: /Person changed/ })[0]!
    await userEvent.click(person)
    expect(await screen.findByText('Changed (private data, hashed)')).toBeInTheDocument()
    expect(screen.getByText('A. Yilmaz')).toBeInTheDocument()
    expect(screen.getByText('Ayşe Yılmaz', { selector: 'ins' })).toBeInTheDocument()
    expect(screen.getByText(/sha256:9f2c…a1/)).toBeInTheDocument()
  })

  it('sends the filters to the API', async () => {
    const queries: string[] = []
    server.use(
      http.get('*/v1/orgs/:org/audit', ({ request }) => {
        queries.push(new URL(request.url).search)
        return undefined
      }),
    )
    mountApp('/orgs/org-1/settings/audit', 'manager')
    await screen.findByRole('list', { name: 'Audit events' })
    await userEvent.selectOptions(screen.getByLabelText('Action'), 'ticket.transitioned')
    await userEvent.selectOptions(screen.getByLabelText('Target type'), 'ticket')
    await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
    await waitFor(() => expect(queries.at(-1)).toContain('action=ticket.transitioned'))
    expect(queries.at(-1)).toContain('target_type=ticket')
    await waitFor(() =>
      expect(
        within(screen.getByRole('list', { name: 'Audit events' }))
          .getAllByRole('button')
          .every((b) => /Ticket status changed/.test(b.textContent ?? '')),
      ).toBe(true),
    )
  })

  it('shows the empty state when nothing matches', async () => {
    mountApp('/orgs/org-1/settings/audit?action=nothing.like.this', 'manager')
    expect(await screen.findByText('No events match')).toBeInTheDocument()
  })

  it('is not available without audit.read', async () => {
    mountApp('/orgs/org-1/settings/audit', 'resident')
    expect(await screen.findByText('You do not have access to the audit log')).toBeInTheDocument()
  })

  it('shows the no-permission state on a 403 from the API', async () => {
    server.use(
      http.get('*/v1/orgs/:org/audit', () =>
        HttpResponse.json({ status: 403, code: 'insufficient_permission' }, { status: 403 }),
      ),
    )
    mountApp('/orgs/org-1/settings/audit', 'manager')
    expect(await screen.findByText('You do not have access to this')).toBeInTheDocument()
  })
})
