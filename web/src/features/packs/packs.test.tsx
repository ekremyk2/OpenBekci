import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mountApp } from '@/test/render'
import { state } from '@/test/handlers/integrations'
import { db } from '@/test/handlers/db'

describe('packs list', () => {
  it('lists the packs with version, countries and the suggested hint for the organization country', async () => {
    await mountApp('/orgs/org-1/settings/packs')
    const list = await screen.findByRole('list', { name: 'Packs' })
    const items = within(list).getAllByRole('listitem')
    expect(items.length).toBeGreaterThanOrEqual(2)
    const tr = items.find(
      (i) => within(i).queryByText('v0.4.0') && /Türkiye|Turkey/.test(i.textContent ?? ''),
    )!
    expect(tr).toBeTruthy()
    expect(within(tr).getByText('Not enabled')).toBeInTheDocument()
    expect(within(tr).getByText('Suggested for your country')).toBeInTheDocument()
    expect(within(tr).getByText('SMS provider: netgsm')).toBeInTheDocument()
    expect(within(tr).getByRole('link', { name: /Set up and enable/ })).toHaveAttribute(
      'href',
      '/orgs/org-1/settings/packs/tr',
    )
  })

  it('shows a Packs tab only with packs.read', async () => {
    await mountApp('/orgs/org-1/settings', { permissions: ['org.read', 'packs.read'] })
    expect(await screen.findByRole('link', { name: 'Packs' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'API keys' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Webhooks' })).not.toBeInTheDocument()
  })

  it('refuses the page without packs.read', async () => {
    await mountApp('/orgs/org-1/settings/packs', { permissions: ['org.read'] })
    expect(await screen.findByText('You do not have access to this page')).toBeInTheDocument()
  })
})

describe('enable a pack', () => {
  it('renders the schema form with x-ui, writes secrets once and maps the server field errors', async () => {
    await mountApp('/orgs/org-1/settings/packs/tr')
    // groups and ordering come from x-ui, labels and help from the pack's labels
    expect(await screen.findByRole('group', { name: 'SMS' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Bank and card' })).toBeInTheDocument()
    expect(await screen.findByLabelText(/SMS provider/)).toBeInTheDocument()
    expect(await screen.findByText(/stored encrypted and never shown again/)).toBeInTheDocument()
    expect(screen.getByText('Not set')).toBeInTheDocument()

    // client validation first: the required secret and provider
    await userEvent.click(screen.getByRole('button', { name: 'Enable pack' }))
    expect((await screen.findAllByText('This field is required')).length).toBeGreaterThanOrEqual(2)
    expect(state.packs.find((p) => p.id === 'tr')!.enabled).toBe(false)

    await userEvent.selectOptions(screen.getByLabelText(/SMS provider/), 'netgsm')
    await userEvent.type(screen.getByLabelText(/Sms api key/), 'k-1234567890')
    await userEvent.type(screen.getByLabelText(/Sms sender/), 'WAYTOOLONGSENDER')
    await userEvent.click(screen.getByRole('button', { name: 'Enable pack' }))
    expect(await screen.findByText('Use at most 11 characters')).toBeInTheDocument()
    // the server's field error for a value the client's schema subset cannot judge
    await userEvent.clear(screen.getByLabelText(/Sms sender/))
    await userEvent.type(screen.getByLabelText(/Sms sender/), 'TAKEN')
    await userEvent.click(screen.getByRole('button', { name: 'Enable pack' }))
    expect(await screen.findByText('This value is not valid')).toBeInTheDocument()
    expect(screen.getByLabelText(/Sms sender/)).toHaveAttribute('aria-invalid', 'true')
    expect(state.packs.find((p) => p.id === 'tr')!.enabled).toBe(false)

    await userEvent.clear(screen.getByLabelText(/Sms sender/))
    await userEvent.type(screen.getByLabelText(/Sms sender/), 'BEKCI')
    await userEvent.click(screen.getByRole('button', { name: 'Enable pack' }))
    await waitFor(() => expect(state.packs.find((p) => p.id === 'tr')!.enabled).toBe(true))
    const stored = state.packs.find((p) => p.id === 'tr')!
    expect(stored.secrets).toEqual({ tr_sms_api_key: 'k-1234567890' })
    expect(stored.settings).toMatchObject({ tr_sms_provider: 'netgsm', tr_sms_sender: 'BEKCI' })
    expect(stored.settings).not.toHaveProperty('tr_sms_api_key')

    // after enabling: settings, never the secret value, with "set"
    expect(
      await screen.findByRole('heading', { name: 'Organization settings' }),
    ).toBeInTheDocument()
    expect(screen.getByText('Set')).toBeInTheDocument()
    expect(screen.getByLabelText(/Sms api key/)).toHaveValue('')
    expect(screen.queryByDisplayValue('k-1234567890')).not.toBeInTheDocument()
  })

  it('enables a pack without settings in one click', async () => {
    state.packs.find((p) => p.id === 'example')!.orgSchema = null
    await mountApp('/orgs/org-1/settings/packs/example')
    await userEvent.click(await screen.findByRole('button', { name: 'Enable pack' }))
    await waitFor(() => expect(state.packs.find((p) => p.id === 'example')!.enabled).toBe(true))
  })

  it('is read-only without packs.manage', async () => {
    await mountApp('/orgs/org-1/settings/packs/tr', { permissions: ['org.read', 'packs.read'] })
    expect(
      await screen.findByText('You can look at the packs but not change them.'),
    ).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Enable pack' })).toBeDisabled()
  })

  it('shows a not-found state for an unknown pack', async () => {
    await mountApp('/orgs/org-1/settings/packs/nope')
    expect(await screen.findByText('Pack not found')).toBeInTheDocument()
  })
})

describe('an enabled pack', () => {
  const enable = (secret = 'abcdefghij') => {
    const tr = state.packs.find((p) => p.id === 'tr')!
    tr.enabled = true
    tr.enabled_at = '2026-11-01T10:00:00Z'
    tr.settings = { tr_sms_provider: 'netgsm', tr_sms_sender: 'EVLER' }
    tr.secrets = { tr_sms_api_key: secret }
  }

  it('saves org settings, keeping an untouched secret and clearing it on request', async () => {
    await mountApp('/orgs/org-1/settings/packs/tr', { seed: () => enable() })
    const sender = await screen.findByLabelText(/Sms sender/)
    expect(sender).toHaveValue('EVLER')
    expect(screen.getByLabelText(/Sms api key/)).toHaveValue('')
    await userEvent.clear(sender)
    await userEvent.type(sender, 'YENI')
    await userEvent.click(screen.getByRole('button', { name: 'Save settings' }))
    await waitFor(() =>
      expect(state.packs.find((p) => p.id === 'tr')!.settings).toMatchObject({
        tr_sms_sender: 'YENI',
      }),
    )
    expect(state.packs.find((p) => p.id === 'tr')!.secrets).toEqual({
      tr_sms_api_key: 'abcdefghij',
    })
  })

  it('edits the settings of one site', async () => {
    await mountApp('/orgs/org-1/settings/packs/tr', { seed: () => enable() })
    const label = await screen.findByLabelText(/Bank account label/)
    await userEvent.type(label, 'Aidat hesabı')
    await userEvent.click(screen.getByRole('button', { name: 'Save site settings' }))
    await waitFor(() =>
      expect(state.packs.find((p) => p.id === 'tr')!.sites['site-a']?.values).toMatchObject({
        tr_bank_account_label: 'Aidat hesabı',
      }),
    )
  })

  it('marks the site settings its presets filled as pack defaults', async () => {
    await mountApp('/orgs/org-1/settings/packs/tr', {
      seed: () => {
        enable()
        ;(db.settings['site-a'] as unknown as Record<string, unknown>).pack_defaults = {
          late_fee_policy: { pack: 'tr', value: { monthly_rate_percent: 5 } },
          other: { pack: 'zz', value: 1 },
        }
      },
    })
    const card = (await screen.findByText('Pack defaults on your sites')).closest(
      'div[class*="rounded"]',
    )!
    expect(await within(card as HTMLElement).findByText('Late fee policy')).toBeInTheDocument()
    expect(within(card as HTMLElement).getByText('Pack default')).toBeInTheDocument()
    expect(within(card as HTMLElement).queryByText('other')).not.toBeInTheDocument()
  })

  it('disables the pack after a confirmation', async () => {
    await mountApp('/orgs/org-1/settings/packs/tr', { seed: () => enable() })
    await userEvent.click(await screen.findByRole('button', { name: 'Disable pack' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(
      within(dialog).getByText(/Everything this pack provides stops working/),
    ).toBeInTheDocument()
    expect(state.packs.find((p) => p.id === 'tr')!.enabled).toBe(true)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Disable pack' }))
    await waitFor(() => expect(state.packs.find((p) => p.id === 'tr')!.enabled).toBe(false))
    expect(await screen.findByText('Enable this pack')).toBeInTheDocument()
  })
})
