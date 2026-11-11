import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/render'
import { db } from '@/test/handlers/db'
import { problem } from '@/test/handlers/http'

const reauth = async () => {
  const heading = await screen.findByRole('heading', { name: 'Confirm it is you' })
  const dialog = heading.closest('[role="dialog"]') as HTMLElement
  await userEvent.type(within(dialog).getByLabelText('Password'), 'correct-password')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
}

describe('people list', () => {
  it('lists persons with account status and filters by status and search', async () => {
    const { router } = await mountApp('/orgs/org-1/people')
    expect(await screen.findByRole('link', { name: 'Ayşe Yılmaz' })).toBeInTheDocument()
    expect(screen.getAllByText('Joined').length).toBeGreaterThan(0)
    expect(screen.getByRole('link', { name: 'Mehmet Kaya' })).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Account status'), 'invited')
    await waitFor(() =>
      expect(screen.queryByRole('link', { name: 'Ayşe Yılmaz' })).not.toBeInTheDocument(),
    )
    expect(screen.getByRole('link', { name: 'Mehmet Kaya' })).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Search people'), 'zzz')
    expect(await screen.findByText('No results')).toBeInTheDocument()
    expect(router.state.location.search).toMatchObject({ join: 'invited', q: 'zzz' })
  })

  it('has an empty state and a no-permission state', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/persons', () =>
        HttpResponse.json({ items: [], next_cursor: null }),
      ),
    )
    await mountApp('/orgs/org-1/people')
    expect(await screen.findByText('No people yet')).toBeInTheDocument()
  })

  it('shows the no-permission page without people.read', async () => {
    await mountApp('/orgs/org-1/people', { permissions: ['sites.read'] })
    expect(
      await screen.findByRole('heading', { level: 1, name: 'You do not have access to this page' }),
    ).toBeInTheDocument()
  })
})

describe('person form', () => {
  it('validates, maps server errors, and reuses the idempotency key on retry', async () => {
    const keys: (string | null)[] = []
    server.use(
      http.post('*/v1/orgs/:orgId/persons', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'))
        return keys.length === 1
          ? problem(422, 'validation_failed', undefined, [
              { field: 'contact.phone', code: 'invalid', message: 'Phone number is not reachable' },
            ])
          : HttpResponse.json({ ...db.persons[0]!, id: 'p-new' }, { status: 201 })
      }),
    )
    await mountApp('/orgs/org-1/people/new')
    await userEvent.click(await screen.findByRole('button', { name: 'Add person' }))
    expect(await screen.findByText("Enter the person's name")).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Full name'), 'Selin Aydın')
    await userEvent.type(screen.getByLabelText('Phone'), 'nope')
    await userEvent.click(screen.getByRole('button', { name: 'Add person' }))
    expect(
      await screen.findByText('Enter a valid phone number, for example +90 555 111 22 33'),
    ).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('Phone'))
    await userEvent.type(screen.getByLabelText('Phone'), '+90 555 000 11 22')
    await userEvent.click(screen.getByRole('button', { name: 'Add person' }))
    expect(await screen.findByText('Phone number is not reachable')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Add person' }))
    await waitFor(() => expect(keys).toHaveLength(2))
    expect(keys[1]).toBe(keys[0])
  })
})

