import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/render'
import { db } from '@/test/handlers/db'
import { problem } from '@/test/handlers/http'

const rows = () => screen.getAllByRole('row').slice(1)
const doors = () => rows().map((r) => within(r).getAllByRole('cell')[0]!.textContent)

describe('units table', () => {
  it('lists units with appurtenances under their flat and a derived vacancy badge', async () => {
    await mountApp('/orgs/org-1/units')
    await screen.findByRole('link', { name: 'A-1' })
    const list = doors()
    expect(list.indexOf('P-1')).toBe(list.indexOf('A-1') + 1)
    const a1 = rows().find((r) => within(r).queryByRole('link', { name: 'A-1' }))!
    expect(within(a1).getByText('Occupied')).toBeInTheDocument()
    const b1 = rows().find((r) => within(r).queryByRole('link', { name: 'B-1' }))!
    expect(within(b1).getByText('Vacant')).toBeInTheDocument()
    expect(
      within(rows().find((r) => within(r).queryByRole('link', { name: 'P-1' }))!).getByText(
        'Appurtenance',
      ),
    ).toBeInTheDocument()
  })

  it('sends the filters to the server and keeps them in the URL', async () => {
    const queries: URLSearchParams[] = []
    server.use(
      http.get('*/v1/orgs/:orgId/units', ({ request }) => {
        queries.push(new URL(request.url).searchParams)
        return HttpResponse.json({ items: [], next_cursor: null })
      }),
    )
    const { router } = await mountApp('/orgs/org-1/units?site=site-a')
    await userEvent.selectOptions(await screen.findByLabelText('Type'), 'shop')
    await userEvent.selectOptions(screen.getByLabelText('Occupancy'), 'yes')
    await userEvent.type(screen.getByLabelText('Search units'), 'B-')
    await waitFor(() => {
      const last = queries[queries.length - 1]!
      expect(last.get('type')).toBe('shop')
      expect(last.get('vacant')).toBe('true')
      expect(last.get('q')).toBe('B-')
      expect(last.get('site_id')).toBe('site-a')
    })
    expect(router.state.location.search).toMatchObject({ type: 'shop', vacant: 'yes', q: 'B-' })
    expect(await screen.findByText('No results')).toBeInTheDocument()
  })

  it('pages through server cursors forwards and back', async () => {
    const cursors: (string | null)[] = []
    server.use(
      http.get('*/v1/orgs/:orgId/units', ({ request }) => {
        const c = new URL(request.url).searchParams.get('cursor')
        cursors.push(c)
        const page = c === 'p2' ? 'B-9' : 'A-9'
        const door_number = page
        return HttpResponse.json({
          items: [{ ...db.units[0]!, id: `u-${page}`, door_number, parent_unit_id: null }],
          next_cursor: c === 'p2' ? null : 'p2',
        })
      }),
    )
    await mountApp('/orgs/org-1/units')
    await screen.findByRole('link', { name: 'A-9' })
    await userEvent.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByRole('link', { name: 'B-9' })
    expect(screen.getByRole('button', { name: /Next/ })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: /Previous/ }))
    await screen.findByRole('link', { name: 'A-9' })
    expect(cursors).toEqual([null, 'p2', null])
  })

  it('shows the error state with a retry, and a no-permission state', async () => {
    let fail = true
    server.use(
      http.get('*/v1/orgs/:orgId/units', () =>
        fail ? problem(500, 'internal_error') : HttpResponse.json({ items: [], next_cursor: null }),
      ),
    )
    await mountApp('/orgs/org-1/units')
    await userEvent.click(await screen.findByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('Something went wrong')).toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('No units yet')).toBeInTheDocument()
  })

  it('renders the no-permission page without units.read', async () => {
    await mountApp('/orgs/org-1/units', { permissions: ['sites.read'] })
    expect(
      await screen.findByRole('heading', { level: 1, name: 'You do not have access to this page' }),
    ).toBeInTheDocument()
  })
})

describe('unit form', () => {
  it('validates, then maps the duplicate door number from the server onto the field', async () => {
    await mountApp('/orgs/org-1/units/new?site=site-a')
    await userEvent.click(await screen.findByRole('button', { name: 'Create unit' }))
    expect(await screen.findByText('Enter the door number, for example A-12')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Door number'), 'A-1')
    await userEvent.selectOptions(screen.getByLabelText('Block'), 'block-a')
    await userEvent.click(screen.getByRole('button', { name: 'Create unit' }))
    expect(
      await screen.findByText('This door number is already used in the site'),
    ).toBeInTheDocument()
  })

  it('creates a unit with one idempotency key across a retry', async () => {
    const keys: (string | null)[] = []
    server.use(
      http.post('*/v1/orgs/:orgId/units', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'))
        return keys.length === 1
          ? problem(503, 'unavailable')
          : HttpResponse.json({ ...db.units[0]!, id: 'u-new', door_number: 'C-7' }, { status: 201 })
      }),
    )
    await mountApp('/orgs/org-1/units/new?site=site-a')
    await userEvent.type(await screen.findByLabelText('Door number'), 'C-7')
    await userEvent.selectOptions(screen.getByLabelText('Block'), 'block-a')
    await userEvent.click(screen.getByRole('button', { name: 'Create unit' }))
    await screen.findByText('Something went wrong. Please try again.')
    await userEvent.click(screen.getByRole('button', { name: 'Create unit' }))
    await waitFor(() => expect(keys).toHaveLength(2))
    expect(keys[1]).toBe(keys[0])
  })

  it('offers the parent flat only for parking and storage', async () => {
    await mountApp('/orgs/org-1/units/new?site=site-a')
    await screen.findByLabelText('Door number')
    expect(screen.queryByLabelText('Belongs to flat')).not.toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Type'), 'parking')
    expect(await screen.findByLabelText('Belongs to flat')).toBeInTheDocument()
  })
})

describe('vehicles', () => {
  it('shows the normalised plate and rejects a duplicate plate with a field error', async () => {
    await mountApp('/orgs/org-1/units/unit-a1')
    expect(await screen.findByText('34 ABC 123')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Download plates/ })).toHaveAttribute(
      'href',
      expect.stringContaining('vehicles:export'),
    )
    await userEvent.type(screen.getByLabelText('Plate'), '34-abc-123')
    await userEvent.click(screen.getByRole('button', { name: 'Add vehicle' }))
    expect(
      await screen.findByText('This plate is already registered in the site'),
    ).toBeInTheDocument()
  })
})
