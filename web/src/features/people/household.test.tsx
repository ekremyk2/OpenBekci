import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/render'
import { db } from '@/test/handlers/db'

describe('household panel on /me', () => {
  it('lets an owner invite, see and revoke household invitations', async () => {
    await mountApp('/orgs/org-1/me', { permissions: ['invitations.household'] })
    await userEvent.click(await screen.findByRole('button', { name: 'Send invitation' }))
    expect(await screen.findByText('Enter their name')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Name'), 'Deniz')
    await userEvent.type(screen.getByLabelText('Phone or email'), '+905550001122')
    await userEvent.click(screen.getByRole('button', { name: 'Send invitation' }))
    const list = await screen.findByRole('list', { name: 'Invitations I sent' })
    expect(within(list).getByText('+905550001122')).toBeInTheDocument()
    await userEvent.click(within(list).getByRole('button', { name: 'Revoke' }))
    await waitFor(() =>
      expect(db.invitations.find((i) => i.destination === '+905550001122')!.status).toBe('revoked'),
    )
  })

  it('is absent without invitations.household', async () => {
    await mountApp('/orgs/org-1/me', { permissions: [] })
    await screen.findByRole('heading', { name: 'My account' })
    expect(screen.queryByText('People in my household')).not.toBeInTheDocument()
  })
})

describe('unit payer overrides', () => {
  it('sends who pays which charge type with the unit', async () => {
    await mountApp('/orgs/org-1/units/unit-a2')
    await userEvent.click(await screen.findByRole('button', { name: 'Add an exception' }))
    await userEvent.type(screen.getByLabelText('Charge type'), 'dues')
    await userEvent.selectOptions(screen.getByLabelText('Pays'), 'owner')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() =>
      expect(db.units.find((u) => u.id === 'unit-a2')!.payer_overrides).toEqual({ dues: 'owner' }),
    )
  })
})

describe('re-authentication dialog', () => {
  it('shows an error for a wrong password and keeps the request pending', async () => {
    await mountApp('/orgs/org-1/people/person-1')
    await userEvent.click(await screen.findByRole('button', { name: 'Show' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Password'), 'nope')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    expect(
      await within(dialog).findByText('That did not work. Check it and try again.'),
    ).toBeInTheDocument()
    await userEvent.clear(within(dialog).getByLabelText('Password'))
    await userEvent.type(within(dialog).getByLabelText('Password'), 'correct-password')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(screen.getByTestId('national-id')).toHaveTextContent('10000000146'))
  })

  it('can confirm with an SMS code sent to the account phone', async () => {
    await mountApp('/orgs/org-1/people/person-1')
    await userEvent.click(await screen.findByRole('button', { name: 'Show' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Send me a code instead' }))
    expect(await within(dialog).findByText('Code sent.')).toBeInTheDocument()
    await userEvent.type(within(dialog).getByLabelText('Code from the SMS'), '123456')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(screen.getByTestId('national-id')).toHaveTextContent('10000000146'))
  })
})

describe('household panel with several units', () => {
  it('shows one section per owned or rented unit, each inviting for its own unit', async () => {
    const posted: string[] = []
    server.use(
      http.post('*/v1/orgs/:orgId/units/:unitId/household-invitations', ({ params }) => {
        posted.push(String(params.unitId))
        return HttpResponse.json(
          { ...db.invitations[0]!, id: 'inv-new', kind: 'household' },
          { status: 201 },
        )
      }),
    )
    await mountApp('/orgs/org-1/me', {
      permissions: ['invitations.household'],
      occupancies: [
        {
          id: 'o1',
          organization_id: 'org-1',
          site_id: 'site-a',
          unit_id: 'unit-12',
          unit_label: 'A Blok 12',
          kind: 'owner',
          may_see_money: true,
        },
        {
          id: 'o2',
          organization_id: 'org-1',
          site_id: 'site-a',
          unit_id: 'unit-30',
          unit_label: 'B Blok 30',
          kind: 'tenant',
          may_see_money: true,
        },
      ],
    })
    const second = await screen.findByRole('region', { name: 'Household of B Blok 30' })
    expect(screen.getByRole('region', { name: 'Household of A Blok 12' })).toBeInTheDocument()
    await userEvent.type(within(second).getByLabelText('Name'), 'Deniz')
    await userEvent.type(within(second).getByLabelText('Phone or email'), '+905550001122')
    await userEvent.click(within(second).getByRole('button', { name: 'Send invitation' }))
    await waitFor(() => expect(posted).toEqual(['unit-30']))
  })

  it('rejects a phone number that is not in international format', async () => {
    await mountApp('/orgs/org-1/me', { permissions: ['invitations.household'] })
    await userEvent.type(await screen.findByLabelText('Name'), 'Deniz')
    await userEvent.type(screen.getByLabelText('Phone or email'), '0555 000 11 22')
    await userEvent.click(screen.getByRole('button', { name: 'Send invitation' }))
    expect(await screen.findByText(/international format/)).toBeInTheDocument()
  })
})
