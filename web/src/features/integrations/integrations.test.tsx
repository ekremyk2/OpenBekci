import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mountApp } from '@/test/render'
import type { Db } from '@/test/handlers/db'
import { state } from '@/test/handlers/integrations'

/** Signed in with a recent re-authentication: sensitive calls go through at once. */
const recent = (d: Db) => {
  d.reauthUntil = Date.now() + 60_000
}

const reauth = async () => {
  const heading = await screen.findByRole('heading', { name: 'Confirm it is you' })
  const dialog = heading.closest('[role="dialog"]') as HTMLElement
  await userEvent.type(within(dialog).getByLabelText('Password'), 'correct-password')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
}

describe('API keys', () => {
  it('lists prefix, scopes, site restriction, last use, expiry and status; revoked keys have no actions', async () => {
    await mountApp('/orgs/org-1/settings/api-keys')
    const row = (await screen.findByText('Accounting export')).closest('tr')!
    expect(within(row).getByText('obk_k3x9q7ab_…')).toBeInTheDocument()
    expect(within(row).getByText('payments.record')).toBeInTheDocument()
    expect(within(row).getByText('reports.read')).toBeInTheDocument()
    expect(within(row).getByText('All sites')).toBeInTheDocument()
    expect(within(row).getByText('Active')).toBeInTheDocument()
    const old = screen.getByText('Old dashboard').closest('tr')!
    expect(within(old).getByText('Revoked')).toBeInTheDocument()
    expect(within(old).getByText('Güneş Sitesi')).toBeInTheDocument()
    expect(within(old).getAllByText('Never')).toHaveLength(2)
    expect(within(old).queryByRole('button')).not.toBeInTheDocument()
  })

  it('is read-only with apikeys.read only', async () => {
    await mountApp('/orgs/org-1/settings/api-keys', { permissions: ['org.read', 'apikeys.read'] })
    await screen.findByText('Accounting export')
    expect(screen.queryByRole('button', { name: 'Create API key' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Rotate/ })).not.toBeInTheDocument()
  })

  it('creates a key through re-authentication and shows the secret once behind a confirmation', async () => {
    await mountApp('/orgs/org-1/settings/api-keys')
    await userEvent.click(await screen.findByRole('button', { name: 'Create API key' }))
    const dialog = await screen.findByRole('dialog', { name: 'Create API key' })

    // client validation
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    expect(await within(dialog).findByText('Enter a name for the key')).toBeInTheDocument()
    expect(within(dialog).getByText('Choose at least one scope')).toBeInTheDocument()

    await userEvent.type(within(dialog).getByLabelText('Name'), 'CI export')
    // the picker offers what the admin holds, never key or webhook management
    expect(
      within(dialog).queryByRole('checkbox', { name: 'apikeys.manage' }),
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByRole('checkbox', { name: 'webhooks.manage' }),
    ).not.toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'tickets.read' }))
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'money.read' }))
    await userEvent.selectOptions(within(dialog).getByLabelText(/Restrict to a site/), 'site-a')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))

    // 403 reauth_required: the dialog asks, then the same request is replayed
    await reauth()
    const reveal = await screen.findByRole('dialog', { name: 'Your new API key' })
    const secret = (within(reveal).getByLabelText('The secret') as HTMLInputElement).value
    expect(secret).toMatch(/^obk_[a-z0-9]{8}_[a-z0-9]{32}$/)
    const created = state.keys.find((k) => k.name === 'CI export')!
    expect(created).toMatchObject({ scopes: ['tickets.read', 'money.read'], site_id: 'site-a' })
    expect(secret.startsWith(`obk_${created.prefix}_`)).toBe(true)

    // cannot be dismissed before the confirmation
    const done = within(reveal).getByRole('button', { name: 'Done' })
    expect(done).toBeDisabled()
    await userEvent.keyboard('{Escape}')
    expect(screen.getByRole('dialog', { name: 'Your new API key' })).toBeInTheDocument()
    await userEvent.click(
      within(reveal).getByRole('checkbox', { name: /I have stored this secret/ }),
    )
    await userEvent.click(done)
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Your new API key' })).not.toBeInTheDocument(),
    )
    // the list shows the key's prefix, never the secret
    const row = (await screen.findByText('CI export')).closest('tr')!
    expect(within(row).getByText(`obk_${created.prefix}_…`)).toBeInTheDocument()
    expect(document.body.textContent).not.toContain(secret)
  })

  it('maps field errors from the server onto the dialog', async () => {
    await mountApp('/orgs/org-1/settings/api-keys', { seed: recent })
    await userEvent.click(await screen.findByRole('button', { name: 'Create API key' }))
    const dialog = await screen.findByRole('dialog', { name: 'Create API key' })
    await userEvent.type(within(dialog).getByLabelText('Name'), 'Bad')
    // `audit.read` is held by the admin but unknown to the mock registry: the server refuses it
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'audit.read' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create key' }))
    expect(await within(dialog).findByText(/audit\.read: .*/)).toBeInTheDocument()
  })

  it('rotates a key after a warning, with re-authentication, and shows the new secret', async () => {
    await mountApp('/orgs/org-1/settings/api-keys')
    await userEvent.click(await screen.findByRole('button', { name: 'Rotate Accounting export' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(within(confirm).getByText(/old key stops working immediately/)).toBeInTheDocument()
    await userEvent.click(within(confirm).getByRole('button', { name: 'Rotate key' }))
    await reauth()
    const reveal = await screen.findByRole('dialog', { name: 'New secret for Accounting export' })
    expect((within(reveal).getByLabelText('The secret') as HTMLInputElement).value).toMatch(/^obk_/)
    expect(state.keys.find((k) => k.id === 'key-1')!.prefix).not.toBe('k3x9q7ab')
  })

  it('revokes a key after a confirmation', async () => {
    await mountApp('/orgs/org-1/settings/api-keys', { seed: recent })
    await userEvent.click(await screen.findByRole('button', { name: 'Revoke Accounting export' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(state.keys.find((k) => k.id === 'key-1')!.status).toBe('active')
    await userEvent.click(within(confirm).getByRole('button', { name: 'Revoke key' }))
    await waitFor(() => expect(state.keys.find((k) => k.id === 'key-1')!.status).toBe('revoked'))
    const row = (await screen.findByText('Accounting export')).closest('tr')!
    await waitFor(() => expect(within(row).getByText('Revoked')).toBeInTheDocument())
  })
})

describe('webhooks', () => {
  it('lists endpoints with events, status and the failing mark', async () => {
    state.endpoints[0]!.failing = true
    state.endpoints[0]!.failing_since = '2026-11-08T08:00:00Z'
    await mountApp('/orgs/org-1/settings/webhooks')
    const list = await screen.findByRole('list', { name: 'Webhook endpoints' })
    expect(within(list).getByText('https://hooks.example.org/openbekci')).toBeInTheDocument()
    expect(within(list).getByText('payment.confirmed')).toBeInTheDocument()
    expect(within(list).getByText('Active')).toBeInTheDocument()
    expect(within(list).getByText(/Failing since/)).toBeInTheDocument()
  })

  it('creates an endpoint: https only, at least one event, secret shown once', async () => {
    await mountApp('/orgs/org-1/settings/webhooks')
    await userEvent.click(await screen.findByRole('button', { name: 'Add endpoint' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add endpoint' })
    await userEvent.clear(within(dialog).getByLabelText('URL'))
    await userEvent.type(within(dialog).getByLabelText('URL'), 'http://insecure.example.org')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create endpoint' }))
    expect(
      await within(dialog).findByText('Enter an address starting with https://'),
    ).toBeInTheDocument()
    expect(within(dialog).getByText('Choose at least one event')).toBeInTheDocument()

    await userEvent.clear(within(dialog).getByLabelText('URL'))
    await userEvent.type(within(dialog).getByLabelText('URL'), 'https://127.0.0.1/hook')
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /ticket\.created/ }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create endpoint' }))
    // the server refuses a private address; its field error lands on the URL
    expect(
      await within(dialog).findByText('This address is not reachable from the internet.'),
    ).toBeInTheDocument()

    await userEvent.clear(within(dialog).getByLabelText('URL'))
    await userEvent.type(within(dialog).getByLabelText('URL'), 'https://hooks.example.org/new')
    await userEvent.type(within(dialog).getByLabelText(/Description/), 'Helpdesk')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create endpoint' }))
    const reveal = await screen.findByRole('dialog', { name: 'Signing secret' })
    expect((within(reveal).getByLabelText('The secret') as HTMLInputElement).value).toMatch(
      /^whsec_/,
    )
    expect(within(reveal).getByRole('button', { name: 'Done' })).toBeDisabled()
    await userEvent.click(within(reveal).getByRole('checkbox'))
    await userEvent.click(within(reveal).getByRole('button', { name: 'Done' }))
    expect(await screen.findByText('https://hooks.example.org/new')).toBeInTheDocument()
    expect(state.endpoints.find((e) => e.url.endsWith('/new'))).toMatchObject({
      events: ['ticket.created'],
      description: 'Helpdesk',
      active: true,
    })
  })

  it('edits only the changed fields with the version in If-Match', async () => {
    await mountApp('/orgs/org-1/settings/webhooks')
    await userEvent.click(
      await screen.findByRole('button', { name: 'Edit https://hooks.example.org/openbekci' }),
    )
    const dialog = await screen.findByRole('dialog', { name: 'Edit endpoint' })
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Active/ }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save endpoint' }))
    await waitFor(() => expect(state.endpoints[0]!.active).toBe(false))
    expect(state.endpoints[0]!.version).toBe(2)
    expect(state.endpoints[0]!.events).toEqual(['payment.confirmed', 'ticket.created'])
    expect(await screen.findByText('Inactive')).toBeInTheDocument()
  })

  it('sends a test ping and shows the resulting delivery status', async () => {
    await mountApp('/orgs/org-1/settings/webhooks')
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Send a test ping to https://hooks.example.org/openbekci',
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: 'Test ping sent' })
    expect(within(dialog).getByText('Succeeded')).toBeInTheDocument()
    expect(within(dialog).getByText('204')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Show deliveries' }))
    const log = await screen.findByRole('region', { name: /Deliveries of/ })
    expect(await within(log).findByText('ping')).toBeInTheDocument()
  })

  it('shows a failing ping with its error and next retry', async () => {
    state.receiver = 'down'
    await mountApp('/orgs/org-1/settings/webhooks')
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Send a test ping to https://hooks.example.org/openbekci',
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: 'Test ping sent' })
    expect(within(dialog).getByText('Pending')).toBeInTheDocument()
    expect(within(dialog).getByText('http_status: 500')).toBeInTheDocument()
    expect(within(dialog).getByText('Next retry')).toBeInTheDocument()
  })

  it('shows the delivery log with status filter, attempts, response code and next retry', async () => {
    await mountApp('/orgs/org-1/settings/webhooks?endpoint=wh-1')
    const log = await screen.findByRole('region', { name: /Deliveries of/ })
    expect(await within(log).findByText('ticket.created')).toBeInTheDocument()
    expect(within(log).getByText('http_status: 500')).toBeInTheDocument()
    expect(within(log).getByText('204')).toBeInTheDocument()
    await userEvent.selectOptions(within(log).getByLabelText('Status'), 'succeeded')
    await waitFor(() =>
      expect(within(log).queryByText('payment.confirmed')).not.toBeInTheDocument(),
    )
    expect(within(log).getByText('ticket.created')).toBeInTheDocument()
  })

  it('rotates the secret, shows it once with the overlap expiry', async () => {
    await mountApp('/orgs/org-1/settings/webhooks')
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Rotate the secret of https://hooks.example.org/openbekci',
      }),
    )
    const confirm = await screen.findByRole('alertdialog')
    expect(within(confirm).getByText(/keeps signing for 24 hours/)).toBeInTheDocument()
    await userEvent.click(within(confirm).getByRole('button', { name: 'Rotate secret' }))
    const reveal = await screen.findByRole('dialog', { name: 'New signing secret' })
    expect((within(reveal).getByLabelText('The secret') as HTMLInputElement).value).toMatch(
      /^whsec_/,
    )
    expect(within(reveal).getByText(/The previous secret keeps signing until/)).toBeInTheDocument()
  })

  it('deletes an endpoint after a confirmation', async () => {
    await mountApp('/orgs/org-1/settings/webhooks')
    await userEvent.click(
      await screen.findByRole('button', { name: 'Delete https://hooks.example.org/openbekci' }),
    )
    const confirm = await screen.findByRole('alertdialog')
    expect(state.endpoints).toHaveLength(1)
    await userEvent.click(within(confirm).getByRole('button', { name: 'Delete endpoint' }))
    await waitFor(() => expect(state.endpoints).toHaveLength(0))
    expect(await screen.findByText('No webhook endpoints yet')).toBeInTheDocument()
  })

  it('hides the management buttons with webhooks.read only', async () => {
    await mountApp('/orgs/org-1/settings/webhooks', { permissions: ['org.read', 'webhooks.read'] })
    await screen.findByText('https://hooks.example.org/openbekci')
    expect(screen.queryByRole('button', { name: 'Add endpoint' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Send a test ping/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deliveries of/ })).toBeInTheDocument()
  })
})