describe('person detail', () => {
  it('masks the national id and reveals it only after re-authentication', async () => {
    await mountApp('/orgs/org-1/people/person-1')
    const id = await screen.findByTestId('national-id')
    expect(id).toHaveTextContent('TR *******1234')
    await userEvent.click(screen.getByRole('button', { name: 'Show' }))
    await reauth()
    await waitFor(() =>
      expect(screen.getByTestId('national-id')).toHaveTextContent('TR 10000000146'),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Hide' }))
    expect(screen.getByTestId('national-id')).toHaveTextContent('*******1234')
  })

  it('asks for a data export, and erases only with a reason', async () => {
    const seen: string[] = []
    server.events.on('request:start', ({ request }) => {
      if (request.method === 'POST') seen.push(decodeURIComponent(new URL(request.url).pathname))
    })
    await mountApp('/orgs/org-1/people/person-1')
    await userEvent.click(await screen.findByRole('button', { name: 'Export data' }))
    await waitFor(() => expect(seen.some((p) => p.endsWith('/person-1/data-export'))).toBe(true))
    await userEvent.click(screen.getByRole('button', { name: 'Erase person' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByRole('button', { name: 'Erase' })).toBeDisabled()
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Data subject request')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Erase' }))
    await waitFor(() => expect(seen.some((p) => p.endsWith('/person-1:erase'))).toBe(true))
    server.events.removeAllListeners()
  })

  it('hides the national id panel without people.national_id', async () => {
    await mountApp('/orgs/org-1/people/person-1', { permissions: ['people.read'] })
    await screen.findByRole('heading', { name: /Ayşe Yılmaz/ })
    expect(screen.queryByText('National id')).not.toBeInTheDocument()
  })

  it('rejects a national id with a wrong checksum before sending it', async () => {
    await mountApp('/orgs/org-1/people/person-3')
    await userEvent.type(await screen.findByLabelText('Number'), '12345678901')
    await userEvent.click(screen.getByRole('button', { name: 'Save number' }))
    expect(
      await screen.findByText('This number is not valid. Check the digits.'),
    ).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('Number'))
    await userEvent.type(screen.getByLabelText('Number'), '10000000146')
    await userEvent.click(screen.getByRole('button', { name: 'Save number' }))
    await waitFor(() => expect(db.nationalIds['person-3']).toBe('10000000146'))
  })

  it('shows the occupancy timeline and toggles "may see money"', async () => {
    await mountApp('/orgs/org-1/people/person-2?tab=units')
    const list = await screen.findByRole('list', { name: 'Occupancy timeline' })
    expect(within(list).getByText('A-1')).toBeInTheDocument()
    expect(within(list).getByText('Tenant')).toBeInTheDocument()
    const box = within(list).getByRole('checkbox', { name: 'May see money' })
    expect(box).toBeChecked()
    await userEvent.click(box)
    await waitFor(() =>
      expect(db.occupancies.find((o) => o.id === 'occ-2')!.may_see_money).toBe(false),
    )
  })

  it('removing a site-owned role needs re-authentication and then succeeds', async () => {
    await mountApp('/orgs/org-1/people/person-3?tab=roles')
    expect(await screen.findByText('Owned by the site')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Remove' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Remove role' }))
    await reauth()
    await waitFor(() => expect(db.memberships.find((m) => m.id === 'mem-2')).toBeUndefined())
  })
})

describe('invitations', () => {
  it('shows who has joined and sends a new invitation by SMS', async () => {
    await mountApp('/orgs/org-1/people/invitations')
    expect(await screen.findByText('+905552223344')).toBeInTheDocument()
    expect(screen.getByText('Waiting')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Invite' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Send invitation' }))
    expect(await within(dialog).findByText('Choose who to invite')).toBeInTheDocument()
    await userEvent.type(within(dialog).getByLabelText('Person'), 'Zey')
    await userEvent.click(await within(dialog).findByRole('button', { name: /Zeynep Demir/ }))
    await userEvent.selectOptions(within(dialog).getByLabelText('Send by'), 'email')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Send invitation' }))
    await waitFor(() =>
      expect(db.invitations.some((i) => i.person_id === 'person-3' && i.channel === 'email')).toBe(
        true,
      ),
    )
  })

  it('maps a missing contact onto the destination field', async () => {
    await mountApp('/orgs/org-1/people/person-4?tab=invitations')
    await userEvent.click(await screen.findByRole('button', { name: 'Invite' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Send invitation' }))
    expect(await within(dialog).findByText('This person has no phone number')).toBeInTheDocument()
  })
})

describe('unit claims queue', () => {
  it('approves a claim with a share and creates the occupancy', async () => {
    await mountApp('/orgs/org-1/claims')
    const row = (await screen.findByText('Burak Şahin')).closest('tr') as HTMLElement
    await userEvent.click(within(row).getByRole('button', { name: 'Approve' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Ownership share (%)'), '150')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Approve claim' }))
    expect(await within(dialog).findByText('Enter a share between 1 and 100')).toBeInTheDocument()
    await userEvent.clear(within(dialog).getByLabelText('Ownership share (%)'))
    await userEvent.type(within(dialog).getByLabelText('Ownership share (%)'), '100')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Approve claim' }))
    await waitFor(() => expect(db.claims.find((c) => c.id === 'claim-1')!.status).toBe('approved'))
    expect(
      db.occupancies.some(
        (o) => o.unit_id === 'unit-b1' && o.person_display_name === 'Burak Şahin',
      ),
    ).toBe(true)
    await waitFor(() => expect(screen.queryByText('Burak Şahin')).not.toBeInTheDocument())
  })

  it('rejects only with a reason', async () => {
    await mountApp('/orgs/org-1/claims')
    const row = (await screen.findByText('Elif Arslan')).closest('tr') as HTMLElement
    await userEvent.click(within(row).getByRole('button', { name: 'Reject' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Reject claim' }))
    expect(await within(dialog).findByText('Give the person a reason')).toBeInTheDocument()
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Not a resident')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Reject claim' }))
    await waitFor(() => expect(db.claims.find((c) => c.id === 'claim-2')!.status).toBe('rejected'))
  })

  it('shows an empty queue when nothing is waiting', async () => {
    await mountApp('/orgs/org-1/claims', {
      seed: (d) => d.claims.forEach((c) => (c.status = 'approved')),
    })
    expect(await screen.findByText('No claims waiting')).toBeInTheDocument()
  })

  it('is not available without claims.decide', async () => {
    await mountApp('/orgs/org-1/claims', { permissions: ['people.read'] })
    expect(
      await screen.findByRole('heading', { level: 1, name: 'You do not have access to this page' }),
    ).toBeInTheDocument()
  })
})

describe('move-out flow', () => {
  it('keeps the default disposition, confirms and links the documents', async () => {
    const keys: (string | null)[] = []
    server.events.on('request:start', ({ request }) => {
      if (request.method === 'POST' && request.url.endsWith('/move-out'))
        keys.push(request.headers.get('Idempotency-Key'))
    })
    await mountApp('/orgs/org-1/move-out/occ-2')
    expect(await screen.findByText(/closing statement for the leaving person/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Confirm move-out' }))
    expect(await screen.findByRole('link', { name: 'Closing statement' })).toHaveAttribute(
      'href',
      '/v1/orgs/org-1/files/file-closing-1',
    )
    expect(screen.getByRole('link', { name: 'Debt status certificate' })).toHaveAttribute(
      'href',
      '/v1/orgs/org-1/files/file-cert-1',
    )
    expect(db.occupancies.find((o) => o.id === 'occ-2')!.valid_to).toBeTruthy()
    expect(db.moveOuts[0]!.open_charges).toBe('carry_to_person')
  })

  it('needs a reason and a person when the debt is handled differently', async () => {
    await mountApp('/orgs/org-1/move-out/occ-2')
    await screen.findByText(/closing statement for the leaving person/)
    await userEvent.click(screen.getByRole('radio', { name: /Write them off/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Confirm move-out' }))
    expect(screen.getByText('Say why the debt is handled differently')).toBeInTheDocument()
    expect(db.moveOuts).toHaveLength(0)
  })
})

describe('site qr page', () => {
  it('renders the poster with the join code and lets the manager rotate it', async () => {
    await mountApp('/orgs/org-1/sites/site-a/qr')
    expect(await screen.findByText('GUNES-7QK2')).toBeInTheDocument()
    expect(
      await screen.findByRole('img', { name: /QR code to join Güneş Sitesi/ }, { timeout: 4000 }),
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'New code' }))
    await waitFor(() => expect(db.joinCode.code).not.toBe('GUNES-7QK2'))
  })

  it('offers to create the first code when the site has none (404)', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/sites/:siteId/join-code', () =>
        HttpResponse.json({ status: 404, code: 'not_found' }, { status: 404 }),
      ),
    )
    await mountApp('/orgs/org-1/sites/site-a/qr')
    expect(await screen.findByText(/no join code yet/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create code' })).toBeInTheDocument()
  })

  it('has no poster while the code is switched off', async () => {
    await mountApp('/orgs/org-1/sites/site-a/qr', { seed: (d) => (d.joinCode.enabled = false) })
    expect(await screen.findByText(/no poster to print/)).toBeInTheDocument()
  })
})

describe('directory, vendors, portfolio', () => {
  it('tells managers they see everyone', async () => {
    await mountApp('/orgs/org-1/directory')
    expect(await screen.findByText(/You see everyone as a manager/)).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Ayşe Yılmaz' })).toBeInTheDocument()
  })

  it('shows the resident view of the directory without people.read', async () => {
    await mountApp('/orgs/org-1/directory', { permissions: ['directory.read'] })
    expect(await screen.findByText('These neighbours chose to be listed.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Ayşe Yılmaz' })).not.toBeInTheDocument()
  })

  it('creates, edits and archives a vendor', async () => {
    await mountApp('/orgs/org-1/vendors')
    expect(await screen.findByText('Asansör Servisi Ltd.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Add vendor' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save vendor' }))
    expect(await within(dialog).findByText('Enter the vendor name')).toBeInTheDocument()
    await userEvent.type(within(dialog).getByLabelText('Name'), 'Temizlik A.Ş.')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save vendor' }))
    await waitFor(() => expect(db.vendors.some((v) => v.name === 'Temizlik A.Ş.')).toBe(true))
    const row = (await screen.findByText('Temizlik A.Ş.')).closest('tr') as HTMLElement
    await userEvent.click(within(row).getByRole('button', { name: 'Archive' }))
    await waitFor(() =>
      expect(db.vendors.find((v) => v.name === 'Temizlik A.Ş.')!.archived).toBe(true),
    )
  })

  it('shows a card per site on the portfolio dashboard', async () => {
    await mountApp('/orgs/org-1/portfolio')
    const sites = await screen.findByRole('list', { name: 'Sites' })
    expect(within(sites).getByRole('link', { name: 'Güneş Sitesi' })).toBeInTheDocument()
    expect(within(sites).getByText('7 units')).toBeInTheDocument()
  })

  it('portfolio needs dashboard.portfolio', async () => {
    await mountApp('/orgs/org-1/portfolio', { permissions: ['sites.read'] })
    expect(
      await screen.findByRole('heading', { level: 1, name: 'You do not have access to this page' }),
    ).toBeInTheDocument()
  })
})

describe('re-authentication replay', () => {
  it('replays a sensitive request with the same Idempotency-Key', async () => {
    const keys: (string | null)[] = []
    server.use(
      http.delete('*/v1/orgs/:orgId/persons/:personId/national-id', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'))
        return keys.length === 1
          ? problem(403, 'reauth_required', 'Confirm it is you to continue.')
          : new HttpResponse(null, { status: 204 })
      }),
    )
    await mountApp('/orgs/org-1/people/person-1')
    await userEvent.click(await screen.findByRole('button', { name: 'Remove' }))
    await reauth()
    await waitFor(() => expect(keys).toHaveLength(2))
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
  })
})

describe('revealed national id', () => {
  it('is kept out of the query caches and hides itself after 30 seconds', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { queryClient } = await mountApp('/orgs/org-1/people/person-1')
      await userEvent.click(await screen.findByRole('button', { name: 'Show' }))
      await reauth()
      await waitFor(() =>
        expect(screen.getByTestId('national-id')).toHaveTextContent('TR 10000000146'),
      )
      const cached = JSON.stringify([
        queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state),
        queryClient
          .getQueryCache()
          .getAll()
          .map((q) => q.state.data),
      ])
      expect(cached).not.toContain('10000000146')
      await act(async () => {}) // let the auto-hide timer register
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000)
      })
      expect(screen.getByTestId('national-id')).toHaveTextContent('*******1234')
    } finally {
      vi.useRealTimers()
    }
  })
})
